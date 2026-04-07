package broker

import (
	"time"

	"github.com/canutoilberto/gomq/internal/domain"
)

// startTTLReaper inicia a goroutine de limpeza de mensagens expiradas.
// Conforme ADR-002 em docs/architecture.md.
func (e *Engine) startTTLReaper(q *queue) {
	// Intervalo de varredura: TTL / 2, mínimo 1 segundo
	interval := time.Duration(q.config.TTLSeconds) * time.Second / 2
	if interval < time.Second {
		interval = time.Second
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-q.ctx.Done():
				return
			case <-e.ctx.Done():
				return
			case <-ticker.C:
				e.reapExpiredMessages(q)
			}
		}
	}()
}

// reapExpiredMessages remove mensagens expiradas da pendingList.
func (e *Engine) reapExpiredMessages(q *queue) {
	q.pendingMu.Lock()
	defer q.pendingMu.Unlock()

	now := time.Now()
	validMessages := make([]*domain.Message, 0, len(q.pendingList))

	for _, msg := range q.pendingList {
		state := msg.GetState()
		if state != domain.MessageStatePending {
			continue
		}

		if !msg.ExpiresAt.IsZero() && now.After(msg.ExpiresAt) {
			msg.SetState(domain.MessageStateExpired)
			q.totalExpired.Add(1)
		} else {
			validMessages = append(validMessages, msg)
		}
	}

	q.pendingList = validMessages
}

// ackTimeoutMonitor monitora entregas com timeout expirado.
// Conforme docs/architecture.md Seção 6.
func (e *Engine) ackTimeoutMonitor() {
	defer e.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.checkAckTimeouts()
		}
	}
}

// checkAckTimeouts verifica e trata entregas com ack timeout expirado.
func (e *Engine) checkAckTimeouts() {
	e.queuesMu.RLock()
	queues := make([]*queue, 0, len(e.queues))
	for _, q := range e.queues {
		queues = append(queues, q)
	}
	e.queuesMu.RUnlock()

	now := time.Now()

	for _, q := range queues {
		var expiredDeliveries []*deliveryInfo

		q.inFlightMu.Lock()
		for deliveryID, info := range q.inFlight {
			if now.After(info.ackDeadline) {
				expiredDeliveries = append(expiredDeliveries, info)
				delete(q.inFlight, deliveryID)
			}
		}
		q.inFlightMu.Unlock()

		// Processar entregas expiradas (tratar como Nack implícito)
		for _, info := range expiredDeliveries {
			q.totalNacked.Add(1)
			info.message.SetState(domain.MessageStateNacked)
			info.message.LastNackReason = "ack_timeout"

			// Verificar retries
			if info.message.Attempt >= q.config.MaxRetries {
				if q.dlq != nil {
					e.moveToDLQ(q, info.message, "ack_timeout")
				}
			} else {
				// Requeue
				info.message.SetState(domain.MessageStatePending)
				select {
				case q.messages <- info.message:
					q.pendingMu.Lock()
					q.pendingList = append(q.pendingList, info.message)
					q.pendingMu.Unlock()
				default:
					if q.dlq != nil {
						e.moveToDLQ(q, info.message, "queue_full_on_requeue")
					}
				}
			}
		}
	}
}

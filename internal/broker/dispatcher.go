package broker

import (
	"time"

	"github.com/google/uuid"

	"github.com/canutoilberto/gomq/internal/domain"
)

// startDispatcher inicia a goroutine de dispatch para uma fila.
func (e *Engine) startDispatcher(q *queue) {
	if q.dispatcherRunning.Swap(true) {
		return // Já está rodando
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer q.dispatcherRunning.Store(false)

		for {
			select {
			case <-q.ctx.Done():
				return
			case <-e.ctx.Done():
				return
			case msg, ok := <-q.messages:
				if !ok {
					return
				}

				// Verificar se a mensagem expirou
				if msg.IsExpired() {
					q.totalExpired.Add(1)
					msg.SetState(domain.MessageStateExpired)
					continue
				}

				// Remover da pendingList
				q.pendingMu.Lock()
				for i, m := range q.pendingList {
					if m.ID == msg.ID {
						q.pendingList = append(q.pendingList[:i], q.pendingList[i+1:]...)
						break
					}
				}
				q.pendingMu.Unlock()

				// Tentar entregar a um consumidor
				delivered := e.deliverToConsumer(q, msg)
				if !delivered {
					// Sem consumidores disponíveis, recolocar na fila
					select {
					case q.messages <- msg:
						q.pendingMu.Lock()
						q.pendingList = append(q.pendingList, msg)
						q.pendingMu.Unlock()
					default:
						// Fila cheia — não deveria acontecer
					}
					// Aguardar um pouco antes de tentar novamente
					time.Sleep(100 * time.Millisecond)
				}
			}
		}
	}()
}

// deliverToConsumer entrega uma mensagem a um consumidor usando round-robin.
func (e *Engine) deliverToConsumer(q *queue, msg *domain.Message) bool {
	q.consumersMu.RLock()
	consumers := make([]*consumerInfo, len(q.consumers))
	copy(consumers, q.consumers)
	q.consumersMu.RUnlock()

	if len(consumers) == 0 {
		return false
	}

	// Round-robin
	startIdx := int(q.roundRobinIdx.Add(1)-1) % len(consumers)

	for i := 0; i < len(consumers); i++ {
		idx := (startIdx + i) % len(consumers)
		consumer := consumers[idx]

		if !consumer.active.Load() {
			continue
		}

		// Incrementar attempt
		msg.Attempt++
		msg.SetState(domain.MessageStateInFlight)

		// Criar delivery
		deliveryID := uuid.New().String()
		now := time.Now()

		delivery := &domain.Delivery{
			DeliveryID:  deliveryID,
			MessageID:   msg.ID,
			QueueName:   q.name,
			ConsumerID:  consumer.id,
			DeliveredAt: now,
			AckDeadline: now.Add(consumer.ackTimeout),
		}

		// Registrar no inFlight
		q.inFlightMu.Lock()
		q.inFlight[deliveryID] = &deliveryInfo{
			delivery:    delivery,
			message:     msg,
			consumer:    consumer,
			ackDeadline: delivery.AckDeadline,
		}
		q.inFlightMu.Unlock()

		// Preparar frame de entrega
		var expiresAt *time.Time
		if !msg.ExpiresAt.IsZero() {
			expiresAt = &msg.ExpiresAt
		}

		frame := domain.DeliverFrame{
			Action:      domain.ActionDeliver,
			DeliveryID:  deliveryID,
			MessageID:   msg.ID,
			Queue:       q.name,
			Body:        msg.Body,
			Headers:     msg.Headers,
			Priority:    msg.Priority,
			Attempt:     msg.Attempt,
			MaxAttempts: q.config.MaxRetries,
			PublishedAt: msg.PublishedAt,
			ExpiresAt:   expiresAt,
		}

		// Enviar para o consumidor (non-blocking)
		select {
		case consumer.deliverCh <- frame:
			return true
		default:
			// Consumidor com buffer cheio, tentar próximo
			q.inFlightMu.Lock()
			delete(q.inFlight, deliveryID)
			q.inFlightMu.Unlock()
			msg.Attempt-- // Reverter incremento
			continue
		}
	}

	return false
}

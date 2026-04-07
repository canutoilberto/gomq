package broker

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/canutoilberto/gomq/internal/domain"
)

// Engine é a implementação principal do broker.
// Implementa domain.QueueManager e domain.MessageBroker.
type Engine struct {
	// queues é o mapa de filas protegido por RWMutex.
	// Conforme docs/architecture.md Seção 4.1.
	queues   map[string]*queue
	queuesMu sync.RWMutex

	// ctx e cancel para shutdown global.
	ctx    context.Context
	cancel context.CancelFunc

	// wg para aguardar goroutines de background.
	wg sync.WaitGroup
}

// New cria uma nova instância do Engine.
func New() *Engine {
	ctx, cancel := context.WithCancel(context.Background())

	e := &Engine{
		queues: make(map[string]*queue),
		ctx:    ctx,
		cancel: cancel,
	}

	// Iniciar goroutine de monitoramento de ack timeout
	e.wg.Add(1)
	go e.ackTimeoutMonitor()

	return e
}

// Shutdown encerra o broker graciosamente.
func (e *Engine) Shutdown(timeout time.Duration) error {
	e.cancel()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("shutdown timeout after %v", timeout)
	}
}

// ============================================================================
// QueueManager Implementation
// ============================================================================

// CreateQueue cria uma nova fila.
func (e *Engine) CreateQueue(ctx context.Context, req domain.CreateQueueRequest) (domain.QueueInfo, error) {
	// Validar nome
	if err := domain.ValidateQueueName(req.Name); err != nil {
		return domain.QueueInfo{}, err
	}

	// Aplicar configuração padrão se não fornecida
	config := domain.DefaultQueueConfig()
	if req.Config != nil {
		// Merge: campos com zero value mantêm o default onde zero é inválido
		config.TTLSeconds = req.Config.TTLSeconds // 0 é válido (sem expiração)
		config.MaxSize = req.Config.MaxSize       // 0 é válido (sem limite)
		config.EnableDLQ = req.Config.EnableDLQ

		// MaxRetries: 0 é inválido pela spec, então 0 significa "use o default"
		if req.Config.MaxRetries > 0 {
			config.MaxRetries = req.Config.MaxRetries
		}
		// Se MaxRetries == 0, mantém o default (3)
	}

	// Validar configuração
	if err := domain.ValidateQueueConfig(config); err != nil {
		return domain.QueueInfo{}, err
	}

	e.queuesMu.Lock()
	defer e.queuesMu.Unlock()

	// Verificar se já existe
	if _, exists := e.queues[req.Name]; exists {
		return domain.QueueInfo{}, domain.NewDomainError(
			domain.ErrCodeQueueAlreadyExists,
			fmt.Sprintf("queue '%s' already exists", req.Name),
		)
	}

	// Criar a fila
	q := newQueue(req.Name, config)
	e.queues[req.Name] = q

	// Criar DLQ se habilitada
	if config.EnableDLQ {
		dlqName := req.Name + ".dlq"
		dlqConfig := domain.QueueConfig{
			TTLSeconds: 0,
			MaxSize:    0,
			EnableDLQ:  false,
			MaxRetries: 1,
		}
		dlq := newQueue(dlqName, dlqConfig)
		dlq.isDLQ = true
		dlq.parentQueue = q
		q.dlq = dlq
		e.queues[dlqName] = dlq

		// Iniciar dispatcher da DLQ
		e.startDispatcher(dlq)
	}

	// Iniciar goroutines de background
	e.startDispatcher(q)
	if config.TTLSeconds > 0 {
		e.startTTLReaper(q)
	}

	return q.toQueueInfo(), nil
}

// GetQueue retorna os detalhes de uma fila.
func (e *Engine) GetQueue(ctx context.Context, name string) (domain.QueueInfo, error) {
	e.queuesMu.RLock()
	q, exists := e.queues[name]
	e.queuesMu.RUnlock()

	if !exists {
		return domain.QueueInfo{}, domain.NewDomainError(
			domain.ErrCodeQueueNotFound,
			fmt.Sprintf("queue '%s' not found", name),
		)
	}

	return q.toQueueInfo(), nil
}

// ListQueues lista todas as filas com paginação.
func (e *Engine) ListQueues(ctx context.Context, page, pageSize int) (domain.ListQueuesResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	e.queuesMu.RLock()
	// Coletar nomes ordenados (excluir DLQs da listagem principal)
	names := make([]string, 0, len(e.queues))
	for name, q := range e.queues {
		if !q.isDLQ {
			names = append(names, name)
		}
	}
	e.queuesMu.RUnlock()

	sort.Strings(names)

	totalItems := len(names)
	totalPages := (totalItems + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	// Calcular slice
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > totalItems {
		start = totalItems
	}
	if end > totalItems {
		end = totalItems
	}

	pageNames := names[start:end]

	// Coletar QueueInfo
	e.queuesMu.RLock()
	queues := make([]domain.QueueInfo, 0, len(pageNames))
	for _, name := range pageNames {
		if q, exists := e.queues[name]; exists {
			queues = append(queues, q.toQueueInfo())
		}
	}
	e.queuesMu.RUnlock()

	return domain.ListQueuesResponse{
		Queues: queues,
		Pagination: domain.Pagination{
			Page:       page,
			PageSize:   pageSize,
			TotalItems: totalItems,
			TotalPages: totalPages,
		},
	}, nil
}

// DeleteQueue remove uma fila.
func (e *Engine) DeleteQueue(ctx context.Context, name string) error {
	e.queuesMu.Lock()
	defer e.queuesMu.Unlock()

	q, exists := e.queues[name]
	if !exists {
		return domain.NewDomainError(
			domain.ErrCodeQueueNotFound,
			fmt.Sprintf("queue '%s' not found", name),
		)
	}

	// Shutdown da fila
	q.shutdown()

	// Remover DLQ se existir
	if q.dlq != nil {
		q.dlq.shutdown()
		delete(e.queues, q.dlq.name)
	}

	delete(e.queues, name)

	return nil
}

// ============================================================================
// MessageBroker Implementation
// ============================================================================

// Publish publica uma mensagem em uma fila.
func (e *Engine) Publish(ctx context.Context, frame domain.PublishFrame) (domain.PublishAckFrame, error) {
	// Validar frame
	if err := domain.ValidatePublishFrame(frame); err != nil {
		return domain.PublishAckFrame{}, err
	}

	e.queuesMu.RLock()
	q, exists := e.queues[frame.Queue]
	e.queuesMu.RUnlock()

	if !exists {
		return domain.PublishAckFrame{}, domain.NewWSDomainError(
			domain.WSErrQueueNotFound,
			fmt.Sprintf("queue '%s' not found", frame.Queue),
		)
	}

	// Criar mensagem
	now := time.Now()
	msg := &domain.Message{
		ID:          uuid.New().String(),
		QueueName:   frame.Queue,
		Body:        frame.Body,
		Headers:     frame.Headers,
		Priority:    frame.Priority,
		Attempt:     0,
		PublishedAt: now,
	}
	msg.SetState(domain.MessageStatePending)

	// Calcular expiração se TTL configurado
	if q.config.TTLSeconds > 0 {
		msg.ExpiresAt = now.Add(time.Duration(q.config.TTLSeconds) * time.Second)
	}

	// Tentar enfileirar (non-blocking)
	select {
	case q.messages <- msg:
		q.totalPublished.Add(1)

		// Adicionar à pendingList para varredura de TTL
		q.pendingMu.Lock()
		q.pendingList = append(q.pendingList, msg)
		q.pendingMu.Unlock()

		return domain.PublishAckFrame{
			Action:    domain.ActionPublishAck,
			RequestID: frame.RequestID,
			MessageID: msg.ID,
			Queue:     frame.Queue,
			Timestamp: now,
		}, nil

	default:
		return domain.PublishAckFrame{}, domain.NewWSDomainError(
			domain.WSErrQueueFull,
			fmt.Sprintf("queue '%s' is full", frame.Queue),
		)
	}
}

// Subscribe registra um consumidor em uma fila.
func (e *Engine) Subscribe(ctx context.Context, frame domain.SubscribeFrame, deliverCh chan<- domain.DeliverFrame) (domain.SubscribeAckFrame, error) {
	e.queuesMu.RLock()
	q, exists := e.queues[frame.Queue]
	e.queuesMu.RUnlock()

	if !exists {
		return domain.SubscribeAckFrame{}, domain.NewWSDomainError(
			domain.WSErrQueueNotFound,
			fmt.Sprintf("queue '%s' not found", frame.Queue),
		)
	}

	// Configurar ack timeout
	ackTimeout := 30 * time.Second
	if frame.AckTimeoutSeconds > 0 {
		ackTimeout = time.Duration(frame.AckTimeoutSeconds) * time.Second
	}

	// Criar consumer
	consumer := &consumerInfo{
		id:         uuid.New().String(),
		deliverCh:  deliverCh,
		ackTimeout: ackTimeout,
	}
	consumer.active.Store(true)

	q.consumersMu.Lock()
	q.consumers = append(q.consumers, consumer)
	q.consumersMu.Unlock()

	return domain.SubscribeAckFrame{
		Action:     domain.ActionSubscribeAck,
		Queue:      frame.Queue,
		ConsumerID: consumer.id,
	}, nil
}

// Unsubscribe remove um consumidor de uma fila.
func (e *Engine) Unsubscribe(ctx context.Context, consumerID string, queueName string) error {
	e.queuesMu.RLock()
	q, exists := e.queues[queueName]
	e.queuesMu.RUnlock()

	if !exists {
		return domain.NewWSDomainError(
			domain.WSErrQueueNotFound,
			fmt.Sprintf("queue '%s' not found", queueName),
		)
	}

	q.consumersMu.Lock()
	defer q.consumersMu.Unlock()

	found := false
	newConsumers := make([]*consumerInfo, 0, len(q.consumers))
	for _, c := range q.consumers {
		if c.id == consumerID {
			c.active.Store(false)
			found = true
		} else {
			newConsumers = append(newConsumers, c)
		}
	}
	q.consumers = newConsumers

	if !found {
		return domain.NewWSDomainError(
			domain.WSErrNotSubscribed,
			fmt.Sprintf("consumer '%s' not subscribed to queue '%s'", consumerID, queueName),
		)
	}

	return nil
}

// Ack confirma o processamento de uma mensagem.
func (e *Engine) Ack(ctx context.Context, frame domain.AckFrame) error {
	// Procurar a entrega em todas as filas
	e.queuesMu.RLock()
	var targetQueue *queue
	var info *deliveryInfo

	for _, q := range e.queues {
		q.inFlightMu.Lock()
		if di, exists := q.inFlight[frame.DeliveryID]; exists {
			info = di
			targetQueue = q
			delete(q.inFlight, frame.DeliveryID)
		}
		q.inFlightMu.Unlock()

		if info != nil {
			break
		}
	}
	e.queuesMu.RUnlock()

	if info == nil {
		return domain.NewWSDomainError(
			domain.WSErrDeliveryNotFound,
			fmt.Sprintf("delivery '%s' not found", frame.DeliveryID),
		)
	}

	// Atualizar métricas
	targetQueue.totalAcked.Add(1)
	info.message.SetState(domain.MessageStateAcked)

	return nil
}

// Nack rejeita o processamento de uma mensagem.
func (e *Engine) Nack(ctx context.Context, frame domain.NackFrame) error {
	// Procurar a entrega em todas as filas
	e.queuesMu.RLock()
	var targetQueue *queue
	var info *deliveryInfo

	for _, q := range e.queues {
		q.inFlightMu.Lock()
		if di, exists := q.inFlight[frame.DeliveryID]; exists {
			info = di
			targetQueue = q
			delete(q.inFlight, frame.DeliveryID)
		}
		q.inFlightMu.Unlock()

		if info != nil {
			break
		}
	}
	e.queuesMu.RUnlock()

	if info == nil {
		return domain.NewWSDomainError(
			domain.WSErrDeliveryNotFound,
			fmt.Sprintf("delivery '%s' not found", frame.DeliveryID),
		)
	}

	// Atualizar métricas
	targetQueue.totalNacked.Add(1)
	info.message.SetState(domain.MessageStateNacked)
	info.message.LastNackReason = frame.Reason

	// Lógica de retry conforme docs/architecture.md Seção 5.2
	shouldRequeue := frame.ShouldRequeue()
	retriesExhausted := info.message.Attempt >= targetQueue.config.MaxRetries

	if !shouldRequeue || retriesExhausted {
		// Mover para DLQ ou descartar
		if targetQueue.dlq != nil {
			e.moveToDLQ(targetQueue, info.message, "max_retries_exceeded")
		}
		// Se não tem DLQ, a mensagem é descartada (já removida do inFlight)
	} else {
		// Requeue: colocar de volta na fila
		info.message.SetState(domain.MessageStatePending)
		select {
		case targetQueue.messages <- info.message:
			// Re-enfileirado com sucesso
		default:
			// Fila cheia, mover para DLQ
			if targetQueue.dlq != nil {
				e.moveToDLQ(targetQueue, info.message, "queue_full_on_requeue")
			}
		}
	}

	return nil
}

// moveToDLQ move uma mensagem para a Dead-Letter Queue.
func (e *Engine) moveToDLQ(q *queue, msg *domain.Message, reason string) {
	if q.dlq == nil {
		return
	}

	// Adicionar headers de DLQ conforme docs/architecture.md Seção 5.4
	if msg.Headers == nil {
		msg.Headers = make(map[string]string)
	}
	msg.Headers["x-dlq-reason"] = reason
	msg.Headers["x-dlq-original-queue"] = q.name
	msg.Headers["x-dlq-moved-at"] = time.Now().Format(time.RFC3339)
	msg.Headers["x-dlq-total-attempts"] = fmt.Sprintf("%d", msg.Attempt)
	if msg.LastNackReason != "" {
		msg.Headers["x-dlq-last-nack-reason"] = msg.LastNackReason
	}

	msg.SetState(domain.MessageStateDLQ)
	msg.QueueName = q.dlq.name

	select {
	case q.dlq.messages <- msg:
		// Movido com sucesso
	default:
		// DLQ cheia — log (em produção usaríamos um logger)
	}
}

package broker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canutoilberto/gomq/internal/domain"
)

// queue representa uma fila interna do broker.
// Implementa as garantias de concorrência definidas em docs/architecture.md Seção 4.
type queue struct {
	name      string
	config    domain.QueueConfig
	createdAt time.Time

	// messages é o channel buffered que armazena mensagens pendentes.
	// Thread-safe nativamente (ADR-001).
	messages chan *domain.Message

	// pendingList mantém referências para varredura de TTL.
	// Protegido por pendingMu.
	pendingList []*domain.Message
	pendingMu   sync.Mutex

	// inFlight armazena mensagens entregues aguardando Ack/Nack.
	// Chave: deliveryID, Valor: deliveryInfo
	inFlight   map[string]*deliveryInfo
	inFlightMu sync.Mutex

	// consumers lista os consumidores inscritos nesta fila.
	// Protegido por consumersMu.
	consumers   []*consumerInfo
	consumersMu sync.RWMutex

	// roundRobinIdx é o índice atual para distribuição round-robin.
	roundRobinIdx atomic.Int64

	// Métricas atômicas (lock-free conforme ADR architecture.md)
	totalPublished atomic.Int64
	totalAcked     atomic.Int64
	totalNacked    atomic.Int64
	totalExpired   atomic.Int64

	// dlq é a referência para a Dead-Letter Queue (nil se desabilitada).
	dlq *queue

	// isDLQ indica se esta fila é uma DLQ (evita recursão).
	isDLQ bool

	// parentQueue é a fila pai (se esta for uma DLQ).
	parentQueue *queue

	// ctx e cancel para shutdown gracioso das goroutines.
	ctx    context.Context
	cancel context.CancelFunc

	// dispatcherRunning indica se o dispatcher está ativo.
	dispatcherRunning atomic.Bool
}

// deliveryInfo contém informações sobre uma entrega em andamento.
type deliveryInfo struct {
	delivery    *domain.Delivery
	message     *domain.Message
	consumer    *consumerInfo
	ackDeadline time.Time
}

// consumerInfo representa um consumidor inscrito.
type consumerInfo struct {
	id         string
	deliverCh  chan<- domain.DeliverFrame
	ackTimeout time.Duration
	active     atomic.Bool
}

// newQueue cria uma nova fila com as configurações fornecidas.
func newQueue(name string, config domain.QueueConfig) *queue {
	ctx, cancel := context.WithCancel(context.Background())

	// Buffer size: se MaxSize > 0, usa MaxSize; senão usa 1.000.000 (conforme ADR-001)
	bufferSize := config.MaxSize
	if bufferSize <= 0 {
		bufferSize = 1_000_000
	}

	q := &queue{
		name:        name,
		config:      config,
		createdAt:   time.Now(),
		messages:    make(chan *domain.Message, bufferSize),
		pendingList: make([]*domain.Message, 0),
		inFlight:    make(map[string]*deliveryInfo),
		consumers:   make([]*consumerInfo, 0),
		ctx:         ctx,
		cancel:      cancel,
	}

	return q
}

// getMetrics retorna as métricas atuais da fila.
func (q *queue) getMetrics() domain.QueueMetrics {
	q.inFlightMu.Lock()
	inFlightCount := int64(len(q.inFlight))
	q.inFlightMu.Unlock()

	q.consumersMu.RLock()
	consumerCount := len(q.consumers)
	q.consumersMu.RUnlock()

	var dlqSize int64
	if q.dlq != nil {
		dlqSize = int64(len(q.dlq.messages))
	}

	return domain.QueueMetrics{
		Pending:        int64(len(q.messages)),
		InFlight:       inFlightCount,
		TotalPublished: q.totalPublished.Load(),
		TotalAcked:     q.totalAcked.Load(),
		TotalNacked:    q.totalNacked.Load(),
		TotalExpired:   q.totalExpired.Load(),
		DLQSize:        dlqSize,
		ConsumerCount:  consumerCount,
	}
}

// toQueueInfo converte para o DTO de resposta.
func (q *queue) toQueueInfo() domain.QueueInfo {
	dlqName := ""
	if q.dlq != nil {
		dlqName = q.dlq.name
	}

	return domain.QueueInfo{
		Name:      q.name,
		Config:    q.config,
		Metrics:   q.getMetrics(),
		DLQName:   dlqName,
		CreatedAt: q.createdAt,
	}
}

// shutdown encerra as goroutines da fila.
func (q *queue) shutdown() {
	q.cancel()

	// Fechar channels de consumidores
	q.consumersMu.Lock()
	for _, c := range q.consumers {
		c.active.Store(false)
	}
	q.consumers = nil
	q.consumersMu.Unlock()
}

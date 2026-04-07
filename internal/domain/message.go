package domain

import (
	"sync/atomic"
	"time"
)

// MessageState representa o estado atual de uma mensagem no seu ciclo de vida.
// Derivado do diagrama de estados em docs/architecture.md (Seção 3.2).
type MessageState int32

const (
	// MessageStatePending - mensagem aguardando na fila para ser entregue.
	MessageStatePending MessageState = iota
	// MessageStateInFlight - mensagem entregue a um consumidor, aguardando Ack/Nack.
	MessageStateInFlight
	// MessageStateAcked - mensagem confirmada pelo consumidor (será removida).
	MessageStateAcked
	// MessageStateNacked - mensagem rejeitada pelo consumidor (será re-enfileirada ou movida para DLQ).
	MessageStateNacked
	// MessageStateExpired - mensagem expirada por TTL (será descartada).
	MessageStateExpired
	// MessageStateDLQ - mensagem movida para Dead-Letter Queue.
	MessageStateDLQ
)

// String retorna a representação textual do estado.
func (s MessageState) String() string {
	switch s {
	case MessageStatePending:
		return "PENDING"
	case MessageStateInFlight:
		return "IN_FLIGHT"
	case MessageStateAcked:
		return "ACKED"
	case MessageStateNacked:
		return "NACKED"
	case MessageStateExpired:
		return "EXPIRED"
	case MessageStateDLQ:
		return "DLQ"
	default:
		return "UNKNOWN"
	}
}

// Message representa uma mensagem no broker.
// Corresponde ao body armazenado internamente após o PublishPayload
// ser recebido via WebSocket (docs/messaging-protocol.yaml).
type Message struct {
	// ID único da mensagem, gerado pelo broker (UUIDv4).
	// Mapeado de: PublishAckPayload.message_id
	ID string

	// QueueName é o nome da fila onde a mensagem foi publicada.
	// Mapeado de: PublishPayload.queue
	QueueName string

	// Body é o conteúdo da mensagem como string (broker é agnóstico ao formato).
	// Mapeado de: PublishPayload.body
	// Limite: 1MB (1.048.576 bytes) conforme docs/architecture.md Seção 7.
	Body string

	// Headers são metadados key-value opcionais.
	// Mapeado de: PublishPayload.headers
	Headers map[string]string

	// Priority da mensagem (0-9, 0=normal, 9=mais alta).
	// Mapeado de: PublishPayload.priority
	// Nota (ADR-003): na v1 é armazenado mas não afeta a ordem de entrega.
	Priority int

	// state é o estado atual no ciclo de vida da mensagem.
	// Usa atomic para acesso thread-safe.
	state atomic.Int32

	// Attempt é o número da tentativa de entrega atual (começa em 1).
	// Mapeado de: DeliverPayload.attempt
	Attempt int

	// PublishedAt é o momento em que a mensagem foi aceita pelo broker.
	// Mapeado de: DeliverPayload.published_at
	PublishedAt time.Time

	// ExpiresAt é o momento de expiração (baseado no TTL da fila).
	// Zero value de time.Time significa sem expiração.
	// Mapeado de: DeliverPayload.expires_at
	ExpiresAt time.Time

	// LastNackReason armazena o motivo do último Nack recebido.
	// Mapeado de: NackPayload.reason
	// Usado para popular o header x-dlq-last-nack-reason quando movido para DLQ.
	LastNackReason string
}

// GetState retorna o estado atual da mensagem de forma thread-safe.
func (m *Message) GetState() MessageState {
	return MessageState(m.state.Load())
}

// SetState define o estado da mensagem de forma thread-safe.
func (m *Message) SetState(s MessageState) {
	m.state.Store(int32(s))
}

// IsExpired verifica se a mensagem ultrapassou seu TTL.
func (m *Message) IsExpired() bool {
	if m.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(m.ExpiresAt)
}

// Delivery representa uma tentativa de entrega de uma mensagem a um consumidor.
// Cada re-entrega gera um novo Delivery com um DeliveryID diferente,
// mas referenciando o mesmo Message.ID.
// Mapeado de: DeliverPayload (docs/messaging-protocol.yaml).
type Delivery struct {
	// DeliveryID é o identificador único desta tentativa de entrega (UUIDv4).
	// Mapeado de: DeliverPayload.delivery_id
	DeliveryID string

	// MessageID referencia a mensagem original.
	// Mapeado de: DeliverPayload.message_id
	MessageID string

	// QueueName é a fila de origem.
	// Mapeado de: DeliverPayload.queue
	QueueName string

	// ConsumerID é o consumidor que recebeu esta entrega.
	// Mapeado de: SubscribeAckPayload.consumer_id
	ConsumerID string

	// DeliveredAt é o momento em que a entrega foi feita.
	DeliveredAt time.Time

	// AckDeadline é o momento limite para receber Ack/Nack.
	// Calculado como: DeliveredAt + ack_timeout_seconds
	AckDeadline time.Time
}

// IsAckExpired verifica se o prazo para Ack/Nack desta entrega expirou.
func (d *Delivery) IsAckExpired() bool {
	return time.Now().After(d.AckDeadline)
}

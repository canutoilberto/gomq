package domain

import "time"

// Este arquivo contém os DTOs do protocolo WebSocket.
// Todos os structs são mapeados diretamente dos schemas em
// docs/messaging-protocol.yaml (components/schemas).

// --- Ações do Protocolo ---

// Action representa o tipo de operação em um frame WebSocket.
type Action string

const (
	ActionPublish      Action = "publish"
	ActionPublishAck   Action = "publish_ack"
	ActionSubscribe    Action = "subscribe"
	ActionSubscribeAck Action = "subscribe_ack"
	ActionUnsubscribe  Action = "unsubscribe"
	ActionDeliver      Action = "deliver"
	ActionAck          Action = "ack"
	ActionNack         Action = "nack"
	ActionError        Action = "error"
)

// --- Frames do Cliente → Broker ---

// PublishFrame é o frame enviado pelo produtor para publicar uma mensagem.
// Mapeado de: components/schemas/PublishPayload
type PublishFrame struct {
	Action    Action            `json:"action"`
	RequestID string            `json:"request_id,omitempty"`
	Queue     string            `json:"queue"`
	Body      string            `json:"body"`
	Headers   map[string]string `json:"headers,omitempty"`
	Priority  int               `json:"priority,omitempty"`
}

// SubscribeFrame é o frame enviado pelo consumidor para se inscrever em uma fila.
// Mapeado de: components/schemas/SubscribePayload
type SubscribeFrame struct {
	Action            Action `json:"action"`
	Queue             string `json:"queue"`
	AckTimeoutSeconds int    `json:"ack_timeout_seconds,omitempty"`
}

// UnsubscribeFrame é o frame para cancelar inscrição.
// Mapeado de: components/schemas/UnsubscribePayload
type UnsubscribeFrame struct {
	Action Action `json:"action"`
	Queue  string `json:"queue"`
}

// AckFrame é o frame de confirmação de processamento.
// Mapeado de: components/schemas/AckPayload
type AckFrame struct {
	Action     Action `json:"action"`
	DeliveryID string `json:"delivery_id"`
}

// NackFrame é o frame de rejeição de processamento.
// Mapeado de: components/schemas/NackPayload
type NackFrame struct {
	Action     Action `json:"action"`
	DeliveryID string `json:"delivery_id"`
	Reason     string `json:"reason,omitempty"`
	Requeue    *bool  `json:"requeue,omitempty"` // ponteiro para distinguir false de ausente (default true)
}

// ShouldRequeue retorna se a mensagem deve ser re-enfileirada.
// Default é true conforme a spec (NackPayload.requeue default: true).
func (n *NackFrame) ShouldRequeue() bool {
	if n.Requeue == nil {
		return true
	}
	return *n.Requeue
}

// --- Frames do Broker → Cliente ---

// PublishAckFrame é a confirmação de que a mensagem foi aceita.
// Mapeado de: components/schemas/PublishAckPayload
type PublishAckFrame struct {
	Action    Action    `json:"action"`
	RequestID string    `json:"request_id,omitempty"`
	MessageID string    `json:"message_id"`
	Queue     string    `json:"queue"`
	Timestamp time.Time `json:"timestamp"`
}

// SubscribeAckFrame é a confirmação de inscrição.
// Mapeado de: components/schemas/SubscribeAckPayload
type SubscribeAckFrame struct {
	Action     Action `json:"action"`
	Queue      string `json:"queue"`
	ConsumerID string `json:"consumer_id"`
}

// DeliverFrame é o frame de entrega de mensagem ao consumidor.
// Mapeado de: components/schemas/DeliverPayload
type DeliverFrame struct {
	Action      Action            `json:"action"`
	DeliveryID  string            `json:"delivery_id"`
	MessageID   string            `json:"message_id"`
	Queue       string            `json:"queue"`
	Body        string            `json:"body"`
	Headers     map[string]string `json:"headers,omitempty"`
	Priority    int               `json:"priority,omitempty"`
	Attempt     int               `json:"attempt"`
	MaxAttempts int               `json:"max_attempts"`
	PublishedAt time.Time         `json:"published_at"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
}

// ErrorFrame é o frame de erro enviado pelo broker.
// Mapeado de: components/schemas/ErrorPayload
type ErrorFrame struct {
	Action    Action      `json:"action"`
	RequestID string      `json:"request_id,omitempty"`
	Code      WSErrorCode `json:"code"`
	Message   string      `json:"message"`
}

// GenericFrame é usado para decodificação inicial do campo "action"
// antes de fazer unmarshal no tipo específico.
type GenericFrame struct {
	Action Action `json:"action"`
}

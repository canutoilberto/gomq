package domain

import "time"

// QueueConfig contém as configurações de uma fila.
// Mapeado diretamente de: components/schemas/QueueConfig (docs/management-api.yaml).
type QueueConfig struct {
	// TTLSeconds é o tempo de vida (em segundos) de cada mensagem na fila.
	// 0 significa sem expiração.
	TTLSeconds int64 `json:"ttl_seconds"`

	// MaxSize é o número máximo de mensagens na fila.
	// 0 significa sem limite (limitado pela memória).
	MaxSize int64 `json:"max_size"`

	// EnableDLQ indica se a Dead-Letter Queue está habilitada.
	EnableDLQ bool `json:"enable_dlq"`

	// MaxRetries é o número máximo de tentativas antes de mover para DLQ.
	// Só é relevante se EnableDLQ for true.
	MaxRetries int `json:"max_retries"`
}

// DefaultQueueConfig retorna a configuração padrão conforme docs/architecture.md Seção 7.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		TTLSeconds: 0,
		MaxSize:    0,
		EnableDLQ:  false,
		MaxRetries: 3,
	}
}

// QueueMetrics contém as métricas em tempo real de uma fila.
// Mapeado diretamente de: components/schemas/QueueMetrics (docs/management-api.yaml).
type QueueMetrics struct {
	Pending        int64 `json:"pending"`
	InFlight       int64 `json:"in_flight"`
	TotalPublished int64 `json:"total_published"`
	TotalAcked     int64 `json:"total_acked"`
	TotalNacked    int64 `json:"total_nacked"`
	TotalExpired   int64 `json:"total_expired"`
	DLQSize        int64 `json:"dlq_size"`
	ConsumerCount  int   `json:"consumer_count"`
}

// QueueInfo representa os detalhes completos de uma fila para resposta da API.
// Mapeado diretamente de: components/schemas/QueueDetails (docs/management-api.yaml).
type QueueInfo struct {
	Name      string       `json:"name"`
	Config    QueueConfig  `json:"config"`
	Metrics   QueueMetrics `json:"metrics"`
	DLQName   string       `json:"dlq_name,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// CreateQueueRequest é o DTO de entrada para criação de fila.
// Mapeado diretamente de: components/schemas/CreateQueueRequest (docs/management-api.yaml).
type CreateQueueRequest struct {
	Name   string       `json:"name"`
	Config *QueueConfig `json:"config,omitempty"`
}

// ListQueuesResponse é o DTO de resposta para listagem de filas.
type ListQueuesResponse struct {
	Queues     []QueueInfo `json:"queues"`
	Pagination Pagination  `json:"pagination"`
}

// Pagination contém informações de paginação.
type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

// ErrorResponse é o DTO padrão de erro da API REST.
type ErrorResponse struct {
	Code    ErrorCode              `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// ErrorCode representa os códigos de erro possíveis.
type ErrorCode string

const (
	ErrCodeQueueAlreadyExists ErrorCode = "QUEUE_ALREADY_EXISTS"
	ErrCodeQueueNotFound      ErrorCode = "QUEUE_NOT_FOUND"
	ErrCodeInvalidQueueName   ErrorCode = "INVALID_QUEUE_NAME"
	ErrCodeInvalidConfig      ErrorCode = "INVALID_CONFIG"
	ErrCodeInternalError      ErrorCode = "INTERNAL_ERROR"
)

// --- Códigos de erro do protocolo WebSocket ---

type WSErrorCode string

const (
	WSErrQueueNotFound    WSErrorCode = "QUEUE_NOT_FOUND"
	WSErrQueueFull        WSErrorCode = "QUEUE_FULL"
	WSErrInvalidPayload   WSErrorCode = "INVALID_PAYLOAD"
	WSErrNotSubscribed    WSErrorCode = "NOT_SUBSCRIBED"
	WSErrDeliveryNotFound WSErrorCode = "DELIVERY_NOT_FOUND"
	WSErrMessageTooLarge  WSErrorCode = "MESSAGE_TOO_LARGE"
	WSErrInternalError    WSErrorCode = "INTERNAL_ERROR"
)

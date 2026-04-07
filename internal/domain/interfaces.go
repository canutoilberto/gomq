package domain

import "context"

// QueueManager define o contrato para operações CRUD de filas.
// Corresponde aos endpoints definidos em docs/management-api.yaml.
//
// Todas as implementações DEVEM ser seguras para uso concorrente
// conforme especificado em docs/architecture.md Seção 4.
type QueueManager interface {
	// CreateQueue cria uma nova fila com o nome e configuração fornecidos.
	// Retorna ErrCodeQueueAlreadyExists se o nome já existir.
	// Retorna ErrCodeInvalidQueueName se o nome não passar na validação.
	// Retorna ErrCodeInvalidConfig se a configuração for inválida.
	// Se EnableDLQ for true, a DLQ "{name}.dlq" é criada automaticamente.
	//
	// Corresponde a: POST /queues
	CreateQueue(ctx context.Context, req CreateQueueRequest) (QueueInfo, error)

	// GetQueue retorna os detalhes e métricas de uma fila específica.
	// Retorna ErrCodeQueueNotFound se a fila não existir.
	//
	// Corresponde a: GET /queues/{name}
	GetQueue(ctx context.Context, name string) (QueueInfo, error)

	// ListQueues retorna todas as filas com paginação.
	// page e pageSize seguem os defaults: page=1, pageSize=20.
	//
	// Corresponde a: GET /queues
	ListQueues(ctx context.Context, page, pageSize int) (ListQueuesResponse, error)

	// DeleteQueue remove uma fila e todas as suas mensagens.
	// Se a fila tiver DLQ, a DLQ também é removida.
	// Se houver consumidores conectados, eles são desconectados.
	// Retorna ErrCodeQueueNotFound se a fila não existir.
	//
	// Corresponde a: DELETE /queues/{name}
	DeleteQueue(ctx context.Context, name string) error
}

// MessageBroker define o contrato para operações de mensageria.
// Corresponde às operations definidas em docs/messaging-protocol.yaml.
//
// Todas as implementações DEVEM ser seguras para uso concorrente
// conforme especificado em docs/architecture.md Seção 4.
type MessageBroker interface {
	// Publish aceita uma mensagem e a armazena na fila especificada.
	// Retorna o ID da mensagem gerado pelo broker.
	//
	// Erros possíveis:
	// - WSErrQueueNotFound: fila não existe
	// - WSErrQueueFull: fila atingiu max_size
	// - WSErrMessageTooLarge: body excede 1MB
	// - WSErrInvalidPayload: campos obrigatórios ausentes
	//
	// Corresponde a: operation publish (messaging-protocol.yaml)
	Publish(ctx context.Context, frame PublishFrame) (PublishAckFrame, error)

	// Subscribe registra um consumidor para receber mensagens de uma fila.
	// Retorna o consumer_id atribuído ao consumidor.
	// O parâmetro deliverCh é o canal por onde o broker enviará DeliverFrames
	// ao consumidor.
	//
	// Erros possíveis:
	// - WSErrQueueNotFound: fila não existe
	//
	// Corresponde a: operation subscribe (messaging-protocol.yaml)
	Subscribe(ctx context.Context, frame SubscribeFrame, deliverCh chan<- DeliverFrame) (SubscribeAckFrame, error)

	// Unsubscribe remove um consumidor de uma fila.
	//
	// Erros possíveis:
	// - WSErrNotSubscribed: consumidor não está inscrito nesta fila
	//
	// Corresponde a: operation unsubscribe (messaging-protocol.yaml)
	Unsubscribe(ctx context.Context, consumerID string, queue string) error

	// Ack confirma o processamento bem-sucedido de uma entrega.
	// Remove a mensagem do mapa in-flight permanentemente.
	//
	// Erros possíveis:
	// - WSErrDeliveryNotFound: delivery_id não encontrado (já expirou ou já foi acked)
	//
	// Corresponde a: operation acknowledge (messaging-protocol.yaml)
	Ack(ctx context.Context, frame AckFrame) error

	// Nack rejeita o processamento de uma entrega.
	// Comportamento conforme docs/architecture.md Seção 5.2:
	// - Se requeue=false ou retries esgotados: move para DLQ ou descarta
	// - Se requeue=true e retries disponíveis: re-enfileira
	//
	// Erros possíveis:
	// - WSErrDeliveryNotFound: delivery_id não encontrado
	//
	// Corresponde a: operation negativeAcknowledge (messaging-protocol.yaml)
	Nack(ctx context.Context, frame NackFrame) error
}

// Consumer representa um consumidor conectado ao broker.
// Usado internamente para gerenciar a distribuição round-robin
// conforme docs/architecture.md Seção 4.1.
type Consumer struct {
	// ID é o identificador único do consumidor (UUIDv4).
	ID string

	// QueueName é a fila em que está inscrito.
	QueueName string

	// DeliverCh é o canal por onde o broker envia mensagens.
	DeliverCh chan<- DeliverFrame

	// AckTimeout é o tempo máximo para receber Ack/Nack (em segundos).
	AckTimeout int
}

// DomainError é o tipo de erro do domínio, contendo código e mensagem.
type DomainError struct {
	Code    ErrorCode
	Message string
}

func (e *DomainError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// NewDomainError cria um novo DomainError.
func NewDomainError(code ErrorCode, message string) *DomainError {
	return &DomainError{Code: code, Message: message}
}

// WSDomainError é o tipo de erro do domínio para o protocolo WebSocket.
type WSDomainError struct {
	Code    WSErrorCode
	Message string
}

func (e *WSDomainError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// NewWSDomainError cria um novo WSDomainError.
func NewWSDomainError(code WSErrorCode, message string) *WSDomainError {
	return &WSDomainError{Code: code, Message: message}
}

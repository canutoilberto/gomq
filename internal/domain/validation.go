package domain

import (
	"regexp"
)

// MaxBodySize é o tamanho máximo do body de uma mensagem (1MB).
// Definido em docs/architecture.md Seção 7.
const MaxBodySize = 1_048_576

// MaxQueueNameLength é o comprimento máximo do nome de uma fila.
const MaxQueueNameLength = 255

// queueNameRegex valida o padrão do nome da fila.
// Mapeado de: components/parameters/QueueName.schema.pattern (management-api.yaml)
var queueNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,255}$`)

// ValidateQueueName verifica se o nome da fila é válido conforme a spec.
func ValidateQueueName(name string) error {
	if name == "" {
		return NewDomainError(ErrCodeInvalidQueueName, "queue name must not be empty")
	}
	if len(name) > MaxQueueNameLength {
		return NewDomainError(ErrCodeInvalidQueueName, "queue name must not exceed 255 characters")
	}
	if !queueNameRegex.MatchString(name) {
		return NewDomainError(ErrCodeInvalidQueueName, "queue name contains invalid characters; allowed: alphanumeric, dots, hyphens, underscores")
	}
	return nil
}

// ValidateQueueConfig verifica se a configuração é válida.
func ValidateQueueConfig(cfg QueueConfig) error {
	if cfg.TTLSeconds < 0 {
		return NewDomainError(ErrCodeInvalidConfig, "ttl_seconds must be >= 0")
	}
	if cfg.MaxSize < 0 {
		return NewDomainError(ErrCodeInvalidConfig, "max_size must be >= 0")
	}
	if cfg.MaxRetries < 1 {
		return NewDomainError(ErrCodeInvalidConfig, "max_retries must be >= 1")
	}
	if cfg.MaxRetries > 100 {
		return NewDomainError(ErrCodeInvalidConfig, "max_retries must be <= 100")
	}
	return nil
}

// ValidatePublishFrame verifica se um frame de publicação é válido.
func ValidatePublishFrame(f PublishFrame) error {
	if f.Queue == "" {
		return NewWSDomainError(WSErrInvalidPayload, "queue is required")
	}
	if f.Body == "" {
		return NewWSDomainError(WSErrInvalidPayload, "body is required")
	}
	if len(f.Body) > MaxBodySize {
		return NewWSDomainError(WSErrMessageTooLarge, "body exceeds maximum size of 1MB")
	}
	if f.Priority < 0 || f.Priority > 9 {
		return NewWSDomainError(WSErrInvalidPayload, "priority must be between 0 and 9")
	}
	return nil
}

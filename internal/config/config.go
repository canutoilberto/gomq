package config

import (
	"os"
	"strconv"
)

// Config contém as configurações do servidor GoMQ.
type Config struct {
	// Port é a porta do servidor HTTP unificado (REST + WebSocket).
	Port int

	// ShutdownTimeoutSeconds é o tempo máximo para shutdown gracioso.
	ShutdownTimeoutSeconds int
}

// Default retorna a configuração padrão conforme docs/architecture.md Seção 7.
func Default() Config {
	return Config{
		Port:                   8080,
		ShutdownTimeoutSeconds: 10,
	}
}

// FromEnv carrega configurações de variáveis de ambiente.
// Prioriza $PORT (convenção de cloud platforms), depois $GOMQ_HTTP_PORT.
func FromEnv() Config {
	cfg := Default()

	// $PORT é a convenção de cloud platforms (Render, Heroku, etc.)
	if v := os.Getenv("PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Port = port
		}
	} else if v := os.Getenv("GOMQ_HTTP_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Port = port
		}
	}

	if v := os.Getenv("GOMQ_SHUTDOWN_TIMEOUT"); v != "" {
		if timeout, err := strconv.Atoi(v); err == nil {
			cfg.ShutdownTimeoutSeconds = timeout
		}
	}

	return cfg
}

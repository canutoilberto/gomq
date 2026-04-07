package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/canutoilberto/gomq/internal/broker"
	"github.com/canutoilberto/gomq/internal/config"
	httpTransport "github.com/canutoilberto/gomq/internal/transport/http"
	wsTransport "github.com/canutoilberto/gomq/internal/transport/ws"
)

func main() {
	cfg := config.FromEnv()

	// Criar engine do broker
	engine := broker.New()

	// Criar servidor HTTP unificado (REST API + WebSocket)
	mux := httpTransport.NewServeMux(engine, wsTransport.NewHandler(engine))
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}

	// Canal para erros
	errCh := make(chan error, 1)

	// Iniciar servidor
	go func() {
		log.Printf("GoMQ server listening on :%d", cfg.Port)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("server error: %w", err)
		}
	}()

	// Aguardar sinal de shutdown ou erro
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received %v, shutting down...", sig)
	case err := <-errCh:
		log.Printf("Server error: %v", err)
	}

	// Shutdown gracioso
	shutdownTimeout := time.Duration(cfg.ShutdownTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	// Shutdown do broker
	if err := engine.Shutdown(shutdownTimeout); err != nil {
		log.Printf("Broker shutdown error: %v", err)
	}

	log.Println("GoMQ shut down successfully")
}

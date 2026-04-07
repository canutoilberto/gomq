package http

import (
	"net/http"

	"github.com/canutoilberto/gomq/internal/domain"
)

// NewServeMux cria um http.ServeMux unificado com REST API + WebSocket.
func NewServeMux(qm domain.QueueManager, wsHandler http.Handler) http.Handler {
	mux := http.NewServeMux()

	h := &handlers{qm: qm}

	// Rotas conforme docs/management-api.yaml
	mux.HandleFunc("POST /api/v1/queues", h.createQueue)
	mux.HandleFunc("GET /api/v1/queues", h.listQueues)
	mux.HandleFunc("GET /api/v1/queues/{name}", h.getQueue)
	mux.HandleFunc("DELETE /api/v1/queues/{name}", h.deleteQueue)

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// WebSocket
	mux.Handle("/ws", wsHandler)

	return mux
}

// NewRouter cria o router HTTP para a Management API (sem WebSocket).
// Util para desenvolvimento local sem dependência de WS.
func NewRouter(qm domain.QueueManager) http.Handler {
	h := &handlers{qm: qm}

	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/queues", h.createQueue)
	mux.HandleFunc("GET /api/v1/queues", h.listQueues)
	mux.HandleFunc("GET /api/v1/queues/{name}", h.getQueue)
	mux.HandleFunc("DELETE /api/v1/queues/{name}", h.deleteQueue)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	return mux
}

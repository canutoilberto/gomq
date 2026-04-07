package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/canutoilberto/gomq/internal/domain"
)

type handlers struct {
	qm domain.QueueManager
}

// createQueue handles POST /api/v1/queues
func (h *handlers) createQueue(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateQueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, domain.ErrCodeInvalidConfig, "invalid request body")
		return
	}

	info, err := h.qm.CreateQueue(r.Context(), req)
	if err != nil {
		handleDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, info)
}

// listQueues handles GET /api/v1/queues
func (h *handlers) listQueues(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	resp, err := h.qm.ListQueues(r.Context(), page, pageSize)
	if err != nil {
		handleDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// getQueue handles GET /api/v1/queues/{name}
func (h *handlers) getQueue(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	info, err := h.qm.GetQueue(r.Context(), name)
	if err != nil {
		handleDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// deleteQueue handles DELETE /api/v1/queues/{name}
func (h *handlers) deleteQueue(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := h.qm.DeleteQueue(r.Context(), name); err != nil {
		handleDomainError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDomainError converte erros de domínio em respostas HTTP.
func handleDomainError(w http.ResponseWriter, err error) {
	switch e := err.(type) {
	case *domain.DomainError:
		status := http.StatusInternalServerError
		switch e.Code {
		case domain.ErrCodeQueueAlreadyExists:
			status = http.StatusConflict
		case domain.ErrCodeQueueNotFound:
			status = http.StatusNotFound
		case domain.ErrCodeInvalidQueueName, domain.ErrCodeInvalidConfig:
			status = http.StatusBadRequest
		}
		writeError(w, status, e.Code, e.Message)
	default:
		writeError(w, http.StatusInternalServerError, domain.ErrCodeInternalError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code domain.ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

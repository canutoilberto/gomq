package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canutoilberto/gomq/internal/broker"
	"github.com/canutoilberto/gomq/internal/domain"
)

// newTestQueueManager cria uma instância de teste do QueueManager.
func newTestQueueManager(t *testing.T) domain.QueueManager {
	t.Helper()
	engine := broker.New()
	t.Cleanup(func() {
		_ = engine.Shutdown(5 * time.Second)
	})
	return engine
}

func TestCreateQueue_Success(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	info, err := qm.CreateQueue(context.Background(), domain.CreateQueueRequest{
		Name: "test-queue",
		Config: &domain.QueueConfig{
			TTLSeconds: 60,
			MaxSize:    1000,
			EnableDLQ:  true,
			MaxRetries: 5,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Name != "test-queue" {
		t.Errorf("expected name 'test-queue', got %q", info.Name)
	}
	if info.Config.TTLSeconds != 60 {
		t.Errorf("expected TTL=60, got %d", info.Config.TTLSeconds)
	}
	if info.Config.MaxSize != 1000 {
		t.Errorf("expected MaxSize=1000, got %d", info.Config.MaxSize)
	}
	if info.Config.EnableDLQ != true {
		t.Error("expected EnableDLQ=true")
	}
	if info.Config.MaxRetries != 5 {
		t.Errorf("expected MaxRetries=5, got %d", info.Config.MaxRetries)
	}
	if info.DLQName != "test-queue.dlq" {
		t.Errorf("expected DLQ name 'test-queue.dlq', got %q", info.DLQName)
	}
	if info.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestCreateQueue_DefaultConfig(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	info, err := qm.CreateQueue(context.Background(), domain.CreateQueueRequest{
		Name: "default-config-queue",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	defaults := domain.DefaultQueueConfig()
	if info.Config.TTLSeconds != defaults.TTLSeconds {
		t.Errorf("expected default TTL=%d, got %d", defaults.TTLSeconds, info.Config.TTLSeconds)
	}
	if info.Config.MaxSize != defaults.MaxSize {
		t.Errorf("expected default MaxSize=%d, got %d", defaults.MaxSize, info.Config.MaxSize)
	}
	if info.Config.EnableDLQ != defaults.EnableDLQ {
		t.Errorf("expected default EnableDLQ=%v, got %v", defaults.EnableDLQ, info.Config.EnableDLQ)
	}
	if info.Config.MaxRetries != defaults.MaxRetries {
		t.Errorf("expected default MaxRetries=%d, got %d", defaults.MaxRetries, info.Config.MaxRetries)
	}
	if info.DLQName != "" {
		t.Errorf("expected empty DLQ name when DLQ disabled, got %q", info.DLQName)
	}
}

func TestCreateQueue_AlreadyExists(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	_, err := qm.CreateQueue(context.Background(), domain.CreateQueueRequest{Name: "dup-queue"})
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	_, err = qm.CreateQueue(context.Background(), domain.CreateQueueRequest{Name: "dup-queue"})
	if err == nil {
		t.Fatal("expected error for duplicate queue, got nil")
	}

	domErr, ok := err.(*domain.DomainError)
	if !ok {
		t.Fatalf("expected *DomainError, got %T", err)
	}
	if domErr.Code != domain.ErrCodeQueueAlreadyExists {
		t.Errorf("expected code %s, got %s", domain.ErrCodeQueueAlreadyExists, domErr.Code)
	}
}

func TestCreateQueue_InvalidName(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	_, err := qm.CreateQueue(context.Background(), domain.CreateQueueRequest{Name: ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}

	domErr, ok := err.(*domain.DomainError)
	if !ok {
		t.Fatalf("expected *DomainError, got %T", err)
	}
	if domErr.Code != domain.ErrCodeInvalidQueueName {
		t.Errorf("expected code %s, got %s", domain.ErrCodeInvalidQueueName, domErr.Code)
	}
}

func TestCreateQueue_InvalidConfig(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	_, err := qm.CreateQueue(context.Background(), domain.CreateQueueRequest{
		Name:   "bad-config",
		Config: &domain.QueueConfig{TTLSeconds: -1, MaxRetries: 3},
	})
	if err == nil {
		t.Fatal("expected error for negative TTL")
	}

	domErr, ok := err.(*domain.DomainError)
	if !ok {
		t.Fatalf("expected *DomainError, got %T", err)
	}
	if domErr.Code != domain.ErrCodeInvalidConfig {
		t.Errorf("expected code %s, got %s", domain.ErrCodeInvalidConfig, domErr.Code)
	}
}

func TestGetQueue_Success(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	_, _ = qm.CreateQueue(context.Background(), domain.CreateQueueRequest{Name: "get-me"})

	info, err := qm.GetQueue(context.Background(), "get-me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "get-me" {
		t.Errorf("expected name 'get-me', got %q", info.Name)
	}
}

func TestGetQueue_NotFound(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	_, err := qm.GetQueue(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent queue")
	}

	domErr, ok := err.(*domain.DomainError)
	if !ok {
		t.Fatalf("expected *DomainError, got %T", err)
	}
	if domErr.Code != domain.ErrCodeQueueNotFound {
		t.Errorf("expected code %s, got %s", domain.ErrCodeQueueNotFound, domErr.Code)
	}
}

func TestListQueues_Empty(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	resp, err := qm.ListQueues(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Queues) != 0 {
		t.Errorf("expected 0 queues, got %d", len(resp.Queues))
	}
	if resp.Pagination.TotalItems != 0 {
		t.Errorf("expected TotalItems=0, got %d", resp.Pagination.TotalItems)
	}
}

func TestListQueues_WithPagination(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	// Criar 5 filas
	for i := range 5 {
		_, _ = qm.CreateQueue(context.Background(), domain.CreateQueueRequest{
			Name: fmt.Sprintf("page-queue-%d", i),
		})
	}

	// Página 1, tamanho 3
	resp, err := qm.ListQueues(context.Background(), 1, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Queues) != 3 {
		t.Errorf("expected 3 queues on page 1, got %d", len(resp.Queues))
	}
	if resp.Pagination.TotalItems != 5 {
		t.Errorf("expected TotalItems=5, got %d", resp.Pagination.TotalItems)
	}
	if resp.Pagination.TotalPages != 2 {
		t.Errorf("expected TotalPages=2, got %d", resp.Pagination.TotalPages)
	}

	// Página 2
	resp, err = qm.ListQueues(context.Background(), 2, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Queues) != 2 {
		t.Errorf("expected 2 queues on page 2, got %d", len(resp.Queues))
	}
}

func TestDeleteQueue_Success(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	_, _ = qm.CreateQueue(context.Background(), domain.CreateQueueRequest{Name: "delete-me"})

	err := qm.DeleteQueue(context.Background(), "delete-me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Confirmar que não existe mais
	_, err = qm.GetQueue(context.Background(), "delete-me")
	if err == nil {
		t.Fatal("expected queue to be deleted")
	}
}

func TestDeleteQueue_WithDLQ(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	_, _ = qm.CreateQueue(context.Background(), domain.CreateQueueRequest{
		Name:   "dlq-parent",
		Config: &domain.QueueConfig{EnableDLQ: true, MaxRetries: 3},
	})

	// Confirmar que DLQ existe
	_, err := qm.GetQueue(context.Background(), "dlq-parent.dlq")
	if err != nil {
		t.Fatalf("DLQ should exist: %v", err)
	}

	// Deletar a fila principal
	err = qm.DeleteQueue(context.Background(), "dlq-parent")
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Confirmar que a DLQ também foi deletada
	_, err = qm.GetQueue(context.Background(), "dlq-parent.dlq")
	if err == nil {
		t.Fatal("DLQ should have been deleted with parent queue")
	}
}

func TestDeleteQueue_NotFound(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	err := qm.DeleteQueue(context.Background(), "ghost-queue")
	if err == nil {
		t.Fatal("expected error for nonexistent queue")
	}

	domErr, ok := err.(*domain.DomainError)
	if !ok {
		t.Fatalf("expected *DomainError, got %T", err)
	}
	if domErr.Code != domain.ErrCodeQueueNotFound {
		t.Errorf("expected code %s, got %s", domain.ErrCodeQueueNotFound, domErr.Code)
	}
}

// --- Teste de concorrência: criação simultânea com mesmo nome ---
func TestConcurrent_CreateSameQueueName(t *testing.T) {
	t.Parallel()
	qm := newTestQueueManager(t)

	const goroutines = 50
	var wg sync.WaitGroup
	var successCount int64

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := qm.CreateQueue(context.Background(), domain.CreateQueueRequest{
				Name: "race-queue",
			})
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	// Exatamente 1 goroutine deve ter sucesso
	if got := atomic.LoadInt64(&successCount); got != 1 {
		t.Errorf("expected exactly 1 successful creation, got %d", got)
	}
}

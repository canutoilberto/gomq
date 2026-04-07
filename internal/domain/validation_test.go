package domain

import (
	"strings"
	"testing"
)

func TestValidateQueueName_Valid(t *testing.T) {
	t.Parallel()

	validNames := []string{
		"orders",
		"orders.created",
		"my-queue",
		"my_queue",
		"Queue123",
		"a",
		strings.Repeat("a", 255),
	}

	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateQueueName(name); err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
		})
	}
}

func TestValidateQueueName_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantCode ErrorCode
	}{
		{"empty", "", ErrCodeInvalidQueueName},
		{"too long", strings.Repeat("a", 256), ErrCodeInvalidQueueName},
		{"spaces", "my queue", ErrCodeInvalidQueueName},
		{"special chars", "queue@#$", ErrCodeInvalidQueueName},
		{"slash", "path/queue", ErrCodeInvalidQueueName},
		{"unicode", "fila_ação", ErrCodeInvalidQueueName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateQueueName(tt.input)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			domErr, ok := err.(*DomainError)
			if !ok {
				t.Fatalf("expected *DomainError, got %T", err)
			}
			if domErr.Code != tt.wantCode {
				t.Errorf("expected code %s, got %s", tt.wantCode, domErr.Code)
			}
		})
	}
}

func TestValidateQueueConfig_Valid(t *testing.T) {
	t.Parallel()

	configs := []QueueConfig{
		DefaultQueueConfig(),
		{TTLSeconds: 60, MaxSize: 1000, EnableDLQ: true, MaxRetries: 5},
		{TTLSeconds: 0, MaxSize: 0, EnableDLQ: false, MaxRetries: 1},
		{TTLSeconds: 999999, MaxSize: 999999, EnableDLQ: true, MaxRetries: 100},
	}

	for i, cfg := range configs {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			t.Parallel()
			if err := ValidateQueueConfig(cfg); err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
		})
	}
}

func TestValidateQueueConfig_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  QueueConfig
	}{
		{"negative TTL", QueueConfig{TTLSeconds: -1, MaxRetries: 3}},
		{"negative MaxSize", QueueConfig{MaxSize: -1, MaxRetries: 3}},
		{"zero MaxRetries", QueueConfig{MaxRetries: 0}},
		{"MaxRetries too high", QueueConfig{MaxRetries: 101}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateQueueConfig(tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestValidatePublishFrame_Valid(t *testing.T) {
	t.Parallel()

	frame := PublishFrame{
		Action:   ActionPublish,
		Queue:    "test-queue",
		Body:     `{"key":"value"}`,
		Priority: 5,
	}

	if err := ValidatePublishFrame(frame); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
}

func TestValidatePublishFrame_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		frame    PublishFrame
		wantCode WSErrorCode
	}{
		{
			"empty queue",
			PublishFrame{Action: ActionPublish, Queue: "", Body: "data"},
			WSErrInvalidPayload,
		},
		{
			"empty body",
			PublishFrame{Action: ActionPublish, Queue: "q", Body: ""},
			WSErrInvalidPayload,
		},
		{
			"body too large",
			PublishFrame{Action: ActionPublish, Queue: "q", Body: strings.Repeat("x", MaxBodySize+1)},
			WSErrMessageTooLarge,
		},
		{
			"negative priority",
			PublishFrame{Action: ActionPublish, Queue: "q", Body: "data", Priority: -1},
			WSErrInvalidPayload,
		},
		{
			"priority too high",
			PublishFrame{Action: ActionPublish, Queue: "q", Body: "data", Priority: 10},
			WSErrInvalidPayload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePublishFrame(tt.frame)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			wsErr, ok := err.(*WSDomainError)
			if !ok {
				t.Fatalf("expected *WSDomainError, got %T", err)
			}
			if wsErr.Code != tt.wantCode {
				t.Errorf("expected code %s, got %s", tt.wantCode, wsErr.Code)
			}
		})
	}
}

func TestNackFrame_ShouldRequeue(t *testing.T) {
	t.Parallel()

	t.Run("nil defaults to true", func(t *testing.T) {
		t.Parallel()
		f := NackFrame{DeliveryID: "abc"}
		if !f.ShouldRequeue() {
			t.Error("expected true when Requeue is nil")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		t.Parallel()
		val := true
		f := NackFrame{DeliveryID: "abc", Requeue: &val}
		if !f.ShouldRequeue() {
			t.Error("expected true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		t.Parallel()
		val := false
		f := NackFrame{DeliveryID: "abc", Requeue: &val}
		if f.ShouldRequeue() {
			t.Error("expected false")
		}
	})
}

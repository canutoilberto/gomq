package domain

import (
	"testing"
	"time"
)

func TestMessage_IsExpired(t *testing.T) {
	t.Parallel()

	t.Run("no expiration (zero time)", func(t *testing.T) {
		t.Parallel()
		m := &Message{ID: "1", ExpiresAt: time.Time{}}
		if m.IsExpired() {
			t.Error("message with zero ExpiresAt should not be expired")
		}
	})

	t.Run("not yet expired", func(t *testing.T) {
		t.Parallel()
		m := &Message{ID: "2", ExpiresAt: time.Now().Add(1 * time.Hour)}
		if m.IsExpired() {
			t.Error("message should not be expired yet")
		}
	})

	t.Run("already expired", func(t *testing.T) {
		t.Parallel()
		m := &Message{ID: "3", ExpiresAt: time.Now().Add(-1 * time.Second)}
		if !m.IsExpired() {
			t.Error("message should be expired")
		}
	})
}

func TestDelivery_IsAckExpired(t *testing.T) {
	t.Parallel()

	t.Run("not expired", func(t *testing.T) {
		t.Parallel()
		d := &Delivery{
			DeliveryID:  "d1",
			DeliveredAt: time.Now(),
			AckDeadline: time.Now().Add(30 * time.Second),
		}
		if d.IsAckExpired() {
			t.Error("delivery should not be expired yet")
		}
	})

	t.Run("expired", func(t *testing.T) {
		t.Parallel()
		d := &Delivery{
			DeliveryID:  "d2",
			DeliveredAt: time.Now().Add(-1 * time.Minute),
			AckDeadline: time.Now().Add(-30 * time.Second),
		}
		if !d.IsAckExpired() {
			t.Error("delivery should be expired")
		}
	})
}

func TestMessageState_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state MessageState
		want  string
	}{
		{MessageStatePending, "PENDING"},
		{MessageStateInFlight, "IN_FLIGHT"},
		{MessageStateAcked, "ACKED"},
		{MessageStateNacked, "NACKED"},
		{MessageStateExpired, "EXPIRED"},
		{MessageStateDLQ, "DLQ"},
		{MessageState(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.state.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultQueueConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultQueueConfig()

	if cfg.TTLSeconds != 0 {
		t.Errorf("expected TTLSeconds=0, got %d", cfg.TTLSeconds)
	}
	if cfg.MaxSize != 0 {
		t.Errorf("expected MaxSize=0, got %d", cfg.MaxSize)
	}
	if cfg.EnableDLQ != false {
		t.Error("expected EnableDLQ=false")
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries=3, got %d", cfg.MaxRetries)
	}
}

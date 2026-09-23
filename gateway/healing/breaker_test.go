package healing

import (
	"testing"
	"time"
)

func TestBreakerTripsAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker("svc-a", 3, time.Second)

	for i := 0; i < 3; i++ {
		if !cb.Allow() {
			t.Fatalf("should allow before threshold, iteration %d", i)
		}
		cb.RecordFailure()
	}

	if cb.Allow() {
		t.Fatal("should deny after threshold consecutive failures")
	}
	if cb.State() != StateOpen {
		t.Fatalf("state = %s, want open", cb.State())
	}
}

func TestBreakerSuccessResetsCount(t *testing.T) {
	cb := NewCircuitBreaker("svc-a", 3, time.Second)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()
	cb.RecordFailure()
	cb.RecordFailure()

	if !cb.Allow() {
		t.Fatal("2 consecutive failures < threshold 3, should allow")
	}
	if cb.State() != StateClosed {
		t.Fatalf("state = %s, want closed", cb.State())
	}
}

func TestBreakerHalfOpenAfterResetWindow(t *testing.T) {
	cb := NewCircuitBreaker("svc-a", 1, 500*time.Millisecond)

	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("open breaker should deny")
	}

	time.Sleep(600 * time.Millisecond)

	if !cb.Allow() {
		t.Fatal("should admit one trial after reset window (half-open)")
	}
	if cb.Allow() {
		t.Fatal("half-open must admit only ONE trial at a time")
	}
}

func TestBreakerHalfOpenSuccessCloses(t *testing.T) {
	cb := NewCircuitBreaker("svc-a", 1, 500*time.Millisecond)

	cb.RecordFailure()
	time.Sleep(600 * time.Millisecond)
	cb.Allow()
	cb.RecordSuccess()

	if !cb.Allow() {
		t.Fatal("breaker should be closed after successful trial")
	}
	if cb.State() != StateClosed {
		t.Fatalf("state = %s, want closed", cb.State())
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker("svc-a", 1, 500*time.Millisecond)

	cb.RecordFailure()
	time.Sleep(600 * time.Millisecond)
	cb.Allow()
	cb.RecordFailure()

	if cb.Allow() {
		t.Fatal("failed trial must re-open the breaker")
	}
	if cb.State() != StateOpen {
		t.Fatalf("state = %s, want open", cb.State())
	}
}

func TestBreakerDisabledAlwaysAllows(t *testing.T) {
	cb := NewCircuitBreaker("svc-a", 0, time.Second)

	for i := 0; i < 100; i++ {
		cb.RecordFailure()
	}
	if !cb.Allow() {
		t.Fatal("threshold <= 0 must disable the breaker")
	}
}

func TestRegistrySameBreakerPerUpstream(t *testing.T) {
	r := NewBreakerRegistry(3, time.Second)

	a1 := r.For("svc-a")
	a2 := r.For("svc-a")
	b := r.For("svc-b")

	if a1 != a2 {
		t.Fatal("same upstream must return the same breaker")
	}
	if a1 == b {
		t.Fatal("different upstreams must get different breakers")
	}
}

package healing

import (
	"log/slog"
	"sync"
	"time"
)

// BreakerState is one of the three classic circuit breaker states.
type BreakerState int

const (
	// StateClosed: normal operation, requests flow, failures are counted.
	StateClosed BreakerState = iota
	// StateOpen: tripped. Healing fails fast until the reset window elapses.
	StateOpen
	// StateHalfOpen: the window elapsed; exactly one trial attempt is allowed.
	StateHalfOpen
)

func (s BreakerState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	}
	return "unknown"
}

// CircuitBreaker tracks consecutive failures for one upstream and decides
// whether healing attempts are allowed. threshold <= 0 disables the breaker.
type CircuitBreaker struct {
	mu               sync.Mutex
	upstream         string
	threshold        int
	resetTimeout     time.Duration
	state            BreakerState
	consecutiveFails int
	openedAt         time.Time
}

// NewCircuitBreaker creates a breaker for one upstream.
func NewCircuitBreaker(upstream string, threshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		upstream:     upstream,
		threshold:    threshold,
		resetTimeout: resetTimeout,
		state:        StateClosed,
	}
}

// Allow reports whether a healing attempt may proceed. It also performs the
// open → half-open transition when the reset window has elapsed. While
// half-open, only one trial attempt is admitted at a time.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.threshold <= 0 {
		return true
	}

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.resetTimeout {
			cb.state = StateHalfOpen
			slog.Info("circuit breaker half-open, admitting one trial",
				"upstream", cb.upstream,
			)
			return true
		}
		return false
	case StateHalfOpen:
		return false
	}
	return false
}

// RecordSuccess closes the breaker and resets the failure count.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.threshold <= 0 {
		return
	}
	if cb.state != StateClosed {
		slog.Info("circuit breaker closed",
			"upstream", cb.upstream,
			"previous_state", cb.state.String(),
		)
	}
	cb.consecutiveFails = 0
	cb.state = StateClosed
}

// RecordFailure counts a failure. A failure while half-open re-opens the
// breaker immediately; otherwise it trips at the threshold.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.threshold <= 0 {
		return
	}

	cb.consecutiveFails++

	if cb.state == StateHalfOpen || cb.consecutiveFails >= cb.threshold {
		cb.trip()
	}
}

// State returns the current state (for logs, tests, and future metrics).
func (cb *CircuitBreaker) State() BreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) trip() {
	cb.state = StateOpen
	cb.openedAt = time.Now()
	slog.Warn("circuit breaker tripped open",
		"upstream", cb.upstream,
		"consecutive_failures", cb.consecutiveFails,
		"reset_in", cb.resetTimeout.String(),
	)
}

// BreakerRegistry hands out one CircuitBreaker per upstream URL,
// creating them on demand.
type BreakerRegistry struct {
	mu           sync.Mutex
	breakers     map[string]*CircuitBreaker
	threshold    int
	resetTimeout time.Duration
}

// NewBreakerRegistry creates a registry with the given breaker settings.
func NewBreakerRegistry(threshold int, resetTimeout time.Duration) *BreakerRegistry {
	return &BreakerRegistry{
		breakers:     make(map[string]*CircuitBreaker),
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}
}

// For returns the breaker for an upstream, creating it on first use.
func (r *BreakerRegistry) For(upstream string) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cb, ok := r.breakers[upstream]; ok {
		return cb
	}
	cb := NewCircuitBreaker(upstream, r.threshold, r.resetTimeout)
	r.breakers[upstream] = cb
	return cb
}

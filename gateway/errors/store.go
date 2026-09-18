package errors

import (
	"sync"
	"time"
)

// FailedRequest captures the full context of a failed request
type FailedRequest struct {
	RequestID     string            `json:"request_id"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Upstream      string            `json:"upstream"`
	Fallback      string            `json:"fallback"`
	StatusCode    int               `json:"status_code"`
	ErrorBody     string            `json:"error_body,omitempty"`
	RequestHeaders map[string][]string `json:"request_headers"`
	RequestBody   string            `json:"request_body,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers"`
	Timestamp     time.Time         `json:"timestamp"`
	DurationMs    int64             `json:"duration_ms"`
}

// Store holds failed requests in memory, keyed by request ID
type Store struct {
	mu       sync.RWMutex
	requests map[string]*FailedRequest
}

// NewStore creates a new error store
func NewStore() *Store {
	return &Store{
		requests: make(map[string]*FailedRequest),
	}
}

// Add stores a failed request
func (s *Store) Add(req *FailedRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[req.RequestID] = req
}

// Get retrieves a failed request by ID
func (s *Store) Get(requestID string) (*FailedRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.requests[requestID]
	return req, ok
}

// List returns all failed requests
func (s *Store) List() []*FailedRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*FailedRequest, 0, len(s.requests))
	for _, req := range s.requests {
		result = append(result, req)
	}
	return result
}

// Clear removes all failed requests
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = make(map[string]*FailedRequest)
}

// IsFailure checks if a status code indicates a failure (4xx or 5xx)
func IsFailure(statusCode int) bool {
	return statusCode >= 400
}

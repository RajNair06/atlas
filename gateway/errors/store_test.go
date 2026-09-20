package errors

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// sampleRequest builds a minimal FailedRequest for store tests.
func sampleRequest(id string) *FailedRequest {
	return &FailedRequest{
		RequestID:  id,
		Method:     "GET",
		Path:       "/buy",
		Upstream:   "http://localhost:8081/buy",
		StatusCode: 503,
		ErrorBody:  "upstream exploded",
		Timestamp:  time.Now(),
	}
}

func TestStoreAddGet(t *testing.T) {
	s := NewStore()
	req := sampleRequest("req-1")

	s.Add(req)

	got, ok := s.Get("req-1")
	if !ok {
		t.Fatal("Get returned not-found for a stored request")
	}
	if got != req {
		t.Fatalf("Get returned %v, want the exact stored request", got)
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := NewStore()

	if _, ok := s.Get("does-not-exist"); ok {
		t.Fatal("Get found a request that was never added")
	}
}

func TestStoreAddOverwritesSameID(t *testing.T) {
	s := NewStore()
	first := sampleRequest("req-1")
	second := sampleRequest("req-1")
	second.StatusCode = 500

	s.Add(first)
	s.Add(second)

	if len(s.List()) != 1 {
		t.Fatalf("store holds %d entries, want 1 (same ID replaces)", len(s.List()))
	}
	got, _ := s.Get("req-1")
	if got.StatusCode != 500 {
		t.Fatalf("status = %d, want the second Add to win", got.StatusCode)
	}
}

func TestStoreList(t *testing.T) {
	s := NewStore()
	for i := 0; i < 5; i++ {
		s.Add(sampleRequest(fmt.Sprintf("req-%d", i)))
	}

	list := s.List()
	if len(list) != 5 {
		t.Fatalf("List returned %d entries, want 5", len(list))
	}

	// Mutating the returned slice must not affect the store's contents.
	list = list[:0]
	if len(s.List()) != 5 {
		t.Fatal("caller truncated the store via the returned slice")
	}
	_ = list
}

func TestStoreClear(t *testing.T) {
	s := NewStore()
	s.Add(sampleRequest("req-1"))
	s.Add(sampleRequest("req-2"))

	s.Clear()

	if len(s.List()) != 0 {
		t.Fatalf("List returned %d entries after Clear, want 0", len(s.List()))
	}
	if _, ok := s.Get("req-1"); ok {
		t.Fatal("Get found an entry after Clear")
	}
	// The store must remain usable after Clear.
	s.Add(sampleRequest("req-3"))
	if _, ok := s.Get("req-3"); !ok {
		t.Fatal("store unusable after Clear")
	}
}

// TestStoreConcurrentAccess hammers the store from many goroutines to prove
// the RWMutex discipline holds. Run with -race: the race detector, not the
// assertions, is what catches unsynchronized access here.
func TestStoreConcurrentAccess(t *testing.T) {
	s := NewStore()
	const writers = 8
	const readers = 8
	const perWriter = 100

	var wg sync.WaitGroup

	// Writers: each adds its own uniquely-keyed requests.
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				s.Add(sampleRequest(fmt.Sprintf("w%d-req-%d", w, i)))
			}
		}(w)
	}

	// Readers: List, Get, and Clear concurrently with the writers.
	// Clear mid-flight is legal: the store must never corrupt or panic,
	// though it may legitimately hold fewer entries afterwards.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_ = s.List()
				s.Get(fmt.Sprintf("w%d-req-%d", r%writers, i))
				if i%25 == 24 && r == 0 {
					// One designated goroutine clears occasionally so the
					// write-lock path of Clear is exercised too.
					s.Clear()
				}
			}
		}(r)
	}

	wg.Wait()

	// After the dust settles the store must still be coherent and usable.
	s.Clear()
	if len(s.List()) != 0 {
		t.Fatalf("List returned %d entries after final Clear, want 0", len(s.List()))
	}
	s.Add(sampleRequest("final"))
	got, ok := s.Get("final")
	if !ok || got.RequestID != "final" {
		t.Fatal("store incoherent after concurrent access")
	}
}

func TestIsFailureBoundary(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{200, false},
		{201, false},
		{301, false},
		{399, false}, // last non-failure
		{400, true},  // first failure
		{401, true},
		{404, true},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{599, true},
	}
	for _, c := range cases {
		if got := IsFailure(c.status); got != c.want {
			t.Errorf("IsFailure(%d) = %v, want %v", c.status, got, c.want)
		}
	}
}

func TestTruncateWithinLimit(t *testing.T) {
	small := strings.Repeat("x", MaxCapturedBody) // exactly at the cap
	if got := Truncate(small); got != small {
		t.Fatal("body at exactly MaxCapturedBody must be stored unchanged")
	}
	if got := Truncate(small[:len(small)-1]); got != small[:len(small)-1] {
		t.Fatal("body under the cap must be stored unchanged")
	}
	if got := Truncate(""); got != "" {
		t.Fatal("empty body must stay empty (no suffix)")
	}
}

func TestTruncateOversized(t *testing.T) {
	big := strings.Repeat("payload-", MaxCapturedBody) // 8x the cap

	got := Truncate(big)

	if !strings.HasSuffix(got, TruncatedSuffix) {
		t.Fatalf("truncated body must end with %q", TruncatedSuffix)
	}
	if len(got) != MaxCapturedBody+len(TruncatedSuffix) {
		t.Fatalf("len = %d, want cap + suffix", len(got))
	}
	if !strings.HasPrefix(big, got[:MaxCapturedBody]) {
		t.Fatal("truncated body must keep the leading bytes of the original")
	}
}

func TestTruncateRespectsUTF8Boundary(t *testing.T) {
	// A string of multi-byte runes; cutting at byte MaxCapturedBody almost
	// certainly lands mid-rune, and Truncate must not produce invalid UTF-8.
	big := strings.Repeat("heal-✓-", MaxCapturedBody)

	got := Truncate(big)

	trimmed := strings.TrimSuffix(got, TruncatedSuffix)
	if !utf8.ValidString(trimmed) {
		t.Fatal("truncated body contains invalid UTF-8 (rune split in half)")
	}
}

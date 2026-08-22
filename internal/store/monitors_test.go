package store

import (
	"context"
	"os"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ATLAS_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://atlas:atlas@localhost:5433/atlas?sslmode=disable"
	}
	s, err := New(context.Background(), dsn)
	if err != nil {
		t.Skipf("database unreachable, skipping: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, "TRUNCATE monitors"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return s
}

func TestMonitorCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	created, err := s.CreateMonitor(ctx, "API", "http", "https://example.com", 60, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected generated id")
	}

	got, err := s.GetMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "API" || got.Target != "https://example.com" {
		t.Errorf("got %+v, want name=API target=https://example.com", got)
	}

	all, err := s.ListMonitors(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("len = %d, want 1", len(all))
	}

	if err := s.DeleteMonitor(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := s.GetMonitor(ctx, created.ID); err != ErrNotFound {
		t.Errorf("get after delete: got %v, want ErrNotFound", err)
	}
}

func TestDeleteMissingMonitor(t *testing.T) {
	s := testStore(t)
	if err := s.DeleteMonitor(context.Background(), 999999); err != ErrNotFound {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

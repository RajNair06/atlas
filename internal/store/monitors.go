package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

type Monitor struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Target    string    `json:"target"`
	Interval  int       `json:"interval"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const monitorColumns = `id, name, type, target, interval, enabled, created_at, updated_at`

func (s *Store) CreateMonitor(ctx context.Context, name, mtype, target string, interval int, enabled bool) (Monitor, error) {
	var m Monitor
	err := s.pool.QueryRow(ctx, `
		INSERT INTO monitors (name, type, target, interval, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+monitorColumns,
		name, mtype, target, interval, enabled,
	).Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.Interval, &m.Enabled, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

func (s *Store) ListMonitors(ctx context.Context) ([]Monitor, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+monitorColumns+` FROM monitors ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	monitors := []Monitor{}
	for rows.Next() {
		var m Monitor
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.Interval, &m.Enabled, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		monitors = append(monitors, m)
	}
	return monitors, rows.Err()
}

func (s *Store) GetMonitor(ctx context.Context, id int64) (Monitor, error) {
	var m Monitor
	err := s.pool.QueryRow(ctx, `SELECT `+monitorColumns+` FROM monitors WHERE id = $1`, id).
		Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.Interval, &m.Enabled, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	return m, err
}

func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM monitors WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

package store

import (
	"context"
	"fmt"
	"time"
)

// Audit appends one row to the audit log, timestamped by the store's clock.
func (s *sqlStore) Audit(ctx context.Context, principal, action, target, result string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit (at, principal, action, target, result) VALUES (?, ?, ?, ?, ?)`,
		s.clock.Now().Unix(), principal, action, target, result)
	if err != nil {
		return fmt.Errorf("store: audit: %w", err)
	}
	return nil
}

// AuditTail returns the most recent limit audit rows, oldest first.
func (s *sqlStore) AuditTail(ctx context.Context, limit int) ([]AuditRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT at, principal, action, target, result FROM (
			SELECT id, at, principal, action, target, result FROM audit ORDER BY id DESC LIMIT ?
		) ORDER BY id ASC`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: audit tail: %w", err)
	}
	defer rows.Close()

	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var atUnix int64
		if err := rows.Scan(&atUnix, &r.Principal, &r.Action, &r.Target, &r.Result); err != nil {
			return nil, fmt.Errorf("store: scan audit row: %w", err)
		}
		r.At = time.Unix(atUnix, 0).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: audit tail: %w", err)
	}
	return out, nil
}

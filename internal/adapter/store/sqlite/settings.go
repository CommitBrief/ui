// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/CommitBrief/ui/internal/port"
)

// settingsStore implements port.SettingsStore on a pool or a transaction.
type settingsStore struct{ q querier }

var _ port.SettingsStore = settingsStore{}

func (s settingsStore) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("sqlite: get setting: %w", err)
	}
	return v, true, nil
}

func (s settingsStore) Set(ctx context.Context, key, value string) error {
	_, err := s.q.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value)
	if err != nil {
		return fmt.Errorf("sqlite: set setting: %w", err)
	}
	return nil
}

func (s settingsStore) Delete(ctx context.Context, key string) error {
	if _, err := s.q.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key); err != nil {
		return fmt.Errorf("sqlite: delete setting: %w", err)
	}
	return nil
}

func (s settingsStore) All(ctx context.Context) (map[string]string, error) {
	rows, err := s.q.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list settings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("sqlite: list settings: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list settings: %w", err)
	}
	return out, nil
}

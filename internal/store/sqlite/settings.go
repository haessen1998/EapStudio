package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (s *Store) LoadSetting(ctx context.Context, key string, target any) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(value), target)
}

func (s *Store) SaveSetting(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_settings(key,value_json,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at`, key, data, time.Now().UTC())
	return err
}

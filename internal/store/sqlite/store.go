package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"eapstudio/internal/command"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/event"
	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	path    string
	traceCh chan driver.Message
	dropped atomic.Uint64
	close   sync.Once
	done    chan struct{}
}

type Alarm struct {
	EquipmentID   string     `json:"equipmentId"`
	AlarmID       string     `json:"alarmId"`
	Code          string     `json:"code"`
	Text          string     `json:"text"`
	Severity      string     `json:"severity"`
	State         string     `json:"state"`
	RaisedAt      time.Time  `json:"raisedAt"`
	ClearedAt     *time.Time `json:"clearedAt,omitempty"`
	CorrelationID string     `json:"correlationId"`
}

type Stats struct {
	TraceCount    int64  `json:"traceCount"`
	EventCount    int64  `json:"eventCount"`
	CommandCount  int64  `json:"commandCount"`
	AlarmCount    int64  `json:"alarmCount"`
	DroppedTrace  uint64 `json:"droppedTrace"`
	DatabaseBytes int64  `json:"databaseBytes"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: path, traceCh: make(chan driver.Message, 2048), done: make(chan struct{})}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	go store.traceWriter()
	return store, nil
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "EapStudio", "eapstudio.db"), nil
}

func (s *Store) Name() string { return "sqlite-history" }

// RecordTrace never blocks the protocol receive path. A bounded queue protects
// the process; dropped records are observable through Stats.
func (s *Store) RecordTrace(value driver.Message) {
	select {
	case s.traceCh <- value:
	default:
		s.dropped.Add(1)
	}
}

// Send is the Router sink for canonical events. It runs only on the async path.
func (s *Store) Send(ctx context.Context, value event.Event) error {
	dataJSON, _ := json.Marshal(value.Data)
	sourceJSON, _ := json.Marshal(value.Source)
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO domain_events
        (id,type,name,equipment_id,correlation_id,causation_id,command_id,occurred_at,source_json,data_json)
        VALUES(?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Type, value.Name, value.EquipmentID, value.CorrelationID, value.CausationID, value.CommandID, value.Timestamp.UTC(), sourceJSON, dataJSON)
	if err != nil {
		return err
	}
	if value.Name == "alarm.raised" || value.Name == "alarm.cleared" {
		return s.applyAlarm(ctx, value)
	}
	return nil
}

func (s *Store) UpsertCommand(ctx context.Context, value command.Command) error {
	parameters, _ := json.Marshal(value.Parameters)
	_, err := s.db.ExecContext(ctx, `INSERT INTO commands
        (id,type,name,equipment_id,correlation_id,causation_id,status,created_at,completed_at,parameters_json,error)
        VALUES(?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET status=excluded.status,completed_at=excluded.completed_at,error=excluded.error,parameters_json=excluded.parameters_json`,
		value.ID, value.Type, value.Name, value.EquipmentID, value.CorrelationID, value.CausationID, value.Status, value.CreatedAt.UTC(), nullableTime(value.CompletedAt), parameters, value.Error)
	return err
}

func (s *Store) Alarms(ctx context.Context, limit int) ([]Alarm, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT equipment_id,alarm_id,code,text,severity,state,raised_at,cleared_at,correlation_id FROM alarms ORDER BY raised_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Alarm
	for rows.Next() {
		var value Alarm
		var cleared sql.NullTime
		if err := rows.Scan(&value.EquipmentID, &value.AlarmID, &value.Code, &value.Text, &value.Severity, &value.State, &value.RaisedAt, &cleared, &value.CorrelationID); err != nil {
			return nil, err
		}
		if cleared.Valid {
			value.ClearedAt = &cleared.Time
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) Stats(ctx context.Context) Stats {
	stats := Stats{DroppedTrace: s.dropped.Load()}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM protocol_traces`).Scan(&stats.TraceCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events`).Scan(&stats.EventCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commands`).Scan(&stats.CommandCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alarms`).Scan(&stats.AlarmCount)
	if info, err := os.Stat(s.path); err == nil {
		stats.DatabaseBytes = info.Size()
	}
	return stats
}

func (s *Store) Close() error {
	s.close.Do(func() { close(s.traceCh); <-s.done })
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS protocol_traces (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT NOT NULL, equipment_id TEXT NOT NULL,
  direction TEXT NOT NULL, occurred_at DATETIME NOT NULL, stream INTEGER NOT NULL, function INTEGER NOT NULL,
  wait_bit INTEGER NOT NULL, system_bytes INTEGER NOT NULL, sml TEXT, raw_hex TEXT, metadata_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_trace_equipment_time ON protocol_traces(equipment_id,occurred_at DESC);
CREATE TABLE IF NOT EXISTS domain_events (
  id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL, equipment_id TEXT NOT NULL,
  correlation_id TEXT, causation_id TEXT, command_id TEXT, occurred_at DATETIME NOT NULL,
  source_json TEXT, data_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_event_correlation ON domain_events(correlation_id,occurred_at);
CREATE INDEX IF NOT EXISTS idx_event_name_time ON domain_events(name,occurred_at DESC);
CREATE TABLE IF NOT EXISTS commands (
  id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL, equipment_id TEXT NOT NULL,
  correlation_id TEXT, causation_id TEXT, status TEXT NOT NULL, created_at DATETIME NOT NULL,
  completed_at DATETIME, parameters_json TEXT, error TEXT
);
CREATE INDEX IF NOT EXISTS idx_command_correlation ON commands(correlation_id,created_at);
CREATE TABLE IF NOT EXISTS alarms (
  equipment_id TEXT NOT NULL, alarm_id TEXT NOT NULL, code TEXT, text TEXT, severity TEXT,
  state TEXT NOT NULL, raised_at DATETIME NOT NULL, cleared_at DATETIME, correlation_id TEXT,
  PRIMARY KEY(equipment_id,alarm_id)
);
CREATE INDEX IF NOT EXISTS idx_alarm_state_time ON alarms(state,raised_at DESC);
`)
	return err
}

func (s *Store) traceWriter() {
	defer close(s.done)
	for value := range s.traceCh {
		metadata, _ := json.Marshal(value.Metadata)
		_, _ = s.db.Exec(`INSERT INTO protocol_traces
            (message_id,equipment_id,direction,occurred_at,stream,function,wait_bit,system_bytes,sml,raw_hex,metadata_json)
            VALUES(?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.EquipmentID, value.Direction, value.Timestamp.UTC(), value.Stream, value.Function, value.Wait, value.SystemBytes, value.SML, value.RawHex, metadata)
	}
}

func (s *Store) applyAlarm(ctx context.Context, value event.Event) error {
	alarmID := fmt.Sprint(value.Data["alarmId"])
	code := fmt.Sprint(value.Data["code"])
	text := fmt.Sprint(value.Data["text"])
	severity := fmt.Sprint(value.Data["severity"])
	if value.Name == "alarm.cleared" {
		_, err := s.db.ExecContext(ctx, `UPDATE alarms SET state='cleared',cleared_at=?,correlation_id=? WHERE equipment_id=? AND alarm_id=?`, value.Timestamp.UTC(), value.CorrelationID, value.EquipmentID, alarmID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO alarms(equipment_id,alarm_id,code,text,severity,state,raised_at,correlation_id)
        VALUES(?,?,?,?,?,'active',?,?) ON CONFLICT(equipment_id,alarm_id) DO UPDATE SET code=excluded.code,text=excluded.text,severity=excluded.severity,state='active',raised_at=excluded.raised_at,cleared_at=NULL,correlation_id=excluded.correlation_id`, value.EquipmentID, alarmID, code, text, severity, value.Timestamp.UTC(), value.CorrelationID)
	return err
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

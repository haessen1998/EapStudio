package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"eapstudio/internal/command"
	"eapstudio/internal/domain"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/event"
)

type HistoryQuery struct {
	Page        int    `json:"page"`
	PageSize    int    `json:"pageSize"`
	EquipmentID string `json:"equipmentId"`
	Search      string `json:"search"`
	Direction   string `json:"direction"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	SinceHours  int    `json:"sinceHours"`
}

type TracePage struct {
	Items []driver.Message `json:"items"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
}

type EventPage struct {
	Items []event.Event `json:"items"`
	Total int64         `json:"total"`
	Page  int           `json:"page"`
}

type CommandPage struct {
	Items []command.Command `json:"items"`
	Total int64             `json:"total"`
	Page  int               `json:"page"`
}

type RetentionResult struct {
	TraceDeleted   int64 `json:"traceDeleted"`
	EventDeleted   int64 `json:"eventDeleted"`
	CommandDeleted int64 `json:"commandDeleted"`
	AlarmDeleted   int64 `json:"alarmDeleted"`
	DatabaseBytes  int64 `json:"databaseBytes"`
}

func normalizeHistoryQuery(query HistoryQuery) HistoryQuery {
	if query.Page < 1 {
		query.Page = 1
	}
	switch query.PageSize {
	case 25, 50, 100, 200:
	default:
		query.PageSize = 25
	}
	if query.SinceHours < 0 {
		query.SinceHours = 0
	}
	return query
}

func commonWhere(query HistoryQuery, timeColumn string) ([]string, []any) {
	var clauses []string
	var args []any
	if query.EquipmentID != "" && query.EquipmentID != "ALL" {
		clauses = append(clauses, "equipment_id = ?")
		args = append(args, query.EquipmentID)
	}
	if query.SinceHours > 0 {
		clauses = append(clauses, timeColumn+" >= ?")
		args = append(args, time.Now().UTC().Add(-time.Duration(query.SinceHours)*time.Hour))
	}
	return clauses, args
}

func whereSQL(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(clauses, " AND ")
}

func (s *Store) QueryTraces(ctx context.Context, input HistoryQuery) (TracePage, error) {
	query := normalizeHistoryQuery(input)
	clauses, args := commonWhere(query, "occurred_at")
	if query.Direction != "" && query.Direction != "ALL" {
		clauses, args = append(clauses, "direction = ?"), append(args, query.Direction)
	}
	if query.Search != "" {
		term := "%" + query.Search + "%"
		clauses, args = append(clauses, "(message_id LIKE ? OR sml LIKE ? OR raw_hex LIKE ? OR ('S' || stream || 'F' || function) LIKE ?)"), append(args, term, term, term, term)
	}
	where := whereSQL(clauses)
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM protocol_traces"+where, args...).Scan(&total); err != nil {
		return TracePage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT message_id,equipment_id,direction,occurred_at,stream,function,wait_bit,system_bytes,sml,raw_hex,metadata_json FROM protocol_traces`+where+` ORDER BY seq DESC LIMIT ? OFFSET ?`, append(args, query.PageSize, (query.Page-1)*query.PageSize)...)
	if err != nil {
		return TracePage{}, err
	}
	defer rows.Close()
	result := TracePage{Total: total, Page: query.Page}
	for rows.Next() {
		var value driver.Message
		var direction string
		var metadataJSON sql.NullString
		if err := rows.Scan(&value.ID, &value.EquipmentID, &direction, &value.Timestamp, &value.Stream, &value.Function, &value.Wait, &value.SystemBytes, &value.SML, &value.RawHex, &metadataJSON); err != nil {
			return TracePage{}, err
		}
		value.Direction = driver.Direction(direction)
		value.Tree = value.SML
		if metadataJSON.Valid {
			_ = json.Unmarshal([]byte(metadataJSON.String), &value.Metadata)
		}
		result.Items = append(result.Items, value)
	}
	return result, rows.Err()
}

func (s *Store) QueryEvents(ctx context.Context, input HistoryQuery) (EventPage, error) {
	query := normalizeHistoryQuery(input)
	clauses, args := commonWhere(query, "occurred_at")
	if query.Name != "" && query.Name != "ALL" {
		clauses, args = append(clauses, "name = ?"), append(args, query.Name)
	}
	if query.Search != "" {
		term := "%" + query.Search + "%"
		clauses, args = append(clauses, "(name LIKE ? OR correlation_id LIKE ? OR data_json LIKE ?)"), append(args, term, term, term)
	}
	where := whereSQL(clauses)
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domain_events"+where, args...).Scan(&total); err != nil {
		return EventPage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,type,name,equipment_id,correlation_id,causation_id,command_id,occurred_at,source_json,data_json FROM domain_events`+where+` ORDER BY occurred_at DESC LIMIT ? OFFSET ?`, append(args, query.PageSize, (query.Page-1)*query.PageSize)...)
	if err != nil {
		return EventPage{}, err
	}
	defer rows.Close()
	result := EventPage{Total: total, Page: query.Page}
	for rows.Next() {
		var value event.Event
		var messageType string
		var sourceJSON, dataJSON sql.NullString
		if err := rows.Scan(&value.ID, &messageType, &value.Name, &value.EquipmentID, &value.CorrelationID, &value.CausationID, &value.CommandID, &value.Timestamp, &sourceJSON, &dataJSON); err != nil {
			return EventPage{}, err
		}
		value.Type = domain.MessageType(messageType)
		_ = json.Unmarshal([]byte(sourceJSON.String), &value.Source)
		_ = json.Unmarshal([]byte(dataJSON.String), &value.Data)
		result.Items = append(result.Items, value)
	}
	return result, rows.Err()
}

func (s *Store) QueryCommands(ctx context.Context, input HistoryQuery) (CommandPage, error) {
	query := normalizeHistoryQuery(input)
	clauses, args := commonWhere(query, "created_at")
	if query.Name != "" && query.Name != "ALL" {
		clauses, args = append(clauses, "name = ?"), append(args, query.Name)
	}
	if query.Status != "" && query.Status != "ALL" {
		clauses, args = append(clauses, "status = ?"), append(args, query.Status)
	}
	if query.Search != "" {
		term := "%" + query.Search + "%"
		clauses, args = append(clauses, "(name LIKE ? OR correlation_id LIKE ? OR parameters_json LIKE ? OR error LIKE ?)"), append(args, term, term, term, term)
	}
	where := whereSQL(clauses)
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM commands"+where, args...).Scan(&total); err != nil {
		return CommandPage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,type,name,equipment_id,correlation_id,causation_id,status,created_at,completed_at,parameters_json,error FROM commands`+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, append(args, query.PageSize, (query.Page-1)*query.PageSize)...)
	if err != nil {
		return CommandPage{}, err
	}
	defer rows.Close()
	result := CommandPage{Total: total, Page: query.Page}
	for rows.Next() {
		var value command.Command
		var messageType, status string
		var completed sql.NullTime
		var parametersJSON sql.NullString
		if err := rows.Scan(&value.ID, &messageType, &value.Name, &value.EquipmentID, &value.CorrelationID, &value.CausationID, &status, &value.CreatedAt, &completed, &parametersJSON, &value.Error); err != nil {
			return CommandPage{}, err
		}
		value.Type, value.Status = domain.MessageType(messageType), command.Status(status)
		if completed.Valid {
			value.CompletedAt = &completed.Time
		}
		_ = json.Unmarshal([]byte(parametersJSON.String), &value.Parameters)
		result.Items = append(result.Items, value)
	}
	return result, rows.Err()
}

func (s *Store) ApplyRetention(ctx context.Context, days int) (RetentionResult, error) {
	if days != 7 && days != 30 && days != 90 && days != 365 {
		return RetentionResult{}, fmt.Errorf("retention days must be 7, 30, 90, or 365")
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetentionResult{}, err
	}
	result := RetentionResult{}
	for _, item := range []struct {
		query  string
		target *int64
	}{
		{"DELETE FROM protocol_traces WHERE occurred_at < ?", &result.TraceDeleted},
		{"DELETE FROM domain_events WHERE occurred_at < ?", &result.EventDeleted},
		{"DELETE FROM commands WHERE created_at < ?", &result.CommandDeleted},
		{"DELETE FROM alarms WHERE state = 'cleared' AND raised_at < ?", &result.AlarmDeleted},
	} {
		value, execErr := tx.ExecContext(ctx, item.query, cutoff)
		if execErr != nil {
			_ = tx.Rollback()
			return RetentionResult{}, execErr
		}
		*item.target, _ = value.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, err
	}
	_, _ = s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if result.TraceDeleted+result.EventDeleted+result.CommandDeleted+result.AlarmDeleted > 0 {
		if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
			return RetentionResult{}, fmt.Errorf("compact history database: %w", err)
		}
	}
	if info, err := os.Stat(s.path); err == nil {
		result.DatabaseBytes = info.Size()
	}
	return result, nil
}

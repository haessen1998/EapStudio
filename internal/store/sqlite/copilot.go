package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"eapstudio/internal/ai"
)

type CopilotPermission struct {
	ID            string         `json:"id"`
	Tool          string         `json:"tool"`
	EquipmentID   string         `json:"equipmentId"`
	Command       string         `json:"command"`
	Summary       string         `json:"summary"`
	Risk          string         `json:"risk"`
	Parameters    map[string]any `json:"parameters"`
	ParameterDiff map[string]any `json:"parameterDiff,omitempty"`
	ExpiresAt     time.Time      `json:"expiresAt,omitempty"`
}

type CopilotMessage struct {
	ID               string             `json:"id"`
	SessionID        string             `json:"sessionId"`
	EquipmentID      string             `json:"equipmentId"`
	Role             string             `json:"role"`
	Text             string             `json:"text"`
	Attachments      []ai.Attachment    `json:"attachments"`
	Evidence         []string           `json:"evidence"`
	Permission       *CopilotPermission `json:"permission,omitempty"`
	PermissionStatus string             `json:"permissionStatus,omitempty"`
	CreatedAt        time.Time          `json:"createdAt"`
}

type CopilotSession struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Scope        string    `json:"scope"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
}

func (s *Store) CreateCopilotSession(ctx context.Context, value CopilotSession) error {
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO copilot_sessions(id,title,scope,created_at,updated_at) VALUES(?,?,?,?,?)`,
		value.ID, value.Title, value.Scope, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	return err
}

func (s *Store) CopilotSessions(ctx context.Context, search string) ([]CopilotSession, error) {
	pattern := "%" + search + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.title,s.scope,s.created_at,s.updated_at,COUNT(m.id)
		FROM copilot_sessions s LEFT JOIN copilot_messages m ON m.session_id=s.id
		WHERE ?='' OR s.title LIKE ? OR s.scope LIKE ?
		GROUP BY s.id ORDER BY s.updated_at DESC`, search, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CopilotSession
	for rows.Next() {
		var value CopilotSession
		if err := rows.Scan(&value.ID, &value.Title, &value.Scope, &value.CreatedAt, &value.UpdatedAt, &value.MessageCount); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) CopilotSessionExists(ctx context.Context, sessionID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM copilot_sessions WHERE id=?)`, sessionID).Scan(&exists)
	return exists == 1, err
}

func (s *Store) UpdateCopilotSessionScope(ctx context.Context, sessionID, scope string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE copilot_sessions SET scope=?,updated_at=? WHERE id=?`, scope, time.Now().UTC(), sessionID)
	return err
}

func (s *Store) TouchCopilotSession(ctx context.Context, sessionID, title string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE copilot_sessions
		SET title=CASE WHEN ?<>'' AND title='New conversation' THEN ? ELSE title END,updated_at=? WHERE id=?`,
		title, title, time.Now().UTC(), sessionID)
	return err
}

func (s *Store) DeleteCopilotSession(ctx context.Context, sessionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM copilot_messages WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM copilot_sessions WHERE id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordCopilotMessage(ctx context.Context, value CopilotMessage) error {
	if value.SessionID == "" {
		value.SessionID = "default"
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	attachments, _ := json.Marshal(value.Attachments)
	evidence, _ := json.Marshal(value.Evidence)
	permission, _ := json.Marshal(value.Permission)
	permissionID := ""
	if value.Permission != nil {
		permissionID = value.Permission.ID
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO copilot_messages
		(id,session_id,equipment_id,role,text,attachments_json,evidence_json,permission_json,permission_id,permission_status,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.SessionID, value.EquipmentID, value.Role, value.Text, attachments, evidence, permission, permissionID, value.PermissionStatus, value.CreatedAt.UTC())
	if err == nil {
		err = s.TouchCopilotSession(ctx, value.SessionID, "")
	}
	return err
}

func (s *Store) CopilotHistory(ctx context.Context, sessionID string, limit int) ([]CopilotMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,equipment_id,role,text,attachments_json,evidence_json,permission_json,permission_status,created_at
		FROM (SELECT *,rowid AS message_rowid FROM copilot_messages WHERE session_id=? ORDER BY created_at DESC,rowid DESC LIMIT ?)
		ORDER BY created_at ASC,message_rowid ASC`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CopilotMessage
	for rows.Next() {
		var value CopilotMessage
		var attachmentsJSON, evidenceJSON, permissionJSON sql.NullString
		if err := rows.Scan(&value.ID, &value.SessionID, &value.EquipmentID, &value.Role, &value.Text, &attachmentsJSON, &evidenceJSON, &permissionJSON, &value.PermissionStatus, &value.CreatedAt); err != nil {
			return nil, err
		}
		if attachmentsJSON.Valid {
			_ = json.Unmarshal([]byte(attachmentsJSON.String), &value.Attachments)
		}
		if evidenceJSON.Valid {
			_ = json.Unmarshal([]byte(evidenceJSON.String), &value.Evidence)
		}
		if permissionJSON.Valid && permissionJSON.String != "null" {
			value.Permission = &CopilotPermission{}
			_ = json.Unmarshal([]byte(permissionJSON.String), value.Permission)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) UpdateCopilotPermission(ctx context.Context, permissionID, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE copilot_messages SET permission_status=? WHERE permission_id=?`, status, permissionID)
	return err
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"eapstudio/internal/ai"
)

type CopilotPermission struct {
	ID          string         `json:"id"`
	Tool        string         `json:"tool"`
	EquipmentID string         `json:"equipmentId"`
	Command     string         `json:"command"`
	Summary     string         `json:"summary"`
	Risk        string         `json:"risk"`
	Parameters  map[string]any `json:"parameters"`
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
	return err
}

func (s *Store) CopilotHistory(ctx context.Context, equipmentID string, limit int) ([]CopilotMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,equipment_id,role,text,attachments_json,evidence_json,permission_json,permission_status,created_at
		FROM (SELECT * FROM copilot_messages WHERE equipment_id=? ORDER BY created_at DESC LIMIT ?)
		ORDER BY created_at ASC`, equipmentID, limit)
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

func (s *Store) ClearCopilotHistory(ctx context.Context, equipmentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM copilot_messages WHERE equipment_id=?`, equipmentID)
	return err
}

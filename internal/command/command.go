package command

import (
	"time"

	"eapstudio/internal/domain"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusSending   Status = "sending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Command is an imperative request. Its Name follows verb.noun ordering.
type Command struct {
	ID            string             `json:"id"`
	Type          domain.MessageType `json:"type"`
	Name          string             `json:"name"`
	EquipmentID   string             `json:"equipmentId"`
	CorrelationID string             `json:"correlationId"`
	CausationID   string             `json:"causationId"`
	Status        Status             `json:"status"`
	CreatedAt     time.Time          `json:"createdAt"`
	CompletedAt   *time.Time         `json:"completedAt,omitempty"`
	Parameters    map[string]any     `json:"parameters"`
	Error         string             `json:"error,omitempty"`
}

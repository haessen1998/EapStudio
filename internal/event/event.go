package event

import (
	"time"

	"eapstudio/internal/domain"
)

// Event is the protocol-independent contract consumed by routes and sinks.
type Event struct {
	ID            string             `json:"id"`
	Type          domain.MessageType `json:"type"`
	Name          string             `json:"name"`
	EquipmentID   string             `json:"equipmentId"`
	CorrelationID string             `json:"correlationId"`
	CausationID   string             `json:"causationId"`
	CommandID     string             `json:"commandId,omitempty"`
	Timestamp     time.Time          `json:"timestamp"`
	Source        Source             `json:"source"`
	Data          map[string]any     `json:"data"`
}

type Source struct {
	Protocol string `json:"protocol"`
	Stream   uint8  `json:"stream"`
	Function uint8  `json:"function"`
	CEID     uint64 `json:"ceid,omitempty"`
}

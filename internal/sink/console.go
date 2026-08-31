package sink

import (
	"context"
	"encoding/json"
	"log"

	"eapstudio/internal/event"
)

type Console struct{ name string }

func NewConsole(name string) *Console { return &Console{name: name} }
func (s *Console) Name() string       { return s.name }
func (s *Console) Send(_ context.Context, value event.Event) error {
	data, _ := json.Marshal(value.Data)
	log.Printf("[%s] EVENT %s -> %s %s", value.EquipmentID, value.Name, s.name, data)
	return nil
}

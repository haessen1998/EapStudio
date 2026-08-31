package automation

import (
	"testing"
	"testing/fstest"

	"eapstudio/internal/domain"
	"eapstudio/internal/event"
)

func TestEngineCreatesCorrelatedCommand(t *testing.T) {
	source := fstest.MapFS{"automations.yaml": {Data: []byte("automations:\n  - name: send_recipe\n    trigger: material.arrived\n    command: send.recipe\n    parameters:\n      recipeId: recipeId\n")}}
	engine, err := Load(source, "automations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	commands := engine.Handle(event.Event{ID: "evt-1", Type: domain.TypeEvent, Name: "material.arrived", EquipmentID: "ETCHER-01", CorrelationID: "flow-1", Data: map[string]any{"recipeId": "ETCH-A"}})
	if len(commands) != 1 {
		t.Fatalf("commands = %#v", commands)
	}
	if commands[0].Type != domain.TypeCommand || commands[0].Name != "send.recipe" || commands[0].CausationID != "evt-1" || commands[0].CorrelationID != "flow-1" {
		t.Fatalf("command = %#v", commands[0])
	}
}

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

func TestEngineMatchesEventAndEquipmentGlobsAdditively(t *testing.T) {
	source := fstest.MapFS{"automations.yaml": {Data: []byte(`automations:
  - name: all_aoi
    trigger: wafer.*
    equipment: [AOI-*]
    command: inspect.wafer
  - name: aoi_01_only
    trigger: wafer.started
    equipment: [AOI-01]
    command: send.recipe
`)}}
	engine, err := Load(source, "automations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base := event.Event{ID: "evt-1", Type: domain.TypeEvent, Name: "wafer.started", EquipmentID: "AOI-01"}
	if got := len(engine.Handle(base)); got != 2 {
		t.Fatalf("AOI-01 commands = %d, want 2", got)
	}
	base.EquipmentID = "AOI-02"
	if got := len(engine.Handle(base)); got != 1 {
		t.Fatalf("AOI-02 commands = %d, want 1", got)
	}
}

func TestReloadReplacesAutomationRules(t *testing.T) {
	source := fstest.MapFS{"automations.yaml": {Data: []byte("automations:\n  - name: before\n    trigger: wafer.started\n    command: send.recipe\n")}}
	engine, err := Load(source, "automations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source["automations.yaml"] = &fstest.MapFile{Data: []byte("automations:\n  - name: after\n    trigger: alarm.raised\n    command: clear.alarm\n")}
	if err := engine.Reload(source, "automations.yaml"); err != nil {
		t.Fatal(err)
	}
	if got := len(engine.Handle(event.Event{ID: "old", Type: domain.TypeEvent, Name: "wafer.started"})); got != 0 {
		t.Fatalf("old rule still active: %d commands", got)
	}
	if got := len(engine.Handle(event.Event{ID: "new", Type: domain.TypeEvent, Name: "alarm.raised"})); got != 1 {
		t.Fatalf("new rule commands = %d", got)
	}
}

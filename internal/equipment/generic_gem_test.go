package equipment

import (
	"context"
	"strings"
	"testing"
	"time"

	"eapstudio/internal/command"
	"eapstudio/internal/domain"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/profile"
)

func TestGenericGemAdapterMapsReportPositions(t *testing.T) {
	compiled, err := profile.Compile(profile.EquipmentProfile{
		APIVersion: "eapstudio/v1alpha1", Kind: "EquipmentProfile", Metadata: profile.Metadata{Name: "test"},
		Spec: profile.Spec{Variables: map[uint64]profile.VariableDefinition{101: {Name: "LotId"}, 102: {Name: "WaferId"}}, Reports: map[uint64]profile.ReportDefinition{2001: {Variables: []uint64{101, 102}}}, Events: map[uint64]profile.EventDefinition{1001: {Name: "wafer.started", Reports: []uint64{2001}, Mapping: map[string]profile.FieldDefinition{"lotId": {Report: 2001, Variable: 101}, "waferId": {Report: 2001, Variable: 102}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	values, err := (GenericGemAdapter{}).Parse(context.Background(), driver.Message{ID: "1", EquipmentID: "ETCHER-01", Timestamp: time.Now(), Stream: 6, Function: 11, CEID: 1001, Reports: map[uint64][]any{2001: {"LOT-001", "WAFER-01"}}}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Name != "wafer.started" || values[0].Type != "event" {
		t.Fatalf("events = %#v", values)
	}
	if got := values[0].Data["waferId"]; got != "WAFER-01" {
		t.Fatalf("waferId = %#v", got)
	}
}

func TestGenericGemAdapterBuildEventReversesProfileMapping(t *testing.T) {
	compiled, err := profile.Compile(profile.EquipmentProfile{
		APIVersion: "eapstudio/v1alpha1", Kind: "EquipmentProfile", Metadata: profile.Metadata{Name: "test"},
		Spec: profile.Spec{Variables: map[uint64]profile.VariableDefinition{201: {Name: "MaterialId"}, 202: {Name: "RecipeId"}}, Reports: map[uint64]profile.ReportDefinition{2101: {Variables: []uint64{201, 202}}}, Events: map[uint64]profile.EventDefinition{1101: {Name: "material.arrived", Reports: []uint64{2101}, Mapping: map[string]profile.FieldDefinition{"materialId": {Report: 2101, Variable: 201}, "recipeId": {Report: 2101, Variable: 202}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := (GenericGemAdapter{}).BuildEvent(context.Background(), "material.arrived", map[string]any{"materialId": "MAT-001", "recipeId": "ETCH-A"}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if message.CEID != 1101 || message.Reports[2101][0] != "MAT-001" || message.Reports[2101][1] != "ETCH-A" {
		t.Fatalf("message = %#v", message)
	}
}

func TestGenericGemAdapterBuildsCommandFromProfile(t *testing.T) {
	compiled, err := profile.Compile(profile.EquipmentProfile{APIVersion: "eapstudio/v1alpha1", Kind: "EquipmentProfile", Metadata: profile.Metadata{Name: "test"}, Spec: profile.Spec{Commands: map[string]profile.CommandDefinition{"send.recipe": {Stream: 7, Function: 3, Wait: true, Parameters: []string{"recipeId"}, SuccessEvent: "recipe.sent", FailureEvent: "recipe.failed"}}}})
	if err != nil {
		t.Fatal(err)
	}
	message, err := (GenericGemAdapter{}).BuildCommand(context.Background(), command.Command{Type: domain.TypeCommand, Name: "send.recipe", Parameters: map[string]any{"recipeId": "ETCH-A"}}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if message.Stream != 7 || message.Function != 3 || !message.Wait || !strings.Contains(message.SML, `<A "ETCH-A">`) {
		t.Fatalf("message = %#v", message)
	}
}

func TestGenericGemAdapterRejectsFailedCommandAck(t *testing.T) {
	successAck := uint8(0)
	compiled, err := profile.Compile(profile.EquipmentProfile{APIVersion: "eapstudio/v1alpha1", Kind: "EquipmentProfile", Metadata: profile.Metadata{Name: "test"}, Spec: profile.Spec{Commands: map[string]profile.CommandDefinition{"send.recipe": {Stream: 7, Function: 3, Wait: true, SuccessEvent: "recipe.sent", FailureEvent: "recipe.failed", SuccessAck: &successAck}}}})
	if err != nil {
		t.Fatal(err)
	}
	failedAck := uint8(1)
	err = (GenericGemAdapter{}).ValidateCommandReply(context.Background(), command.Command{Name: "send.recipe"}, &driver.Message{Stream: 7, Function: 4, Ack: &failedAck}, compiled)
	if err == nil {
		t.Fatal("expected equipment rejection")
	}
}

func TestGenericGemAdapterParsesAlarm(t *testing.T) {
	values, err := (GenericGemAdapter{}).Parse(context.Background(), driver.Message{
		ID: "alarm-1", EquipmentID: "ETCHER-01", Stream: 5, Function: 1,
		Fields: map[string]any{"alarmId": "7001", "code": "128", "text": "Pressure high", "severity": "critical", "active": true},
	}, &profile.CompiledProfile{})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Name != "alarm.raised" || values[0].CorrelationID != "alarm-ETCHER-01-7001" {
		t.Fatalf("alarm event = %#v", values)
	}
}

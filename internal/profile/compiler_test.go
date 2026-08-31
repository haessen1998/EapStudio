package profile

import "testing"

func TestCompileBuildsReportVariablePositions(t *testing.T) {
	compiled, err := Compile(EquipmentProfile{
		APIVersion: "eapstudio/v1alpha1", Kind: "EquipmentProfile", Metadata: Metadata{Name: "test"},
		Spec: Spec{
			Variables: map[uint64]VariableDefinition{101: {Name: "LotId"}, 102: {Name: "WaferId"}},
			Reports:   map[uint64]ReportDefinition{2001: {Variables: []uint64{101, 102}}},
			Events:    map[uint64]EventDefinition{1001: {Name: "wafer.started", Mapping: map[string]FieldDefinition{"waferId": {Report: 2001, Variable: 102}}}},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got := compiled.VariablePositions[2001][102]; got != 1 {
		t.Fatalf("variable position = %d, want 1", got)
	}
	if got := compiled.Metadata.Adapter; got != "generic-gem" {
		t.Fatalf("default adapter = %q", got)
	}
}

func TestCompileRejectsUnknownVariable(t *testing.T) {
	_, err := Compile(EquipmentProfile{APIVersion: "eapstudio/v1alpha1", Kind: "EquipmentProfile", Metadata: Metadata{Name: "bad"}, Spec: Spec{Reports: map[uint64]ReportDefinition{1: {Variables: []uint64{99}}}}})
	if err == nil {
		t.Fatal("Compile() expected an unknown variable error")
	}
}

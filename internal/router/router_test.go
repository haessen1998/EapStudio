package router

import (
	"context"
	"testing"
	"testing/fstest"

	"eapstudio/internal/domain"
	"eapstudio/internal/event"
	"eapstudio/internal/sink"
)

func TestRouteCombinesWildcardAndSpecificEquipmentRulesWithoutDuplicateSink(t *testing.T) {
	source := fstest.MapFS{"routes.yaml": {Data: []byte(`routes:
  - name: common-aoi
    match:
      names: [wafer.*]
      equipment: [AOI-*]
    sinks: [common]
  - name: aoi-01-extra
    match:
      names: [wafer.started]
      equipment: [AOI-01]
    sinks: [common, exact]
`)}}
	common := sink.NewMemory("common")
	exact := sink.NewMemory("exact")
	value := event.Event{ID: "evt-1", Type: domain.TypeEvent, Name: "wafer.started", EquipmentID: "AOI-01"}
	router, err := Load(source, "routes.yaml", common, exact)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Route(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if got := len(common.Deliveries()); got != 1 {
		t.Fatalf("common deliveries = %d, want 1", got)
	}
	if got := len(exact.Deliveries()); got != 1 {
		t.Fatalf("exact deliveries = %d, want 1", got)
	}
}

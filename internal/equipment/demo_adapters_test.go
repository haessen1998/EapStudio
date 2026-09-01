package equipment

import (
	"context"
	"os"
	"testing"

	"eapstudio/internal/profile"
)

func TestDemoAOIV200AdapterTranslatesVendorResultCode(t *testing.T) {
	compiled, err := profile.Load(os.DirFS("../.."), "profiles/demo/aoi-v200.yaml")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("demo-aoi-v200")
	if err != nil {
		t.Fatal(err)
	}
	message, err := adapter.BuildEvent(context.Background(), "inspection.completed", map[string]any{
		"waferId": "WAFER-01", "defectCount": 3, "result": "REVIEW",
	}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if got := message.Reports[3001][2]; got != 1 {
		t.Fatalf("wire result code = %#v", got)
	}
	message.ID, message.EquipmentID = "aoi-message", "AOI-01"
	values, err := adapter.Parse(context.Background(), message, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Data["result"] != "REVIEW" || values[0].Data["requiresReview"] != true {
		t.Fatalf("canonical event = %#v", values)
	}
}

func TestDemoOvenT300AdapterConvertsDeciDegrees(t *testing.T) {
	compiled, err := profile.Load(os.DirFS("../.."), "profiles/demo/oven-t300.yaml")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("demo-oven-t300")
	if err != nil {
		t.Fatal(err)
	}
	message, err := adapter.BuildEvent(context.Background(), "temperature.deviated", map[string]any{
		"batchId": "BATCH-01", "recipe": "BAKE-420", "temperatureC": 438.5,
	}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if got := message.Reports[4001][2]; got != 4385 {
		t.Fatalf("wire temperature = %#v", got)
	}
	message.ID, message.EquipmentID = "oven-message", "OVEN-01"
	values, err := adapter.Parse(context.Background(), message, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Data["temperatureC"] != 438.5 || values[0].Data["temperatureUnit"] != "C" {
		t.Fatalf("canonical event = %#v", values)
	}
}

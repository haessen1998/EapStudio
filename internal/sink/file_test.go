package sink

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eapstudio/internal/event"
)

func TestFileSinkAppendsCanonicalJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	output, err := NewFile("file-events", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"evt-1", "evt-2"} {
		if err := output.Send(context.Background(), event.Event{ID: id, Name: "material.arrived", EquipmentID: "ETCHER-01"}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 2 || !strings.Contains(string(data), `"id":"evt-2"`) {
		t.Fatalf("jsonl = %q", data)
	}
}

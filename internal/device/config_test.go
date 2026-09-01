package device

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveConfigCanReplaceExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.yaml")
	first := Config{Devices: []Definition{{ID: "AOI-01", Badge: "01", Profile: "profiles/aoi.yaml"}}}
	if err := SaveConfig(path, first); err != nil {
		t.Fatal(err)
	}
	second := Config{Devices: []Definition{{ID: "AOI-02", Badge: "B", Profile: "profiles/aoi.yaml", Adapter: "vendor-aoi"}}}
	if err := SaveConfig(path, second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := DecodeConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Devices) != 1 || loaded.Devices[0].ID != "AOI-02" || loaded.Devices[0].Adapter != "vendor-aoi" {
		t.Fatalf("loaded config = %#v", loaded)
	}
}

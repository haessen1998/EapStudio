package device

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
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

func TestMigratePackagedAdaptersOnlyUpgradesMatchingGenericDemo(t *testing.T) {
	source := fstest.MapFS{"configs/devices.yaml": {Data: []byte(`devices:
  - id: AOI-01
    profile: profiles/demo/aoi.yaml
    adapter: demo-aoi
  - id: OVEN-01
    profile: profiles/demo/oven.yaml
    adapter: demo-oven
`)}}
	runtime := Config{Devices: []Definition{
		{ID: "AOI-01", Profile: "profiles/demo/aoi.yaml", Adapter: "generic-gem"},
		{ID: "OVEN-01", Profile: "profiles/demo/oven.yaml", Adapter: "custom-oven"},
	}}
	migrated, changed, err := MigratePackagedAdapters(source, "configs/devices.yaml", runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || migrated.Devices[0].Adapter != "demo-aoi" || migrated.Devices[1].Adapter != "custom-oven" {
		t.Fatalf("migrated = %#v, changed = %v", migrated, changed)
	}
}

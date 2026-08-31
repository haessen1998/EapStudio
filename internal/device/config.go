package device

import (
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Devices []Definition `yaml:"devices" json:"devices"`
}
type Definition struct {
	ID          string           `yaml:"id" json:"id"`
	Name        string           `yaml:"name" json:"name"`
	Profile     string           `yaml:"profile" json:"profile"`
	Driver      string           `yaml:"driver" json:"driver"`
	AutoConnect bool             `yaml:"autoConnect" json:"autoConnect"`
	Connection  ConnectionConfig `yaml:"connection" json:"connection"`
}
type ConnectionConfig struct {
	Protocol  string `yaml:"protocol" json:"protocol"`
	Mode      string `yaml:"mode" json:"mode"`
	Host      string `yaml:"host" json:"host"`
	Port      int    `yaml:"port" json:"port"`
	SessionID uint16 `yaml:"sessionId" json:"sessionId"`
}

func LoadConfig(source fs.FS, path string) (Config, error) {
	data, err := fs.ReadFile(source, path)
	if err != nil {
		return Config{}, fmt.Errorf("read devices: %w", err)
	}
	var config Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode devices: %w", err)
	}
	seen := map[string]bool{}
	for _, device := range config.Devices {
		if device.ID == "" || device.Profile == "" {
			return Config{}, fmt.Errorf("every device requires id and profile")
		}
		if seen[device.ID] {
			return Config{}, fmt.Errorf("duplicate device id %q", device.ID)
		}
		seen[device.ID] = true
	}
	return config, nil
}

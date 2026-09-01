package device

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Devices []Definition `yaml:"devices" json:"devices"`
}
type Definition struct {
	ID          string           `yaml:"id" json:"id"`
	Badge       string           `yaml:"badge,omitempty" json:"badge"`
	Name        string           `yaml:"name" json:"name"`
	Profile     string           `yaml:"profile" json:"profile"`
	Adapter     string           `yaml:"adapter,omitempty" json:"adapter"`
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
	return DecodeConfig(data)
}

func DecodeConfig(data []byte) (Config, error) {
	var config Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode devices: %w", err)
	}
	config = NormalizeConfig(config)
	if err := ValidateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func ValidateConfig(config Config) error {
	seen := map[string]bool{}
	for _, device := range config.Devices {
		if device.ID == "" || device.Profile == "" {
			return fmt.Errorf("every device requires id and profile")
		}
		if seen[device.ID] {
			return fmt.Errorf("duplicate device id %q", device.ID)
		}
		if device.Connection.Port < 0 || device.Connection.Port > 65535 {
			return fmt.Errorf("device %q has invalid port %d", device.ID, device.Connection.Port)
		}
		seen[device.ID] = true
	}
	return nil
}

func NormalizeConfig(config Config) Config {
	for index := range config.Devices {
		if config.Devices[index].Adapter == "" {
			config.Devices[index].Adapter = "generic-gem"
		}
		if config.Devices[index].Driver == "" {
			config.Devices[index].Driver = "simulator"
		}
	}
	return config
}

func LoadRuntimeConfig(source fs.FS, embeddedPath string) (Config, string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, "", err
	}
	path := filepath.Join(dir, "EapStudio", "devices.yaml")
	data, err := os.ReadFile(path)
	if err == nil {
		config, decodeErr := DecodeConfig(data)
		return config, path, decodeErr
	}
	if !os.IsNotExist(err) {
		return Config{}, path, fmt.Errorf("read runtime devices: %w", err)
	}
	config, err := LoadConfig(source, embeddedPath)
	if err != nil {
		return Config{}, path, err
	}
	if err := SaveConfig(path, config); err != nil {
		return Config{}, path, err
	}
	return config, path, nil
}

func SaveConfig(path string, config Config) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("runtime devices config path is unavailable")
	}
	config = NormalizeConfig(config)
	if err := ValidateConfig(config); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode devices: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create devices config directory: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write devices config: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		// Windows does not replace an existing destination with os.Rename.
		// Fall back to a direct write so subsequent Settings saves remain reliable.
		if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace devices config: %w", writeErr)
		}
		_ = os.Remove(temporary)
	}
	return nil
}

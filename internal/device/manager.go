package device

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"sync"

	"eapstudio/internal/automation"
	"eapstudio/internal/command"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/equipment"
	"eapstudio/internal/profile"
	"eapstudio/internal/router"
)

type Manager struct {
	mu       sync.RWMutex
	runtimes map[string]*Runtime
}

func NewManager(source fs.FS, config Config, routes *router.Router, engine *automation.Engine, recorder Recorder, onChange func()) (*Manager, error) {
	manager := &Manager{runtimes: map[string]*Runtime{}}
	profiles := map[string]*profile.CompiledProfile{}
	for _, definition := range config.Devices {
		compiled, ok := profiles[definition.Profile]
		if !ok {
			var err error
			compiled, err = profile.Load(source, definition.Profile)
			if err != nil {
				return nil, err
			}
			profiles[definition.Profile] = compiled
		}
		var protocol driver.Driver
		switch definition.Driver {
		case "simulator", "":
			protocol = driver.NewSimulatorDriver(definition.ID)
		case "go-secs":
			var err error
			protocol, err = driver.NewGoSecsDriver(driver.ConnectionConfig{Host: definition.Connection.Host, Port: definition.Connection.Port, Mode: definition.Connection.Mode, SessionID: definition.Connection.SessionID})
			if err != nil {
				return nil, fmt.Errorf("create driver for %s: %w", definition.ID, err)
			}
		default:
			return nil, fmt.Errorf("unknown driver %q", definition.Driver)
		}
		adapter, err := equipment.NewAdapter(definition.Adapter)
		if err != nil {
			return nil, fmt.Errorf("create adapter for %s: %w", definition.ID, err)
		}
		manager.runtimes[definition.ID] = NewRuntime(definition, compiled, protocol, adapter, routes, engine, recorder, onChange)
	}
	return manager, nil
}

func (m *Manager) Connect(ctx context.Context, id string) error {
	runtime, err := m.runtime(id)
	if err != nil {
		return err
	}
	return runtime.Connect(ctx)
}
func (m *Manager) Disconnect(id string) error {
	runtime, err := m.runtime(id)
	if err != nil {
		return err
	}
	return runtime.Disconnect()
}
func (m *Manager) EmitScenario(id, scenario string) error {
	runtime, err := m.runtime(id)
	if err != nil {
		return err
	}
	return runtime.EmitScenario(scenario)
}
func (m *Manager) SubmitCommand(id, name string, parameters map[string]any, correlationID, causationID string) (command.Command, error) {
	runtime, err := m.runtime(id)
	if err != nil {
		return command.Command{}, err
	}
	return runtime.SubmitCommand(name, parameters, correlationID, causationID)
}
func (m *Manager) ConnectAuto(ctx context.Context, config Config) {
	for _, definition := range config.Devices {
		if definition.AutoConnect {
			_ = m.Connect(ctx, definition.ID)
		}
	}
}
func (m *Manager) Snapshots() []Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Snapshot, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		result = append(result, runtime.Snapshot())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (m *Manager) runtime(id string) (*Runtime, error) {
	m.mu.RLock()
	runtime, ok := m.runtimes[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown device %q", id)
	}
	return runtime, nil
}

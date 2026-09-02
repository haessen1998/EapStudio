package device

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"sync"
	"time"

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
	order    []string
	routes   *router.Router
	engine   *automation.Engine
	recorder Recorder
	onChange func()
}

func NewManager(source fs.FS, config Config, routes *router.Router, engine *automation.Engine, recorder Recorder, onChange func()) (*Manager, error) {
	manager := &Manager{runtimes: map[string]*Runtime{}, order: make([]string, 0, len(config.Devices)), routes: routes, engine: engine, recorder: recorder, onChange: onChange}
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
		manager.order = append(manager.order, definition.ID)
	}
	return manager, nil
}

// ApplyConfig atomically swaps only changed DeviceRuntime instances. Existing
// runtimes retain their connection and live buffers; changed/new runtimes are
// rebuilt from the supplied Profile source and auto-connected.
func (m *Manager) ApplyConfig(source fs.FS, config Config, forceProfiles map[string]bool) error {
	profiles := map[string]*profile.CompiledProfile{}
	m.mu.RLock()
	current := make(map[string]*Runtime, len(m.runtimes))
	for id, runtime := range m.runtimes {
		current[id] = runtime
	}
	m.mu.RUnlock()

	next := make(map[string]*Runtime, len(config.Devices))
	created := make([]*Runtime, 0)
	for _, definition := range config.Devices {
		if runtime, exists := current[definition.ID]; exists && reflect.DeepEqual(runtime.definition, definition) && !forceProfiles[definition.Profile] {
			next[definition.ID] = runtime
			continue
		}
		compiled, ok := profiles[definition.Profile]
		if !ok {
			var err error
			compiled, err = profile.Load(source, definition.Profile)
			if err != nil {
				for _, value := range created {
					value.Shutdown()
				}
				return err
			}
			profiles[definition.Profile] = compiled
		}
		protocol, adapter, err := buildRuntimeDependencies(definition)
		if err != nil {
			for _, value := range created {
				value.Shutdown()
			}
			return err
		}
		runtime := NewRuntime(definition, compiled, protocol, adapter, m.routes, m.engine, m.recorder, m.onChange)
		next[definition.ID] = runtime
		created = append(created, runtime)
	}

	order := make([]string, 0, len(config.Devices))
	for _, definition := range config.Devices {
		order = append(order, definition.ID)
	}
	m.mu.Lock()
	m.runtimes, m.order = next, order
	m.mu.Unlock()
	for id, runtime := range current {
		if next[id] != runtime {
			runtime.Shutdown()
		}
	}
	for _, definition := range config.Devices {
		if definition.AutoConnect && next[definition.ID] != current[definition.ID] {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = next[definition.ID].Connect(ctx)
			cancel()
		}
	}
	return nil
}

func buildRuntimeDependencies(definition Definition) (driver.Driver, equipment.Adapter, error) {
	var protocol driver.Driver
	switch definition.Driver {
	case "simulator", "":
		protocol = driver.NewSimulatorDriver(definition.ID)
	case "go-secs":
		var err error
		protocol, err = driver.NewGoSecsDriver(driver.ConnectionConfig{Host: definition.Connection.Host, Port: definition.Connection.Port, Mode: definition.Connection.Mode, SessionID: definition.Connection.SessionID})
		if err != nil {
			return nil, nil, fmt.Errorf("create driver for %s: %w", definition.ID, err)
		}
	default:
		return nil, nil, fmt.Errorf("unknown driver %q", definition.Driver)
	}
	adapter, err := equipment.NewAdapter(definition.Adapter)
	if err != nil {
		return nil, nil, fmt.Errorf("create adapter for %s: %w", definition.ID, err)
	}
	return protocol, adapter, nil
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
func (m *Manager) SendMessage(ctx context.Context, id string, message driver.Message) (MessageExchange, error) {
	runtime, err := m.runtime(id)
	if err != nil {
		return MessageExchange{}, err
	}
	return runtime.SendMessage(ctx, message)
}
func (m *Manager) ExecuteCommandNow(ctx context.Context, id, name string, parameters map[string]any, correlationID, causationID string) (CommandExchange, error) {
	runtime, err := m.runtime(id)
	if err != nil {
		return CommandExchange{}, err
	}
	return runtime.ExecuteCommandNow(ctx, name, parameters, correlationID, causationID)
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
	for _, id := range m.order {
		if runtime, ok := m.runtimes[id]; ok {
			result = append(result, runtime.Snapshot())
		}
	}
	return result
}

func (m *Manager) SetOrder(order []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := make([]string, 0, len(m.runtimes))
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		if _, exists := m.runtimes[id]; exists && !seen[id] {
			next = append(next, id)
			seen[id] = true
		}
	}
	for _, id := range m.order {
		if !seen[id] {
			next = append(next, id)
		}
	}
	m.order = next
}

func (m *Manager) Close() {
	m.mu.RLock()
	runtimes := make([]*Runtime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	m.mu.RUnlock()
	for _, runtime := range runtimes {
		runtime.Shutdown()
	}
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

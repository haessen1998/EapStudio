package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"eapstudio/internal/ai"
	"eapstudio/internal/automation"
	"eapstudio/internal/device"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/equipment"
	"eapstudio/internal/event"
	"eapstudio/internal/profile"
	"eapstudio/internal/router"
	"eapstudio/internal/sink"
	sqlitestore "eapstudio/internal/store/sqlite"
	"gopkg.in/yaml.v3"
)

type StudioService struct {
	packagedSource       fs.FS
	runtimeMu            sync.RWMutex
	manager              *device.Manager
	router               *router.Router
	config               device.Config
	automation           *automation.Engine
	store                *sqlitestore.Store
	updates              chan struct{}
	aiMu                 sync.RWMutex
	aiConfig             ai.Config
	aiAPIKey             string
	activeAIProfileID    string
	equipmentConfigPath  string
	equipmentConfigMu    sync.Mutex
	ruleSource           fs.FS
	profileSource        fs.FS
	runtimeConfigDir     string
	fileSinkPath         string
	routeConfigPath      string
	automationConfigPath string
	ruleReloadMu         sync.Mutex
	ruleFingerprint      [32]byte
	watchCancel          context.CancelFunc
	appContext           context.Context
	workspaceRoot        string
	activeWorkspaceID    string
	pending              map[string]AIActionPermission
	pendingEquipment     map[string]pendingEquipmentAction
	permissionSequence   atomic.Uint64
	copilotEvents        chan CopilotStreamEvent
}

type WorkspaceSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Active bool   `json:"active"`
}

type workspaceMetadata struct {
	Name string `json:"name"`
}

type StudioSnapshot struct {
	Devices     []device.Snapshot   `json:"devices"`
	Routes      []router.Rule       `json:"routes"`
	Deliveries  []sink.Delivery     `json:"deliveries"`
	Automations []automation.Rule   `json:"automations"`
	Alarms      []sqlitestore.Alarm `json:"alarms"`
	Storage     sqlitestore.Stats   `json:"storage"`
	Generated   time.Time           `json:"generated"`
}

type CopilotReply struct {
	Answer      string              `json:"answer"`
	Evidence    []string            `json:"evidence"`
	Suggestions []string            `json:"suggestions"`
	Permission  *AIActionPermission `json:"permission,omitempty"`
	Tools       []ai.ToolResult     `json:"tools,omitempty"`
}

type CopilotStreamEvent struct {
	RequestID string        `json:"requestId"`
	SessionID string        `json:"sessionId"`
	Delta     string        `json:"delta,omitempty"`
	Done      bool          `json:"done"`
	Reply     *CopilotReply `json:"reply,omitempty"`
}

type AIActionPermission struct {
	ID            string                     `json:"id"`
	Tool          string                     `json:"tool"`
	EquipmentID   string                     `json:"equipmentId"`
	Command       string                     `json:"command"`
	Summary       string                     `json:"summary"`
	Risk          string                     `json:"risk"`
	Parameters    map[string]any             `json:"parameters"`
	ParameterDiff map[string]ParameterChange `json:"parameterDiff"`
	ExpiresAt     time.Time                  `json:"expiresAt"`
	SessionID     string                     `json:"-"`
}

type ParameterChange struct {
	Before any `json:"before,omitempty"`
	After  any `json:"after"`
}

type PermissionPolicy struct {
	Mode       string   `json:"mode"`
	Equipment  []string `json:"equipment"`
	Commands   []string `json:"commands"`
	TTLMinutes int      `json:"ttlMinutes"`
}

type EquipmentCommandRequest struct {
	EquipmentID    string         `json:"equipmentId"`
	Command        string         `json:"command"`
	Parameters     map[string]any `json:"parameters"`
	TimeoutSeconds int            `json:"timeoutSeconds"`
}

type EquipmentMessageRequest struct {
	EquipmentID    string `json:"equipmentId"`
	SML            string `json:"sml"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type EquipmentActionResult struct {
	Status  string                  `json:"status"`
	Message string                  `json:"message"`
	Request *driver.Message         `json:"request,omitempty"`
	Reply   *driver.Message         `json:"reply,omitempty"`
	Command *EquipmentCommandResult `json:"command,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

// EquipmentCommandResult keeps the public binding independent from the command package
// while exposing the identifiers and final status operators need.
type EquipmentCommandResult struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type pendingEquipmentAction struct {
	Kind       string
	Permission AIActionPermission
	Command    EquipmentCommandRequest
	Message    driver.Message
	Timeout    time.Duration
}

func defaultPermissionPolicy() PermissionPolicy {
	return PermissionPolicy{Mode: "ask", Equipment: []string{"*"}, Commands: []string{"*"}, TTLMinutes: 5}
}

func (s *StudioService) PermissionPolicy() (PermissionPolicy, error) {
	value := defaultPermissionPolicy()
	_, err := s.store.LoadSetting(context.Background(), "permission_policy", &value)
	return value, err
}

func (s *StudioService) SavePermissionPolicy(value PermissionPolicy) error {
	if value.Mode != "ask" && value.Mode != "deny" {
		return fmt.Errorf("permission mode must be ask or deny")
	}
	if value.TTLMinutes < 1 || value.TTLMinutes > 60 {
		return fmt.Errorf("permission TTL must be between 1 and 60 minutes")
	}
	if len(value.Equipment) == 0 || len(value.Commands) == 0 {
		return fmt.Errorf("equipment and command patterns are required")
	}
	for _, pattern := range append(append([]string{}, value.Equipment...), value.Commands...) {
		if _, err := pathpkg.Match(pattern, ""); err != nil {
			return fmt.Errorf("invalid permission pattern %q", pattern)
		}
	}
	return s.store.SaveSetting(context.Background(), "permission_policy", value)
}

func NewStudioService(source fs.FS) (*StudioService, error) {
	databasePath, err := sqlitestore.DefaultPath()
	if err != nil {
		return nil, err
	}
	workspaceRoot, workspaceID, configDir, err := initializeWorkspaces(source, filepath.Dir(databasePath))
	if err != nil {
		return nil, err
	}
	config, err := device.LoadConfig(os.DirFS(configDir), "devices.yaml")
	if err != nil {
		return nil, err
	}
	service, err := newStudioServiceWithConfig(source, databasePath, config, filepath.Join(configDir, "devices.yaml"), os.DirFS(configDir), "routes.yaml", "automations.yaml")
	if err != nil {
		return nil, err
	}
	service.workspaceRoot = workspaceRoot
	service.activeWorkspaceID = workspaceID
	return service, nil
}

func initializeWorkspaces(source fs.FS, configRoot string) (string, string, string, error) {
	root := filepath.Join(configRoot, "workspaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", "", "", err
	}
	activePath := filepath.Join(configRoot, "active-workspace")
	active := ""
	if data, err := os.ReadFile(activePath); err == nil {
		active = strings.TrimSpace(string(data))
	}
	if !validWorkspaceID(active) {
		active = "default"
	}
	directory := filepath.Join(root, active)
	if _, err := os.Stat(filepath.Join(directory, "devices.yaml")); os.IsNotExist(err) {
		if err := initializeWorkspaceDirectory(source, configRoot, directory, "Default Factory Line", true); err != nil {
			return "", "", "", err
		}
	}
	if err := migrateWorkspaceDevices(source, directory); err != nil {
		return "", "", "", err
	}
	if err := migrateWorkspaceProfiles(directory); err != nil {
		return "", "", "", err
	}
	if err := os.WriteFile(activePath, []byte(active+"\n"), 0o600); err != nil {
		return "", "", "", err
	}
	return root, active, directory, nil
}

func migrateWorkspaceDevices(source fs.FS, directory string) error {
	path := filepath.Join(directory, "devices.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw struct {
		Devices []struct {
			ID   string  `yaml:"id"`
			Role *string `yaml:"role"`
		} `yaml:"devices"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("inspect legacy devices: %w", err)
	}
	missingRole := map[string]bool{}
	for _, definition := range raw.Devices {
		missingRole[definition.ID] = definition.Role == nil
	}
	config, err := device.DecodeConfig(data)
	if err != nil {
		return err
	}
	config, adapterChanged, err := device.MigratePackagedAdapters(source, "configs/devices.yaml", config)
	if err != nil {
		return err
	}
	changed := adapterChanged
	for index := range config.Devices {
		definition := &config.Devices[index]
		if !missingRole[definition.ID] {
			continue
		}
		changed = true
		if definition.Driver == "simulator" {
			// Before workspaces, simulator meant an in-process fake and its HSMS
			// address was unused. Upgrade it to a real passive Equipment Twin.
			definition.Role = "equipment-simulator"
			definition.Connection.Mode = "passive"
			definition.Connection.Host = "0.0.0.0"
		} else {
			definition.Role = "controller"
		}
	}
	if !changed {
		return nil
	}
	if err := preserveLegacyFile(path, data); err != nil {
		return err
	}
	return device.SaveConfig(path, config)
}

func preserveLegacyFile(path string, data []byte) error {
	backup := path + ".legacy.bak"
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(backup, data, 0o600)
}

func migrateWorkspaceProfiles(directory string) error {
	root := filepath.Join(directory, "profiles")
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), "\n  simulator:") {
			return err
		}
		compiled, err := profile.Decode(data)
		if err != nil {
			return err
		}
		encoded, err := yaml.Marshal(compiled.EquipmentProfile)
		if err != nil {
			return err
		}
		if err := preserveLegacyFile(path, data); err != nil {
			return err
		}
		temporary := path + ".migration.tmp"
		if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			if writeErr := os.WriteFile(path, encoded, 0o600); writeErr != nil {
				_ = os.Remove(temporary)
				return writeErr
			}
			_ = os.Remove(temporary)
		}
		return nil
	})
}

func initializeWorkspaceDirectory(source fs.FS, legacyRoot, directory, name string, migrateLegacy bool) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	for _, item := range []struct{ packagedPath, name string }{{"configs/devices.yaml", "devices.yaml"}, {"configs/routes.yaml", "routes.yaml"}, {"configs/automations.yaml", "automations.yaml"}} {
		seedSource, seedPath := source, item.packagedPath
		if migrateLegacy {
			if _, err := os.Stat(filepath.Join(legacyRoot, item.name)); err == nil {
				seedSource, seedPath = os.DirFS(legacyRoot), item.name
			}
		}
		if err := materializeRuntimeFile(seedSource, seedPath, filepath.Join(directory, item.name)); err != nil {
			return err
		}
	}
	profileSource := source
	if migrateLegacy {
		if info, err := os.Stat(filepath.Join(legacyRoot, "profiles")); err == nil && info.IsDir() {
			profileSource = os.DirFS(legacyRoot)
		}
	}
	if err := materializeRuntimeTree(profileSource, "profiles", directory); err != nil {
		return err
	}
	if err := ensureRuntimeFileSinkRoute(filepath.Join(directory, "routes.yaml")); err != nil {
		return err
	}
	metadata, _ := json.MarshalIndent(workspaceMetadata{Name: name}, "", "  ")
	return os.WriteFile(filepath.Join(directory, "workspace.json"), metadata, 0o600)
}

func validWorkspaceID(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func ensureRuntimeFileSinkRoute(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "canonical-file-audit") {
		return nil
	}
	addition := "\n  - name: canonical-file-audit\n    match:\n      names: [\"*\"]\n      equipment: [\"*\"]\n    sinks: [file-events]\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.WriteString(addition); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func newStudioService(source fs.FS, databasePath string) (*StudioService, error) {
	config, err := device.LoadConfig(source, "configs/devices.yaml")
	if err != nil {
		return nil, err
	}
	// The package-local constructor is used by deterministic tests. Production
	// uses NewStudioService, where simulator runtimes bind real HSMS ports.
	for index := range config.Devices {
		if config.Devices[index].Driver == "simulator" {
			config.Devices[index].Driver = "simulator-memory"
			config.Devices[index].Role = "controller"
		}
	}
	return newStudioServiceWithConfig(source, databasePath, config, "", source, "configs/routes.yaml", "configs/automations.yaml")
}

func newStudioServiceWithConfig(source fs.FS, databasePath string, config device.Config, configPath string, ruleSource fs.FS, routePath string, automationPath string) (*StudioService, error) {
	mockMQ := sink.NewMemory("mock-mq")
	qualityMQ := sink.NewMemory("quality-mq")
	thermalMQ := sink.NewMemory("thermal-mq")
	configDir := filepath.Dir(configPath)
	if configPath == "" {
		configDir = filepath.Dir(databasePath)
	}
	fileOutput, err := sink.NewFile("file-events", filepath.Join(configDir, "events", "canonical-events.jsonl"))
	if err != nil {
		return nil, err
	}
	history, err := sqlitestore.Open(databasePath)
	if err != nil {
		return nil, err
	}
	routes, err := router.Load(ruleSource, routePath, mockMQ, qualityMQ, thermalMQ, fileOutput, history)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	engine, err := automation.Load(ruleSource, automationPath)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	service := &StudioService{packagedSource: source, router: routes, config: config, automation: engine, store: history, updates: make(chan struct{}, 1), aiConfig: ai.Config{Provider: "local"}, equipmentConfigPath: configPath, ruleSource: ruleSource, profileSource: ruleSource, runtimeConfigDir: configDir, fileSinkPath: fileOutput.Path(), routeConfigPath: routePath, automationConfigPath: automationPath, pending: map[string]AIActionPermission{}, pendingEquipment: map[string]pendingEquipmentAction{}, copilotEvents: make(chan CopilotStreamEvent, 1024)}
	if err := service.loadStoredAIConfig(); err != nil {
		_ = history.Close()
		return nil, err
	}
	manager, err := device.NewManager(ruleSource, config, routes, engine, history, service.notify)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	service.manager = manager
	return service, nil
}

func materializeRuntimeTree(source fs.FS, root, targetRoot string) error {
	return fs.WalkDir(source, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		return materializeRuntimeFile(source, path, filepath.Join(targetRoot, filepath.FromSlash(path)))
	})
}

func (s *StudioService) FileSinkPath() string { return s.fileSinkPath }

func materializeRuntimeFile(source fs.FS, embeddedPath string, targetPath string) error {
	if _, err := os.Stat(targetPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect runtime config %s: %w", targetPath, err)
	}
	data, err := fs.ReadFile(source, embeddedPath)
	if err != nil {
		return fmt.Errorf("read embedded config %s: %w", embeddedPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create runtime config directory: %w", err)
	}
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		return fmt.Errorf("write runtime config %s: %w", targetPath, err)
	}
	return nil
}

func (s *StudioService) ListWorkspaces() ([]WorkspaceSummary, error) {
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return nil, err
	}
	result := make([]WorkspaceSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validWorkspaceID(entry.Name()) {
			continue
		}
		directory := filepath.Join(s.workspaceRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(directory, "devices.yaml")); err != nil {
			continue
		}
		metadata := workspaceMetadata{Name: entry.Name()}
		if data, err := os.ReadFile(filepath.Join(directory, "workspace.json")); err == nil {
			_ = json.Unmarshal(data, &metadata)
		}
		result = append(result, WorkspaceSummary{ID: entry.Name(), Name: metadata.Name, Path: directory, Active: entry.Name() == s.activeWorkspaceID})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Active != result[j].Active {
			return result[i].Active
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func workspaceSlug(name string) string {
	var result strings.Builder
	separator := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			result.WriteRune(char)
			separator = false
		} else if result.Len() > 0 && !separator {
			result.WriteByte('-')
			separator = true
		}
	}
	value := strings.Trim(result.String(), "-")
	if value == "" {
		value = "workspace"
	}
	return value
}

func (s *StudioService) CreateWorkspace(name string) (WorkspaceSummary, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return WorkspaceSummary{}, fmt.Errorf("workspace name is required")
	}
	base := workspaceSlug(name)
	id := base
	for sequence := 2; ; sequence++ {
		if _, err := os.Stat(filepath.Join(s.workspaceRoot, id)); os.IsNotExist(err) {
			break
		}
		id = fmt.Sprintf("%s-%d", base, sequence)
	}
	directory := filepath.Join(s.workspaceRoot, id)
	if err := initializeWorkspaceDirectory(s.packagedSource, "", directory, name, false); err != nil {
		_ = os.RemoveAll(directory)
		return WorkspaceSummary{}, err
	}
	return WorkspaceSummary{ID: id, Name: name, Path: directory}, nil
}

func (s *StudioService) DeleteWorkspace(id string) error {
	if !validWorkspaceID(id) {
		return fmt.Errorf("invalid workspace id")
	}
	if id == s.activeWorkspaceID {
		return fmt.Errorf("switch to another workspace before deleting the active workspace")
	}
	target := filepath.Clean(filepath.Join(s.workspaceRoot, id))
	root := filepath.Clean(s.workspaceRoot) + string(os.PathSeparator)
	if !strings.HasPrefix(target+string(os.PathSeparator), root) {
		return fmt.Errorf("workspace path escapes the workspace root")
	}
	return os.RemoveAll(target)
}

func (s *StudioService) SwitchWorkspace(id string) (WorkspaceSummary, error) {
	if !validWorkspaceID(id) {
		return WorkspaceSummary{}, fmt.Errorf("invalid workspace id")
	}
	directory := filepath.Join(s.workspaceRoot, id)
	if err := migrateWorkspaceDevices(s.packagedSource, directory); err != nil {
		return WorkspaceSummary{}, err
	}
	if err := migrateWorkspaceProfiles(directory); err != nil {
		return WorkspaceSummary{}, err
	}
	config, err := device.LoadConfig(os.DirFS(directory), "devices.yaml")
	if err != nil {
		return WorkspaceSummary{}, err
	}
	fileOutput, err := sink.NewFile("file-events", filepath.Join(directory, "events", "canonical-events.jsonl"))
	if err != nil {
		return WorkspaceSummary{}, err
	}
	routes, err := router.Load(os.DirFS(directory), "routes.yaml", sink.NewMemory("mock-mq"), sink.NewMemory("quality-mq"), sink.NewMemory("thermal-mq"), fileOutput, s.store)
	if err != nil {
		return WorkspaceSummary{}, err
	}
	engine, err := automation.Load(os.DirFS(directory), "automations.yaml")
	if err != nil {
		return WorkspaceSummary{}, err
	}
	manager, err := device.NewManager(os.DirFS(directory), config, routes, engine, s.store, s.notify)
	if err != nil {
		return WorkspaceSummary{}, err
	}
	activePath := filepath.Join(filepath.Dir(s.workspaceRoot), "active-workspace")
	if err := os.WriteFile(activePath, []byte(id+"\n"), 0o600); err != nil {
		manager.Close()
		return WorkspaceSummary{}, err
	}

	s.runtimeMu.Lock()
	oldManager := s.manager
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	s.manager, s.router, s.automation, s.config = manager, routes, engine, config
	s.equipmentConfigPath = filepath.Join(directory, "devices.yaml")
	s.ruleSource, s.profileSource = os.DirFS(directory), os.DirFS(directory)
	s.runtimeConfigDir, s.fileSinkPath = directory, fileOutput.Path()
	s.routeConfigPath, s.automationConfigPath = "routes.yaml", "automations.yaml"
	s.activeWorkspaceID = id
	appContext := s.appContext
	s.runtimeMu.Unlock()
	s.aiMu.Lock()
	// A one-shot write permission is grounded in the runtime that produced it.
	// Never carry permission cards across a workspace boundary.
	s.pending = map[string]AIActionPermission{}
	s.pendingEquipment = map[string]pendingEquipmentAction{}
	s.aiMu.Unlock()
	if oldManager != nil {
		oldManager.Close()
	}
	if appContext != nil {
		s.startRuntime(appContext)
	}
	s.notify()
	workspaces, _ := s.ListWorkspaces()
	for _, value := range workspaces {
		if value.ID == id {
			return value, nil
		}
	}
	return WorkspaceSummary{ID: id, Name: id, Path: directory, Active: true}, nil
}

func (s *StudioService) startRuntime(ctx context.Context) {
	s.runtimeMu.RLock()
	manager, config := s.manager, s.config
	s.runtimeMu.RUnlock()
	manager.ConnectAuto(ctx, config)
	watchCtx, cancel := context.WithCancel(ctx)
	s.runtimeMu.Lock()
	s.watchCancel = cancel
	s.runtimeMu.Unlock()
	if fingerprint, err := s.rulesFingerprint(); err == nil {
		s.ruleReloadMu.Lock()
		s.ruleFingerprint = fingerprint
		s.ruleReloadMu.Unlock()
	}
	go s.watchRules(watchCtx)
}

func (s *StudioService) start(ctx context.Context) {
	s.runtimeMu.Lock()
	s.appContext = ctx
	s.runtimeMu.Unlock()
	s.startRuntime(ctx)
}
func (s *StudioService) updateSignal() <-chan struct{} { return s.updates }
func (s *StudioService) copilotEventSignal() <-chan CopilotStreamEvent {
	return s.copilotEvents
}
func (s *StudioService) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

func (s *StudioService) Snapshot() StudioSnapshot {
	s.runtimeMu.RLock()
	manager, routes, engine := s.manager, s.router, s.automation
	s.runtimeMu.RUnlock()
	alarms, _ := s.store.Alarms(context.Background(), 200)
	return StudioSnapshot{Devices: manager.Snapshots(), Routes: routes.Rules(), Deliveries: routes.Deliveries(), Automations: engine.Rules(), Alarms: alarms, Storage: s.store.Stats(context.Background()), Generated: time.Now()}
}

func (s *StudioService) close() error {
	s.runtimeMu.Lock()
	if s.watchCancel != nil {
		s.watchCancel()
	}
	manager := s.manager
	s.manager = nil
	s.runtimeMu.Unlock()
	if manager != nil {
		manager.Close()
	}
	return s.store.Close()
}

func (s *StudioService) ConnectDevice(id string) error {
	s.runtimeMu.RLock()
	manager := s.manager
	timeout := 10 * time.Second
	for _, definition := range s.config.Devices {
		if definition.ID == id {
			timeout = time.Duration(definition.Connection.ConnectTimeoutSeconds) * time.Second
			break
		}
	}
	s.runtimeMu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return manager.Connect(ctx, id)
}
func (s *StudioService) DisconnectDevice(id string) error {
	s.runtimeMu.RLock()
	manager := s.manager
	defer s.runtimeMu.RUnlock()
	return manager.Disconnect(id)
}
func (s *StudioService) EmitSimulatorScenario(id string, scenario string) error {
	return s.EmitScenario(id, scenario)
}
func (s *StudioService) EmitScenario(id string, scenario string) error {
	s.runtimeMu.RLock()
	manager := s.manager
	defer s.runtimeMu.RUnlock()
	return manager.EmitScenario(id, scenario)
}

// PrepareEquipmentCommand validates a Profile command and creates a one-shot
// permission card. Nothing is sent until ResolveEquipmentAction is allowed.
func (s *StudioService) PrepareEquipmentCommand(request EquipmentCommandRequest) (AIActionPermission, error) {
	selected := s.selectedEquipment(strings.TrimSpace(request.EquipmentID))
	if selected == nil {
		return AIActionPermission{}, fmt.Errorf("equipment %q was not found", request.EquipmentID)
	}
	if selected.State != driver.StateSelected {
		return AIActionPermission{}, fmt.Errorf("device %s is not selected", selected.ID)
	}
	if selected.Role != "controller" {
		return AIActionPermission{}, fmt.Errorf("device %s is an Equipment Twin; Profile commands are Host → Equipment", selected.ID)
	}
	var definition *device.AvailableCommand
	for index := range selected.Available {
		if selected.Available[index].Name == request.Command {
			definition = &selected.Available[index]
			break
		}
	}
	if definition == nil {
		return AIActionPermission{}, fmt.Errorf("profile %s has no command %q", selected.ProfileName, request.Command)
	}
	if request.Parameters == nil {
		request.Parameters = map[string]any{}
	}
	for _, name := range definition.Parameters {
		if _, ok := request.Parameters[name]; !ok {
			return AIActionPermission{}, fmt.Errorf("command %q requires parameter %q", request.Command, name)
		}
	}
	policy, err := s.PermissionPolicy()
	if err != nil {
		return AIActionPermission{}, err
	}
	if policy.Mode == "deny" || !matchesPolicy(policy.Equipment, selected.ID) || !matchesPolicy(policy.Commands, request.Command) {
		return AIActionPermission{}, fmt.Errorf("%s to %s is outside the configured write allowlist", request.Command, selected.ID)
	}
	timeout := normalizeSendTimeout(request.TimeoutSeconds)
	request.TimeoutSeconds = int(timeout / time.Second)
	id := fmt.Sprintf("permission-%06d", s.permissionSequence.Add(1))
	permission := AIActionPermission{
		ID: id, Tool: "send.command", EquipmentID: selected.ID, Command: request.Command,
		Summary:    fmt.Sprintf("Send Profile command %s as S%dF%d", request.Command, definition.Stream, definition.Function),
		Risk:       "This sends a message to the selected equipment and may change equipment state.",
		Parameters: request.Parameters, ParameterDiff: s.commandParameterDiff(selected.ID, request.Command, request.Parameters),
		ExpiresAt: time.Now().Add(time.Duration(policy.TTLMinutes) * time.Minute),
	}
	s.aiMu.Lock()
	s.pendingEquipment[id] = pendingEquipmentAction{Kind: "command", Permission: permission, Command: request, Timeout: timeout}
	s.aiMu.Unlock()
	return permission, nil
}

// PrepareEquipmentMessage validates complete SML and creates a one-shot
// permission card for an expert/raw SECS send.
func (s *StudioService) PrepareEquipmentMessage(request EquipmentMessageRequest) (AIActionPermission, error) {
	selected := s.selectedEquipment(strings.TrimSpace(request.EquipmentID))
	if selected == nil {
		return AIActionPermission{}, fmt.Errorf("equipment %q was not found", request.EquipmentID)
	}
	if selected.State != driver.StateSelected {
		return AIActionPermission{}, fmt.Errorf("device %s is not selected", selected.ID)
	}
	request.SML = strings.TrimSpace(request.SML)
	if len(request.SML) > 256<<10 {
		return AIActionPermission{}, fmt.Errorf("outbound SML exceeds 256 KiB")
	}
	message, err := driver.ParseOutboundSML(request.SML)
	if err != nil {
		return AIActionPermission{}, err
	}
	name := message.Name()
	policy, err := s.PermissionPolicy()
	if err != nil {
		return AIActionPermission{}, err
	}
	if policy.Mode == "deny" || !matchesPolicy(policy.Equipment, selected.ID) || !matchesPolicy(policy.Commands, name) {
		return AIActionPermission{}, fmt.Errorf("%s to %s is outside the configured write allowlist", name, selected.ID)
	}
	timeout := normalizeSendTimeout(request.TimeoutSeconds)
	id := fmt.Sprintf("permission-%06d", s.permissionSequence.Add(1))
	parameters := map[string]any{"wait": message.Wait, "timeoutSeconds": int(timeout / time.Second), "sml": request.SML}
	direction, summaryTarget := "Host → Equipment", "equipment"
	if selected.Role == "equipment-simulator" {
		direction, summaryTarget = "Equipment → Host", "connected Host"
	}
	permission := AIActionPermission{
		ID: id, Tool: "send.secs-message", EquipmentID: selected.ID, Command: name,
		Summary:    fmt.Sprintf("Send raw %s%s to %s (%s)", name, map[bool]string{true: " W"}[message.Wait], summaryTarget, direction),
		Risk:       "Raw SML bypasses Profile semantics. Verify direction, stream, function, W bit, item types, and values before allowing.",
		Parameters: parameters, ParameterDiff: map[string]ParameterChange{},
		ExpiresAt: time.Now().Add(time.Duration(policy.TTLMinutes) * time.Minute),
	}
	s.aiMu.Lock()
	s.pendingEquipment[id] = pendingEquipmentAction{Kind: "message", Permission: permission, Message: message, Timeout: timeout}
	s.aiMu.Unlock()
	return permission, nil
}

func normalizeSendTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 30
	}
	if seconds > 120 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func (s *StudioService) commandParameterDiff(equipmentID, name string, parameters map[string]any) map[string]ParameterChange {
	diff := map[string]ParameterChange{}
	var previous map[string]any
	if history, err := s.store.QueryCommands(context.Background(), sqlitestore.HistoryQuery{Page: 1, PageSize: 1, EquipmentID: equipmentID, Name: name}); err == nil && len(history.Items) > 0 {
		previous = history.Items[0].Parameters
	}
	for key, after := range parameters {
		var before any
		if previous != nil {
			before = previous[key]
		}
		if !reflect.DeepEqual(before, after) {
			diff[key] = ParameterChange{Before: before, After: after}
		}
	}
	return diff
}

// ResolveEquipmentAction consumes a prepared action exactly once. Send errors
// are returned in the result so a received negative reply remains visible.
func (s *StudioService) ResolveEquipmentAction(permissionID string, allow bool) (EquipmentActionResult, error) {
	s.aiMu.Lock()
	action, ok := s.pendingEquipment[permissionID]
	if ok {
		delete(s.pendingEquipment, permissionID)
	}
	s.aiMu.Unlock()
	if !ok {
		return EquipmentActionResult{}, fmt.Errorf("permission request does not exist or was already resolved")
	}
	if time.Now().After(action.Permission.ExpiresAt) {
		return EquipmentActionResult{Status: "expired", Message: "Permission expired; nothing was sent."}, nil
	}
	if !allow {
		return EquipmentActionResult{Status: "denied", Message: "Permission denied; nothing was sent."}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), action.Timeout)
	defer cancel()
	s.runtimeMu.RLock()
	manager := s.manager
	defer s.runtimeMu.RUnlock()
	if action.Kind == "command" {
		exchange, err := manager.ExecuteCommandNow(ctx, action.Command.EquipmentID, action.Command.Command, action.Command.Parameters, "operator-"+permissionID, permissionID)
		result := EquipmentActionResult{
			Status: string(exchange.Command.Status), Message: fmt.Sprintf("%s completed with status %s", exchange.Command.Name, exchange.Command.Status),
			Request: &exchange.Request, Reply: exchange.Reply,
			Command: &EquipmentCommandResult{ID: exchange.Command.ID, Name: exchange.Command.Name, Status: string(exchange.Command.Status)},
		}
		if err != nil {
			result.Status, result.Error = "failed", err.Error()
			result.Message = "Command failed: " + err.Error()
		}
		return result, nil
	}
	exchange, err := manager.SendMessage(ctx, action.Permission.EquipmentID, action.Message)
	result := EquipmentActionResult{Status: "succeeded", Message: "Message sent.", Request: &exchange.Request, Reply: exchange.Reply}
	if action.Message.Wait && exchange.Reply != nil {
		result.Message = fmt.Sprintf("Message sent; received %s.", exchange.Reply.Name())
	} else if action.Message.Wait {
		result.Message = "Message sent, but no secondary reply was returned."
	}
	if err != nil {
		result.Status, result.Error = "failed", err.Error()
		result.Message = "Message failed: " + err.Error()
	}
	return result, nil
}

func (s *StudioService) AIConfig() ai.Config {
	s.aiMu.RLock()
	defer s.aiMu.RUnlock()
	return s.aiConfig
}

func (s *StudioService) ConfigureAI(config ai.Config, apiKey string) error {
	switch config.Provider {
	case "local", "responses", "chat":
	default:
		return fmt.Errorf("unsupported AI provider %q", config.Provider)
	}
	s.aiMu.Lock()
	s.aiConfig = config
	s.aiAPIKey = strings.TrimSpace(apiKey)
	s.aiMu.Unlock()
	return nil
}

func (s *StudioService) ActiveAIProfileID() string {
	s.aiMu.RLock()
	defer s.aiMu.RUnlock()
	return s.activeAIProfileID
}

func (s *StudioService) ActivateAIProfile(id string) error {
	profiles, err := s.store.AIProfiles(context.Background())
	if err != nil {
		return err
	}
	for _, value := range profiles {
		if value.ID != id {
			continue
		}
		if err := s.ConfigureAI(ai.Config{Provider: value.Provider, BaseURL: value.BaseURL, Model: value.Model}, value.APIKey); err != nil {
			return err
		}
		s.aiMu.Lock()
		s.activeAIProfileID = value.ID
		s.aiMu.Unlock()
		return nil
	}
	return fmt.Errorf("AI profile %q was not found", id)
}

type AIProfileConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"baseURL"`
	Model     string `json:"model"`
	APIKey    string `json:"apiKey,omitempty"`
	HasAPIKey bool   `json:"hasApiKey"`
	IsDefault bool   `json:"isDefault"`
}

func (s *StudioService) ListAIProfiles() ([]AIProfileConfig, error) {
	stored, err := s.store.AIProfiles(context.Background())
	if err != nil {
		return nil, err
	}
	result := make([]AIProfileConfig, 0, len(stored))
	for _, value := range stored {
		result = append(result, AIProfileConfig{ID: value.ID, Name: value.Name, Provider: value.Provider, BaseURL: value.BaseURL, Model: value.Model, HasAPIKey: value.HasAPIKey, IsDefault: value.IsDefault})
	}
	return result, nil
}

func (s *StudioService) SaveAIProfiles(profiles []AIProfileConfig, defaultID string) error {
	stored := make([]sqlitestore.AIProfile, 0, len(profiles))
	for _, value := range profiles {
		if value.Provider != "local" && value.Provider != "responses" && value.Provider != "chat" {
			return fmt.Errorf("unsupported AI provider %q", value.Provider)
		}
		stored = append(stored, sqlitestore.AIProfile{ID: value.ID, Name: value.Name, Provider: value.Provider, BaseURL: value.BaseURL, Model: value.Model, APIKey: strings.TrimSpace(value.APIKey), IsDefault: value.ID == defaultID})
	}
	if err := s.store.SaveAIProfiles(context.Background(), stored, defaultID); err != nil {
		return err
	}
	return s.loadStoredAIConfig()
}

func (s *StudioService) loadStoredAIConfig() error {
	profiles, err := s.store.AIProfiles(context.Background())
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		profiles = []sqlitestore.AIProfile{
			{ID: "local", Name: "Local grounded", Provider: "local", IsDefault: true},
			{ID: "responses", Name: "OpenAI Responses", Provider: "responses", BaseURL: "https://api.openai.com/v1", Model: "gpt-5.6-luna"},
			{ID: "chat", Name: "Chat compatible", Provider: "chat", BaseURL: "https://api.openai.com/v1", Model: "gpt-5.6-luna"},
		}
		if err := s.store.SaveAIProfiles(context.Background(), profiles, "local"); err != nil {
			return err
		}
	}
	for _, value := range profiles {
		if value.IsDefault {
			return s.ActivateAIProfile(value.ID)
		}
	}
	return nil
}

func (s *StudioService) TestAIConfiguration(config ai.Config, apiKey string) (string, error) {
	if config.Provider == "local" {
		return "Local grounded provider is ready.", nil
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" && config == s.AIConfig() {
		s.aiMu.RLock()
		apiKey = s.aiAPIKey
		s.aiMu.RUnlock()
	}
	if apiKey == "" {
		apiKey = os.Getenv("EAPSTUDIO_AI_API_KEY")
	}
	provider, err := ai.NewProvider(config, apiKey)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	answer, err := provider.Complete(ctx, ai.Request{System: "Return a short connection status only.", Prompt: "Reply with: EapStudio AI connection ready."})
	if err != nil {
		return "", err
	}
	return answer, nil
}

type EquipmentConfigSaveResult struct {
	Path            string `json:"path"`
	RestartRequired bool   `json:"restartRequired"`
}

func (s *StudioService) EquipmentConfigPath() string { return s.equipmentConfigPath }

func (s *StudioService) SaveEquipmentConfig(config device.Config) (EquipmentConfigSaveResult, error) {
	s.equipmentConfigMu.Lock()
	defer s.equipmentConfigMu.Unlock()
	config = device.NormalizeConfig(config)
	if err := device.ValidateConfig(config); err != nil {
		return EquipmentConfigSaveResult{}, err
	}
	if err := s.manager.ApplyConfig(s.profileSource, config, nil); err != nil {
		return EquipmentConfigSaveResult{}, err
	}
	if err := device.SaveConfig(s.equipmentConfigPath, config); err != nil {
		return EquipmentConfigSaveResult{}, err
	}
	s.config = config
	return EquipmentConfigSaveResult{Path: s.equipmentConfigPath, RestartRequired: false}, nil
}

type ProfileSummary struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Vendor  string `json:"vendor"`
	Model   string `json:"model"`
	Version string `json:"version"`
	Adapter string `json:"adapter"`
	Valid   bool   `json:"valid"`
	Error   string `json:"error,omitempty"`
}

type ProfileDocument struct {
	Path string `json:"path"`
	YAML string `json:"yaml"`
}

type ProfileValidation struct {
	Valid    bool           `json:"valid"`
	Error    string         `json:"error,omitempty"`
	Summary  ProfileSummary `json:"summary"`
	Warnings []string       `json:"warnings"`
}

type ProfilePreview struct {
	Message driver.Message `json:"message"`
	Events  []event.Event  `json:"events"`
}

type ProfileSaveResult struct {
	Path            string   `json:"path"`
	ReloadedDevices []string `json:"reloadedDevices"`
}

type MessageCatalogItem struct {
	Stream      int    `json:"stream"`
	Function    int    `json:"function"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Primary     bool   `json:"primary"`
	SML         string `json:"sml"`
}

type MessageTemplateSaveRequest struct {
	ProfilePath string `json:"profilePath"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	SML         string `json:"sml"`
}

func (s *StudioService) MessageCatalog() []MessageCatalogItem {
	known := map[string][2]string{
		"S1F1":  {"Are You Online", "Establish application-level communication"},
		"S1F3":  {"Selected Equipment Status Request", "Request status variable values"},
		"S1F13": {"Establish Communications Request", "GEM communication establishment"},
		"S2F13": {"Equipment Constant Request", "Request equipment constants"},
		"S2F15": {"New Equipment Constant Send", "Change equipment constants"},
		"S2F17": {"Date and Time Request", "Request equipment clock"},
		"S2F31": {"Date and Time Set", "Set equipment clock"},
		"S2F33": {"Define Report", "Define data collection reports"},
		"S2F35": {"Link Event Report", "Link reports to collection events"},
		"S2F37": {"Enable Event Report", "Enable or disable event reports"},
		"S2F41": {"Host Command Send", "Send a remote command"},
		"S5F1":  {"Alarm Report Send", "Equipment alarm notification"},
		"S5F3":  {"Enable Alarm Send", "Enable or disable alarms"},
		"S5F5":  {"List Alarms Request", "Request alarm definitions"},
		"S6F11": {"Event Report Send", "Equipment collection event"},
		"S6F15": {"Event Report Request", "Request an event report"},
		"S7F1":  {"Process Program Load Inquire", "Ask permission to download a recipe"},
		"S7F3":  {"Process Program Send", "Download a recipe"},
		"S7F5":  {"Process Program Request", "Upload a recipe"},
		"S7F17": {"Delete Process Program", "Delete a recipe"},
		"S10F3": {"Terminal Display Single", "Display one terminal message"},
		"S10F5": {"Terminal Display Multi", "Display multiple terminal messages"},
	}
	result := make([]MessageCatalogItem, 0, 17*13)
	for stream := 1; stream <= 17; stream++ {
		for function := 1; function <= 13; function++ {
			key := fmt.Sprintf("S%dF%d", stream, function)
			result = append(result, catalogMessage(stream, function, known[key]))
		}
	}
	// GEM streams contain commonly used functions above F13 (for example
	// S2F41 Host Command Send). Keep the requested S1F1-S17F13 base matrix and
	// add every known standard entry outside that rectangle.
	for key, value := range known {
		var stream, function int
		if _, err := fmt.Sscanf(key, "S%dF%d", &stream, &function); err == nil && function > 13 {
			result = append(result, catalogMessage(stream, function, value))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Stream != result[j].Stream {
			return result[i].Stream < result[j].Stream
		}
		return result[i].Function < result[j].Function
	})
	return result
}

func catalogMessage(stream, function int, known [2]string) MessageCatalogItem {
	key := fmt.Sprintf("S%dF%d", stream, function)
	name, description := key, "Custom SECS-II message"
	if known[0] != "" {
		name, description = known[0], known[1]
	}
	primary := function%2 == 1
	header := key
	if primary {
		header += " W"
	}
	return MessageCatalogItem{Stream: stream, Function: function, Name: name, Description: description, Primary: primary, SML: header + "\n."}
}

func (s *StudioService) SaveMessageTemplate(request MessageTemplateSaveRequest) (ProfileSaveResult, error) {
	clean, err := safeProfilePath(request.ProfilePath)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	data, err := fs.ReadFile(s.profileSource, clean)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	compiled, err := profile.Decode(data)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	message, err := driver.ParseOutboundSML(request.SML)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return ProfileSaveResult{}, fmt.Errorf("template name is required")
	}
	displayName := strings.TrimSpace(request.DisplayName)
	if displayName == "" {
		displayName = name
	}
	document := compiled.EquipmentProfile
	switch request.Kind {
	case "command":
		if message.Function%2 == 0 {
			return ProfileSaveResult{}, fmt.Errorf("commands must use an odd primary function")
		}
		if document.Spec.Commands == nil {
			document.Spec.Commands = map[string]profile.CommandDefinition{}
		}
		document.Spec.Commands[name] = profile.CommandDefinition{DisplayName: displayName, Stream: message.Stream, Function: message.Function, Wait: message.Wait, SML: request.SML, SuccessEvent: "message.accepted", FailureEvent: "message.failed"}
	case "scenario":
		if document.Spec.Scenarios == nil {
			document.Spec.Scenarios = map[string]profile.SimulatorScenario{}
		}
		document.Spec.Scenarios[name] = profile.SimulatorScenario{DisplayName: displayName, Message: profile.MessageTemplate{Stream: message.Stream, Function: message.Function, Wait: message.Wait, SML: request.SML}}
	default:
		return ProfileSaveResult{}, fmt.Errorf("template kind must be command or scenario")
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	return s.SaveProfile(clean, string(encoded))
}

func (s *StudioService) ListProfiles() ([]ProfileSummary, error) {
	var result []ProfileSummary
	err := fs.WalkDir(s.profileSource, "profiles", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".yaml") {
			return nil
		}
		compiled, err := profile.Load(s.profileSource, path)
		value := ProfileSummary{Path: filepath.ToSlash(path), Valid: err == nil}
		if err != nil {
			value.Error = err.Error()
		} else {
			value.Name, value.Vendor, value.Model, value.Version, value.Adapter = compiled.Metadata.Name, compiled.Metadata.Vendor, compiled.Metadata.Model, compiled.Metadata.Version, compiled.Metadata.Adapter
		}
		result = append(result, value)
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, err
}

func (s *StudioService) ReadProfile(path string) (ProfileDocument, error) {
	clean, err := safeProfilePath(path)
	if err != nil {
		return ProfileDocument{}, err
	}
	data, err := fs.ReadFile(s.profileSource, clean)
	return ProfileDocument{Path: clean, YAML: string(data)}, err
}

func (s *StudioService) ValidateProfileYAML(path, yamlText string) ProfileValidation {
	compiled, err := profile.Decode([]byte(yamlText))
	if err != nil {
		return ProfileValidation{Valid: false, Error: err.Error(), Summary: ProfileSummary{Path: path}}
	}
	summary := ProfileSummary{Path: path, Name: compiled.Metadata.Name, Vendor: compiled.Metadata.Vendor, Model: compiled.Metadata.Model, Version: compiled.Metadata.Version, Adapter: compiled.Metadata.Adapter, Valid: true}
	warnings := []string{}
	if len(compiled.Spec.Events) == 0 {
		warnings = append(warnings, "Profile defines no canonical events")
	}
	if len(compiled.Spec.Commands) == 0 {
		warnings = append(warnings, "Profile defines no commands")
	}
	if _, adapterErr := equipment.NewAdapter(compiled.Metadata.Adapter); adapterErr != nil {
		return ProfileValidation{Valid: false, Error: adapterErr.Error(), Summary: summary}
	}
	return ProfileValidation{Valid: true, Summary: summary, Warnings: warnings}
}

func (s *StudioService) PreviewProfileEvent(yamlText, eventName string, data map[string]any) (ProfilePreview, error) {
	compiled, err := profile.Decode([]byte(yamlText))
	if err != nil {
		return ProfilePreview{}, err
	}
	adapter, err := equipment.NewAdapter(compiled.Metadata.Adapter)
	if err != nil {
		return ProfilePreview{}, err
	}
	message, err := adapter.BuildEvent(context.Background(), eventName, data, compiled)
	if err != nil {
		return ProfilePreview{}, err
	}
	message.ID, message.EquipmentID = "workbench-preview", "WORKBENCH"
	events, err := adapter.Parse(context.Background(), message, compiled)
	return ProfilePreview{Message: message, Events: events}, err
}

func (s *StudioService) SaveProfile(path, yamlText string) (ProfileSaveResult, error) {
	clean, err := safeProfilePath(path)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	validation := s.ValidateProfileYAML(clean, yamlText)
	if !validation.Valid {
		return ProfileSaveResult{}, fmt.Errorf("invalid profile: %s", validation.Error)
	}
	target := filepath.Join(s.runtimeConfigDir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return ProfileSaveResult{}, err
	}
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, []byte(yamlText), 0o600); err != nil {
		return ProfileSaveResult{}, err
	}
	if err := os.Rename(temporary, target); err != nil {
		if writeErr := os.WriteFile(target, []byte(yamlText), 0o600); writeErr != nil {
			return ProfileSaveResult{}, writeErr
		}
		_ = os.Remove(temporary)
	}
	config, err := s.runtimeEquipmentConfig()
	if err != nil {
		return ProfileSaveResult{}, err
	}
	if err := s.manager.ApplyConfig(s.profileSource, config, map[string]bool{clean: true}); err != nil {
		return ProfileSaveResult{}, err
	}
	result := ProfileSaveResult{Path: target}
	for _, definition := range config.Devices {
		if filepath.ToSlash(definition.Profile) == clean {
			result.ReloadedDevices = append(result.ReloadedDevices, definition.ID)
		}
	}
	return result, nil
}

func safeProfilePath(value string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	if !strings.HasPrefix(clean, "profiles/") || !strings.HasSuffix(strings.ToLower(clean), ".yaml") || strings.Contains(clean, "..") {
		return "", fmt.Errorf("profile path must be a YAML file under profiles/")
	}
	return clean, nil
}

func (s *StudioService) SaveDeviceOrder(order []string) error {
	s.equipmentConfigMu.Lock()
	defer s.equipmentConfigMu.Unlock()
	config, err := s.runtimeEquipmentConfig()
	if err != nil {
		return err
	}
	byID := make(map[string]device.Definition, len(config.Devices))
	for _, definition := range config.Devices {
		byID[definition.ID] = definition
	}
	reordered := make([]device.Definition, 0, len(config.Devices))
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		if definition, exists := byID[id]; exists && !seen[id] {
			reordered = append(reordered, definition)
			seen[id] = true
		}
	}
	for _, definition := range config.Devices {
		if !seen[definition.ID] {
			reordered = append(reordered, definition)
		}
	}
	config.Devices = reordered
	if err := device.SaveConfig(s.equipmentConfigPath, config); err != nil {
		return err
	}
	if s.manager != nil {
		s.manager.SetOrder(order)
	}
	return nil
}

type EquipmentConfigComparison struct {
	RuntimePath   string   `json:"runtimePath"`
	PackagedCount int      `json:"packagedCount"`
	RuntimeCount  int      `json:"runtimeCount"`
	Missing       []string `json:"missing"`
	Extra         []string `json:"extra"`
	Changed       []string `json:"changed"`
}

type EquipmentMergeResult struct {
	Added           []string `json:"added"`
	Path            string   `json:"path"`
	RestartRequired bool     `json:"restartRequired"`
}

func (s *StudioService) CompareEquipmentConfig() (EquipmentConfigComparison, error) {
	s.equipmentConfigMu.Lock()
	defer s.equipmentConfigMu.Unlock()
	packaged, err := device.LoadConfig(s.packagedSource, "configs/devices.yaml")
	if err != nil {
		return EquipmentConfigComparison{}, err
	}
	runtimeConfig, err := s.runtimeEquipmentConfig()
	if err != nil {
		return EquipmentConfigComparison{}, err
	}
	packagedByID, runtimeByID := map[string]device.Definition{}, map[string]device.Definition{}
	for _, value := range packaged.Devices {
		packagedByID[value.ID] = value
	}
	for _, value := range runtimeConfig.Devices {
		runtimeByID[value.ID] = value
	}
	result := EquipmentConfigComparison{RuntimePath: s.equipmentConfigPath, PackagedCount: len(packaged.Devices), RuntimeCount: len(runtimeConfig.Devices)}
	for id, value := range packagedByID {
		runtimeValue, ok := runtimeByID[id]
		if !ok {
			result.Missing = append(result.Missing, id)
		} else if !reflect.DeepEqual(value, runtimeValue) {
			result.Changed = append(result.Changed, id)
		}
	}
	for id := range runtimeByID {
		if _, ok := packagedByID[id]; !ok {
			result.Extra = append(result.Extra, id)
		}
	}
	sort.Strings(result.Missing)
	sort.Strings(result.Extra)
	sort.Strings(result.Changed)
	return result, nil
}

func (s *StudioService) MergePackagedDemoDevices() (EquipmentMergeResult, error) {
	s.equipmentConfigMu.Lock()
	defer s.equipmentConfigMu.Unlock()
	packaged, err := device.LoadConfig(s.packagedSource, "configs/devices.yaml")
	if err != nil {
		return EquipmentMergeResult{}, err
	}
	runtimeConfig, err := s.runtimeEquipmentConfig()
	if err != nil {
		return EquipmentMergeResult{}, err
	}
	existing := map[string]bool{}
	for _, value := range runtimeConfig.Devices {
		existing[value.ID] = true
	}
	result := EquipmentMergeResult{Path: s.equipmentConfigPath, RestartRequired: true}
	for _, value := range packaged.Devices {
		if !existing[value.ID] {
			runtimeConfig.Devices = append(runtimeConfig.Devices, value)
			result.Added = append(result.Added, value.ID)
		}
	}
	if len(result.Added) > 0 {
		if err := s.manager.ApplyConfig(s.profileSource, runtimeConfig, nil); err != nil {
			return EquipmentMergeResult{}, err
		}
		if err := device.SaveConfig(s.equipmentConfigPath, runtimeConfig); err != nil {
			return EquipmentMergeResult{}, err
		}
		s.config = runtimeConfig
		result.RestartRequired = false
	}
	return result, nil
}

func (s *StudioService) runtimeEquipmentConfig() (device.Config, error) {
	if s.equipmentConfigPath == "" {
		return s.config, nil
	}
	data, err := os.ReadFile(s.equipmentConfigPath)
	if err != nil {
		return device.Config{}, err
	}
	return device.DecodeConfig(data)
}

func (s *StudioService) QueryTraceHistory(query sqlitestore.HistoryQuery) (sqlitestore.TracePage, error) {
	return s.store.QueryTraces(context.Background(), query)
}
func (s *StudioService) QueryEventHistory(query sqlitestore.HistoryQuery) (sqlitestore.EventPage, error) {
	return s.store.QueryEvents(context.Background(), query)
}
func (s *StudioService) QueryCommandHistory(query sqlitestore.HistoryQuery) (sqlitestore.CommandPage, error) {
	return s.store.QueryCommands(context.Background(), query)
}
func (s *StudioService) ApplyHistoryRetention(days int) (sqlitestore.RetentionResult, error) {
	result, err := s.store.ApplyRetention(context.Background(), days)
	if err == nil {
		s.notify()
	}
	return result, err
}

type RuleReloadResult struct {
	Routes      int `json:"routes"`
	Automations int `json:"automations"`
}

func (s *StudioService) ReloadRules() (RuleReloadResult, error) {
	s.ruleReloadMu.Lock()
	defer s.ruleReloadMu.Unlock()
	s.runtimeMu.RLock()
	routerValue, automationValue := s.router, s.automation
	source, routePath, automationPath := s.ruleSource, s.routeConfigPath, s.automationConfigPath
	s.runtimeMu.RUnlock()
	routes, err := routerValue.PrepareRules(source, routePath)
	if err != nil {
		return RuleReloadResult{}, err
	}
	automations, err := automation.ReadRules(source, automationPath)
	if err != nil {
		return RuleReloadResult{}, err
	}
	routerValue.ReplaceRules(routes)
	automationValue.ReplaceRules(automations)
	if fingerprint, err := s.rulesFingerprint(); err == nil {
		s.ruleFingerprint = fingerprint
	}
	s.notify()
	return RuleReloadResult{Routes: len(routes), Automations: len(automations)}, nil
}

func (s *StudioService) rulesFingerprint() ([32]byte, error) {
	s.runtimeMu.RLock()
	source, routePath, automationPath := s.ruleSource, s.routeConfigPath, s.automationConfigPath
	s.runtimeMu.RUnlock()
	routes, err := fs.ReadFile(source, routePath)
	if err != nil {
		return [32]byte{}, err
	}
	automations, err := fs.ReadFile(source, automationPath)
	if err != nil {
		return [32]byte{}, err
	}
	data := make([]byte, 0, len(routes)+len(automations)+1)
	data = append(data, routes...)
	data = append(data, 0)
	data = append(data, automations...)
	return sha256.Sum256(data), nil
}

func (s *StudioService) watchRules(ctx context.Context) {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fingerprint, err := s.rulesFingerprint()
			s.ruleReloadMu.Lock()
			current := s.ruleFingerprint
			s.ruleReloadMu.Unlock()
			if err == nil && fingerprint != current {
				_, _ = s.ReloadRules()
			}
		}
	}
}

// AskCopilot is a grounded, deterministic first-pass assistant. A provider-backed
// implementation can replace it without moving credentials into the frontend.
func (s *StudioService) AskCopilot(question string, equipmentID string, attachments []ai.Attachment) CopilotReply {
	if err := validateAttachments(attachments); err != nil {
		return CopilotReply{Answer: "附件无效：" + err.Error()}
	}
	snapshot := s.Snapshot()
	var selected *device.Snapshot
	for index := range snapshot.Devices {
		if snapshot.Devices[index].ID == equipmentID {
			selected = &snapshot.Devices[index]
			break
		}
	}
	if selected == nil && len(snapshot.Devices) > 0 {
		selected = &snapshot.Devices[0]
	}
	if selected == nil {
		return CopilotReply{Answer: "当前没有已配置的设备。"}
	}
	if commandIntent(question) {
		permission, err := s.newCommandPermission(*selected)
		if err != nil {
			return CopilotReply{Answer: "权限策略已阻止该命令：" + err.Error(), Evidence: []string{"permission policy"}}
		}
		return CopilotReply{
			Answer:   fmt.Sprintf("我已准备 %s，但它会向 %s 发送设备命令，需要你在下方权限卡中明确授权。", permission.Command, permission.EquipmentID),
			Evidence: []string{"目标设备 " + permission.EquipmentID, "Profile command " + permission.Command}, Permission: &permission,
		}
	}

	config := s.AIConfig()
	if config.Provider != "local" {
		s.aiMu.RLock()
		apiKey := s.aiAPIKey
		s.aiMu.RUnlock()
		if apiKey == "" {
			apiKey = os.Getenv("EAPSTUDIO_AI_API_KEY")
		}
		provider, err := ai.NewProvider(config, apiKey)
		if err != nil {
			return CopilotReply{Answer: "AI provider 配置错误：" + err.Error()}
		}
		contextJSON, _ := json.Marshal(selected)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		answer, err := provider.Complete(ctx, ai.Request{
			System: "You are EapStudio Equipment Copilot. Answer using only the supplied runtime snapshot. Never claim a command was sent and never bypass UI permission approval.",
			Prompt: question + "\n\nRuntime snapshot:\n" + string(contextJSON), Attachments: attachments,
		})
		if err != nil {
			return CopilotReply{Answer: "AI provider 调用失败：" + err.Error(), Evidence: []string{config.Provider + " adapter"}}
		}
		return CopilotReply{Answer: answer, Evidence: []string{config.Provider + " adapter", "设备 Runtime 快照"}}
	}
	if len(attachments) > 0 {
		return CopilotReply{Answer: "Local grounded provider 不会读取图片或文件。请选择 Responses 或 Chat adapter 后重试。", Evidence: []string{"attachment rejected by local provider"}}
	}

	latestMessage := "暂无消息"
	if count := len(selected.Messages); count > 0 {
		message := selected.Messages[count-1]
		latestMessage = fmt.Sprintf("%s S%dF%d（%s）", message.Direction, message.Stream, message.Function, message.Timestamp.Format("15:04:05"))
	}
	latestEvent := "暂无 Canonical Event"
	if count := len(selected.Events); count > 0 {
		value := selected.Events[count-1]
		latestEvent = fmt.Sprintf("%s，数据 %v", value.Name, value.Data)
	}
	answer := fmt.Sprintf("%s 当前处于 %s 状态，使用 %s/%s Profile。最近一条协议消息是 %s；最近转换结果是 %s。", selected.ID, selected.State, selected.Vendor, selected.Model, latestMessage, latestEvent)
	questionLower := strings.ToLower(question)
	if strings.Contains(questionLower, "material") || strings.Contains(questionLower, "recipe") || strings.Contains(question, "物料") || strings.Contains(question, "配方") || strings.Contains(question, "关联") {
		for index := len(selected.Events) - 1; index >= 0; index-- {
			trigger := selected.Events[index]
			if trigger.Name != "material.arrived" {
				continue
			}
			chain := []string{"event:" + trigger.Name}
			for _, value := range selected.Commands {
				if value.CorrelationID == trigger.CorrelationID {
					chain = append(chain, "command:"+value.Name+"("+string(value.Status)+")")
				}
			}
			for _, value := range selected.Events {
				if value.CorrelationID == trigger.CorrelationID && value.ID != trigger.ID {
					chain = append(chain, "event:"+value.Name)
				}
			}
			answer = fmt.Sprintf("通过 correlationId %s 找到完整因果链：%s。触发事件 %s，Automation 生成 send.recipe，结果事件通过 causationId/commandId 回指命令。", trigger.CorrelationID, strings.Join(chain, " → "), trigger.ID)
			break
		}
	}
	if strings.Contains(questionLower, "1001") || strings.Contains(question, "事件") {
		answer += " CEID 1001 在当前 Profile 中定义为 wafer.started；material.arrived 会通过 Automation 生成 send.recipe Command。"
	}
	return CopilotReply{Answer: answer, Evidence: []string{"设备 Runtime 快照", "demo-etcher-x100 Profile", "最近消息与事件环形记录"}, Suggestions: []string{"解释最近一条 S6F11", "检查事件路由结果", "为未知 CEID 生成 Profile 草案"}}
}

// AskCopilotStream starts a provider-backed streaming request and returns
// immediately. Deltas and the final reply are emitted through
// studio:copilot-stream so the Wails call itself never buffers the full answer.
func (s *StudioService) AskCopilotStream(requestID string, sessionID string, question string, scope string, attachments []ai.Attachment) error {
	if strings.TrimSpace(requestID) == "" {
		return fmt.Errorf("request ID is required")
	}
	if strings.TrimSpace(question) == "" {
		return fmt.Errorf("question is required")
	}
	if err := validateAttachments(attachments); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session ID is required")
	}
	exists, err := s.store.CopilotSessionExists(context.Background(), sessionID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("copilot session %q was not found", sessionID)
	}
	if err := s.validateCopilotScope(scope); err != nil {
		return err
	}
	go s.runCopilotStream(requestID, sessionID, question, scope, attachments)
	return nil
}

func (s *StudioService) runCopilotStream(requestID, sessionID, question, scope string, attachments []ai.Attachment) {
	selected := s.selectedEquipment(scope)
	if scope != "ALL" && selected == nil {
		s.finishCopilotStream(requestID, sessionID, scope, CopilotReply{Answer: "指定设备不存在或已被移除。"})
		return
	}
	storedAttachments := make([]ai.Attachment, len(attachments))
	for index, attachment := range attachments {
		storedAttachments[index] = ai.Attachment{Name: attachment.Name, MediaType: attachment.MediaType, Size: attachment.Size}
	}
	_ = s.store.RecordCopilotMessage(context.Background(), sqlitestore.CopilotMessage{
		ID: "user-" + requestID, SessionID: sessionID, EquipmentID: scope, Role: "user", Text: question,
		Attachments: storedAttachments, CreatedAt: time.Now().UTC(),
	})
	_ = s.store.TouchCopilotSession(context.Background(), sessionID, copilotSessionTitle(question))

	config := s.AIConfig()
	if config.Provider == "local" || commandIntent(question) {
		var reply CopilotReply
		if scope == "ALL" {
			reply = s.askAllCopilot(question, attachments)
		} else {
			reply = s.AskCopilot(question, scope, attachments)
		}
		if !commandIntent(question) {
			reply.Tools = s.copilotReadTools(question, scope)
		}
		if reply.Permission != nil {
			reply.Permission.SessionID = sessionID
		}
		for _, chunk := range textChunks(reply.Answer, 12) {
			s.copilotEvents <- CopilotStreamEvent{RequestID: requestID, SessionID: sessionID, Delta: chunk}
			time.Sleep(14 * time.Millisecond)
		}
		s.finishCopilotStream(requestID, sessionID, scope, reply)
		return
	}

	s.aiMu.RLock()
	apiKey := s.aiAPIKey
	s.aiMu.RUnlock()
	if apiKey == "" {
		apiKey = os.Getenv("EAPSTUDIO_AI_API_KEY")
	}
	provider, err := ai.NewProvider(config, apiKey)
	if err != nil {
		s.finishCopilotStream(requestID, sessionID, scope, CopilotReply{Answer: "AI provider 配置错误：" + err.Error()})
		return
	}
	tools := s.copilotReadTools(question, scope)
	contextJSON, _ := json.Marshal(tools)
	conversation, _ := s.store.CopilotHistory(context.Background(), sessionID, 24)
	conversationText := copilotConversationContext(conversation, "user-"+requestID)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	var answer strings.Builder
	err = provider.Stream(ctx, ai.Request{
		System: "You are EapStudio Copilot. Use the supplied runtime snapshot for studio and equipment facts. You may answer general non-device questions normally, but distinguish general knowledge from runtime evidence. Never claim a command was sent and never bypass UI permission approval.",
		Prompt: conversationText + "Current user question:\n" + question + "\n\nTyped read-tool results:\n" + string(contextJSON), Attachments: attachments,
	}, func(delta string) error {
		answer.WriteString(delta)
		s.copilotEvents <- CopilotStreamEvent{RequestID: requestID, SessionID: sessionID, Delta: delta}
		return nil
	})
	if err != nil {
		message := "AI provider 调用失败：" + err.Error()
		if answer.Len() == 0 {
			for _, chunk := range textChunks(message, 12) {
				s.copilotEvents <- CopilotStreamEvent{RequestID: requestID, SessionID: sessionID, Delta: chunk}
			}
		}
		s.finishCopilotStream(requestID, sessionID, scope, CopilotReply{Answer: answer.String() + message, Evidence: []string{config.Provider + " adapter"}, Tools: tools})
		return
	}
	s.finishCopilotStream(requestID, sessionID, scope, CopilotReply{Answer: answer.String(), Evidence: []string{config.Provider + " adapter", "typed runtime tools"}, Tools: tools})
}

func (s *StudioService) copilotReadTools(question, scope string) []ai.ToolResult {
	snapshot := s.Snapshot()
	var scoped any = snapshot
	if scope != "ALL" {
		scoped = s.selectedEquipment(scope)
	}
	results := []ai.ToolResult{{Name: "runtime.snapshot", Input: map[string]any{"scope": scope}, Result: scoped}}
	lower := strings.ToLower(question)
	query := sqlitestore.HistoryQuery{Page: 1, PageSize: 25, EquipmentID: scope, SinceHours: 24}
	if strings.Contains(lower, "message") || strings.Contains(lower, "secs") || strings.Contains(question, "报文") || strings.Contains(question, "消息") {
		if value, err := s.store.QueryTraces(context.Background(), query); err == nil {
			results = append(results, ai.ToolResult{Name: "history.messages", Input: map[string]any{"scope": scope, "hours": 24}, Result: value})
		}
	}
	if strings.Contains(lower, "event") || strings.Contains(lower, "correlation") || strings.Contains(question, "事件") || strings.Contains(question, "关联") {
		if value, err := s.store.QueryEvents(context.Background(), query); err == nil {
			results = append(results, ai.ToolResult{Name: "history.events", Input: map[string]any{"scope": scope, "hours": 24}, Result: value})
		}
	}
	if strings.Contains(lower, "command") || strings.Contains(question, "命令") || strings.Contains(question, "执行结果") {
		if value, err := s.store.QueryCommands(context.Background(), query); err == nil {
			results = append(results, ai.ToolResult{Name: "history.commands", Input: map[string]any{"scope": scope, "hours": 24}, Result: value})
		}
	}
	if strings.Contains(lower, "profile") || strings.Contains(question, "配置") {
		if value, err := s.ListProfiles(); err == nil {
			results = append(results, ai.ToolResult{Name: "profiles.list", Input: map[string]any{}, Result: value})
		}
	}
	return results
}

func (s *StudioService) askAllCopilot(question string, attachments []ai.Attachment) CopilotReply {
	if len(attachments) > 0 {
		return CopilotReply{Answer: "Local grounded 是离线规则助手，不会读取图片或文件。请选择 Responses 或 Chat 配置后重试。", Evidence: []string{"local rule engine"}}
	}
	if commandIntent(question) {
		return CopilotReply{Answer: "发送命令前必须把会话范围切换到一台具体设备；“全部设备”范围不会推断命令目标。", Evidence: []string{"command target guard"}}
	}
	snapshot := s.Snapshot()
	online := 0
	questionLower := strings.ToLower(question)
	var matches []string
	for _, value := range snapshot.Devices {
		if value.State == "selected" {
			online++
		}
		if strings.Contains(questionLower, strings.ToLower(value.ID)) || strings.Contains(questionLower, strings.ToLower(value.Name)) {
			latest := "暂无事件"
			if count := len(value.Events); count > 0 {
				latest = value.Events[count-1].Name
			}
			matches = append(matches, fmt.Sprintf("%s（%s）状态为 %s，Profile %s，最近事件 %s", value.ID, value.Name, value.State, value.ProfileName, latest))
		}
	}
	if len(matches) > 0 {
		return CopilotReply{Answer: strings.Join(matches, "；") + "。", Evidence: []string{"全部设备 Runtime 快照", "设备 Profile 与事件记录"}}
	}
	return CopilotReply{Answer: fmt.Sprintf("当前 Studio 有 %d 台设备，其中 %d 台在线；已加载 %d 条 Router 规则、%d 条 Automation 规则和 %d 条告警。Local grounded 只检索本地 Runtime、历史与配置；通用知识问答请切换到 Responses 或 Chat 配置。", len(snapshot.Devices), online, len(snapshot.Routes), len(snapshot.Automations), len(snapshot.Alarms)), Evidence: []string{"全部设备 Runtime 快照", "Router / Automation 配置", "SQLite 历史"}}
}

func copilotConversationContext(history []sqlitestore.CopilotMessage, currentID string) string {
	if len(history) == 0 {
		return ""
	}
	start := len(history) - 12
	if start < 0 {
		start = 0
	}
	var result strings.Builder
	result.WriteString("Previous conversation for context:\n")
	for _, message := range history[start:] {
		if message.ID == currentID || message.Text == "" {
			continue
		}
		text := []rune(message.Text)
		if len(text) > 1200 {
			text = text[:1200]
		}
		fmt.Fprintf(&result, "%s: %s\n", message.Role, string(text))
	}
	result.WriteString("\n")
	return result.String()
}

func (s *StudioService) selectedEquipment(equipmentID string) *device.Snapshot {
	snapshot := s.Snapshot()
	for index := range snapshot.Devices {
		if snapshot.Devices[index].ID == equipmentID {
			return &snapshot.Devices[index]
		}
	}
	return nil
}

func (s *StudioService) finishCopilotStream(requestID, sessionID, scope string, reply CopilotReply) {
	permission := toStoredPermission(reply.Permission)
	status := ""
	if permission != nil {
		status = "pending"
	}
	_ = s.store.RecordCopilotMessage(context.Background(), sqlitestore.CopilotMessage{
		ID: "assistant-" + requestID, SessionID: sessionID, EquipmentID: scope, Role: "assistant", Text: reply.Answer,
		Evidence: reply.Evidence, Permission: permission, PermissionStatus: status, CreatedAt: time.Now().UTC(),
	})
	s.copilotEvents <- CopilotStreamEvent{RequestID: requestID, SessionID: sessionID, Done: true, Reply: &reply}
}

func textChunks(value string, size int) []string {
	characters := []rune(value)
	var result []string
	for start := 0; start < len(characters); start += size {
		end := start + size
		if end > len(characters) {
			end = len(characters)
		}
		result = append(result, string(characters[start:end]))
	}
	return result
}

func toStoredPermission(value *AIActionPermission) *sqlitestore.CopilotPermission {
	if value == nil {
		return nil
	}
	diff := make(map[string]any, len(value.ParameterDiff))
	for key, change := range value.ParameterDiff {
		diff[key] = change
	}
	return &sqlitestore.CopilotPermission{ID: value.ID, Tool: value.Tool, EquipmentID: value.EquipmentID, Command: value.Command, Summary: value.Summary, Risk: value.Risk, Parameters: value.Parameters, ParameterDiff: diff, ExpiresAt: value.ExpiresAt}
}

func (s *StudioService) ListCopilotSessions(search string) ([]sqlitestore.CopilotSession, error) {
	return s.store.CopilotSessions(context.Background(), strings.TrimSpace(search))
}

func (s *StudioService) CreateCopilotSession(scope string) (sqlitestore.CopilotSession, error) {
	if scope == "" {
		scope = "ALL"
	}
	if err := s.validateCopilotScope(scope); err != nil {
		return sqlitestore.CopilotSession{}, err
	}
	now := time.Now().UTC()
	value := sqlitestore.CopilotSession{ID: fmt.Sprintf("session-%d", now.UnixNano()), Title: "New conversation", Scope: scope, CreatedAt: now, UpdatedAt: now}
	return value, s.store.CreateCopilotSession(context.Background(), value)
}

func (s *StudioService) UpdateCopilotSessionScope(sessionID, scope string) error {
	exists, err := s.store.CopilotSessionExists(context.Background(), sessionID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("copilot session %q was not found", sessionID)
	}
	if err := s.validateCopilotScope(scope); err != nil {
		return err
	}
	return s.store.UpdateCopilotSessionScope(context.Background(), sessionID, scope)
}

func (s *StudioService) DeleteCopilotSession(sessionID string) error {
	return s.store.DeleteCopilotSession(context.Background(), sessionID)
}

func (s *StudioService) CopilotHistory(sessionID string) ([]sqlitestore.CopilotMessage, error) {
	return s.store.CopilotHistory(context.Background(), sessionID, 200)
}

func (s *StudioService) validateCopilotScope(scope string) error {
	if scope == "ALL" {
		return nil
	}
	if s.selectedEquipment(scope) == nil {
		return fmt.Errorf("equipment %q was not found", scope)
	}
	return nil
}

func copilotSessionTitle(question string) string {
	value := []rune(strings.TrimSpace(question))
	if len(value) > 32 {
		value = append(value[:32], '…')
	}
	return string(value)
}

func validateAttachments(attachments []ai.Attachment) error {
	if len(attachments) > 4 {
		return fmt.Errorf("最多支持 4 个附件")
	}
	total := 0
	for _, attachment := range attachments {
		if attachment.Name == "" || attachment.MediaType == "" || !strings.HasPrefix(attachment.DataURL, "data:") {
			return fmt.Errorf("附件名称、类型或数据缺失")
		}
		if attachment.Size < 0 || attachment.Size > 5<<20 {
			return fmt.Errorf("%s 超过 5 MB 限制", attachment.Name)
		}
		total += attachment.Size
	}
	if total > 12<<20 {
		return fmt.Errorf("附件总大小超过 12 MB")
	}
	return nil
}

func commandIntent(question string) bool {
	lower := strings.ToLower(question)
	return strings.Contains(question, "发送命令") || strings.Contains(question, "下发命令") || strings.Contains(question, "发送配方") || strings.Contains(question, "执行命令") || strings.Contains(lower, "send command") || strings.Contains(lower, "execute command")
}

func (s *StudioService) newCommandPermission(selected device.Snapshot) (AIActionPermission, error) {
	if selected.Role != "controller" {
		return AIActionPermission{}, fmt.Errorf("device %s is an Equipment Twin; commands are Host → Equipment definitions", selected.ID)
	}
	policy, err := s.PermissionPolicy()
	if err != nil {
		return AIActionPermission{}, err
	}
	if policy.Mode == "deny" || !matchesPolicy(policy.Equipment, selected.ID) || !matchesPolicy(policy.Commands, "send.recipe") {
		return AIActionPermission{}, fmt.Errorf("send.recipe to %s is outside the configured allowlist", selected.ID)
	}
	parameters := map[string]any{"recipeId": "ETCH-A", "materialId": "AI-REQUEST"}
	for index := len(selected.Events) - 1; index >= 0; index-- {
		if selected.Events[index].Name != "material.arrived" {
			continue
		}
		for _, key := range []string{"recipeId", "materialId"} {
			if value, ok := selected.Events[index].Data[key]; ok {
				parameters[key] = value
			}
		}
		break
	}
	id := fmt.Sprintf("permission-%06d", s.permissionSequence.Add(1))
	diff := map[string]ParameterChange{}
	var previous map[string]any
	if history, historyErr := s.store.QueryCommands(context.Background(), sqlitestore.HistoryQuery{Page: 1, PageSize: 25, EquipmentID: selected.ID, Name: "send.recipe"}); historyErr == nil && len(history.Items) > 0 {
		previous = history.Items[0].Parameters
	}
	for key, after := range parameters {
		var before any
		if previous != nil {
			before = previous[key]
		}
		if !reflect.DeepEqual(before, after) {
			diff[key] = ParameterChange{Before: before, After: after}
		}
	}
	permission := AIActionPermission{ID: id, Tool: "send.command", EquipmentID: selected.ID, Command: "send.recipe", Summary: "Send recipe parameters to equipment", Risk: "This writes to a live equipment session and may change equipment state.", Parameters: parameters, ParameterDiff: diff, ExpiresAt: time.Now().Add(time.Duration(policy.TTLMinutes) * time.Minute)}
	s.aiMu.Lock()
	s.pending[id] = permission
	s.aiMu.Unlock()
	return permission, nil
}

func matchesPolicy(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if ok, _ := pathpkg.Match(pattern, value); ok {
			return true
		}
	}
	return false
}

func (s *StudioService) ResolveAIAction(permissionID string, allow bool) CopilotReply {
	s.aiMu.Lock()
	permission, ok := s.pending[permissionID]
	if ok {
		delete(s.pending, permissionID)
	}
	s.aiMu.Unlock()
	if !ok {
		return CopilotReply{Answer: "该权限请求不存在或已经处理。"}
	}
	if time.Now().After(permission.ExpiresAt) {
		_ = s.store.UpdateCopilotPermission(context.Background(), permissionID, "expired")
		return CopilotReply{Answer: "权限请求已过期，没有发送命令。", Evidence: []string{"permission expired"}}
	}
	if !allow {
		_ = s.store.UpdateCopilotPermission(context.Background(), permissionID, "denied")
		reply := CopilotReply{Answer: fmt.Sprintf("已拒绝 %s；没有向 %s 发送任何消息。", permission.Command, permission.EquipmentID), Evidence: []string{"permission denied"}}
		s.recordCopilotResolution(permission.SessionID, permission.EquipmentID, reply)
		return reply
	}
	_ = s.store.UpdateCopilotPermission(context.Background(), permissionID, "allowed")
	correlationID := "ai-" + permission.ID
	s.runtimeMu.RLock()
	manager := s.manager
	value, err := manager.SubmitCommand(permission.EquipmentID, permission.Command, permission.Parameters, correlationID, permission.ID)
	s.runtimeMu.RUnlock()
	if err != nil {
		reply := CopilotReply{Answer: "命令执行失败：" + err.Error(), Evidence: []string{"permission allowed", "command rejected before send"}}
		s.recordCopilotResolution(permission.SessionID, permission.EquipmentID, reply)
		return reply
	}
	reply := CopilotReply{Answer: fmt.Sprintf("已授权并提交 %s，commandId=%s。执行结果会以 recipe.sent 或 recipe.failed Event 返回。", value.Name, value.ID), Evidence: []string{"permission allowed", "command queue " + permission.EquipmentID}}
	s.recordCopilotResolution(permission.SessionID, permission.EquipmentID, reply)
	return reply
}

func (s *StudioService) recordCopilotResolution(sessionID, equipmentID string, reply CopilotReply) {
	id := fmt.Sprintf("assistant-resolution-%d", time.Now().UnixNano())
	_ = s.store.RecordCopilotMessage(context.Background(), sqlitestore.CopilotMessage{
		ID: id, SessionID: sessionID, EquipmentID: equipmentID, Role: "assistant", Text: reply.Answer,
		Evidence: reply.Evidence, CreatedAt: time.Now().UTC(),
	})
}

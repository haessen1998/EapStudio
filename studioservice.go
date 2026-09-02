package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
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
	"eapstudio/internal/router"
	"eapstudio/internal/sink"
	sqlitestore "eapstudio/internal/store/sqlite"
)

type StudioService struct {
	packagedSource       fs.FS
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
	routeConfigPath      string
	automationConfigPath string
	ruleReloadMu         sync.Mutex
	ruleFingerprint      [32]byte
	watchCancel          context.CancelFunc
	pending              map[string]AIActionPermission
	permissionSequence   atomic.Uint64
	copilotEvents        chan CopilotStreamEvent
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
}

type CopilotStreamEvent struct {
	RequestID string        `json:"requestId"`
	SessionID string        `json:"sessionId"`
	Delta     string        `json:"delta,omitempty"`
	Done      bool          `json:"done"`
	Reply     *CopilotReply `json:"reply,omitempty"`
}

type AIActionPermission struct {
	ID          string         `json:"id"`
	Tool        string         `json:"tool"`
	EquipmentID string         `json:"equipmentId"`
	Command     string         `json:"command"`
	Summary     string         `json:"summary"`
	Risk        string         `json:"risk"`
	Parameters  map[string]any `json:"parameters"`
}

func NewStudioService(source fs.FS) (*StudioService, error) {
	databasePath, err := sqlitestore.DefaultPath()
	if err != nil {
		return nil, err
	}
	config, configPath, err := device.LoadRuntimeConfig(source, "configs/devices.yaml")
	if err != nil {
		return nil, err
	}
	configDir := filepath.Dir(configPath)
	for _, item := range []struct{ embedded, name string }{{"configs/routes.yaml", "routes.yaml"}, {"configs/automations.yaml", "automations.yaml"}} {
		if err := materializeRuntimeFile(source, item.embedded, filepath.Join(configDir, item.name)); err != nil {
			return nil, err
		}
	}
	return newStudioServiceWithConfig(source, databasePath, config, configPath, os.DirFS(configDir), "routes.yaml", "automations.yaml")
}

func newStudioService(source fs.FS, databasePath string) (*StudioService, error) {
	config, err := device.LoadConfig(source, "configs/devices.yaml")
	if err != nil {
		return nil, err
	}
	return newStudioServiceWithConfig(source, databasePath, config, "", source, "configs/routes.yaml", "configs/automations.yaml")
}

func newStudioServiceWithConfig(source fs.FS, databasePath string, config device.Config, configPath string, ruleSource fs.FS, routePath string, automationPath string) (*StudioService, error) {
	mockMQ := sink.NewMemory("mock-mq")
	qualityMQ := sink.NewMemory("quality-mq")
	thermalMQ := sink.NewMemory("thermal-mq")
	history, err := sqlitestore.Open(databasePath)
	if err != nil {
		return nil, err
	}
	routes, err := router.Load(ruleSource, routePath, mockMQ, qualityMQ, thermalMQ, history)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	engine, err := automation.Load(ruleSource, automationPath)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	service := &StudioService{packagedSource: source, router: routes, config: config, automation: engine, store: history, updates: make(chan struct{}, 1), aiConfig: ai.Config{Provider: "local"}, equipmentConfigPath: configPath, ruleSource: ruleSource, routeConfigPath: routePath, automationConfigPath: automationPath, pending: map[string]AIActionPermission{}, copilotEvents: make(chan CopilotStreamEvent, 1024)}
	if err := service.loadStoredAIConfig(); err != nil {
		_ = history.Close()
		return nil, err
	}
	manager, err := device.NewManager(source, config, routes, engine, history, service.notify)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	service.manager = manager
	return service, nil
}

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

func (s *StudioService) start(ctx context.Context) {
	s.manager.ConnectAuto(ctx, s.config)
	watchCtx, cancel := context.WithCancel(ctx)
	s.watchCancel = cancel
	if fingerprint, err := s.rulesFingerprint(); err == nil {
		s.ruleReloadMu.Lock()
		s.ruleFingerprint = fingerprint
		s.ruleReloadMu.Unlock()
	}
	go s.watchRules(watchCtx)
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
	alarms, _ := s.store.Alarms(context.Background(), 200)
	return StudioSnapshot{Devices: s.manager.Snapshots(), Routes: s.router.Rules(), Deliveries: s.router.Deliveries(), Automations: s.automation.Rules(), Alarms: alarms, Storage: s.store.Stats(context.Background()), Generated: time.Now()}
}

func (s *StudioService) close() error {
	if s.watchCancel != nil {
		s.watchCancel()
	}
	return s.store.Close()
}

func (s *StudioService) ConnectDevice(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.manager.Connect(ctx, id)
}
func (s *StudioService) DisconnectDevice(id string) error { return s.manager.Disconnect(id) }
func (s *StudioService) EmitSimulatorScenario(id string, scenario string) error {
	return s.manager.EmitScenario(id, scenario)
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
	if err := device.SaveConfig(s.equipmentConfigPath, config); err != nil {
		return EquipmentConfigSaveResult{}, err
	}
	return EquipmentConfigSaveResult{Path: s.equipmentConfigPath, RestartRequired: true}, nil
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
		if err := device.SaveConfig(s.equipmentConfigPath, runtimeConfig); err != nil {
			return EquipmentMergeResult{}, err
		}
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
	routes, err := s.router.PrepareRules(s.ruleSource, s.routeConfigPath)
	if err != nil {
		return RuleReloadResult{}, err
	}
	automations, err := automation.ReadRules(s.ruleSource, s.automationConfigPath)
	if err != nil {
		return RuleReloadResult{}, err
	}
	s.router.ReplaceRules(routes)
	s.automation.ReplaceRules(automations)
	if fingerprint, err := s.rulesFingerprint(); err == nil {
		s.ruleFingerprint = fingerprint
	}
	s.notify()
	return RuleReloadResult{Routes: len(routes), Automations: len(automations)}, nil
}

func (s *StudioService) rulesFingerprint() ([32]byte, error) {
	routes, err := fs.ReadFile(s.ruleSource, s.routeConfigPath)
	if err != nil {
		return [32]byte{}, err
	}
	automations, err := fs.ReadFile(s.ruleSource, s.automationConfigPath)
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
		permission := s.newCommandPermission(*selected)
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
	var runtimeContext any = selected
	if scope == "ALL" {
		runtimeContext = s.Snapshot()
	}
	contextJSON, _ := json.Marshal(runtimeContext)
	conversation, _ := s.store.CopilotHistory(context.Background(), sessionID, 24)
	conversationText := copilotConversationContext(conversation, "user-"+requestID)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	var answer strings.Builder
	err = provider.Stream(ctx, ai.Request{
		System: "You are EapStudio Copilot. Use the supplied runtime snapshot for studio and equipment facts. You may answer general non-device questions normally, but distinguish general knowledge from runtime evidence. Never claim a command was sent and never bypass UI permission approval.",
		Prompt: conversationText + "Current user question:\n" + question + "\n\nRuntime snapshot:\n" + string(contextJSON), Attachments: attachments,
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
		s.finishCopilotStream(requestID, sessionID, scope, CopilotReply{Answer: answer.String() + message, Evidence: []string{config.Provider + " adapter"}})
		return
	}
	s.finishCopilotStream(requestID, sessionID, scope, CopilotReply{Answer: answer.String(), Evidence: []string{config.Provider + " adapter", "Runtime 快照"}})
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
	return &sqlitestore.CopilotPermission{ID: value.ID, Tool: value.Tool, EquipmentID: value.EquipmentID, Command: value.Command, Summary: value.Summary, Risk: value.Risk, Parameters: value.Parameters}
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

func (s *StudioService) newCommandPermission(selected device.Snapshot) AIActionPermission {
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
	permission := AIActionPermission{ID: id, Tool: "send.command", EquipmentID: selected.ID, Command: "send.recipe", Summary: "Send recipe parameters to equipment", Risk: "This writes to a live equipment session and may change equipment state.", Parameters: parameters}
	s.aiMu.Lock()
	s.pending[id] = permission
	s.aiMu.Unlock()
	return permission
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
	if !allow {
		_ = s.store.UpdateCopilotPermission(context.Background(), permissionID, "denied")
		reply := CopilotReply{Answer: fmt.Sprintf("已拒绝 %s；没有向 %s 发送任何消息。", permission.Command, permission.EquipmentID), Evidence: []string{"permission denied"}}
		s.recordCopilotResolution(permission.EquipmentID, reply)
		return reply
	}
	_ = s.store.UpdateCopilotPermission(context.Background(), permissionID, "allowed")
	correlationID := "ai-" + permission.ID
	value, err := s.manager.SubmitCommand(permission.EquipmentID, permission.Command, permission.Parameters, correlationID, permission.ID)
	if err != nil {
		reply := CopilotReply{Answer: "命令执行失败：" + err.Error(), Evidence: []string{"permission allowed", "command rejected before send"}}
		s.recordCopilotResolution(permission.EquipmentID, reply)
		return reply
	}
	reply := CopilotReply{Answer: fmt.Sprintf("已授权并提交 %s，commandId=%s。执行结果会以 recipe.sent 或 recipe.failed Event 返回。", value.Name, value.ID), Evidence: []string{"permission allowed", "command queue " + permission.EquipmentID}}
	s.recordCopilotResolution(permission.EquipmentID, reply)
	return reply
}

func (s *StudioService) recordCopilotResolution(equipmentID string, reply CopilotReply) {
	id := fmt.Sprintf("assistant-resolution-%d", time.Now().UnixNano())
	_ = s.store.RecordCopilotMessage(context.Background(), sqlitestore.CopilotMessage{
		ID: id, SessionID: "default", EquipmentID: equipmentID, Role: "assistant", Text: reply.Answer,
		Evidence: reply.Evidence, CreatedAt: time.Now().UTC(),
	})
}

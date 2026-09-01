package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	manager              *device.Manager
	router               *router.Router
	config               device.Config
	automation           *automation.Engine
	store                *sqlitestore.Store
	updates              chan struct{}
	aiMu                 sync.RWMutex
	aiConfig             ai.Config
	aiAPIKey             string
	equipmentConfigPath  string
	ruleSource           fs.FS
	routeConfigPath      string
	automationConfigPath string
	ruleReloadMu         sync.Mutex
	ruleFingerprint      [32]byte
	watchCancel          context.CancelFunc
	pending              map[string]AIActionPermission
	permissionSequence   atomic.Uint64
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
	service := &StudioService{router: routes, config: config, automation: engine, store: history, updates: make(chan struct{}, 1), aiConfig: ai.Config{Provider: "local"}, equipmentConfigPath: configPath, ruleSource: ruleSource, routeConfigPath: routePath, automationConfigPath: automationPath, pending: map[string]AIActionPermission{}}
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

func (s *StudioService) TestAIConfiguration(config ai.Config, apiKey string) (string, error) {
	if config.Provider == "local" {
		return "Local grounded provider is ready.", nil
	}
	apiKey = strings.TrimSpace(apiKey)
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
	if err := device.SaveConfig(s.equipmentConfigPath, config); err != nil {
		return EquipmentConfigSaveResult{}, err
	}
	return EquipmentConfigSaveResult{Path: s.equipmentConfigPath, RestartRequired: true}, nil
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
		return CopilotReply{Answer: fmt.Sprintf("已拒绝 %s；没有向 %s 发送任何消息。", permission.Command, permission.EquipmentID), Evidence: []string{"permission denied"}}
	}
	correlationID := "ai-" + permission.ID
	value, err := s.manager.SubmitCommand(permission.EquipmentID, permission.Command, permission.Parameters, correlationID, permission.ID)
	if err != nil {
		return CopilotReply{Answer: "命令执行失败：" + err.Error(), Evidence: []string{"permission allowed", "command rejected before send"}}
	}
	return CopilotReply{Answer: fmt.Sprintf("已授权并提交 %s，commandId=%s。执行结果会以 recipe.sent 或 recipe.failed Event 返回。", value.Name, value.ID), Evidence: []string{"permission allowed", "command queue " + permission.EquipmentID}}
}

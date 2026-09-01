package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
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
	manager            *device.Manager
	router             *router.Router
	config             device.Config
	automation         *automation.Engine
	store              *sqlitestore.Store
	updates            chan struct{}
	aiMu               sync.RWMutex
	aiConfig           ai.Config
	pending            map[string]AIActionPermission
	permissionSequence atomic.Uint64
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
	return newStudioService(source, databasePath)
}

func newStudioService(source fs.FS, databasePath string) (*StudioService, error) {
	config, err := device.LoadConfig(source, "configs/devices.yaml")
	if err != nil {
		return nil, err
	}
	mockMQ := sink.NewMemory("mock-mq")
	history, err := sqlitestore.Open(databasePath)
	if err != nil {
		return nil, err
	}
	routes, err := router.Load(source, "configs/routes.yaml", mockMQ, history)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	engine, err := automation.Load(source, "configs/automations.yaml")
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	service := &StudioService{router: routes, config: config, automation: engine, store: history, updates: make(chan struct{}, 1), aiConfig: ai.Config{Provider: "local"}, pending: map[string]AIActionPermission{}}
	manager, err := device.NewManager(source, config, routes, engine, history, service.notify)
	if err != nil {
		_ = history.Close()
		return nil, err
	}
	service.manager = manager
	return service, nil
}

func (s *StudioService) start(ctx context.Context)     { s.manager.ConnectAuto(ctx, s.config) }
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

func (s *StudioService) close() error { return s.store.Close() }

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

func (s *StudioService) ConfigureAI(config ai.Config) error {
	switch config.Provider {
	case "local", "responses", "chat":
	default:
		return fmt.Errorf("unsupported AI provider %q", config.Provider)
	}
	s.aiMu.Lock()
	s.aiConfig = config
	s.aiMu.Unlock()
	return nil
}

// AskCopilot is a grounded, deterministic first-pass assistant. A provider-backed
// implementation can replace it without moving credentials into the frontend.
func (s *StudioService) AskCopilot(question string, equipmentID string) CopilotReply {
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
		provider, err := ai.NewProvider(config, os.Getenv("EAPSTUDIO_AI_API_KEY"))
		if err != nil {
			return CopilotReply{Answer: "AI provider 配置错误：" + err.Error()}
		}
		contextJSON, _ := json.Marshal(selected)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		answer, err := provider.Complete(ctx, ai.Request{
			System: "You are EapStudio Equipment Copilot. Answer using only the supplied runtime snapshot. Never claim a command was sent and never bypass UI permission approval.",
			Prompt: question + "\n\nRuntime snapshot:\n" + string(contextJSON),
		})
		if err != nil {
			return CopilotReply{Answer: "AI provider 调用失败：" + err.Error(), Evidence: []string{config.Provider + " adapter"}}
		}
		return CopilotReply{Answer: answer, Evidence: []string{config.Provider + " adapter", "设备 Runtime 快照"}}
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

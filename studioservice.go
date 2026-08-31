package main

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"eapstudio/internal/automation"
	"eapstudio/internal/device"
	"eapstudio/internal/router"
	"eapstudio/internal/sink"
	sqlitestore "eapstudio/internal/store/sqlite"
)

type StudioService struct {
	manager    *device.Manager
	router     *router.Router
	config     device.Config
	automation *automation.Engine
	store      *sqlitestore.Store
	updates    chan struct{}
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
	Answer      string   `json:"answer"`
	Evidence    []string `json:"evidence"`
	Suggestions []string `json:"suggestions"`
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
	service := &StudioService{router: routes, config: config, automation: engine, store: history, updates: make(chan struct{}, 1)}
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

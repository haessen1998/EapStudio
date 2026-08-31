package device

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"eapstudio/internal/automation"
	"eapstudio/internal/command"
	"eapstudio/internal/domain"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/equipment"
	"eapstudio/internal/event"
	"eapstudio/internal/profile"
	"eapstudio/internal/router"
)

type Runtime struct {
	definition Definition
	profile    *profile.CompiledProfile
	driver     driver.Driver
	adapter    equipment.Adapter
	router     *router.Router
	automation *automation.Engine
	recorder   Recorder
	queue      chan driver.Message
	commands   chan command.Command
	stop       chan struct{}
	onChange   func()
	simCancel  context.CancelFunc
	simSeq     atomic.Uint64

	mu         sync.RWMutex
	state      driver.ConnectionState
	detail     string
	messages   []driver.Message
	events     []event.Event
	commandLog []command.Command
}

type Recorder interface {
	RecordTrace(driver.Message)
	UpsertCommand(context.Context, command.Command) error
}

type SimulatorScenario struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Event       string `json:"event"`
	Direction   string `json:"direction"`
	Stream      uint8  `json:"stream"`
	Function    uint8  `json:"function"`
}

type Snapshot struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Profile     string                 `json:"profile"`
	ProfileName string                 `json:"profileName"`
	Vendor      string                 `json:"vendor"`
	Model       string                 `json:"model"`
	Driver      string                 `json:"driver"`
	Host        string                 `json:"host"`
	Port        int                    `json:"port"`
	State       driver.ConnectionState `json:"state"`
	StateDetail string                 `json:"stateDetail"`
	Messages    []driver.Message       `json:"messages"`
	Events      []event.Event          `json:"events"`
	Commands    []command.Command      `json:"commands"`
	Scenarios   []SimulatorScenario    `json:"scenarios"`
}

func NewRuntime(definition Definition, compiled *profile.CompiledProfile, protocol driver.Driver, adapter equipment.Adapter, routes *router.Router, engine *automation.Engine, recorder Recorder, onChange func()) *Runtime {
	runtime := &Runtime{definition: definition, profile: compiled, driver: protocol, adapter: adapter, router: routes, automation: engine, recorder: recorder, queue: make(chan driver.Message, 128), commands: make(chan command.Command, 64), stop: make(chan struct{}), state: driver.StateDisconnected, onChange: onChange}
	protocol.OnMessage(runtime.receive)
	protocol.OnState(runtime.stateChanged)
	go runtime.process()
	go runtime.processCommands()
	return runtime
}

func (r *Runtime) Connect(ctx context.Context) error {
	if err := r.driver.Open(ctx); err != nil {
		return err
	}
	if _, ok := r.driver.(*driver.SimulatorDriver); ok {
		r.startSimulator()
	}
	return nil
}
func (r *Runtime) Disconnect() error {
	r.mu.Lock()
	if r.simCancel != nil {
		r.simCancel()
		r.simCancel = nil
	}
	r.mu.Unlock()
	return r.driver.Close()
}
func (r *Runtime) EmitScenario(name string) error {
	simulator, ok := r.driver.(*driver.SimulatorDriver)
	if !ok {
		return fmt.Errorf("device %s is not using the simulator", r.definition.ID)
	}
	scenario, ok := r.profile.Spec.Simulator.Scenarios[name]
	if !ok {
		return fmt.Errorf("profile %s has no simulator scenario %q", r.profile.Metadata.Name, name)
	}
	sequence := r.simSeq.Add(1)
	if scenario.Event != "" {
		message, err := r.adapter.BuildEvent(context.Background(), scenario.Event, resolveScenarioData(scenario.Data, sequence), r.profile)
		if err != nil {
			return err
		}
		message.Metadata["scenario"] = name
		return simulator.Deliver(message)
	}

	message := driver.Message{
		ID:          fmt.Sprintf("scenario-%s-%06d", name, sequence),
		EquipmentID: r.definition.ID,
		Timestamp:   time.Now(),
		Stream:      scenario.Message.Stream,
		Function:    scenario.Message.Function,
		Wait:        scenario.Message.Wait,
		SML:         replaceScenarioText(scenario.Message.SML, sequence),
		Tree:        replaceScenarioText(scenario.Message.SML, sequence),
		Fields:      resolveScenarioData(scenario.Message.Fields, sequence),
		Metadata:    map[string]string{"scenario": name},
	}
	if scenario.Direction == "inbound" {
		return simulator.Deliver(message)
	}
	message.Direction = driver.DirectionOut
	r.recordMessage(message)
	reply, err := r.driver.Send(context.Background(), message)
	if reply != nil {
		reply.EquipmentID = r.definition.ID
		r.recordMessage(*reply)
	}
	return err
}

func (r *Runtime) receive(message driver.Message) {
	message.EquipmentID = r.definition.ID
	message.Direction = driver.DirectionIn
	r.recordMessage(message)
	// The protocol acknowledgement stays on the receive path and never waits for routing or AI.
	if message.Wait && ((message.Stream == 6 && message.Function == 11) || (message.Stream == 5 && message.Function == 1)) {
		response := driver.Message{ID: "reply-" + message.ID, EquipmentID: r.definition.ID, Direction: driver.DirectionOut, Timestamp: time.Now(), Stream: message.Stream, Function: message.Function + 1, SML: fmt.Sprintf("S%dF%d\n<B 0>\n.", message.Stream, message.Function+1)}
		if err := r.driver.Reply(context.Background(), message, response); err != nil {
			response.Metadata = map[string]string{"error": err.Error()}
		}
		r.recordMessage(response)
	}
	select {
	case r.queue <- message:
	default:
		r.stateChanged(driver.StateError, "event pipeline queue is full")
	}
}

func (r *Runtime) process() {
	for {
		select {
		case <-r.stop:
			return
		case message := <-r.queue:
			events, err := r.adapter.Parse(context.Background(), message, r.profile)
			if err != nil {
				r.stateChanged(driver.StateError, err.Error())
				continue
			}
			for _, value := range events {
				r.publish(value)
			}
		}
	}
}

func (r *Runtime) processCommands() {
	for {
		select {
		case <-r.stop:
			return
		case value := <-r.commands:
			r.executeCommand(value)
		}
	}
}

func (r *Runtime) executeCommand(value command.Command) {
	value.Status = command.StatusSending
	r.upsertCommand(value)
	outbound, err := r.adapter.BuildCommand(context.Background(), value, r.profile)
	if err == nil {
		outbound.ID = "out-" + value.ID
		outbound.EquipmentID = value.EquipmentID
		r.recordMessage(outbound)
		var reply *driver.Message
		reply, err = r.driver.Send(context.Background(), outbound)
		if reply != nil {
			reply.EquipmentID = value.EquipmentID
			r.recordMessage(*reply)
		}
		if err == nil {
			err = r.adapter.ValidateCommandReply(context.Background(), value, reply, r.profile)
		}
	}
	definition := r.profile.Spec.Commands[value.Name]
	now := time.Now()
	value.CompletedAt = &now
	eventName := definition.SuccessEvent
	if err != nil {
		value.Status = command.StatusFailed
		value.Error = err.Error()
		eventName = definition.FailureEvent
	} else {
		value.Status = command.StatusSucceeded
	}
	r.upsertCommand(value)
	data := make(map[string]any, len(value.Parameters)+1)
	for key, item := range value.Parameters {
		data[key] = item
	}
	if err != nil {
		data["error"] = err.Error()
	}
	r.publish(event.Event{ID: "evt-" + value.ID, Type: domain.TypeEvent, Name: eventName, EquipmentID: value.EquipmentID, CorrelationID: value.CorrelationID, CausationID: value.ID, CommandID: value.ID, Timestamp: now, Source: event.Source{Protocol: "secs-gem", Stream: definition.Stream, Function: definition.Function}, Data: data})
}

func (r *Runtime) publish(value event.Event) {
	r.recordEvent(value)
	_ = r.router.Route(context.Background(), value)
	for _, next := range r.automation.Handle(value) {
		if _, ok := r.profile.Spec.Commands[next.Name]; !ok {
			r.stateChanged(driver.StateError, "automation created undefined command "+next.Name)
			continue
		}
		r.upsertCommand(next)
		select {
		case r.commands <- next:
		default:
			r.stateChanged(driver.StateError, "command queue is full")
		}
	}
}

func (r *Runtime) startSimulator() {
	names := r.scenarioNames(true)
	if len(names) == 0 {
		return
	}
	r.mu.Lock()
	if r.simCancel != nil {
		r.simCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.simCancel = cancel
	r.mu.Unlock()
	go func() {
		timer := time.NewTimer(350 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_ = r.EmitScenario(names[0])
		}
		ticker := time.NewTicker(12 * time.Second)
		defer ticker.Stop()
		index := 1
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.EmitScenario(names[index%len(names)])
				index++
			}
		}
	}()
}

func (r *Runtime) scenarioNames(eventsOnly bool) []string {
	names := make([]string, 0, len(r.profile.Spec.Simulator.Scenarios))
	for name, scenario := range r.profile.Spec.Simulator.Scenarios {
		if eventsOnly && scenario.Event == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for index, name := range names {
		if name == "material-arrival" {
			names[0], names[index] = names[index], names[0]
			break
		}
	}
	return names
}

func resolveScenarioData(data map[string]any, sequence uint64) map[string]any {
	result := make(map[string]any, len(data))
	replacement := fmt.Sprintf("%03d", sequence)
	for key, value := range data {
		if text, ok := value.(string); ok {
			result[key] = strings.ReplaceAll(text, "${sequence}", replacement)
		} else {
			result[key] = value
		}
	}
	return result
}

func replaceScenarioText(value string, sequence uint64) string {
	return strings.ReplaceAll(value, "${sequence}", fmt.Sprintf("%03d", sequence))
}

func (r *Runtime) stateChanged(state driver.ConnectionState, detail string) {
	r.mu.Lock()
	r.state = state
	r.detail = detail
	r.mu.Unlock()
	r.changed()
}
func (r *Runtime) recordMessage(value driver.Message) {
	r.mu.Lock()
	r.messages = append(r.messages, value)
	if len(r.messages) > 100 {
		r.messages = append([]driver.Message(nil), r.messages[len(r.messages)-100:]...)
	}
	r.mu.Unlock()
	if r.recorder != nil {
		r.recorder.RecordTrace(value)
	}
	r.changed()
}
func (r *Runtime) recordEvent(value event.Event) {
	r.mu.Lock()
	r.events = append(r.events, value)
	if len(r.events) > 100 {
		r.events = append([]event.Event(nil), r.events[len(r.events)-100:]...)
	}
	r.mu.Unlock()
	r.changed()
}
func (r *Runtime) upsertCommand(value command.Command) {
	r.mu.Lock()
	found := false
	for index := range r.commandLog {
		if r.commandLog[index].ID == value.ID {
			r.commandLog[index] = value
			found = true
			break
		}
	}
	if !found {
		r.commandLog = append(r.commandLog, value)
	}
	if len(r.commandLog) > 100 {
		r.commandLog = append([]command.Command(nil), r.commandLog[len(r.commandLog)-100:]...)
	}
	r.mu.Unlock()
	if r.recorder != nil {
		_ = r.recorder.UpsertCommand(context.Background(), value)
	}
	r.changed()
}
func (r *Runtime) changed() {
	if r.onChange != nil {
		r.onChange()
	}
}

func (r *Runtime) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	messages := make([]driver.Message, len(r.messages))
	copy(messages, r.messages)
	events := make([]event.Event, len(r.events))
	copy(events, r.events)
	commands := make([]command.Command, len(r.commandLog))
	copy(commands, r.commandLog)
	scenarios := make([]SimulatorScenario, 0, len(r.profile.Spec.Simulator.Scenarios))
	for _, name := range r.scenarioNames(false) {
		scenario := r.profile.Spec.Simulator.Scenarios[name]
		scenarios = append(scenarios, SimulatorScenario{ID: name, DisplayName: scenario.DisplayName, Event: scenario.Event, Direction: scenario.Direction, Stream: scenario.Message.Stream, Function: scenario.Message.Function})
	}
	return Snapshot{ID: r.definition.ID, Name: r.definition.Name, Profile: r.definition.Profile, ProfileName: r.profile.Metadata.Name, Vendor: r.profile.Metadata.Vendor, Model: r.profile.Metadata.Model, Driver: r.definition.Driver, Host: r.definition.Connection.Host, Port: r.definition.Connection.Port, State: r.state, StateDetail: r.detail, Messages: messages, Events: events, Commands: commands, Scenarios: scenarios}
}

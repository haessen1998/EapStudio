package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"eapstudio/internal/ai"
	"eapstudio/internal/command"
	"eapstudio/internal/domain"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/event"
)

func TestStoreSeparatesTraceAndDomainRecords(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	store.RecordTrace(driver.Message{ID: "msg-1", EquipmentID: "ETCHER-01", Direction: driver.DirectionIn, Timestamp: now, Stream: 5, Function: 1, RawHex: "0102"})
	value := event.Event{ID: "evt-1", Type: domain.TypeEvent, Name: "alarm.raised", EquipmentID: "ETCHER-01", CorrelationID: "alarm-ETCHER-01-7001", Timestamp: now, Data: map[string]any{"alarmId": "7001", "code": "128", "text": "Pressure high", "severity": "critical"}}
	if err := store.Send(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCommand(context.Background(), command.Command{ID: "cmd-1", Type: domain.TypeCommand, Name: "send.recipe", EquipmentID: "ETCHER-01", Status: command.StatusPending, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for store.Stats(context.Background()).TraceCount == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stats := store.Stats(context.Background())
	if stats.TraceCount != 1 || stats.EventCount != 1 || stats.CommandCount != 1 || stats.AlarmCount != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	alarms, err := store.Alarms(context.Background(), 10)
	if err != nil || len(alarms) != 1 || alarms[0].State != "active" {
		t.Fatalf("alarms = %#v, err = %v", alarms, err)
	}
	traces, err := store.QueryTraces(context.Background(), HistoryQuery{Page: 1, PageSize: 25, EquipmentID: "ETCHER-01", Search: "S5F1"})
	if err != nil || traces.Total != 1 || len(traces.Items) != 1 || traces.Items[0].RawHex != "0102" {
		t.Fatalf("traces = %#v, err = %v", traces, err)
	}
	events, err := store.QueryEvents(context.Background(), HistoryQuery{Page: 1, PageSize: 25, Name: "alarm.raised"})
	if err != nil || events.Total != 1 || len(events.Items) != 1 || events.Items[0].CorrelationID != value.CorrelationID {
		t.Fatalf("events = %#v, err = %v", events, err)
	}
	commands, err := store.QueryCommands(context.Background(), HistoryQuery{Page: 1, PageSize: 25, Status: string(command.StatusPending)})
	if err != nil || commands.Total != 1 || len(commands.Items) != 1 || commands.Items[0].Name != "send.recipe" {
		t.Fatalf("commands = %#v, err = %v", commands, err)
	}
}

func TestCopilotHistoryPersistsMessagesAndPermissionState(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "copilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	permission := &CopilotPermission{ID: "permission-1", Tool: "send.command", EquipmentID: "AOI-01", Command: "send.recipe"}
	if err := store.RecordCopilotMessage(ctx, CopilotMessage{ID: "user-1", EquipmentID: "AOI-01", Role: "user", Text: "status", Attachments: []ai.Attachment{{Name: "manual.txt", MediaType: "text/plain", Size: 10}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCopilotMessage(ctx, CopilotMessage{ID: "assistant-1", EquipmentID: "AOI-01", Role: "assistant", Text: "selected", Evidence: []string{"runtime"}, Permission: permission, PermissionStatus: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateCopilotPermission(ctx, permission.ID, "allowed"); err != nil {
		t.Fatal(err)
	}
	history, err := store.CopilotHistory(ctx, "AOI-01", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Text != "status" || history[1].PermissionStatus != "allowed" || history[1].Permission == nil {
		t.Fatalf("history = %#v", history)
	}
	if err := store.ClearCopilotHistory(ctx, "AOI-01"); err != nil {
		t.Fatal(err)
	}
	history, err = store.CopilotHistory(ctx, "AOI-01", 20)
	if err != nil || len(history) != 0 {
		t.Fatalf("cleared history = %#v, err = %v", history, err)
	}
}

func TestApplyRetentionPrunesHistoryButPreservesActiveAlarms(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().UTC().AddDate(0, 0, -40)
	if _, err := store.db.Exec(`
		INSERT INTO protocol_traces(message_id,equipment_id,direction,occurred_at,stream,function,wait_bit,system_bytes,sml,raw_hex,metadata_json) VALUES('old-message','EQP-01','IN',?,1,1,0,1,'','','{}');
		INSERT INTO domain_events(id,type,name,equipment_id,correlation_id,causation_id,command_id,occurred_at,source_json,data_json) VALUES('old-event','event','material.arrived','EQP-01','','','',?,'{}','{}');
		INSERT INTO commands(id,type,name,equipment_id,correlation_id,causation_id,status,created_at,parameters_json,error) VALUES('old-command','command','send.recipe','EQP-01','','','succeeded',?,'{}','');
		INSERT INTO alarms(equipment_id,alarm_id,code,text,severity,state,raised_at,correlation_id) VALUES('EQP-01','active','1','active','warning','active',?,'active');
		INSERT INTO alarms(equipment_id,alarm_id,code,text,severity,state,raised_at,cleared_at,correlation_id) VALUES('EQP-01','cleared','2','cleared','warning','cleared',?,?, 'cleared');
	`, old, old, old, old, old, old); err != nil {
		t.Fatal(err)
	}

	result, err := store.ApplyRetention(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if result.TraceDeleted != 1 || result.EventDeleted != 1 || result.CommandDeleted != 1 || result.AlarmDeleted != 1 {
		t.Fatalf("retention result = %#v", result)
	}
	alarms, err := store.Alarms(context.Background(), 10)
	if err != nil || len(alarms) != 1 || alarms[0].AlarmID != "active" {
		t.Fatalf("alarms = %#v, err = %v", alarms, err)
	}
}

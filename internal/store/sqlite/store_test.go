package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
}

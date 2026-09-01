package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDemoPipelineRunsForTwoDevices(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/eapstudio-test.db")
	if err != nil {
		t.Fatal(err)
	}
	service.start(context.Background())
	defer func() {
		_ = service.DisconnectDevice("ETCHER-01")
		_ = service.DisconnectDevice("ETCHER-02")
		_ = service.close()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := service.Snapshot()
		if len(snapshot.Devices) == 2 && len(snapshot.Deliveries) >= 8 && len(snapshot.Devices[0].Commands) > 0 && len(snapshot.Devices[1].Commands) > 0 {
			for _, device := range snapshot.Devices {
				if device.State != "selected" {
					t.Fatalf("device %s state = %s", device.ID, device.State)
				}
				if len(device.Messages) < 2 {
					t.Fatalf("device %s missing S6F11/S6F12 trace", device.ID)
				}
				if device.Events[0].Name != "material.arrived" || device.Events[0].Type != "event" {
					t.Fatalf("first event = %#v", device.Events[0])
				}
				if device.Commands[0].Name != "send.recipe" || device.Commands[0].Type != "command" || device.Commands[0].Status != "succeeded" {
					t.Fatalf("command = %#v", device.Commands[0])
				}
				if device.Events[1].Name != "recipe.sent" || device.Events[1].CorrelationID != device.Events[0].CorrelationID {
					t.Fatalf("outcome event = %#v", device.Events[1])
				}
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pipeline did not produce events in time: %#v", service.Snapshot())
}

func TestSimulatorSupportsAlarmAndArbitraryOutboundMessages(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/eapstudio-test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	if err := service.ConnectDevice("ETCHER-01"); err != nil {
		t.Fatal(err)
	}
	defer service.DisconnectDevice("ETCHER-01")
	if err := service.EmitSimulatorScenario("ETCHER-01", "alarm-raised"); err != nil {
		t.Fatal(err)
	}
	if err := service.EmitSimulatorScenario("ETCHER-01", "remote-command"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := service.Snapshot()
		device := snapshot.Devices[0]
		seen := map[string]bool{}
		for _, message := range device.Messages {
			seen[message.Name()] = true
		}
		if seen["S5F1"] && seen["S5F2"] && seen["S2F41"] && seen["S2F42"] && len(snapshot.Alarms) == 1 {
			for _, message := range device.Messages {
				if message.RawHex == "" {
					t.Fatalf("message %s has no raw HSMS frame", message.Name())
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("simulator messages/alarms missing: %#v", service.Snapshot())
}

func TestCopilotCommandRequiresExplicitPermission(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/eapstudio-test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	if err := service.ConnectDevice("ETCHER-01"); err != nil {
		t.Fatal(err)
	}
	defer service.DisconnectDevice("ETCHER-01")

	request := service.AskCopilot("请发送命令", "ETCHER-01")
	if request.Permission == nil || request.Permission.Command != "send.recipe" {
		t.Fatalf("permission = %#v", request.Permission)
	}
	denied := service.ResolveAIAction(request.Permission.ID, false)
	if !strings.Contains(denied.Answer, "拒绝") || len(service.Snapshot().Devices[0].Commands) != 0 {
		t.Fatalf("denied reply=%#v commands=%#v", denied, service.Snapshot().Devices[0].Commands)
	}

	request = service.AskCopilot("请下发命令", "ETCHER-01")
	allowed := service.ResolveAIAction(request.Permission.ID, true)
	if !strings.Contains(allowed.Answer, "已授权") {
		t.Fatalf("allowed reply = %#v", allowed)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		commands := service.Snapshot().Devices[0].Commands
		if len(commands) == 1 && commands[0].Status == "succeeded" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approved command did not complete: %#v", service.Snapshot().Devices[0].Commands)
}

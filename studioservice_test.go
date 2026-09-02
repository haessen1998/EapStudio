package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eapstudio/internal/ai"
	"eapstudio/internal/device"
)

func TestCompareAndMergePackagedEquipmentConfig(t *testing.T) {
	source := os.DirFS(".")
	packaged, err := device.LoadConfig(source, "configs/devices.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(packaged.Devices) < 3 {
		t.Fatalf("packaged demos = %d", len(packaged.Devices))
	}
	runtimeConfig := device.Config{Devices: append([]device.Definition(nil), packaged.Devices[:2]...)}
	configPath := filepath.Join(t.TempDir(), "devices.yaml")
	if err := device.SaveConfig(configPath, runtimeConfig); err != nil {
		t.Fatal(err)
	}
	service, err := newStudioServiceWithConfig(source, filepath.Join(t.TempDir(), "history.db"), runtimeConfig, configPath, source, "configs/routes.yaml", "configs/automations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()

	comparison, err := service.CompareEquipmentConfig()
	if err != nil {
		t.Fatal(err)
	}
	if comparison.RuntimeCount != 2 || comparison.PackagedCount != len(packaged.Devices) || len(comparison.Missing) != len(packaged.Devices)-2 {
		t.Fatalf("comparison = %#v", comparison)
	}
	merged, err := service.MergePackagedDemoDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Added) != len(packaged.Devices)-2 || merged.RestartRequired {
		t.Fatalf("merge = %#v", merged)
	}
	saved, err := device.LoadConfig(os.DirFS(filepath.Dir(configPath)), filepath.Base(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Devices) != len(packaged.Devices) {
		t.Fatalf("saved devices = %d, want %d", len(saved.Devices), len(packaged.Devices))
	}
	order := make([]string, 0, len(saved.Devices))
	for index := len(saved.Devices) - 1; index >= 0; index-- {
		order = append(order, saved.Devices[index].ID)
	}
	if err := service.SaveDeviceOrder(order); err != nil {
		t.Fatal(err)
	}
	reordered, err := device.LoadConfig(os.DirFS(filepath.Dir(configPath)), filepath.Base(configPath))
	if err != nil || reordered.Devices[0].ID != order[0] {
		t.Fatalf("reordered = %#v, err = %v", reordered, err)
	}
	wantRuntimeFirst := order[0]
	if snapshots := service.Snapshot().Devices; len(snapshots) != len(order) || snapshots[0].ID != wantRuntimeFirst {
		t.Fatalf("runtime order was not updated: %#v", snapshots)
	}
}

func snapshotDevice(t *testing.T, service *StudioService, id string) device.Snapshot {
	t.Helper()
	for _, value := range service.Snapshot().Devices {
		if value.ID == id {
			return value
		}
	}
	t.Fatalf("device %s not found", id)
	return device.Snapshot{}
}

func TestConfigureAIKeepsAPIKeyInBackendOnly(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/eapstudio-test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	config := ai.Config{Provider: "responses", BaseURL: "https://example.invalid/v1", Model: "test-model"}
	if err := service.ConfigureAI(config, "  session-secret  "); err != nil {
		t.Fatal(err)
	}
	if service.aiAPIKey != "session-secret" || service.AIConfig() != config {
		t.Fatalf("AI config/key not retained correctly: config=%#v key=%q", service.AIConfig(), service.aiAPIKey)
	}
	profiles, err := service.ListAIProfiles()
	if err != nil || len(profiles) != 3 || profiles[0].APIKey != "" {
		t.Fatalf("public AI profiles = %#v, err = %v", profiles, err)
	}
}

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
		if len(snapshot.Devices) == 4 && len(snapshot.Deliveries) >= 16 {
			ready := true
			for _, value := range snapshot.Devices {
				ready = ready && len(value.Commands) > 0
			}
			if !ready {
				time.Sleep(25 * time.Millisecond)
				continue
			}
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
		device := snapshotDevice(t, service, "ETCHER-01")
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

	request := service.AskCopilot("请发送命令", "ETCHER-01", nil)
	if request.Permission == nil || request.Permission.Command != "send.recipe" {
		t.Fatalf("permission = %#v", request.Permission)
	}
	if len(request.Permission.ParameterDiff) == 0 || request.Permission.ExpiresAt.Before(time.Now()) {
		t.Fatalf("permission change preview/expiry = %#v", request.Permission)
	}
	denied := service.ResolveAIAction(request.Permission.ID, false)
	if !strings.Contains(denied.Answer, "拒绝") || len(snapshotDevice(t, service, "ETCHER-01").Commands) != 0 {
		t.Fatalf("denied reply=%#v commands=%#v", denied, snapshotDevice(t, service, "ETCHER-01").Commands)
	}

	request = service.AskCopilot("请下发命令", "ETCHER-01", nil)
	allowed := service.ResolveAIAction(request.Permission.ID, true)
	if !strings.Contains(allowed.Answer, "已授权") {
		t.Fatalf("allowed reply = %#v", allowed)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		commands := snapshotDevice(t, service, "ETCHER-01").Commands
		if len(commands) == 1 && commands[0].Status == "succeeded" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approved command did not complete: %#v", snapshotDevice(t, service, "ETCHER-01").Commands)
}

func TestPermissionPolicyCanDenyAIWrites(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/permission-policy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	policy := defaultPermissionPolicy()
	policy.Mode = "deny"
	if err := service.SavePermissionPolicy(policy); err != nil {
		t.Fatal(err)
	}
	reply := service.AskCopilot("请发送命令", "ETCHER-01", nil)
	if reply.Permission != nil || !strings.Contains(reply.Answer, "阻止") {
		t.Fatalf("reply = %#v", reply)
	}
}

func TestCopilotStreamPersistsConversation(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/copilot-stream.db")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	session, err := service.CreateCopilotSession("ETCHER-01")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AskCopilotStream("request-1", session.ID, "设备状态", "ETCHER-01", nil); err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	deadline := time.After(2 * time.Second)
	for {
		select {
		case value := <-service.copilotEventSignal():
			if value.RequestID != "request-1" {
				continue
			}
			streamed.WriteString(value.Delta)
			if value.Done {
				if value.Reply == nil || streamed.String() != value.Reply.Answer {
					t.Fatalf("streamed=%q reply=%#v", streamed.String(), value.Reply)
				}
				history, err := service.CopilotHistory(session.ID)
				if err != nil || len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
					t.Fatalf("history=%#v err=%v", history, err)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for copilot stream")
		}
	}
}

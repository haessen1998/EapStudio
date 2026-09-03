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
	sqlitestore "eapstudio/internal/store/sqlite"
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

func TestWorkspaceInitializationCreationAndHotSwitch(t *testing.T) {
	configRoot := t.TempDir()
	workspaceRoot, activeID, directory, err := initializeWorkspaces(os.DirFS("."), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	if activeID != "default" {
		t.Fatalf("active workspace = %q", activeID)
	}
	for _, path := range []string{"devices.yaml", "routes.yaml", "automations.yaml", "profiles/demo/etcher-x100.yaml"} {
		if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(path))); err != nil {
			t.Fatalf("workspace file %s: %v", path, err)
		}
	}
	config, err := device.LoadConfig(os.DirFS(directory), "devices.yaml")
	if err != nil || len(config.Devices) != 3 {
		t.Fatalf("default workspace devices = %#v, err = %v", config.Devices, err)
	}
	service, err := newStudioServiceWithConfig(os.DirFS("."), filepath.Join(configRoot, "history.db"), config, filepath.Join(directory, "devices.yaml"), os.DirFS(directory), "routes.yaml", "automations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	service.workspaceRoot, service.activeWorkspaceID = workspaceRoot, activeID
	created, err := service.CreateWorkspace("Mixed Factory")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := service.SwitchWorkspace(created.ID)
	if err != nil || !selected.Active || service.activeWorkspaceID != created.ID {
		t.Fatalf("selected workspace = %#v, err = %v", selected, err)
	}
	if err := service.DeleteWorkspace(created.ID); err == nil {
		t.Fatal("active workspace deletion should be rejected")
	}
	if err := service.DeleteWorkspace("default"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceInitializationPreservesPartialLegacyRuntime(t *testing.T) {
	configRoot := t.TempDir()
	legacyYAML := `devices:
  - id: ETCHER-01
    name: Legacy simulator
    profile: profiles/demo/etcher-x100.yaml
    driver: simulator
    autoConnect: true
    connection: {protocol: hsms-ss, mode: active, host: 127.0.0.1, port: 5001, sessionId: 0}
  - id: C6-340
    name: Production C6-340
    profile: profiles/demo/etcher-x100.yaml
    driver: go-secs
    autoConnect: true
    connection: {protocol: hsms-ss, mode: active, host: 192.0.2.34, port: 8848, sessionId: 0}
`
	if err := os.WriteFile(filepath.Join(configRoot, "devices.yaml"), []byte(legacyYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, directory, err := initializeWorkspaces(os.DirFS("."), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := device.LoadConfig(os.DirFS(directory), "devices.yaml")
	if err != nil || len(migrated.Devices) != 2 || migrated.Devices[1].ID != "C6-340" {
		t.Fatalf("legacy devices were not preserved: %#v, err=%v", migrated.Devices, err)
	}
	if migrated.Devices[0].Role != "equipment-simulator" || migrated.Devices[0].Connection.Mode != "passive" || migrated.Devices[0].Connection.Host != "0.0.0.0" {
		t.Fatalf("legacy simulator was not upgraded to a passive twin: %#v", migrated.Devices[0])
	}
	if migrated.Devices[1].Role != "controller" || migrated.Devices[1].Connection.Host != "192.0.2.34" {
		t.Fatalf("production controller was not preserved: %#v", migrated.Devices[1])
	}
	if _, err := os.Stat(filepath.Join(directory, "devices.yaml.legacy.bak")); err != nil {
		t.Fatalf("legacy devices backup: %v", err)
	}
	for _, path := range []string{"routes.yaml", "automations.yaml", "profiles/demo/etcher-x100.yaml"} {
		if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(path))); err != nil {
			t.Fatalf("packaged fallback %s: %v", path, err)
		}
	}
}

func TestMessageCatalogCoversS1F1ThroughS17F13(t *testing.T) {
	catalog := (&StudioService{}).MessageCatalog()
	if len(catalog) < 17*13 {
		t.Fatalf("catalog size = %d, want at least %d", len(catalog), 17*13)
	}
	foundS2F41, foundS17F13 := false, false
	for _, item := range catalog {
		foundS2F41 = foundS2F41 || item.Stream == 2 && item.Function == 41
		foundS17F13 = foundS17F13 || item.Stream == 17 && item.Function == 13
	}
	if !foundS2F41 || !foundS17F13 {
		t.Fatalf("catalog missing required messages: S2F41=%v S17F13=%v", foundS2F41, foundS17F13)
	}
}

func TestWorkspaceProfileMigrationPreservesBackupAndOutboundMessages(t *testing.T) {
	directory := t.TempDir()
	profileDirectory := filepath.Join(directory, "profiles")
	if err := os.MkdirAll(profileDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profileDirectory, "legacy.yaml")
	legacy := `apiVersion: eapstudio/v1alpha1
kind: EquipmentProfile
metadata: {name: legacy}
spec:
  simulator:
    scenarios:
      recipe-download:
        displayName: Download recipe
        direction: outbound
        message: {stream: 7, function: 3, wait: true, sml: "S7F3 W\n."}
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateWorkspaceProfiles(directory); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "simulator:") || !strings.Contains(string(data), "download.recipe") {
		t.Fatalf("unexpected migrated profile:\n%s", data)
	}
	if _, err := os.Stat(path + ".legacy.bak"); err != nil {
		t.Fatalf("legacy profile backup: %v", err)
	}
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

func TestDemoPipelineRunsForThreeDevices(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/eapstudio-test.db")
	if err != nil {
		t.Fatal(err)
	}
	service.start(context.Background())
	defer func() {
		_ = service.DisconnectDevice("ETCHER-01")
		_ = service.close()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := service.Snapshot()
		if len(snapshot.Devices) == 3 && len(snapshot.Deliveries) >= 12 {
			ready := true
			for _, value := range snapshot.Devices {
				ready = ready && len(value.Commands) > 0 && value.Commands[0].Status == "succeeded" && len(value.Events) > 1
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

func TestSimulatorSupportsEquipmentAlarmMessages(t *testing.T) {
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

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := service.Snapshot()
		device := snapshotDevice(t, service, "ETCHER-01")
		seen := map[string]bool{}
		for _, message := range device.Messages {
			seen[message.Name()] = true
		}
		if seen["S5F1"] && seen["S5F2"] && len(snapshot.Alarms) == 1 {
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

func TestOperatorCanSendRawSMLAndReceiveReplyOnce(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/eapstudio-test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	if err := service.ConnectDevice("ETCHER-01"); err != nil {
		t.Fatal(err)
	}
	defer service.DisconnectDevice("ETCHER-01")

	before := len(snapshotDevice(t, service, "ETCHER-01").Messages)
	permission, err := service.PrepareEquipmentMessage(EquipmentMessageRequest{EquipmentID: "ETCHER-01", SML: "S1F1 W\n.", TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if permission.Command != "S1F1" || permission.Tool != "send.secs-message" {
		t.Fatalf("permission = %#v", permission)
	}
	if got := len(snapshotDevice(t, service, "ETCHER-01").Messages); got != before {
		t.Fatalf("prepare sent a message: before=%d after=%d", before, got)
	}

	result, err := service.ResolveEquipmentAction(permission.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.Request == nil || result.Request.Name() != "S1F1" || result.Reply == nil || result.Reply.Name() != "S1F2" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := service.ResolveEquipmentAction(permission.ID, true); err == nil {
		t.Fatal("the same one-shot permission was accepted twice")
	}
}

func TestOperatorProfileCommandPersistsOutcomeAndReply(t *testing.T) {
	service, err := newStudioService(os.DirFS("."), t.TempDir()+"/eapstudio-test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer service.close()
	if err := service.ConnectDevice("ETCHER-01"); err != nil {
		t.Fatal(err)
	}
	defer service.DisconnectDevice("ETCHER-01")

	permission, err := service.PrepareEquipmentCommand(EquipmentCommandRequest{
		EquipmentID: "ETCHER-01", Command: "send.recipe", TimeoutSeconds: 10,
		Parameters: map[string]any{"recipeId": "ETCH-A", "materialId": "MAT-REAL"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ResolveEquipmentAction(permission.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.Command == nil || result.Command.Name != "send.recipe" || result.Reply == nil || result.Reply.Name() != "S7F4" {
		t.Fatalf("result = %#v", result)
	}
	history, err := service.QueryCommandHistory(sqlitestore.HistoryQuery{Page: 1, PageSize: 25, EquipmentID: "ETCHER-01", Name: "send.recipe"})
	if err != nil || len(history.Items) == 0 || history.Items[0].Status != "succeeded" {
		t.Fatalf("history = %#v, err = %v", history, err)
	}
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

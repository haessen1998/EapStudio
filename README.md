# EapStudio

EapStudio is an AI-powered SECS/GEM Equipment Integration Studio built with Wails 3, Go, React 19, shadcn/ui conventions, and `arloliu/go-secs/v2`.

The first milestone proves one complete multi-equipment path:

```text
Equipment / Simulator
        ↓
go-secs/v2 anti-corruption driver
        ↓
isolated DeviceRuntime
        ├── fast path: S6F11 W → S6F12
        ├── trace queue → SQLite protocol_traces
        └── async path
                 ↓
          Generic GEM Adapter
                 ↓
          Compiled Profile
                 ↓
 Canonical Event (noun.verb, type=event)
          ┌──────┴────────┐
          ▼               ▼
   Automation Engine    Router
          │
 Command (verb.noun, type=command)
          │
   Adapter.Build → Driver
          │
 Command outcome Event
          ┌──────┴──────┐
       Mock MQ     SQLite history
```

Two simulator-backed device instances (`ETCHER-01` and `ETCHER-02`) share one `demo-etcher-x100` Profile while retaining independent connection state, message queues, traces, and event pipelines.

## Run

Prerequisites: Go 1.25+, Node.js 20+, and Wails 3 CLI beta.9 or later.

```powershell
cd D:\Programs\Go\EapStudio
wails3 dev
```

Production build:

```powershell
wails3 build
```

Verification:

```powershell
go test ./...
cd frontend
npm run build
```

## Configuration

- `configs/devices.yaml` is the packaged equipment template. On first run it is copied to the OS user config directory as `EapStudio/devices.yaml`; Settings edits that runtime file and reports that a restart is required.
- `profiles/demo/etcher-x100.yaml` defines variables, reports, events, and field mappings for a model.
- `configs/routes.yaml` maps canonical event-name and equipment-ID glob selectors to sinks.
- `configs/automations.yaml` maps Event and equipment glob selectors to Commands and parameter projections.

Keep equipment identity out of the canonical name: use `wafer.started` with `equipmentId: AOI-01`, not `AOI.01.wafer.started`. A common route can select `names: [wafer.*]` plus `equipment: [AOI-*]`, while another rule selects `equipment: [AOI-01]` for additional sinks. Every matching route contributes sinks and duplicate sink deliveries are removed. Automation rules are additive, so common and device-specific rules may both create Commands.

The demo Profile declares reverse-mapped canonical scenarios and arbitrary SxFy templates. `material-arrival` and `wafer-start` use `GenericGemAdapter.BuildEvent` to generate S6F11 CEID/RPTID/value structures. `alarm-raised`, `alarm-cleared`, `remote-command`, and `recipe-download` demonstrate inbound S5F1 plus outbound S2F41 and S7F3 without hard-coded simulator methods.

Protocol traces, including SML and raw hex when available, are queued from the fast path into `protocol_traces`. Canonical events reach `domain_events` through the async Router sink; commands and the alarm projection use separate tables in the same WAL-enabled SQLite database. This keeps writes isolated while preserving correlation queries across one durable history.

## Message contract

- Commands use `type: command` and verb–noun names such as `send.recipe`.
- Events use `type: event` and noun–verb/state names such as `material.arrived` and `recipe.sent`.
- `correlationId` follows one business flow.
- `causationId` points to the Event or Command that directly caused the record.
- Command outcome Events also carry `commandId`.

The included automation demonstrates `material.arrived → send.recipe → recipe.sent`. The incoming S6F11 still receives S6F12 on the protocol fast path before Automation, routing, or AI work begins.

To connect real equipment, change a device's `driver` from `simulator` to `go-secs` and set its HSMS host, port, mode, and session ID. SDK types remain confined to `internal/driver/secs`.

## Project boundaries

- `internal/driver/secs`: go-secs/v2 integration and SDK-neutral messages.
- `internal/device`: multi-device manager, isolated runtimes, fast/async paths.
- `internal/automation` and `internal/command`: Event-triggered business rules and per-device serialized command execution.
- `internal/profile`: strict YAML loading, validation, and compilation.
- `internal/equipment`: profile-driven generic GEM interpretation.
- `internal/event`: protocol-independent event contract.
- `internal/router` and `internal/sink`: downstream distribution.
- `internal/store/sqlite`: non-blocking protocol trace writer, routed event history, commands, and alarm projection.
- `frontend/src/components/ui`: shadcn/ui-style primitives.
- `frontend/src/components/ai-elements`: Copilot conversation, message, tool, and prompt components.

The current Copilot is deterministic and grounded in the live runtime/Profile snapshot. `StudioService.AskCopilot` is the provider boundary for adding OpenAI or another compatible model later; credentials and model calls stay in Go, outside the frontend and protocol response path.

The Settings page maintains a list of local, OpenAI Responses API compatible, and Chat Completions compatible configurations, with one explicit runtime default. An API Key entered in Settings is passed to Go for the current process and is never written to localStorage; `EAPSTUDIO_AI_API_KEY` remains the fallback. The equipment editor persists IDs, display badges, Profile paths, Adapter names, drivers, and connection settings. `generic-gem` is built in, while model-specific Adapter names must be registered in the backend registry. Read-only questions are answered from the selected DeviceRuntime snapshot. A write request such as sending `send.recipe` produces a typed Allow/Deny permission card; only **Allow once** submits the command to the device queue.

Messages expose working SML, structured Tree, and offset/hex/ASCII Raw views; simulator SML is encoded into complete HSMS frames so Raw remains useful without physical equipment. Messages, canonical events, and alarms use the configurable 25/50/100/200-record page size and equipment filters rather than endless scrolling, so an investigation keeps a stable position as live data arrives. The demo simulator also raises an initial S5F1 alarm to populate the alarm projection and its state/severity/equipment filters.

Repository-aware coding agents should follow `AGENTS.md`. It describes how to turn equipment manuals and sample logs into Profiles, model-specific Adapters when necessary, simulator scenarios, evidence-backed equipment status answers, and permission-gated commands.

## Release workflow

`.github/workflows/release.yml` follows the Quick project pattern: it validates `build/config.yml` against a future `vX.Y.Z` tag, tests Go and React, builds Windows x64 and macOS universal artifacts, produces SHA-256 files, and publishes a GitHub Release only for a tag. Manual runs build downloadable workflow artifacts but do not create a release or tag.

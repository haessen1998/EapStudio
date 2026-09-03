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

The default workspace contains three networked Equipment Twin runtimes: `ETCHER-01`, `AOI-01`, and `OVEN-01`. They bind real passive HSMS ports so an external EAP or control application can connect to the simulated factory line. A workspace may instead contain only production controller runtimes, or any mixture of controller and Equipment Twin roles.

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

- `configs/devices.yaml` is the packaged three-twin template. Runtime configuration lives under `EapStudio/workspaces/<workspace-id>/`.
- `profiles/demo/etcher-x100.yaml` defines variables, reports, events, and field mappings for a model.
- `configs/routes.yaml` maps canonical event-name and equipment-ID glob selectors to sinks.
- `configs/automations.yaml` maps Event and equipment glob selectors to Commands and parameter projections.

The sample routes include shared material flow, Etcher production, AOI quality, an `AOI-01` exact exception, Oven thermal events, and a catch-all SQLite history route. The sample automations demonstrate common material-arrival recipe selection plus AOI review and Oven temperature-deviation actions.

Each workspace owns `devices.yaml`, `routes.yaml`, `automations.yaml`, `profiles/`, and `events/`. The active workspace is persisted and can be switched from the sidebar footer or managed in Settings. New workspaces start from the packaged three-twin template. On upgrade, the previous flat runtime configuration is copied into the default workspace and retained; files rewritten from a legacy schema also receive a `.legacy.bak` backup. Rules are watched and atomically swapped within about 1.5 seconds. Equipment changes and Profile Workbench saves hot-rebuild only affected DeviceRuntime instances; a previously connected affected runtime reconnects after reload.

Keep equipment identity out of the canonical name: use `wafer.started` with `equipmentId: AOI-01`, not `AOI.01.wafer.started`. A common route can select `names: [wafer.*]` plus `equipment: [AOI-*]`, while another rule selects `equipment: [AOI-01]` for additional sinks. Every matching route contributes sinks and duplicate sink deliveries are removed. Automation rules are additive, so common and device-specific rules may both create Commands.

Direction has one stable meaning independent of deployment topology: Profile `commands` are always **Host → Equipment**, while top-level Profile `scenarios` are always **Equipment → Host**. `material-arrival` and `wafer-start` use `GenericGemAdapter.BuildEvent` to generate S6F11 CEID/RPTID/value structures; alarm scenarios demonstrate S5F1. Legacy `simulator.scenarios` profiles are migrated to top-level `scenarios`. Commands may be created by Automation or sent manually from Device detail. On a controller runtime a scenario is injected into the local parsing pipeline; on an Equipment Twin it is transmitted over its real HSMS connection.

Protocol traces, including SML and raw hex when available, are queued from the fast path into `protocol_traces`. Canonical events reach `domain_events` through the async Router sink; commands and the alarm projection use separate tables in the same WAL-enabled SQLite database. The real `file-events` sink also appends matching canonical events as JSONL under the runtime config directory. This keeps writes isolated while preserving correlation queries across one durable history.

## Message contract

- Commands use `type: command` and verb–noun names such as `send.recipe`.
- Events use `type: event` and noun–verb/state names such as `material.arrived` and `recipe.sent`.
- `correlationId` follows one business flow.
- `causationId` points to the Event or Command that directly caused the record.
- Command outcome Events also carry `commandId`.

The included automation demonstrates `material.arrived → send.recipe → recipe.sent`. The incoming S6F11 still receives S6F12 on the protocol fast path before Automation, routing, or AI work begins.

A `controller` runtime uses `go-secs` to connect to real equipment and sends Profile commands. An `equipment-simulator` runtime uses the same real transport in the opposite application role, normally passive on `0.0.0.0:<port>`, responds to Host primaries, and emits Profile scenarios. Connect-attempt and T3 reply timeouts are configured per device and shown in Device detail. Request/reply traces are written to Messages and SQLite. SDK types remain confined to `internal/driver/secs`.

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

The local Copilot mode is a deterministic Runtime inspector rather than a language model. Responses and Chat configurations use application-controlled typed read tools (`runtime.snapshot`, message/event/command history, and Profile inventory) before model inference; tool results are visible in the conversation. Credentials and model calls stay in Go, outside the frontend and protocol response path.

The Workbench lists writable workspace Profiles, applies strict schema/compiler validation, previews Canonical Event → Adapter.BuildEvent → SECS → Adapter.Parse round trips, and hot reloads devices using a saved Profile. The SECS Message Lab contains the complete S1F1 through S17F13 base matrix plus common higher GEM functions such as S2F41, known GEM descriptions, an SML editor, target-runtime selection, permission-gated transmission, secondary-reply display, and promotion of a proven SML message into a Profile command or scenario. Device detail exposes connection attempts, lifecycle detail, IN/OUT counts, parse failures, queue drops, command failures, timeouts, and the last transport error.

Equipment write policy is persisted in SQLite. It supports deny-all or allowlist-plus-explicit-approval, equipment/command glob allowlists, and approval expiry. It applies to Profile commands, raw `SxFy` sends, and AI actions and never auto-approves a write. Permission cards show a parameter diff against the previous command of the same type; command execution outcomes remain queryable on the Commands page and through SQLite history.

The Settings page maintains a list of local, OpenAI Responses API compatible, and Chat Completions compatible configurations, with one explicit runtime default. An API Key entered in Settings is passed to Go for the current process and is never written to localStorage; `EAPSTUDIO_AI_API_KEY` remains the fallback. OpenAI Responses profiles receive a current default model when an older saved profile has an empty model. The Copilot supports up to four images or files (5 MB each): Responses receives native `input_image`/`input_file` content, while Chat-compatible endpoints receive images and supported text documents. Provider errors are decoded and displayed in the conversation. The equipment editor persists IDs, display badges, Profile paths, Adapter names, drivers, and connection settings. `generic-gem` is built in, while model-specific Adapter names must be registered in the backend registry. Read-only questions are answered from the selected DeviceRuntime snapshot. A write request such as sending `send.recipe` produces a typed Allow/Deny permission card; only **Allow once** submits the command to the device queue.

Messages expose working SML, structured Tree, and offset/hex/ASCII Raw views; simulator SML is encoded into complete HSMS frames so Raw remains useful without physical equipment. Messages, canonical events, and alarms use the configurable 25/50/100/200-record page size and equipment filters rather than endless scrolling, so an investigation keeps a stable position as live data arrives. The demo simulator also raises an initial S5F1 alarm to populate the alarm projection and its state/severity/equipment filters.

Repository-aware coding agents should follow `AGENTS.md`. It describes how to turn equipment manuals and sample logs into Profiles, model-specific Adapters when necessary, simulator scenarios, evidence-backed equipment status answers, and permission-gated commands.

## Release workflow

`.github/workflows/release.yml` follows the Quick project pattern: it validates `build/config.yml` against a future `vX.Y.Z` tag, tests Go and React, builds Windows x64 and macOS universal artifacts, produces SHA-256 files, and publishes a GitHub Release only for a tag. Manual runs build downloadable workflow artifacts but do not create a release or tag.

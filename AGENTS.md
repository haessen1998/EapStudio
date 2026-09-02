# EapStudio Agent Guide

## Mission

EapStudio is an AI-powered SECS/GEM Equipment Integration Studio. Changes must preserve a complete, observable chain from protocol messages to canonical events, automation commands, equipment replies, durable history, and Copilot evidence.

An agent working in this repository is expected to help engineers integrate a new equipment model from vendor manuals and sample logs, generate or refine Profiles and Adapters, explain live equipment state from recorded evidence, and prepare equipment commands. Any operation that writes to equipment requires explicit user approval in the UI.

## Architecture invariants

- `internal/driver/secs` is the anti-corruption boundary around `arloliu/go-secs/v2`. Do not leak SDK types outside this package.
- A `DeviceRuntime` owns one equipment connection, protocol queues, command queue, and short live history. Never share connection state across equipment.
- The protocol fast path may acknowledge messages and enqueue trace records only. It must not wait for SQLite, Router sinks, AI, or business automation.
- `Profile` describes equipment knowledge: variables, reports, CEIDs, commands, simulator templates, and model-specific mappings.
- Profile `commands` always mean Host → Equipment. Top-level Profile `scenarios` always mean Equipment → Host; never reverse these meanings based on whether a Runtime is real or simulated.
- A `controller` Runtime represents the Host side connected to production equipment. An `equipment-simulator` Runtime represents a network-accessible Equipment Twin, normally using a passive HSMS endpoint. Workspaces may mix both roles.
- `Adapter` translates protocol messages to canonical events and canonical commands back to protocol messages.
- `Automation Engine` consumes events and creates commands. `Router` distributes canonical events and must not depend on SECS message structures.
- Keep equipment identity in `equipmentId`, not in canonical names. Use event/equipment glob selectors in routes and automations (`wafer.*` + `AOI-*`) for shared behavior and an exact equipment selector for exceptions.
- Events use `type=event` and noun/state names such as `material.arrived`, `alarm.raised`, and `recipe.sent`.
- Commands use `type=command` and verb/object names such as `send.recipe`.
- Preserve `correlationId`, `causationId`, and `commandId` so AI and operators can reconstruct the causal chain without coupling events.

## Integrating equipment from documentation

When given an equipment communication manual, GEM interface document, SML samples, or message logs:

1. Identify the vendor, model, protocol mode, session ID assumptions, supported streams/functions, CEIDs, RPTIDs, VIDs, alarms, remote commands, recipe messages, and ACK semantics.
2. Add a versioned YAML Profile under `profiles/<vendor-or-family>/`. Do not edit the demo Profile to represent unrelated equipment.
3. Define every referenced VID and report before defining an event. Map canonical fields by RPTID and VID rather than positional magic numbers in Go.
4. Define outbound commands with odd primary functions, wait-bit behavior, parameters, success/failure events, and success ACK.
5. Add top-level `scenarios` only for representative Equipment → Host messages. Use event reverse mapping when the message is an S6F11 represented by a canonical event; use a generic `message` template for other equipment-originated SxFy messages such as S5F1. Put Host → Equipment S2F41/S7F3 definitions under `commands`.
6. Use `generic-gem` when the Profile can express the mapping. Create a model-specific Adapter only when decoding, validation, or message construction cannot be represented declaratively. Keep model-specific code in a focused adapter file and register it explicitly.
7. Add compiler, parser, reverse-mapping, ACK, and simulator tests. Include at least one captured or manual-derived sample with identifying production values anonymized.
8. Update `configs/devices.yaml` only when adding an intentional runnable example. Never insert real credentials, production IPs, material identifiers, or proprietary payloads.

## Reading logs and reporting state

- Treat SQLite protocol traces as transport evidence and canonical events/commands as business evidence.
- Prefer the latest state-changing event, active alarm projection, connection state, and command outcome over inference from a single raw message.
- State what is directly observed and what is inferred. Include equipment ID, SxFy/CEID, timestamp, and correlation ID when they support the answer.
- Raw hex and SML are traceability data. Do not expose SDK-private objects or claim a raw payload exists for generated simulator messages when it does not.
- An unknown CEID should remain `secs.unknown`; propose a Profile mapping instead of inventing semantics.

## AI actions and permissions

- Read-only tools may inspect snapshots, Profiles, traces, events, routes, alarms, and SQLite history without an approval card.
- Any action that can change equipment or external state—sending a command, replying outside the required protocol ACK path, connecting/disconnecting a real device, changing a Profile in use, or publishing externally—must create a typed pending action.
- Show the exact equipment, command, parameters, and risk in an Allow/Deny card. Conversation text such as “continue”, “yes”, or “go ahead” is not an approval mechanism.
- Execute a pending write once only after the user clicks Allow. Deny must leave an auditable result and send nothing. Expired or already resolved permission IDs must not execute.
- AI provider adapters must not run on the protocol fast path. API keys come from `EAPSTUDIO_AI_API_KEY` and must never be returned to the frontend, logged, committed, or stored in localStorage.
- Responses API and Chat Completions adapters share the same grounded request contract. Provider output cannot bypass command validation, Profile validation, or permission checks.

## Persistence and lists

- Use one WAL-enabled SQLite database with separate tables: `protocol_traces`, `domain_events`, `commands`, and `alarms`.
- Fast-path trace recording must remain a bounded non-blocking queue with observable drop counts.
- Canonical events are persisted through the Router `sqlite-history` sink on the async path.
- User-facing messages, events, alarms, and history lists should use explicit pagination with stable ordering. Do not add endless scrolling for diagnostic records.
- Runtime configuration is workspace-scoped under `EapStudio/workspaces/<id>/`: `devices.yaml`, `routes.yaml`, `automations.yaml`, `profiles/`, and `events/`. Never write a Profile or rule into another workspace during a hot switch.

## Verification

Run these before handing off a change:

```powershell
go test ./...
wails3 generate bindings -clean=true -ts -i
npm run build --prefix frontend
wails3 build
```

Do not create a release tag unless the user explicitly chooses the milestone and version. `.github/workflows/release.yml` validates future `vX.Y.Z` tags against `build/config.yml`.

import { CSSProperties, FormEvent, PointerEvent as ReactPointerEvent, useEffect, useMemo, useState } from "react"
import {
  Activity, BellRing, Bot, Boxes, Cable, ChevronRight, CircleGauge, Cpu, Database, FlaskConical,
  ChevronLeft, Code2, GitBranch, GripVertical, MessageSquareText, Play, PlugZap, Radio, RefreshCw, Route, Server,
  Search, Settings2, ShieldCheck, ShieldX, Sparkles, Unplug, Waves,
} from "lucide-react"
import { Events } from "@wailsio/runtime"
import { StudioService, type StudioSnapshot } from "../bindings/eapstudio"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Conversation, ConversationContent, ConversationEmptyState } from "@/components/ai-elements/conversation"
import { Message as AIMessage, MessageContent, MessageResponse } from "@/components/ai-elements/message"
import { PromptInput, PromptInputSubmit, PromptInputTextarea } from "@/components/ai-elements/prompt-input"
import { Tool, ToolContent, ToolHeader } from "@/components/ai-elements/tool"
import { cn } from "@/lib/utils"

type Device = NonNullable<StudioSnapshot["devices"]>[number]
type SecsMessage = NonNullable<Device["messages"]>[number]
type CanonicalEvent = NonNullable<Device["events"]>[number]
type EquipmentCommand = NonNullable<Device["commands"]>[number]
type AlarmRecord = NonNullable<StudioSnapshot["alarms"]>[number]
type PermissionRequest = NonNullable<Awaited<ReturnType<typeof StudioService.AskCopilot>>["permission"]>
type Page = "overview" | "devices" | "messages" | "events" | "alarms" | "router" | "simulator"

const navItems: { id: Page; label: string; icon: typeof Activity }[] = [
  { id: "overview", label: "Overview", icon: CircleGauge },
  { id: "devices", label: "Devices", icon: Server },
  { id: "messages", label: "Messages", icon: MessageSquareText },
  { id: "events", label: "Events", icon: Waves },
  { id: "alarms", label: "Alarms", icon: BellRing },
  { id: "router", label: "Router", icon: Route },
  { id: "simulator", label: "Simulator", icon: FlaskConical },
]

const emptySnapshot: StudioSnapshot = { devices: [], routes: [], deliveries: [], automations: [], alarms: [], storage: { traceCount: 0, eventCount: 0, commandCount: 0, alarmCount: 0, droppedTrace: 0 }, generated: new Date().toISOString() }

function App() {
  const [snapshot, setSnapshot] = useState<StudioSnapshot>(emptySnapshot)
  const [page, setPage] = useState<Page>("overview")
  const [selectedDeviceID, setSelectedDeviceID] = useState("ETCHER-01")
  const [selectedMessage, setSelectedMessage] = useState<SecsMessage | null>(null)
  const [error, setError] = useState("")
  const [query, setQuery] = useState("")
  const [leftWidth, setLeftWidth] = useState(() => Number(localStorage.getItem("eapstudio.leftWidth")) || 220)
  const [rightWidth, setRightWidth] = useState(() => Number(localStorage.getItem("eapstudio.rightWidth")) || 360)
  const [leftCollapsed, setLeftCollapsed] = useState(false)
  const [rightCollapsed, setRightCollapsed] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)

  const devices = snapshot.devices ?? []
  const selectedDevice = devices.find((device) => device.id === selectedDeviceID) ?? devices[0]
  const allMessages = useMemo(() => devices.flatMap((device) => device.messages ?? []).sort((a, b) => b.timestamp.localeCompare(a.timestamp)), [devices])
  const allEvents = useMemo(() => devices.flatMap((device) => device.events ?? []).sort((a, b) => b.timestamp.localeCompare(a.timestamp)), [devices])
  const normalizedQuery = query.trim().toLowerCase()
  const visibleMessages = useMemo(() => allMessages.filter((message) => !normalizedQuery || `${message.equipmentId} s${message.stream}f${message.function} ${message.direction} ${message.sml}`.toLowerCase().includes(normalizedQuery)), [allMessages, normalizedQuery])
  const visibleEvents = useMemo(() => allEvents.filter((event) => !normalizedQuery || `${event.name} ${event.equipmentId} ${event.correlationId} ${JSON.stringify(event.data)}`.toLowerCase().includes(normalizedQuery)), [allEvents, normalizedQuery])
  const visibleAlarms = useMemo(() => (snapshot.alarms ?? []).filter((alarm) => !normalizedQuery || `${alarm.equipmentId} ${alarm.alarmId} ${alarm.code} ${alarm.text} ${alarm.severity} ${alarm.state}`.toLowerCase().includes(normalizedQuery)), [snapshot.alarms, normalizedQuery])

  const refresh = () => StudioService.Snapshot().then(setSnapshot).catch((reason) => setError(String(reason)))

  useEffect(() => {
    document.documentElement.dataset.theme = localStorage.getItem("eapstudio.theme") ?? "dark"
    StudioService.ConfigureAI({ provider: localStorage.getItem("eapstudio.ai.provider") ?? "local", baseURL: localStorage.getItem("eapstudio.ai.baseURL") ?? "", model: localStorage.getItem("eapstudio.ai.model") ?? "" }).catch(() => undefined)
    refresh()
    const cancel = Events.On("studio:snapshot-changed", (event) => setSnapshot(event.data as StudioSnapshot))
    return () => cancel()
  }, [])

  useEffect(() => { localStorage.setItem("eapstudio.leftWidth", String(leftWidth)) }, [leftWidth])
  useEffect(() => { localStorage.setItem("eapstudio.rightWidth", String(rightWidth)) }, [rightWidth])

  useEffect(() => {
    if (!selectedMessage && allMessages.length) setSelectedMessage(allMessages[0])
  }, [allMessages, selectedMessage])

  const invoke = async (action: () => Promise<unknown>) => {
    setError("")
    try { await action(); await refresh() } catch (reason) { setError(String(reason)) }
  }

  const beginResize = (side: "left" | "right", event: ReactPointerEvent) => {
    event.preventDefault()
    const startX = event.clientX
    const startWidth = side === "left" ? leftWidth : rightWidth
    const move = (moveEvent: PointerEvent) => {
      const delta = moveEvent.clientX - startX
      const next = side === "left" ? startWidth + delta : startWidth - delta
      if (side === "left") setLeftWidth(Math.min(360, Math.max(176, next)))
      else setRightWidth(Math.min(560, Math.max(300, next)))
    }
    const stop = () => { window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", stop) }
    window.addEventListener("pointermove", move)
    window.addEventListener("pointerup", stop)
  }

  const shellStyle = { "--left-width": `${leftCollapsed ? 58 : leftWidth}px`, "--right-width": `${rightCollapsed ? 52 : rightWidth}px` } as CSSProperties

  return (
    <div className="app-shell" style={shellStyle}>
      <aside className={cn("sidebar", leftCollapsed && "sidebar-collapsed")}>
        <div className="brand-block">
          <div className="brand-mark"><Radio className="size-5" /></div>
          <div className="sidebar-label"><div className="brand-name">EapStudio</div><div className="brand-subtitle">Equipment integration</div></div>
          <button className="panel-collapse" onClick={() => setLeftCollapsed((value) => !value)} aria-label="Toggle sidebar"><ChevronLeft className={cn("size-3.5", leftCollapsed && "rotate-180")}/></button>
        </div>

        <div className="sidebar-body"><nav className="nav-list">
          <p className="nav-label">Workspace</p>
          {navItems.map((item) => <button title={item.label} key={item.id} className={cn("nav-item", page === item.id && "nav-item-active")} onClick={() => setPage(item.id)}><item.icon className="size-4 shrink-0" /><span className="sidebar-label">{item.label}</span>{page === item.id && <ChevronRight className="sidebar-label ml-auto size-3.5" />}</button>)}
        </nav>

        <div className="mt-5 min-h-0 flex-1">
          <div className="mb-2 flex items-center justify-between px-2"><p className="nav-label !mb-0">Equipment</p><Badge variant="outline">{devices.length}</Badge></div>
          <div className="space-y-1 overflow-y-auto">
            {devices.map((device) => <button key={device.id} onClick={() => { setSelectedDeviceID(device.id); setPage("devices") }} className={cn("device-nav", device.id === selectedDevice?.id && "device-nav-active")}>
              <span className={cn("status-dot", device.state === "selected" && "status-online")} />
              <span className="sidebar-label min-w-0 text-left"><span className="block truncate text-xs font-medium">{device.id}</span><span className="block truncate text-[10px] text-muted-foreground">{device.model} · {device.driver}</span></span>
            </button>)}
          </div>
        </div>

        </div><button className="sidebar-footer" onClick={() => setSettingsOpen((value) => !value)}><Settings2 className="size-4 shrink-0" /><span className="sidebar-label">Settings · Runtime v0.1.0</span><span className="sidebar-label ml-auto text-emerald-400">●</span></button>
        {settingsOpen && <SidebarSettings devices={devices} onClose={() => setSettingsOpen(false)} />}
      </aside>
      <ResizeHandle side="left" hidden={leftCollapsed} onPointerDown={(event) => beginResize("left", event)} onDoubleClick={() => setLeftCollapsed(true)} />

      <main className="main-area">
        <header className="topbar">
          <div><h1 className="text-base font-semibold">{navItems.find((item) => item.id === page)?.label}</h1><p className="text-xs text-muted-foreground">AI-powered SECS/GEM Equipment Integration Studio</p></div>
          <label className="global-search"><Search className="size-3.5"/><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search equipment, SxFy, event, alarm…"/></label>
          <div className="flex items-center gap-2"><Badge variant="success"><span className="mr-1 size-1.5 rounded-full bg-emerald-400" />Runtime healthy</Badge><Button variant="outline" size="icon" onClick={refresh} aria-label="Refresh"><RefreshCw className="size-4" /></Button></div>
        </header>

        {error && <div className="mx-5 mt-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-red-300">{error}</div>}

        <section className="content-pane">
            {page === "overview" && <Overview devices={devices} messages={visibleMessages} events={visibleEvents} alarmCount={visibleAlarms.filter((alarm) => alarm.state === "active").length} storage={snapshot.storage} onOpen={(id) => { setSelectedDeviceID(id); setPage("devices") }} />}
            {page === "devices" && selectedDevice && <DeviceDetail device={selectedDevice} onConnect={() => invoke(() => StudioService.ConnectDevice(selectedDevice.id))} onDisconnect={() => invoke(() => StudioService.DisconnectDevice(selectedDevice.id))} onEmit={(scenario) => invoke(() => StudioService.EmitSimulatorScenario(selectedDevice.id, scenario))} />}
            {page === "messages" && <Messages messages={visibleMessages} selected={selectedMessage} onSelect={setSelectedMessage} />}
            {page === "events" && <EventsPage events={visibleEvents} />}
            {page === "alarms" && <AlarmsPage alarms={visibleAlarms} />}
            {page === "router" && <RouterPage snapshot={snapshot} query={normalizedQuery} />}
            {page === "simulator" && <Simulator devices={devices} onEmit={(id, scenario) => invoke(() => StudioService.EmitSimulatorScenario(id, scenario))} />}
        </section>
      </main>
      <ResizeHandle side="right" hidden={rightCollapsed} onPointerDown={(event) => beginResize("right", event)} onDoubleClick={() => setRightCollapsed(true)} />
      <Copilot device={selectedDevice} collapsed={rightCollapsed} onToggle={() => setRightCollapsed((value) => !value)} />
    </div>
  )
}

function ResizeHandle({ side, hidden, onPointerDown, onDoubleClick }: { side: "left" | "right"; hidden: boolean; onPointerDown: (event: ReactPointerEvent) => void; onDoubleClick: () => void }) {
  return <div className={cn("resize-handle", hidden && "resize-handle-hidden")} data-side={side} onPointerDown={onPointerDown} onDoubleClick={onDoubleClick}><GripVertical className="size-3"/></div>
}

function Overview({ devices, messages, events, alarmCount, storage, onOpen }: { devices: Device[]; messages: SecsMessage[]; events: CanonicalEvent[]; alarmCount: number; storage: StudioSnapshot["storage"]; onOpen: (id: string) => void }) {
  const online = devices.filter((device) => device.state === "selected").length
  return <div className="page-stack">
    <div className="hero-strip"><div><div className="eyebrow"><Sparkles className="size-3.5" /> Runtime / Studio / Copilot</div><h2>Equipment integration, made observable.</h2><p>Two isolated DeviceRuntime instances share one compiled Profile while preserving independent protocol state and pipelines.</p></div><div className="pipeline-mini"><span>HSMS</span><ChevronRight/><span>Profile</span><ChevronRight/><span>Event</span><ChevronRight/><span>Router</span></div></div>
    <div className="metric-grid">
      <Metric icon={Server} label="Equipment" value={`${online}/${devices.length}`} detail="HSMS Selected" color="cyan" />
      <Metric icon={MessageSquareText} label="Message trace" value={String(messages.length)} detail="IN / OUT retained" color="violet" />
      <Metric icon={Waves} label="Canonical events" value={String(events.length)} detail="Protocol independent" color="emerald" />
      <Metric icon={BellRing} label="Active alarms" value={String(alarmCount)} detail={`${storage?.traceCount ?? 0} traces persisted`} color="amber" />
    </div>
    <div className="section-heading"><div><h3>Equipment fleet</h3><p>Each connection runs in its own isolated runtime.</p></div></div>
    <div className="grid grid-cols-2 gap-3">{devices.map((device) => <Card key={device.id} className="equipment-card cursor-pointer" onClick={() => onOpen(device.id)}><CardHeader className="flex-row items-start justify-between"><div><CardTitle>{device.name}</CardTitle><CardDescription>{device.id} · {device.vendor} {device.model}</CardDescription></div><Badge variant={device.state === "selected" ? "success" : "outline"}>{device.state}</Badge></CardHeader><CardContent><div className="connection-line"><Cable className="size-3.5" /> {device.host}:{device.port}<span>HSMS-SS</span></div><div className="card-stats"><span><b>{device.messages?.length ?? 0}</b> messages</span><span><b>{device.events?.length ?? 0}</b> events</span><span><b>generic-gem</b> adapter</span></div></CardContent></Card>)}</div>
    <div className="section-heading"><div><h3>Live protocol activity</h3><p>Fast replies and asynchronous business processing are shown together.</p></div></div>
    <MessageTable messages={messages.slice(0, 7)} />
  </div>
}

function Metric({ icon: Icon, label, value, detail, color }: { icon: typeof Server; label: string; value: string; detail: string; color: string }) {
  return <Card className="metric-card"><CardContent className="flex items-center gap-3 p-4"><div className={`metric-icon metric-${color}`}><Icon className="size-4" /></div><div><p className="text-[11px] text-muted-foreground">{label}</p><p className="text-xl font-semibold tracking-tight">{value}</p><p className="text-[10px] text-muted-foreground">{detail}</p></div></CardContent></Card>
}

function DeviceDetail({ device, onConnect, onDisconnect, onEmit }: { device: Device; onConnect: () => void; onDisconnect: () => void; onEmit: (scenario: string) => void }) {
  const defaultScenario = device.scenarios?.[0]?.id
  return <div className="page-stack"><div className="detail-header"><div className="equipment-glyph"><Cpu className="size-6" /></div><div><div className="flex items-center gap-2"><h2>{device.name}</h2><Badge variant={device.state === "selected" ? "success" : "outline"}>{device.state}</Badge></div><p>{device.id} · {device.vendor} {device.model}</p></div><div className="ml-auto flex gap-2"><Button variant="outline" disabled={!defaultScenario} onClick={() => defaultScenario && onEmit(defaultScenario)}><Play className="size-4" />Emit scenario</Button>{device.state === "selected" ? <Button variant="outline" onClick={onDisconnect}><Unplug className="size-4" />Disconnect</Button> : <Button onClick={onConnect}><PlugZap className="size-4" />Connect</Button>}</div></div>
    <div className="grid grid-cols-3 gap-3"><InfoCard label="Connection" value={`${device.host}:${device.port}`} sub="HSMS-SS / Active" icon={Cable}/><InfoCard label="Equipment Profile" value={device.profileName} sub={`${device.vendor} · ${device.model}`} icon={Boxes}/><InfoCard label="Adapter" value="generic-gem" sub="Profile-driven parsing" icon={Database}/></div>
    <div className="architecture-line"><div><Radio/>go-secs/v2</div><ChevronRight/><div><Cpu/>DeviceRuntime</div><ChevronRight/><div><Boxes/>Compiled Profile</div><ChevronRight/><div><Waves/>Canonical Event</div><ChevronRight/><div><GitBranch/>Router</div></div>
    <div className="section-heading"><div><h3>Latest messages</h3><p>S6F12 is issued on the fast path before event conversion.</p></div></div><MessageTable messages={[...(device.messages ?? [])].reverse().slice(0, 8)} />
    <div className="section-heading"><div><h3>Commands</h3><p>Automation decisions use verb.noun names and preserve their causal chain.</p></div></div><CommandCards commands={[...(device.commands ?? [])].reverse().slice(0, 4)} />
    <div className="section-heading"><div><h3>Converted events</h3><p>Stable noun.verb facts ready for MQ, MES and AI correlation.</p></div></div><EventCards events={[...(device.events ?? [])].reverse().slice(0, 6)} />
  </div>
}

function InfoCard({ label, value, sub, icon: Icon }: { label: string; value: string; sub: string; icon: typeof Cable }) { return <Card><CardContent className="flex gap-3 p-4"><div className="rounded-lg bg-primary/10 p-2 text-primary"><Icon className="size-4" /></div><div><p className="text-[10px] uppercase tracking-wider text-muted-foreground">{label}</p><p className="mt-1 text-sm font-medium">{value}</p><p className="text-[10px] text-muted-foreground">{sub}</p></div></CardContent></Card> }

function Messages({ messages, selected, onSelect }: { messages: SecsMessage[]; selected: SecsMessage | null; onSelect: (message: SecsMessage) => void }) {
  const [direction, setDirection] = useState("ALL")
  const [view, setView] = useState<"sml" | "tree" | "raw">("sml")
  const [page, setPage] = useState(1)
  const filtered = messages.filter((message) => direction === "ALL" || message.direction === direction)
  const pageItems = paginate(filtered, page, 25)
  return <div className="split-inspector"><div className="message-list"><div className="panel-heading"><div><h3>Message trace</h3><p>{filtered.length} messages from all runtimes</p></div><select className="filter-select" value={direction} onChange={(event) => { setDirection(event.target.value); setPage(1) }}><option>ALL</option><option>IN</option><option>OUT</option></select></div>{pageItems.map((message) => <button key={`${message.id}-${message.direction}`} className={cn("message-row", selected?.id === message.id && selected.direction === message.direction && "message-row-active")} onClick={() => onSelect(message)}><Badge variant={message.direction === "IN" ? "success" : "default"}>{message.direction}</Badge><span className="font-mono text-xs font-semibold">S{message.stream}F{message.function}{message.wait ? " W" : ""}</span><span className="ml-auto text-[10px] text-muted-foreground">{formatTime(message.timestamp)}</span><span className="w-full truncate text-left text-[10px] text-muted-foreground">{message.equipmentId} · system {message.systemBytes}</span></button>)}<Pager page={page} total={filtered.length} pageSize={25} onChange={setPage}/></div><div className="inspector-panel">{selected ? <><div className="panel-heading"><div><h3>S{selected.stream}F{selected.function}{selected.wait ? " W" : ""}</h3><p>{selected.equipmentId} · {selected.direction} · {formatTime(selected.timestamp)}</p></div><Badge variant="outline">SECS-II</Badge></div><div className="tab-strip">{(["sml", "tree", "raw"] as const).map((tab) => <button key={tab} className={view === tab ? "tab-active" : ""} onClick={() => setView(tab)}>{tab.toUpperCase()}</button>)}</div>{view === "sml" && <pre className="sml-view">{selected.sml || "<EMPTY>"}</pre>}{view === "tree" && <MessageTree value={selected.tree || selected.sml}/>} {view === "raw" && <RawInspector hex={selected.rawHex}/>}</> : <div className="empty-panel">Select a message to inspect it.</div>}</div></div>
}

function MessageTree({ value }: { value: string }) {
  const rows = (value || "<EMPTY>").split(/\r?\n/).filter((line) => line.trim() && line.trim() !== ".")
  return <div className="tree-view">{rows.map((line, index) => { const depth = Math.floor((line.length - line.trimStart().length) / 2); const token = line.trim().match(/^<?([A-Z][0-9]?|L)/)?.[1]; return <div className="tree-node" key={`${index}-${line}`} style={{ paddingLeft: `${10 + depth * 18}px` }}><span className="tree-branch">{depth ? "└" : "●"}</span>{token && <Badge variant="outline">{token}</Badge>}<code>{line.trim()}</code></div> })}</div>
}

function RawInspector({ hex }: { hex?: string }) {
  const bytes = (hex ?? "").replace(/\s/g, "").match(/.{1,2}/g) ?? []
  if (!bytes.length) return <div className="empty-panel inspector-empty"><Code2 className="size-5"/>Raw bytes are unavailable for this generated simulator message.</div>
  const rows = Array.from({ length: Math.ceil(bytes.length / 16) }, (_, index) => bytes.slice(index * 16, index * 16 + 16))
  return <div className="hex-view">{rows.map((row, index) => <div className="hex-row" key={index}><span>{(index * 16).toString(16).padStart(6, "0")}</span><code>{row.join(" ").padEnd(47, " ")}</code><b>{row.map((item) => { const value = Number.parseInt(item, 16); return value >= 32 && value <= 126 ? String.fromCharCode(value) : "." }).join("")}</b></div>)}</div>
}

function EventsPage({ events }: { events: CanonicalEvent[] }) {
  const names = [...new Set(events.map((event) => event.name))]
  const [name, setName] = useState("ALL")
  const [page, setPage] = useState(1)
  const filtered = events.filter((event) => name === "ALL" || event.name === name)
  return <div className="page-stack"><div className="section-heading !mt-0"><div><h3>Canonical events</h3><p>SECS/GEM semantics normalized for downstream consumers.</p></div><div className="filter-bar"><select className="filter-select" value={name} onChange={(event) => { setName(event.target.value); setPage(1) }}><option>ALL</option>{names.map((item) => <option key={item}>{item}</option>)}</select><Badge>{filtered.length} events</Badge></div></div><EventCards events={paginate(filtered, page, 25)} /><Pager page={page} total={filtered.length} pageSize={25} onChange={setPage}/></div>
}
function EventCards({ events }: { events: CanonicalEvent[] }) { return <div className="space-y-2">{events.length ? events.map((event) => <Card key={event.id} className="event-card"><CardContent className="flex items-start gap-3 p-4"><div className="event-icon"><Waves className="size-4" /></div><div className="min-w-0 flex-1"><div className="flex items-center gap-2"><Badge>event</Badge><span className="text-sm font-medium">{event.name}</span>{event.source.ceid ? <Badge variant="outline">CEID {event.source.ceid}</Badge> : null}<span className="ml-auto text-[10px] text-muted-foreground">{formatTime(event.timestamp)}</span></div><p className="mt-1 text-[11px] text-muted-foreground">{event.equipmentId} · correlation {event.correlationId} · caused by {event.causationId}</p><pre className="event-json">{JSON.stringify(event.data, null, 2)}</pre></div></CardContent></Card>) : <div className="empty-panel">No canonical events yet.</div>}</div> }

function CommandCards({ commands }: { commands: EquipmentCommand[] }) { return <div className="space-y-2">{commands.length ? commands.map((command) => <Card key={command.id} className="event-card"><CardContent className="flex items-start gap-3 p-4"><div className="command-icon"><Play className="size-4" /></div><div className="min-w-0 flex-1"><div className="flex items-center gap-2"><Badge variant="warning">command</Badge><span className="text-sm font-medium">{command.name}</span><Badge variant={command.status === "succeeded" ? "success" : "outline"}>{command.status}</Badge><span className="ml-auto text-[10px] text-muted-foreground">{formatTime(command.createdAt)}</span></div><p className="mt-1 text-[11px] text-muted-foreground">{command.equipmentId} · correlation {command.correlationId} · caused by {command.causationId}</p><pre className="event-json">{JSON.stringify(command.parameters, null, 2)}</pre></div></CardContent></Card>) : <div className="empty-panel">No commands generated yet.</div>}</div> }

function RouterPage({ snapshot, query }: { snapshot: StudioSnapshot; query: string }) {
  const routes = (snapshot.routes ?? []).filter((route) => !query || `${route.name} ${route.match.names?.join(" ")} ${route.sinks?.join(" ")}`.toLowerCase().includes(query))
  const deliveries = (snapshot.deliveries ?? []).filter((delivery) => !query || `${delivery.eventName} ${delivery.sink} ${delivery.status}`.toLowerCase().includes(query))
  return <div className="page-stack"><div className="section-heading !mt-0"><div><h3>Automation engine</h3><p>Events trigger business rules which create commands; transport remains an execution detail.</p></div><Badge variant="success">active</Badge></div><div className="space-y-3">{(snapshot.automations ?? []).map((rule) => <Card key={rule.name}><CardContent className="route-rule"><div className="route-name"><Sparkles className="size-4" /><div><p>{rule.name}</p><span>event → command</span></div></div><div className="route-values"><Badge>{rule.trigger}</Badge></div><ChevronRight className="size-4 text-muted-foreground"/><div className="route-values"><Badge variant="warning">{rule.command}</Badge></div></CardContent></Card>)}</div><div className="section-heading"><div><h3>Event router</h3><p>Routes know only event names and data — never SECS message internals.</p></div></div><div className="space-y-3">{routes.map((route) => <Card key={route.name}><CardContent className="route-rule"><div className="route-name"><GitBranch className="size-4" /><div><p>{route.name}</p><span>match event name</span></div></div><div className="route-values">{route.match.names?.map((name) => <Badge key={name}>{name}</Badge>)}</div><ChevronRight className="size-4 text-muted-foreground"/><div className="route-values">{route.sinks?.map((sink) => <Badge variant="outline" key={sink}>{sink}</Badge>)}</div></CardContent></Card>)}</div><div className="section-heading"><div><h3>Recent deliveries</h3><p>Sink dispatch history from the async runtime.</p></div></div><div className="table-shell"><table><thead><tr><th>Status</th><th>Event</th><th>Sink</th><th>Time</th></tr></thead><tbody>{[...deliveries].reverse().slice(0, 12).map((delivery, index) => <tr key={`${delivery.eventId}-${delivery.sink}-${index}`}><td><Badge variant="success">{delivery.status}</Badge></td><td>{delivery.eventName}</td><td>{delivery.sink}</td><td>{formatTime(delivery.timestamp)}</td></tr>)}</tbody></table></div></div>
}

function Simulator({ devices, onEmit }: { devices: Device[]; onEmit: (id: string, scenario: string) => void }) { return <div className="page-stack"><div className="simulator-hero"><div className="simulator-rings"><Radio className="size-7" /></div><div><h2>Profile-driven Equipment Simulator</h2><p>Scenarios can reverse-map canonical events into S6F11, or declare arbitrary inbound/outbound SxFy templates such as S5F1, S2F41 and S7F3.</p></div></div><div className="grid grid-cols-2 gap-3">{devices.map((device) => <Card key={device.id}><CardHeader><div className="flex items-center justify-between"><CardTitle>{device.id}</CardTitle><Badge variant="success">simulator</Badge></div><CardDescription>{device.model} · {(device.scenarios ?? []).length} declared scenarios</CardDescription></CardHeader><CardContent><div className="space-y-2">{(device.scenarios ?? []).map((scenario) => <div className="scenario-row" key={scenario.id}><div><p>{scenario.displayName}</p><span>{scenario.event ? `${scenario.event} → Profile → S6F11` : `${scenario.direction.toUpperCase()} · S${scenario.stream}F${scenario.function}`}</span></div><Button size="sm" onClick={() => onEmit(device.id, scenario.id)}><Play className="size-3.5" />Emit</Button></div>)}</div></CardContent></Card>)}</div></div> }

function AlarmsPage({ alarms }: { alarms: AlarmRecord[] }) {
  const [state, setState] = useState("ALL")
  const [severity, setSeverity] = useState("ALL")
  const [page, setPage] = useState(1)
  const filtered = alarms.filter((alarm) => (state === "ALL" || alarm.state === state) && (severity === "ALL" || alarm.severity === severity))
  return <div className="page-stack"><div className="section-heading !mt-0"><div><h3>Equipment alarms</h3><p>S5F1 alarm reports projected from the immutable event history.</p></div><div className="filter-bar"><select className="filter-select" value={state} onChange={(event) => { setState(event.target.value); setPage(1) }}><option>ALL</option><option value="active">ACTIVE</option><option value="cleared">CLEARED</option></select><select className="filter-select" value={severity} onChange={(event) => { setSeverity(event.target.value); setPage(1) }}><option>ALL</option><option value="critical">CRITICAL</option><option value="warning">WARNING</option><option value="info">INFO</option></select></div></div><div className="table-shell"><table><thead><tr><th>State</th><th>Alarm</th><th>Equipment</th><th>Severity</th><th>Raised</th><th>Cleared</th></tr></thead><tbody>{paginate(filtered, page, 25).map((alarm) => <tr key={`${alarm.equipmentId}-${alarm.alarmId}`}><td><Badge variant={alarm.state === "active" ? "warning" : "outline"}>{alarm.state}</Badge></td><td><div className="font-medium">{alarm.alarmId} · {alarm.text}</div><div className="text-[9px] text-muted-foreground">code {alarm.code} · {alarm.correlationId}</div></td><td>{alarm.equipmentId}</td><td>{alarm.severity}</td><td>{formatTime(alarm.raisedAt)}</td><td>{alarm.clearedAt ? formatTime(alarm.clearedAt) : "—"}</td></tr>)}</tbody></table>{filtered.length === 0 && <div className="empty-panel !min-h-32">No alarms match the current filters.</div>}</div><Pager page={page} total={filtered.length} pageSize={25} onChange={setPage}/></div>
}

function SidebarSettings({ devices, onClose }: { devices: Device[]; onClose: () => void }) {
  const [theme, setTheme] = useState(() => localStorage.getItem("eapstudio.theme") ?? "dark")
  const [provider, setProvider] = useState(() => localStorage.getItem("eapstudio.ai.provider") ?? "local")
  const [model, setModel] = useState(() => localStorage.getItem("eapstudio.ai.model") ?? "")
  const [baseURL, setBaseURL] = useState(() => localStorage.getItem("eapstudio.ai.baseURL") ?? "")
  useEffect(() => {
    localStorage.setItem("eapstudio.theme", theme)
    document.documentElement.dataset.theme = theme
  }, [theme])
  const saveAI = async () => {
    localStorage.setItem("eapstudio.ai.provider", provider)
    localStorage.setItem("eapstudio.ai.model", model)
    localStorage.setItem("eapstudio.ai.baseURL", baseURL)
    await StudioService.ConfigureAI({ provider, model, baseURL })
    onClose()
  }
  return <div className="settings-popover"><div className="settings-title"><div><b>Studio settings</b><span>{devices.length} configured devices</span></div><button onClick={onClose}>×</button></div><div className="settings-form"><label>Theme<select value={theme} onChange={(event) => setTheme(event.target.value)}><option value="dark">Dark</option><option value="light">Light</option><option value="system">System</option></select></label><label>AI API adapter<select value={provider} onChange={(event) => setProvider(event.target.value)}><option value="local">Local grounded</option><option value="responses">Responses API</option><option value="chat">Chat Completions</option></select></label><label>Base URL<input value={baseURL} onChange={(event) => setBaseURL(event.target.value)} placeholder="https://api.openai.com/v1"/></label><label>Model<input value={model} onChange={(event) => setModel(event.target.value)} placeholder="gpt model"/></label><p>Secret: <code>EAPSTUDIO_AI_API_KEY</code></p><Button size="sm" onClick={saveAI}>Save settings</Button></div></div>
}

function Copilot({ device, collapsed, onToggle }: { device?: Device; collapsed: boolean; onToggle: () => void }) {
  type ChatMessage = { id: string; from: "user" | "assistant"; text: string; evidence?: string[]; permission?: PermissionRequest; permissionStatus?: "pending" | "allowed" | "denied" }
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [prompt, setPrompt] = useState("")
  const [loading, setLoading] = useState(false)
  const [provider, setProvider] = useState("local")
  useEffect(() => { StudioService.AIConfig().then((config) => setProvider(config.provider)) }, [loading])
  const ask = async (event: FormEvent) => { event.preventDefault(); const question = prompt.trim(); if (!question || !device) return; setPrompt(""); setMessages((value) => [...value, { id: `user-${Date.now()}`, from: "user", text: question }]); setLoading(true); try { const reply = await StudioService.AskCopilot(question, device.id); setMessages((value) => [...value, { id: `assistant-${Date.now()}`, from: "assistant", text: reply.answer, evidence: reply.evidence ?? [], permission: reply.permission ?? undefined, permissionStatus: reply.permission ? "pending" : undefined }]) } finally { setLoading(false) } }
  const resolvePermission = async (messageID: string, permissionID: string, allow: boolean) => { setMessages((value) => value.map((message) => message.id === messageID ? { ...message, permissionStatus: allow ? "allowed" : "denied" } : message)); const reply = await StudioService.ResolveAIAction(permissionID, allow); setMessages((value) => [...value, { id: `assistant-${Date.now()}`, from: "assistant", text: reply.answer, evidence: reply.evidence ?? [] }]) }
  return <aside className={cn("copilot-panel", collapsed && "copilot-collapsed")}><div className="copilot-header"><button className="copilot-icon" onClick={onToggle}><Bot className="size-4" /></button><div className="copilot-label"><h3>Equipment Copilot</h3><p>Grounded in live runtime context</p></div><Badge variant="success" className="copilot-label ml-auto">{provider}</Badge><button className="panel-collapse" onClick={onToggle}><ChevronRight className={cn("size-3.5", collapsed && "rotate-180")}/></button></div><div className="copilot-body"><Conversation><ConversationContent>{messages.length === 0 ? <ConversationEmptyState icon={<Sparkles className="size-7 text-primary" />} title="Ask about this equipment" description="Inspect state, explain messages, trace events, or prepare a permission-gated command." /> : messages.map((message) => <AIMessage key={message.id} from={message.from}><MessageContent><MessageResponse>{message.text}</MessageResponse>{message.evidence?.length ? <Tool className="mt-3"><ToolHeader title="Runtime context"/><ToolContent>{message.evidence.join(" · ")}</ToolContent></Tool> : null}{message.permission && <PermissionCard permission={message.permission} status={message.permissionStatus ?? "pending"} onAllow={() => resolvePermission(message.id, message.permission!.id, true)} onDeny={() => resolvePermission(message.id, message.permission!.id, false)}/>}</MessageContent></AIMessage>)}</ConversationContent></Conversation><div className="suggestions">{["Explain latest message", "Trace material flow", "发送命令"].map((suggestion) => <button key={suggestion} onClick={() => setPrompt(suggestion)}>{suggestion}</button>)}</div><PromptInput className="copilot-composer" onSubmit={ask}><PromptInputTextarea value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder={`Ask about ${device?.id ?? "equipment"}…`} /><PromptInputSubmit loading={loading} /></PromptInput><p className="copilot-note">Write actions require explicit card approval.</p></div></aside>
}

function PermissionCard({ permission, status, onAllow, onDeny }: { permission: PermissionRequest; status: "pending" | "allowed" | "denied"; onAllow: () => void; onDeny: () => void }) {
  return <div className={cn("permission-card", `permission-${status}`)}><div className="permission-heading"><ShieldCheck className="size-4"/><div><b>Permission required</b><span>{permission.tool}</span></div><Badge variant={status === "allowed" ? "success" : status === "denied" ? "outline" : "warning"}>{status}</Badge></div><p>{permission.summary}</p><pre>{JSON.stringify({ equipment: permission.equipmentId, command: permission.command, parameters: permission.parameters }, null, 2)}</pre><div className="permission-risk">{permission.risk}</div>{status === "pending" && <div className="permission-actions"><Button size="sm" variant="outline" onClick={onDeny}><ShieldX className="size-3.5"/>Deny</Button><Button size="sm" onClick={onAllow}><ShieldCheck className="size-3.5"/>Allow once</Button></div>}</div>
}

function paginate<T>(items: T[], page: number, pageSize: number) { return items.slice((Math.max(1, page) - 1) * pageSize, Math.max(1, page) * pageSize) }
function Pager({ page, total, pageSize, onChange }: { page: number; total: number; pageSize: number; onChange: (page: number) => void }) { const pages = Math.max(1, Math.ceil(total / pageSize)); if (total <= pageSize) return null; return <div className="pager"><Button size="sm" variant="outline" disabled={page <= 1} onClick={() => onChange(page - 1)}>Previous</Button><span>Page {Math.min(page, pages)} of {pages} · {total} records</span><Button size="sm" variant="outline" disabled={page >= pages} onClick={() => onChange(page + 1)}>Next</Button></div> }

function MessageTable({ messages }: { messages: SecsMessage[] }) { return <div className="table-shell"><table><thead><tr><th>Direction</th><th>Message</th><th>Equipment</th><th>System bytes</th><th>Time</th></tr></thead><tbody>{messages.map((message, index) => <tr key={`${message.id}-${message.direction}-${index}`}><td><Badge variant={message.direction === "IN" ? "success" : "default"}>{message.direction}</Badge></td><td className="font-mono font-medium">S{message.stream}F{message.function}{message.wait ? " W" : ""}</td><td>{message.equipmentId}</td><td className="font-mono text-muted-foreground">{message.systemBytes}</td><td>{formatTime(message.timestamp)}</td></tr>)}</tbody></table></div> }
function formatTime(value: string) { return new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }) }

export default App

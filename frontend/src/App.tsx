import {
  CSSProperties,
  DragEvent as ReactDragEvent,
  FormEvent,
  PointerEvent as ReactPointerEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Activity,
  BellRing,
  Bot,
  Boxes,
  Cable,
  ChevronRight,
  CircleGauge,
  Cpu,
  Database,
  FlaskConical,
  Code2,
  GitBranch,
  GripVertical,
  MessageSquareText,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Play,
  PlugZap,
  Radio,
  RefreshCw,
  Route,
  Server,
  FileText,
  Image as ImageIcon,
  Paperclip,
  Plus,
  Search,
  Settings2,
  ShieldCheck,
  ShieldX,
  Sparkles,
  Trash2,
  Unplug,
  Waves,
  X,
} from "lucide-react";
import { Events } from "@wailsio/runtime";
import {
  StudioService,
  type CopilotStreamEvent,
  type StudioSnapshot,
} from "../bindings/eapstudio";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Conversation,
  ConversationContent,
  ConversationEmptyState,
} from "@/components/ai-elements/conversation";
import {
  Message as AIMessage,
  MessageContent,
  MessageResponse,
} from "@/components/ai-elements/message";
import {
  PromptInput,
  PromptInputSubmit,
  PromptInputTextarea,
} from "@/components/ai-elements/prompt-input";
import { Tool, ToolContent, ToolHeader } from "@/components/ai-elements/tool";
import { cn } from "@/lib/utils";

type Device = NonNullable<StudioSnapshot["devices"]>[number];
type SecsMessage = NonNullable<Device["messages"]>[number];
type CanonicalEvent = NonNullable<Device["events"]>[number];
type EquipmentCommand = NonNullable<Device["commands"]>[number];
type AlarmRecord = NonNullable<StudioSnapshot["alarms"]>[number];
type PermissionRequest = NonNullable<
  Awaited<ReturnType<typeof StudioService.AskCopilot>>["permission"]
>;
type EquipmentActionResult = Awaited<
  ReturnType<typeof StudioService.ResolveEquipmentAction>
>;
type CopilotAttachment = {
  name: string;
  mediaType: string;
  dataURL: string;
  size: number;
};
type PageSize = 25 | 50 | 100 | 200;
type AIProfile = {
  id: string;
  name: string;
  provider: "local" | "responses" | "chat";
  baseURL: string;
  model: string;
  apiKey: string;
  hasApiKey?: boolean;
  isDefault?: boolean;
};
type EquipmentDraft = {
  key: string;
  id: string;
  badge: string;
  name: string;
  profile: string;
  adapter: string;
  driver: string;
  autoConnect: boolean;
  protocol: string;
  mode: string;
  host: string;
  port: number;
  sessionId: number;
};
type ConfigComparison = {
  runtimePath: string;
  packagedCount: number;
  runtimeCount: number;
  missing: string[] | null;
  extra: string[] | null;
  changed: string[] | null;
};
type Page =
  | "overview"
  | "devices"
  | "messages"
  | "events"
  | "commands"
  | "alarms"
  | "workbench"
  | "router"
  | "simulator"
  | "settings";

const navItems: { id: Page; label: string; icon: typeof Activity }[] = [
  { id: "overview", label: "Overview", icon: CircleGauge },
  { id: "devices", label: "Devices", icon: Server },
  { id: "messages", label: "Messages", icon: MessageSquareText },
  { id: "events", label: "Events", icon: Waves },
  { id: "commands", label: "Commands", icon: Play },
  { id: "alarms", label: "Alarms", icon: BellRing },
  { id: "workbench", label: "Workbench", icon: Code2 },
  { id: "router", label: "Router", icon: Route },
  { id: "simulator", label: "Simulator", icon: FlaskConical },
];

const emptySnapshot: StudioSnapshot = {
  devices: [],
  routes: [],
  deliveries: [],
  automations: [],
  alarms: [],
  storage: {
    traceCount: 0,
    eventCount: 0,
    commandCount: 0,
    alarmCount: 0,
    droppedTrace: 0,
    databaseBytes: 0,
  },
  generated: new Date().toISOString(),
};
const pageSizes: PageSize[] = [25, 50, 100, 200];

function loadPageSize(): PageSize {
  const value = Number(localStorage.getItem("eapstudio.pageSize"));
  return pageSizes.includes(value as PageSize) ? (value as PageSize) : 25;
}

function initialAIProfiles(): AIProfile[] {
  const baseURL = "https://api.openai.com/v1";
  const model = "gpt-5.6-luna";
  return [
    {
      id: "local",
      name: "Local grounded",
      provider: "local",
      baseURL: "",
      model: "",
      apiKey: "",
    },
    {
      id: "responses",
      name: "OpenAI Responses",
      provider: "responses",
      baseURL,
      model,
      apiKey: "",
    },
    {
      id: "chat",
      name: "Chat compatible",
      provider: "chat",
      baseURL,
      model,
      apiKey: "",
    },
  ];
}

function compactDeviceLabel(id: string) {
  const parts = id.split(/[-_]/).filter(Boolean);
  return (parts[parts.length - 1] ?? id).slice(0, 3).toUpperCase();
}

function App() {
  const [snapshot, setSnapshot] = useState<StudioSnapshot>(emptySnapshot);
  const [page, setPage] = useState<Page>("overview");
  const [selectedDeviceID, setSelectedDeviceID] = useState("ETCHER-01");
  const [selectedMessage, setSelectedMessage] = useState<SecsMessage | null>(
    null,
  );
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [leftWidth, setLeftWidth] = useState(
    () => Number(localStorage.getItem("eapstudio.leftWidth")) || 220,
  );
  const [rightWidth, setRightWidth] = useState(
    () => Number(localStorage.getItem("eapstudio.rightWidth")) || 360,
  );
  const [leftCollapsed, setLeftCollapsed] = useState(false);
  const [rightCollapsed, setRightCollapsed] = useState(false);
  const [pageSize, setPageSize] = useState<PageSize>(loadPageSize);
  const [deviceOrder, setDeviceOrder] = useState<string[]>([]);
  const [draggedDeviceID, setDraggedDeviceID] = useState("");
  const deviceOrderSaveTimer = useRef<number | undefined>(undefined);

  const rawDevices = snapshot.devices ?? [];
  const devices = useMemo(() => {
    const byID = new Map(rawDevices.map((device) => [device.id, device]));
    return [
      ...deviceOrder.filter((id) => byID.has(id)).map((id) => byID.get(id)!),
      ...rawDevices.filter((device) => !deviceOrder.includes(device.id)),
    ];
  }, [rawDevices, deviceOrder]);
  const selectedDevice =
    devices.find((device) => device.id === selectedDeviceID) ?? devices[0];
  const allMessages = useMemo(
    () =>
      devices
        .flatMap((device) => device.messages ?? [])
        .sort((a, b) => b.timestamp.localeCompare(a.timestamp)),
    [devices],
  );
  const allEvents = useMemo(
    () =>
      devices
        .flatMap((device) => device.events ?? [])
        .sort((a, b) => b.timestamp.localeCompare(a.timestamp)),
    [devices],
  );
  const allCommands = useMemo(
    () =>
      devices
        .flatMap((device) => device.commands ?? [])
        .sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
    [devices],
  );
  const normalizedQuery = query.trim().toLowerCase();
  const visibleMessages = useMemo(
    () =>
      allMessages.filter(
        (message) =>
          !normalizedQuery ||
          `${message.equipmentId} s${message.stream}f${message.function} ${message.direction} ${message.sml}`
            .toLowerCase()
            .includes(normalizedQuery),
      ),
    [allMessages, normalizedQuery],
  );
  const visibleEvents = useMemo(
    () =>
      allEvents.filter(
        (event) =>
          !normalizedQuery ||
          `${event.name} ${event.equipmentId} ${event.correlationId} ${JSON.stringify(event.data)}`
            .toLowerCase()
            .includes(normalizedQuery),
      ),
    [allEvents, normalizedQuery],
  );
  const visibleAlarms = useMemo(
    () =>
      (snapshot.alarms ?? []).filter(
        (alarm) =>
          !normalizedQuery ||
          `${alarm.equipmentId} ${alarm.alarmId} ${alarm.code} ${alarm.text} ${alarm.severity} ${alarm.state}`
            .toLowerCase()
            .includes(normalizedQuery),
      ),
    [snapshot.alarms, normalizedQuery],
  );

  const refresh = () =>
    StudioService.ReloadRules()
      .then(() => StudioService.Snapshot())
      .then(setSnapshot)
      .catch((reason) => setError(String(reason)));

  useEffect(() => {
    document.documentElement.dataset.theme =
      localStorage.getItem("eapstudio.theme") ?? "dark";
    const retentionDays = Number(
      localStorage.getItem("eapstudio.historyRetentionDays"),
    );
    if ([7, 30, 90, 365].includes(retentionDays)) {
      StudioService.ApplyHistoryRetention(retentionDays).catch(() => undefined);
    }
    refresh();
    const cancel = Events.On("studio:snapshot-changed", (event) =>
      setSnapshot(event.data as StudioSnapshot),
    );
    return () => cancel();
  }, []);

  useEffect(() => {
    localStorage.setItem("eapstudio.leftWidth", String(leftWidth));
  }, [leftWidth]);
  useEffect(() => {
    localStorage.setItem("eapstudio.rightWidth", String(rightWidth));
  }, [rightWidth]);
  useEffect(
    () => () => {
      if (deviceOrderSaveTimer.current)
        window.clearTimeout(deviceOrderSaveTimer.current);
    },
    [],
  );
  useEffect(() => {
    localStorage.setItem("eapstudio.pageSize", String(pageSize));
  }, [pageSize]);

  useEffect(() => {
    const ids = rawDevices.map((device) => device.id);
    setDeviceOrder((current) => {
      const next = [
        ...current.filter((id) => ids.includes(id)),
        ...ids.filter((id) => !current.includes(id)),
      ];
      return next.join("\u0000") === current.join("\u0000") ? current : next;
    });
  }, [snapshot.devices]);

  useEffect(() => {
    if (!selectedMessage && allMessages.length)
      setSelectedMessage(allMessages[0]);
  }, [allMessages, selectedMessage]);

  const invoke = async (action: () => Promise<unknown>) => {
    setError("");
    try {
      await action();
      await refresh();
    } catch (reason) {
      setError(String(reason));
    }
  };

  const beginResize = (side: "left" | "right", event: ReactPointerEvent) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = side === "left" ? leftWidth : rightWidth;
    const move = (moveEvent: PointerEvent) => {
      const delta = moveEvent.clientX - startX;
      const next = side === "left" ? startWidth + delta : startWidth - delta;
      if (side === "left") setLeftWidth(Math.min(360, Math.max(176, next)));
      else setRightWidth(Math.min(560, Math.max(300, next)));
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };

  const shellStyle = {
    "--left-width": `${leftCollapsed ? 58 : leftWidth}px`,
    "--right-handle-width": `${rightCollapsed ? 0 : 5}px`,
    "--right-width": `${rightCollapsed ? 0 : rightWidth}px`,
  } as CSSProperties;

  const dropDevice = (targetID: string, event: ReactDragEvent) => {
    event.preventDefault();
    const sourceID =
      event.dataTransfer.getData("text/plain") || draggedDeviceID;
    if (!sourceID || sourceID === targetID) return;
    const next = [...deviceOrder];
    const sourceIndex = next.indexOf(sourceID);
    const targetIndex = next.indexOf(targetID);
    if (sourceIndex < 0 || targetIndex < 0) return;
    next.splice(sourceIndex, 1);
    next.splice(targetIndex, 0, sourceID);
    persistDeviceOrder(next);
    setDraggedDeviceID("");
  };

  const persistDeviceOrder = (order: string[]) => {
    setDeviceOrder(order);
    if (deviceOrderSaveTimer.current)
      window.clearTimeout(deviceOrderSaveTimer.current);
    deviceOrderSaveTimer.current = window.setTimeout(() => {
      StudioService.SaveDeviceOrder(order).catch((reason) =>
        setError(`Save device order failed: ${String(reason)}`),
      );
    }, 150);
  };

  return (
    <div className="app-shell" style={shellStyle}>
      <aside className={cn("sidebar", leftCollapsed && "sidebar-collapsed")}>
        <div className="brand-block">
          <div className="brand-mark">
            <Radio className="size-5" />
          </div>
          <div className="sidebar-label">
            <div className="brand-name">EapStudio</div>
            <div className="brand-subtitle">Equipment integration</div>
          </div>
        </div>

        <div className="sidebar-body">
          <nav className="nav-list">
            <p className="nav-label">Workspace</p>
            {navItems.map((item) => (
              <button
                title={item.label}
                key={item.id}
                className={cn(
                  "nav-item",
                  page === item.id && "nav-item-active",
                )}
                onClick={() => setPage(item.id)}
              >
                <item.icon className="size-4 shrink-0" />
                <span className="sidebar-label">{item.label}</span>
                {page === item.id && (
                  <ChevronRight className="sidebar-label ml-auto size-3.5" />
                )}
              </button>
            ))}
          </nav>

          <div className="mt-5 min-h-0 flex-1">
            <div className="mb-2 flex items-center justify-between px-2">
              <p className="nav-label !mb-0">Equipment</p>
              <Badge variant="outline">{devices.length}</Badge>
            </div>
            <div className="equipment-list space-y-1">
              {devices.map((device) => (
                <button
                  key={device.id}
                  title={`${device.id} · ${device.state}`}
                  draggable={!leftCollapsed}
                  onDragStart={(event) => {
                    event.dataTransfer.effectAllowed = "move";
                    event.dataTransfer.setData("text/plain", device.id);
                    setDraggedDeviceID(device.id);
                  }}
                  onDragEnd={() => setDraggedDeviceID("")}
                  onDragOver={(event) => {
                    event.preventDefault();
                    event.dataTransfer.dropEffect = "move";
                  }}
                  onDrop={(event) => dropDevice(device.id, event)}
                  onClick={() => {
                    setSelectedDeviceID(device.id);
                    setPage("devices");
                  }}
                  className={cn(
                    "device-nav",
                    device.id === selectedDevice?.id && "device-nav-active",
                    draggedDeviceID === device.id && "device-nav-dragging",
                  )}
                >
                  <span
                    className={cn(
                      "device-monogram",
                      device.state === "selected" && "device-monogram-online",
                    )}
                  >
                    {device.badge || compactDeviceLabel(device.id)}
                  </span>
                  <span className="sidebar-label min-w-0 text-left">
                    <span className="block truncate text-xs font-medium">
                      {device.id}
                    </span>
                    <span className="block truncate text-[10px] text-muted-foreground">
                      {device.profileName} · {device.adapter}
                    </span>
                  </span>
                  <GripVertical className="sidebar-label device-drag-handle size-3.5" />
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="sidebar-footer">
          <div
            className="sidebar-runtime sidebar-label"
            title="Runtime healthy"
          >
            <span className="status-dot status-online" />
            <span>Runtime healthy</span>
          </div>
          <Button
            className="sidebar-footer-action"
            variant="ghost"
            size="icon"
            onClick={refresh}
            aria-label="Refresh runtime"
            title="Reload Router/Automation rules and refresh runtime"
          >
            <RefreshCw className="size-4" />
          </Button>
          <Button
            className={cn(
              "sidebar-footer-action",
              page === "settings" && "sidebar-footer-action-active",
            )}
            variant="ghost"
            size="icon"
            onClick={() => setPage("settings")}
            aria-label="Settings"
            title="Settings"
          >
            <Settings2 className="size-4" />
          </Button>
        </div>
      </aside>
      <ResizeHandle
        side="left"
        hidden={leftCollapsed}
        onPointerDown={(event) => beginResize("left", event)}
        onDoubleClick={() => setLeftCollapsed(true)}
      />

      <main className="main-area">
        <header className="topbar">
          <Button
            className="topbar-panel-toggle"
            variant="outline"
            size="icon"
            onClick={() => setLeftCollapsed((value) => !value)}
            aria-label={leftCollapsed ? "Expand sidebar" : "Collapse sidebar"}
          >
            {leftCollapsed ? (
              <PanelLeftOpen className="size-4" />
            ) : (
              <PanelLeftClose className="size-4" />
            )}
          </Button>
          <div>
            <h1 className="text-base font-semibold">
              {page === "settings"
                ? "Settings"
                : navItems.find((item) => item.id === page)?.label}
            </h1>
            <p className="text-xs text-muted-foreground">
              AI-powered SECS/GEM Equipment Integration Studio
            </p>
          </div>
          <label className="global-search">
            <Search className="size-3.5" />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search equipment, SxFy, event, alarm…"
            />
          </label>
          <div className="flex items-center gap-2">
            <Button
              className="topbar-panel-toggle"
              variant="outline"
              size="icon"
              onClick={() => setRightCollapsed((value) => !value)}
              aria-label={
                rightCollapsed ? "Expand copilot" : "Collapse copilot"
              }
            >
              {rightCollapsed ? (
                <PanelRightOpen className="size-4" />
              ) : (
                <PanelRightClose className="size-4" />
              )}
            </Button>
          </div>
        </header>

        {error && (
          <div className="mx-5 mt-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-red-300">
            {error}
          </div>
        )}

        <section className="content-pane">
          {page === "overview" && (
            <Overview
              devices={devices}
              messages={visibleMessages}
              events={visibleEvents}
              alarmCount={
                visibleAlarms.filter((alarm) => alarm.state === "active").length
              }
              storage={snapshot.storage}
              onOpen={(id) => {
                setSelectedDeviceID(id);
                setPage("devices");
              }}
            />
          )}
          {page === "devices" && selectedDevice && (
            <DeviceDetail
              device={selectedDevice}
              onConnect={() =>
                invoke(() => StudioService.ConnectDevice(selectedDevice.id))
              }
              onDisconnect={() =>
                invoke(() => StudioService.DisconnectDevice(selectedDevice.id))
              }
              onEmit={(scenario) =>
                invoke(() =>
                  StudioService.EmitSimulatorScenario(
                    selectedDevice.id,
                    scenario,
                  ),
                )
              }
            />
          )}
          {page === "messages" && (
            <Messages
              messages={visibleMessages}
              equipmentIDs={devices.map((device) => device.id)}
              query={query}
              selected={selectedMessage}
              pageSize={pageSize}
              onSelect={setSelectedMessage}
            />
          )}
          {page === "events" && (
            <EventsPage
              events={visibleEvents}
              equipmentIDs={devices.map((device) => device.id)}
              query={query}
              pageSize={pageSize}
            />
          )}
          {page === "commands" && (
            <CommandsPage
              commands={allCommands}
              equipmentIDs={devices.map((device) => device.id)}
              query={query}
              pageSize={pageSize}
            />
          )}
          {page === "alarms" && (
            <AlarmsPage alarms={visibleAlarms} pageSize={pageSize} />
          )}
          {page === "router" && (
            <RouterPage snapshot={snapshot} query={normalizedQuery} />
          )}
          {page === "workbench" && <ProfileWorkbench />}
          {page === "simulator" && (
            <Simulator
              devices={devices}
              onEmit={(id, scenario) =>
                invoke(() => StudioService.EmitSimulatorScenario(id, scenario))
              }
            />
          )}
          {page === "settings" && (
            <SettingsPage
              devices={devices}
              storage={snapshot.storage}
              pageSize={pageSize}
              onPageSizeChange={setPageSize}
              onDeviceOrderChange={persistDeviceOrder}
            />
          )}
        </section>
      </main>
      <ResizeHandle
        side="right"
        hidden={rightCollapsed}
        onPointerDown={(event) => beginResize("right", event)}
        onDoubleClick={() => setRightCollapsed(true)}
      />
      <Copilot devices={devices} collapsed={rightCollapsed} />
    </div>
  );
}

function ResizeHandle({
  side,
  hidden,
  onPointerDown,
  onDoubleClick,
}: {
  side: "left" | "right";
  hidden: boolean;
  onPointerDown: (event: ReactPointerEvent) => void;
  onDoubleClick: () => void;
}) {
  return (
    <div
      className={cn("resize-handle", hidden && "resize-handle-hidden")}
      data-side={side}
      onPointerDown={onPointerDown}
      onDoubleClick={onDoubleClick}
    >
      <GripVertical className="size-3" />
    </div>
  );
}

function Overview({
  devices,
  messages,
  events,
  alarmCount,
  storage,
  onOpen,
}: {
  devices: Device[];
  messages: SecsMessage[];
  events: CanonicalEvent[];
  alarmCount: number;
  storage: StudioSnapshot["storage"];
  onOpen: (id: string) => void;
}) {
  const online = devices.filter((device) => device.state === "selected").length;
  return (
    <div className="page-stack">
      <div className="hero-strip">
        <div>
          <div className="eyebrow">
            <Sparkles className="size-3.5" /> Runtime / Studio / Copilot
          </div>
          <h2>Equipment integration, made observable.</h2>
          <p>
            Two isolated DeviceRuntime instances share one compiled Profile
            while preserving independent protocol state and pipelines.
          </p>
        </div>
        <div className="pipeline-mini">
          <span>HSMS</span>
          <ChevronRight />
          <span>Profile</span>
          <ChevronRight />
          <span>Event</span>
          <ChevronRight />
          <span>Router</span>
        </div>
      </div>
      <div className="metric-grid">
        <Metric
          icon={Server}
          label="Equipment"
          value={`${online}/${devices.length}`}
          detail="HSMS Selected"
          color="cyan"
        />
        <Metric
          icon={MessageSquareText}
          label="Message trace"
          value={String(messages.length)}
          detail="IN / OUT retained"
          color="violet"
        />
        <Metric
          icon={Waves}
          label="Canonical events"
          value={String(events.length)}
          detail="Protocol independent"
          color="emerald"
        />
        <Metric
          icon={BellRing}
          label="Active alarms"
          value={String(alarmCount)}
          detail={`${storage?.traceCount ?? 0} traces persisted`}
          color="amber"
        />
      </div>
      <div className="section-heading">
        <div>
          <h3>Equipment fleet</h3>
          <p>Each connection runs in its own isolated runtime.</p>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        {devices.map((device) => (
          <Card
            key={device.id}
            className="equipment-card cursor-pointer"
            onClick={() => onOpen(device.id)}
          >
            <CardHeader className="flex-row items-start justify-between">
              <div>
                <CardTitle>{device.name}</CardTitle>
                <CardDescription>
                  {device.id} · {device.vendor} {device.model}
                </CardDescription>
              </div>
              <Badge
                variant={device.state === "selected" ? "success" : "outline"}
              >
                {device.state}
              </Badge>
            </CardHeader>
            <CardContent>
              <div className="connection-line">
                <Cable className="size-3.5" /> {device.host}:{device.port}
                <span>{device.protocol}</span>
              </div>
              <div className="card-stats">
                <span>
                  <b>{device.messages?.length ?? 0}</b> messages
                </span>
                <span>
                  <b>{device.events?.length ?? 0}</b> events
                </span>
                <span>
                  <b>{device.adapter}</b> adapter
                </span>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="section-heading">
        <div>
          <h3>Live protocol activity</h3>
          <p>
            Fast replies and asynchronous business processing are shown
            together.
          </p>
        </div>
      </div>
      <MessageTable messages={messages.slice(0, 7)} />
    </div>
  );
}

function Metric({
  icon: Icon,
  label,
  value,
  detail,
  color,
}: {
  icon: typeof Server;
  label: string;
  value: string;
  detail: string;
  color: string;
}) {
  return (
    <Card className="metric-card">
      <CardContent className="flex items-center gap-3 p-4">
        <div className={`metric-icon metric-${color}`}>
          <Icon className="size-4" />
        </div>
        <div>
          <p className="text-[11px] text-muted-foreground">{label}</p>
          <p className="text-xl font-semibold tracking-tight">{value}</p>
          <p className="text-[10px] text-muted-foreground">{detail}</p>
        </div>
      </CardContent>
    </Card>
  );
}

function DeviceDetail({
  device,
  onConnect,
  onDisconnect,
  onEmit,
}: {
  device: Device;
  onConnect: () => void;
  onDisconnect: () => void;
  onEmit: (scenario: string) => void;
}) {
  const scenarios = device.scenarios ?? [];
  const [scenario, setScenario] = useState(scenarios[0]?.id ?? "");
  useEffect(() => {
    setScenario(device.scenarios?.[0]?.id ?? "");
  }, [device.id]);
  return (
    <div className="page-stack">
      <div className="detail-header">
        <div className="equipment-glyph">
          <Cpu className="size-6" />
        </div>
        <div>
          <div className="flex items-center gap-2">
            <h2>{device.name}</h2>
            <Badge
              variant={device.state === "selected" ? "success" : "outline"}
            >
              {device.state}
            </Badge>
          </div>
          <p>
            {device.id} · {device.vendor} {device.model}
          </p>
        </div>
        <div className="ml-auto flex gap-2">
          {device.driver === "simulator" && (
            <>
              <select
                className="filter-select scenario-select"
                value={scenario}
                onChange={(event) => setScenario(event.target.value)}
                aria-label="Simulator scenario"
              >
                {scenarios.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.displayName} ·{" "}
                    {item.event || `S${item.stream}F${item.function}`}
                  </option>
                ))}
              </select>
              <Button
                variant="outline"
                disabled={!scenario}
                onClick={() => scenario && onEmit(scenario)}
              >
                <Play className="size-4" />
                Emit inbound
              </Button>
            </>
          )}
          {device.state === "selected" ? (
            <Button variant="outline" onClick={onDisconnect}>
              <Unplug className="size-4" />
              Disconnect
            </Button>
          ) : (
            <Button onClick={onConnect}>
              <PlugZap className="size-4" />
              Connect
            </Button>
          )}
        </div>
      </div>
      <div className="detail-summary-grid grid grid-cols-3 gap-3">
        <InfoCard
          label="Connection"
          value={`${device.host}:${device.port}`}
          sub={`${device.protocol} / ${device.mode}`}
          icon={Cable}
        />
        <InfoCard
          label="Equipment Profile"
          value={device.profileName}
          sub={`${device.vendor} · ${device.model}`}
          icon={Boxes}
        />
        <InfoCard
          label="Adapter"
          value={device.adapter}
          sub="Profile-driven parsing"
          icon={Database}
        />
      </div>
      <EquipmentSender device={device} />
      <Card className="diagnostics-card">
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>Connection & protocol diagnostics</CardTitle>
            <CardDescription>
              {device.stateDetail || "No lifecycle detail reported"}
            </CardDescription>
          </div>
          <Badge variant={device.diagnostics.lastError ? "warning" : "success"}>
            {device.diagnostics.lastError ? "attention" : "healthy"}
          </Badge>
        </CardHeader>
        <CardContent className="diagnostics-grid">
          <span>
            <b>{device.diagnostics.connectAttempts}</b>connect attempts
          </span>
          <span>
            <b>{device.diagnostics.messagesIn}</b>messages in
          </span>
          <span>
            <b>{device.diagnostics.messagesOut}</b>messages out
          </span>
          <span>
            <b>{device.diagnostics.parseErrors}</b>parse errors
          </span>
          <span>
            <b>{device.diagnostics.queueDrops}</b>queue drops
          </span>
          <span>
            <b>{device.diagnostics.commandFailures}</b>command failures
          </span>
          {device.driver === "go-secs" && (
            <>
              <span>
                <b>{device.diagnostics.protocol.reconnects}</b>HSMS reconnects
              </span>
              <span>
                <b>{device.diagnostics.protocol.inflight}</b>transactions in
                flight
              </span>
              <span>
                <b>
                  {device.diagnostics.protocol.linktestReceived}/
                  {device.diagnostics.protocol.linktestSent}
                </b>
                linktest OK / sent
              </span>
              <span>
                <b>{device.diagnostics.protocol.linktestErrors}</b>linktest / T6
                errors
              </span>
              <span>
                <b>{device.diagnostics.protocol.rejectReceived}</b>peer rejects
              </span>
              <span>
                <b>{device.diagnostics.protocol.separateReceived}</b>peer
                separates
              </span>
            </>
          )}
          {device.diagnostics.lastError && (
            <p>{device.diagnostics.lastError}</p>
          )}
        </CardContent>
      </Card>
      <div className="architecture-line">
        <div>
          <Radio />
          go-secs/v2
        </div>
        <ChevronRight />
        <div>
          <Cpu />
          DeviceRuntime
        </div>
        <ChevronRight />
        <div>
          <Boxes />
          Compiled Profile
        </div>
        <ChevronRight />
        <div>
          <Waves />
          Canonical Event
        </div>
        <ChevronRight />
        <div>
          <GitBranch />
          Router
        </div>
      </div>
      <div className="section-heading">
        <div>
          <h3>Latest messages</h3>
          <p>S6F12 is issued on the fast path before event conversion.</p>
        </div>
      </div>
      <MessageTable
        messages={[...(device.messages ?? [])].reverse().slice(0, 8)}
      />
      <div className="section-heading">
        <div>
          <h3>Commands</h3>
          <p>
            Automation decisions use verb.noun names and preserve their causal
            chain.
          </p>
        </div>
      </div>
      <CommandCards
        commands={[...(device.commands ?? [])].reverse().slice(0, 4)}
      />
      <div className="section-heading">
        <div>
          <h3>Converted events</h3>
          <p>Stable noun.verb facts ready for MQ, MES and AI correlation.</p>
        </div>
      </div>
      <EventCards events={[...(device.events ?? [])].reverse().slice(0, 6)} />
    </div>
  );
}

function EquipmentSender({ device }: { device: Device }) {
  const commands = device.availableCommands ?? [];
  const [mode, setMode] = useState<"command" | "sml">(
    commands.length ? "command" : "sml",
  );
  const [commandName, setCommandName] = useState(commands[0]?.name ?? "");
  const [parameters, setParameters] = useState<Record<string, string>>({});
  const [sml, setSML] = useState("S1F1 W\n.");
  const [timeoutSeconds, setTimeoutSeconds] = useState(30);
  const [permission, setPermission] = useState<PermissionRequest | null>(null);
  const [permissionStatus, setPermissionStatus] = useState<
    "pending" | "allowed" | "denied" | "expired"
  >("pending");
  const [result, setResult] = useState<EquipmentActionResult | null>(null);
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const selectedCommand = commands.find((item) => item.name === commandName);

  useEffect(() => {
    const first = device.availableCommands?.[0];
    setMode(first ? "command" : "sml");
    setCommandName(first?.name ?? "");
    setParameters({});
    setPermission(null);
    setResult(null);
    setStatus("");
  }, [device.id]);

  const resetPrepared = () => {
    setPermission(null);
    setResult(null);
    setStatus("");
    setPermissionStatus("pending");
  };

  const prepare = async () => {
    setBusy(true);
    setResult(null);
    setStatus("");
    try {
      const next =
        mode === "command"
          ? await StudioService.PrepareEquipmentCommand({
              equipmentId: device.id,
              command: commandName,
              parameters,
              timeoutSeconds,
            })
          : await StudioService.PrepareEquipmentMessage({
              equipmentId: device.id,
              sml,
              timeoutSeconds,
            });
      setPermission(next);
      setPermissionStatus("pending");
      setStatus("Review the exact payload below. Nothing has been sent yet.");
    } catch (reason) {
      setStatus(`Prepare failed · ${String(reason)}`);
    } finally {
      setBusy(false);
    }
  };

  const resolve = async (allow: boolean) => {
    if (!permission || busy) return;
    setBusy(true);
    try {
      const next = await StudioService.ResolveEquipmentAction(
        permission.id,
        allow,
      );
      setPermissionStatus(
        next.status === "expired"
          ? "expired"
          : allow
            ? "allowed"
            : "denied",
      );
      setResult(next);
      setStatus(next.message);
    } catch (reason) {
      setStatus(`Send failed · ${String(reason)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="equipment-sender">
      <CardHeader className="flex-row items-start justify-between">
        <div>
          <CardTitle>Host → Equipment</CardTitle>
          <CardDescription>
            Send a Profile command or expert SML and wait for the secondary
            reply.
          </CardDescription>
        </div>
        <Badge variant={device.state === "selected" ? "success" : "warning"}>
          {device.state === "selected" ? "ready" : "connect first"}
        </Badge>
      </CardHeader>
      <CardContent className="equipment-sender-body">
        <div className="sender-mode-row">
          <Button
            size="sm"
            variant={mode === "command" ? "default" : "outline"}
            disabled={!commands.length}
            onClick={() => {
              setMode("command");
              resetPrepared();
            }}
          >
            Profile command
          </Button>
          <Button
            size="sm"
            variant={mode === "sml" ? "default" : "outline"}
            onClick={() => {
              setMode("sml");
              resetPrepared();
            }}
          >
            Raw SML
          </Button>
          <label className="sender-timeout">
            Reply timeout
            <select
              value={timeoutSeconds}
              onChange={(event) => {
                setTimeoutSeconds(Number(event.target.value));
                resetPrepared();
              }}
            >
              {[10, 30, 60, 120].map((value) => (
                <option key={value} value={value}>
                  {value}s
                </option>
              ))}
            </select>
          </label>
        </div>

        {mode === "command" ? (
          <div className="sender-command-form">
            <label>
              Command
              <select
                value={commandName}
                onChange={(event) => {
                  setCommandName(event.target.value);
                  setParameters({});
                  resetPrepared();
                }}
              >
                {commands.map((item) => (
                  <option key={item.name} value={item.name}>
                    {item.displayName || item.name} · S{item.stream}F
                    {item.function}
                    {item.wait ? " W" : ""}
                  </option>
                ))}
              </select>
            </label>
            <div className="sender-parameters">
              {(selectedCommand?.parameters ?? []).map((name) => (
                <label key={name}>
                  {name}
                  <input
                    value={parameters[name] ?? ""}
                    onChange={(event) => {
                      setParameters((value) => ({
                        ...value,
                        [name]: event.target.value,
                      }));
                      resetPrepared();
                    }}
                    placeholder={`Enter ${name}`}
                  />
                </label>
              ))}
              {!selectedCommand?.parameters?.length && (
                <p>This Profile command has no parameters.</p>
              )}
            </div>
          </div>
        ) : (
          <label className="sender-sml">
            Complete SML message
            <textarea
              value={sml}
              onChange={(event) => {
                setSML(event.target.value);
                resetPrepared();
              }}
              spellCheck={false}
              placeholder={'S1F3 W\n<L[1]\n  <U4 1001>\n>\n.'}
            />
            <span>
              The SxFy header and W bit are parsed from SML. Raw mode bypasses
              Profile command validation.
            </span>
          </label>
        )}

        <div className="sender-actions">
          <p>{status || "Prepare creates a one-shot permission card."}</p>
          <Button
            size="sm"
            disabled={busy || device.state !== "selected" || !!permission}
            onClick={() => void prepare()}
          >
            <Play className="size-3.5" />
            {busy ? "Working…" : "Review send"}
          </Button>
          {permission && permissionStatus !== "pending" && (
            <Button size="sm" variant="outline" onClick={resetPrepared}>
              Prepare another
            </Button>
          )}
        </div>

        {permission && (
          <PermissionCard
            permission={permission}
            status={permissionStatus}
            onAllow={() => void resolve(true)}
            onDeny={() => void resolve(false)}
          />
        )}

        {result && (result.request || result.reply) && (
          <div className="sender-exchange">
            <div>
              <div className="sender-exchange-heading">
                <b>Primary request</b>
                {result.request && (
                  <Badge variant="outline">
                    S{result.request.stream}F{result.request.function}
                    {result.request.wait ? " W" : ""}
                  </Badge>
                )}
              </div>
              <pre>{result.request?.sml || "No request body recorded"}</pre>
            </div>
            <div>
              <div className="sender-exchange-heading">
                <b>Secondary reply</b>
                {result.reply ? (
                  <Badge variant={result.error ? "warning" : "success"}>
                    S{result.reply.stream}F{result.reply.function}
                  </Badge>
                ) : (
                  <Badge variant="outline">none</Badge>
                )}
              </div>
              <pre>
                {result.reply?.sml ||
                  (result.request?.wait
                    ? result.error || "No reply received"
                    : "W bit is off; no reply expected")}
              </pre>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function InfoCard({
  label,
  value,
  sub,
  icon: Icon,
}: {
  label: string;
  value: string;
  sub: string;
  icon: typeof Cable;
}) {
  return (
    <Card>
      <CardContent className="flex items-center gap-3 p-4">
        <div className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
          <Icon className="size-4" />
        </div>
        <div>
          <p className="text-[10px] uppercase tracking-wider text-muted-foreground">
            {label}
          </p>
          <p className="mt-1 text-sm font-medium">{value}</p>
          <p className="text-[10px] text-muted-foreground">{sub}</p>
        </div>
      </CardContent>
    </Card>
  );
}

function Messages({
  messages,
  equipmentIDs,
  query,
  selected,
  pageSize,
  onSelect,
}: {
  messages: SecsMessage[];
  equipmentIDs: string[];
  query: string;
  selected: SecsMessage | null;
  pageSize: PageSize;
  onSelect: (message: SecsMessage) => void;
}) {
  const [direction, setDirection] = useState("ALL");
  const [equipment, setEquipment] = useState("ALL");
  const [mode, setMode] = useState<"live" | "history">("live");
  const [sinceHours, setSinceHours] = useState(24);
  const [history, setHistory] = useState<{
    items: SecsMessage[];
    total: number;
  }>({ items: [], total: 0 });
  const [loading, setLoading] = useState(false);
  const [view, setView] = useState<"sml" | "tree" | "raw">("sml");
  const [page, setPage] = useState(1);
  const filtered = messages.filter(
    (message) =>
      (direction === "ALL" || message.direction === direction) &&
      (equipment === "ALL" || message.equipmentId === equipment),
  );
  const pageItems =
    mode === "history" ? history.items : paginate(filtered, page, pageSize);
  const total = mode === "history" ? history.total : filtered.length;
  useEffect(() => setPage(1), [pageSize, mode, sinceHours, query]);
  useEffect(() => {
    if (mode !== "history") return;
    let active = true;
    setLoading(true);
    StudioService.QueryTraceHistory({
      page,
      pageSize,
      equipmentId: equipment,
      search: query,
      direction,
      name: "",
      status: "",
      sinceHours,
    })
      .then((result) => {
        if (!active) return;
        const items = (result.items ?? []) as SecsMessage[];
        setHistory({ items, total: result.total });
        if (items.length) onSelect(items[0]);
      })
      .catch(() => active && setHistory({ items: [], total: 0 }))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [mode, page, pageSize, equipment, direction, query, sinceHours]);
  return (
    <div className="split-inspector">
      <div className="message-list">
        <div className="panel-heading">
          <div>
            <h3>Message trace</h3>
            <p>{loading ? "Loading SQLite…" : `${total} ${mode} messages`}</p>
          </div>
          <div className="filter-bar">
            <div className="mode-switch">
              <button
                className={mode === "live" ? "active" : ""}
                onClick={() => setMode("live")}
              >
                Live
              </button>
              <button
                className={mode === "history" ? "active" : ""}
                onClick={() => setMode("history")}
              >
                History
              </button>
            </div>
            <select
              className="filter-select"
              value={equipment}
              onChange={(event) => {
                setEquipment(event.target.value);
                setPage(1);
              }}
            >
              <option value="ALL">ALL EQUIPMENT</option>
              {equipmentIDs.map((id) => (
                <option key={id}>{id}</option>
              ))}
            </select>
            {mode === "history" && (
              <select
                className="filter-select"
                value={sinceHours}
                onChange={(event) => {
                  setSinceHours(Number(event.target.value));
                  setPage(1);
                }}
              >
                <option value={1}>1 HOUR</option>
                <option value={24}>24 HOURS</option>
                <option value={168}>7 DAYS</option>
                <option value={720}>30 DAYS</option>
                <option value={0}>ALL TIME</option>
              </select>
            )}
            <select
              className="filter-select"
              value={direction}
              onChange={(event) => {
                setDirection(event.target.value);
                setPage(1);
              }}
            >
              <option>ALL</option>
              <option>IN</option>
              <option>OUT</option>
            </select>
          </div>
        </div>
        <div className="message-page">
          {pageItems.map((message) => (
            <button
              key={`${message.id}-${message.direction}`}
              className={cn(
                "message-row",
                selected?.id === message.id &&
                  selected.direction === message.direction &&
                  "message-row-active",
              )}
              onClick={() => onSelect(message)}
            >
              <Badge
                variant={message.direction === "IN" ? "success" : "default"}
              >
                {message.direction}
              </Badge>
              <span className="font-mono text-xs font-semibold">
                S{message.stream}F{message.function}
                {message.wait ? " W" : ""}
              </span>
              <span className="ml-auto text-[10px] text-muted-foreground">
                {formatTime(message.timestamp)}
              </span>
              <span className="w-full truncate text-left text-[10px] text-muted-foreground">
                {message.equipmentId} · system {message.systemBytes}
              </span>
            </button>
          ))}
        </div>
        <Pager
          page={page}
          total={total}
          pageSize={pageSize}
          onChange={setPage}
        />
      </div>
      <div className="inspector-panel">
        {selected ? (
          <>
            <div className="panel-heading">
              <div>
                <h3>
                  S{selected.stream}F{selected.function}
                  {selected.wait ? " W" : ""}
                </h3>
                <p>
                  {selected.equipmentId} · {selected.direction} ·{" "}
                  {formatTime(selected.timestamp)}
                </p>
              </div>
              <Badge variant="outline">SECS-II</Badge>
            </div>
            <div className="tab-strip">
              {(["sml", "tree", "raw"] as const).map((tab) => (
                <button
                  key={tab}
                  className={view === tab ? "tab-active" : ""}
                  onClick={() => setView(tab)}
                >
                  {tab.toUpperCase()}
                </button>
              ))}
            </div>
            {view === "sml" && (
              <pre className="sml-view">{selected.sml || "<EMPTY>"}</pre>
            )}
            {view === "tree" && (
              <MessageTree value={selected.tree || selected.sml} />
            )}{" "}
            {view === "raw" && <RawInspector hex={selected.rawHex} />}
          </>
        ) : (
          <div className="empty-panel">Select a message to inspect it.</div>
        )}
      </div>
    </div>
  );
}

function MessageTree({ value }: { value: string }) {
  const rows = (value || "<EMPTY>")
    .split(/\r?\n/)
    .filter((line) => line.trim() && line.trim() !== ".");
  return (
    <div className="tree-view">
      {rows.map((line, index) => {
        const depth = Math.floor((line.length - line.trimStart().length) / 2);
        const token = line.trim().match(/^<?([A-Z][0-9]?|L)/)?.[1];
        return (
          <div
            className="tree-node"
            key={`${index}-${line}`}
            style={{ paddingLeft: `${10 + depth * 18}px` }}
          >
            <span className="tree-branch">{depth ? "└" : "●"}</span>
            {token && <Badge variant="outline">{token}</Badge>}
            <code>{line.trim()}</code>
          </div>
        );
      })}
    </div>
  );
}

function RawInspector({ hex }: { hex?: string }) {
  const bytes = (hex ?? "").replace(/\s/g, "").match(/.{1,2}/g) ?? [];
  if (!bytes.length)
    return (
      <div className="empty-panel inspector-empty">
        <Code2 className="size-5" />
        Raw bytes are unavailable for this generated simulator message.
      </div>
    );
  const rows = Array.from(
    { length: Math.ceil(bytes.length / 16) },
    (_, index) => bytes.slice(index * 16, index * 16 + 16),
  );
  return (
    <div className="hex-view">
      {rows.map((row, index) => (
        <div className="hex-row" key={index}>
          <span>{(index * 16).toString(16).padStart(6, "0")}</span>
          <code>{row.join(" ").padEnd(47, " ")}</code>
          <b>
            {row
              .map((item) => {
                const value = Number.parseInt(item, 16);
                return value >= 32 && value <= 126
                  ? String.fromCharCode(value)
                  : ".";
              })
              .join("")}
          </b>
        </div>
      ))}
    </div>
  );
}

function EventsPage({
  events,
  equipmentIDs,
  query,
  pageSize,
}: {
  events: CanonicalEvent[];
  equipmentIDs: string[];
  query: string;
  pageSize: PageSize;
}) {
  const names = [...new Set(events.map((event) => event.name))];
  const [name, setName] = useState("ALL");
  const [equipment, setEquipment] = useState("ALL");
  const [mode, setMode] = useState<"live" | "history">("live");
  const [sinceHours, setSinceHours] = useState(24);
  const [history, setHistory] = useState<{
    items: CanonicalEvent[];
    total: number;
  }>({ items: [], total: 0 });
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const filtered = events.filter(
    (event) =>
      (name === "ALL" || event.name === name) &&
      (equipment === "ALL" || event.equipmentId === equipment),
  );
  const pageItems =
    mode === "history" ? history.items : paginate(filtered, page, pageSize);
  const total = mode === "history" ? history.total : filtered.length;
  useEffect(() => setPage(1), [pageSize, mode, sinceHours, query]);
  useEffect(() => {
    if (mode !== "history") return;
    let active = true;
    setLoading(true);
    StudioService.QueryEventHistory({
      page,
      pageSize,
      equipmentId: equipment,
      search: query,
      direction: "",
      name,
      status: "",
      sinceHours,
    })
      .then((result) => {
        if (!active) return;
        setHistory({
          items: (result.items ?? []) as CanonicalEvent[],
          total: result.total,
        });
      })
      .catch(() => active && setHistory({ items: [], total: 0 }))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [mode, page, pageSize, equipment, name, query, sinceHours]);
  return (
    <div className="page-stack">
      <div className="section-heading !mt-0">
        <div>
          <h3>Canonical events</h3>
          <p>
            {loading
              ? "Loading SQLite…"
              : `${total} ${mode} events · SECS/GEM semantics normalized for downstream consumers.`}
          </p>
        </div>
        <div className="filter-bar">
          <div className="mode-switch">
            <button
              className={mode === "live" ? "active" : ""}
              onClick={() => setMode("live")}
            >
              Live
            </button>
            <button
              className={mode === "history" ? "active" : ""}
              onClick={() => setMode("history")}
            >
              History
            </button>
          </div>
          <select
            className="filter-select"
            value={equipment}
            onChange={(event) => {
              setEquipment(event.target.value);
              setPage(1);
            }}
          >
            <option value="ALL">ALL EQUIPMENT</option>
            {equipmentIDs.map((id) => (
              <option key={id}>{id}</option>
            ))}
          </select>
          <select
            className="filter-select"
            value={name}
            onChange={(event) => {
              setName(event.target.value);
              setPage(1);
            }}
          >
            <option>ALL</option>
            {names.map((item) => (
              <option key={item}>{item}</option>
            ))}
          </select>
          {mode === "history" && (
            <select
              className="filter-select"
              value={sinceHours}
              onChange={(event) => {
                setSinceHours(Number(event.target.value));
                setPage(1);
              }}
            >
              <option value={1}>1 HOUR</option>
              <option value={24}>24 HOURS</option>
              <option value={168}>7 DAYS</option>
              <option value={720}>30 DAYS</option>
              <option value={0}>ALL TIME</option>
            </select>
          )}
          <Badge>{total} events</Badge>
        </div>
      </div>
      <EventCards events={pageItems} />
      <Pager page={page} total={total} pageSize={pageSize} onChange={setPage} />
    </div>
  );
}

function CommandsPage({
  commands,
  equipmentIDs,
  query,
  pageSize,
}: {
  commands: EquipmentCommand[];
  equipmentIDs: string[];
  query: string;
  pageSize: PageSize;
}) {
  const names = [...new Set(commands.map((command) => command.name))];
  const statuses = [...new Set(commands.map((command) => command.status))];
  const [name, setName] = useState("ALL");
  const [status, setStatus] = useState("ALL");
  const [equipment, setEquipment] = useState("ALL");
  const [mode, setMode] = useState<"live" | "history">("live");
  const [sinceHours, setSinceHours] = useState(24);
  const [history, setHistory] = useState<{
    items: EquipmentCommand[];
    total: number;
  }>({ items: [], total: 0 });
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const filtered = commands.filter(
    (command) =>
      (name === "ALL" || command.name === name) &&
      (status === "ALL" || command.status === status) &&
      (equipment === "ALL" || command.equipmentId === equipment),
  );
  const pageItems =
    mode === "history" ? history.items : paginate(filtered, page, pageSize);
  const total = mode === "history" ? history.total : filtered.length;
  useEffect(() => setPage(1), [pageSize, mode, sinceHours, query]);
  useEffect(() => {
    if (mode !== "history") return;
    let active = true;
    setLoading(true);
    StudioService.QueryCommandHistory({
      page,
      pageSize,
      equipmentId: equipment,
      search: query,
      direction: "",
      name,
      status,
      sinceHours,
    })
      .then((result) => {
        if (!active) return;
        setHistory({
          items: (result.items ?? []) as EquipmentCommand[],
          total: result.total,
        });
      })
      .catch(() => active && setHistory({ items: [], total: 0 }))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [mode, page, pageSize, equipment, name, status, query, sinceHours]);
  return (
    <div className="page-stack">
      <div className="section-heading !mt-0">
        <div>
          <h3>Equipment commands</h3>
          <p>
            {loading
              ? "Loading SQLite…"
              : `${total} ${mode} commands · intent, execution state, and event correlation.`}
          </p>
        </div>
        <div className="filter-bar">
          <div className="mode-switch">
            <button
              className={mode === "live" ? "active" : ""}
              onClick={() => setMode("live")}
            >
              Live
            </button>
            <button
              className={mode === "history" ? "active" : ""}
              onClick={() => setMode("history")}
            >
              History
            </button>
          </div>
          <select
            className="filter-select"
            value={equipment}
            onChange={(event) => {
              setEquipment(event.target.value);
              setPage(1);
            }}
          >
            <option value="ALL">ALL EQUIPMENT</option>
            {equipmentIDs.map((id) => (
              <option key={id}>{id}</option>
            ))}
          </select>
          <select
            className="filter-select"
            value={name}
            onChange={(event) => {
              setName(event.target.value);
              setPage(1);
            }}
          >
            <option>ALL</option>
            {names.map((item) => (
              <option key={item}>{item}</option>
            ))}
          </select>
          <select
            className="filter-select"
            value={status}
            onChange={(event) => {
              setStatus(event.target.value);
              setPage(1);
            }}
          >
            <option>ALL</option>
            {statuses.map((item) => (
              <option key={item}>{item}</option>
            ))}
          </select>
          {mode === "history" && (
            <select
              className="filter-select"
              value={sinceHours}
              onChange={(event) => {
                setSinceHours(Number(event.target.value));
                setPage(1);
              }}
            >
              <option value={1}>1 HOUR</option>
              <option value={24}>24 HOURS</option>
              <option value={168}>7 DAYS</option>
              <option value={720}>30 DAYS</option>
              <option value={0}>ALL TIME</option>
            </select>
          )}
        </div>
      </div>
      <CommandCards commands={pageItems} />
      <Pager page={page} total={total} pageSize={pageSize} onChange={setPage} />
    </div>
  );
}
function EventCards({ events }: { events: CanonicalEvent[] }) {
  return (
    <div className="space-y-2">
      {events.length ? (
        events.map((event) => (
          <Card key={event.id} className="event-card">
            <CardContent className="flex items-start gap-3 p-4">
              <div className="event-icon">
                <Waves className="size-4" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <Badge>event</Badge>
                  <span className="text-sm font-medium">{event.name}</span>
                  {event.source.ceid ? (
                    <Badge variant="outline">CEID {event.source.ceid}</Badge>
                  ) : null}
                  <span className="ml-auto text-[10px] text-muted-foreground">
                    {formatTime(event.timestamp)}
                  </span>
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  {event.equipmentId} · correlation {event.correlationId} ·
                  caused by {event.causationId}
                </p>
                <pre className="event-json">
                  {JSON.stringify(event.data, null, 2)}
                </pre>
              </div>
            </CardContent>
          </Card>
        ))
      ) : (
        <div className="empty-panel">No canonical events yet.</div>
      )}
    </div>
  );
}

function CommandCards({ commands }: { commands: EquipmentCommand[] }) {
  return (
    <div className="space-y-2">
      {commands.length ? (
        commands.map((command) => (
          <Card key={command.id} className="event-card">
            <CardContent className="flex items-start gap-3 p-4">
              <div className="command-icon">
                <Play className="size-4" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <Badge variant="warning">command</Badge>
                  <span className="text-sm font-medium">{command.name}</span>
                  <Badge
                    variant={
                      command.status === "succeeded" ? "success" : "outline"
                    }
                  >
                    {command.status}
                  </Badge>
                  <span className="ml-auto text-[10px] text-muted-foreground">
                    {formatTime(command.createdAt)}
                  </span>
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  {command.equipmentId} · correlation {command.correlationId} ·
                  caused by {command.causationId}
                </p>
                <pre className="event-json">
                  {JSON.stringify(command.parameters, null, 2)}
                </pre>
              </div>
            </CardContent>
          </Card>
        ))
      ) : (
        <div className="empty-panel">No commands generated yet.</div>
      )}
    </div>
  );
}

function RouterPage({
  snapshot,
  query,
}: {
  snapshot: StudioSnapshot;
  query: string;
}) {
  const routes = (snapshot.routes ?? []).filter(
    (route) =>
      !query ||
      `${route.name} ${route.match.names?.join(" ")} ${route.match.equipment?.join(" ")} ${route.sinks?.join(" ")}`
        .toLowerCase()
        .includes(query),
  );
  const deliveries = (snapshot.deliveries ?? []).filter(
    (delivery) =>
      !query ||
      `${delivery.eventName} ${delivery.sink} ${delivery.status}`
        .toLowerCase()
        .includes(query),
  );
  return (
    <div className="page-stack">
      <div className="section-heading !mt-0">
        <div>
          <h3>Automation engine</h3>
          <p>
            Event and equipment glob selectors create commands; transport
            remains an execution detail.
          </p>
        </div>
        <Badge variant="success">active</Badge>
      </div>
      <div className="space-y-3">
        {(snapshot.automations ?? []).map((rule) => (
          <Card key={rule.name}>
            <CardContent className="route-rule">
              <div className="route-name">
                <Sparkles className="size-4" />
                <div>
                  <p title={rule.name}>{rule.name}</p>
                  <span>event → command</span>
                </div>
              </div>
              <div className="route-values">
                <Badge>{rule.trigger}</Badge>
                {rule.equipment?.map((pattern) => (
                  <Badge variant="outline" key={pattern}>
                    {pattern}
                  </Badge>
                ))}
              </div>
              <ChevronRight className="size-4 text-muted-foreground" />
              <div className="route-values">
                <Badge variant="warning">{rule.command}</Badge>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="section-heading">
        <div>
          <h3>Event router</h3>
          <p>
            Routes match canonical event names plus equipment IDs — never SECS
            message internals.
          </p>
        </div>
      </div>
      <div className="space-y-3">
        {routes.map((route) => (
          <Card key={route.name}>
            <CardContent className="route-rule">
              <div className="route-name">
                <GitBranch className="size-4" />
                <div>
                  <p title={route.name}>{route.name}</p>
                  <span>event + equipment glob</span>
                </div>
              </div>
              <div className="route-values">
                {route.match.names?.map((name) => (
                  <Badge key={name}>{name}</Badge>
                ))}
                {route.match.equipment?.map((pattern) => (
                  <Badge variant="outline" key={pattern}>
                    {pattern}
                  </Badge>
                ))}
              </div>
              <ChevronRight className="size-4 text-muted-foreground" />
              <div className="route-values">
                {route.sinks?.map((sink) => (
                  <Badge variant="outline" key={sink}>
                    {sink}
                  </Badge>
                ))}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="section-heading">
        <div>
          <h3>Recent deliveries</h3>
          <p>Sink dispatch history from the async runtime.</p>
        </div>
      </div>
      <div className="table-shell">
        <table>
          <thead>
            <tr>
              <th>Status</th>
              <th>Event</th>
              <th>Sink</th>
              <th>Time</th>
            </tr>
          </thead>
          <tbody>
            {[...deliveries]
              .reverse()
              .slice(0, 12)
              .map((delivery, index) => (
                <tr key={`${delivery.eventId}-${delivery.sink}-${index}`}>
                  <td>
                    <Badge variant="success">{delivery.status}</Badge>
                  </td>
                  <td>{delivery.eventName}</td>
                  <td>{delivery.sink}</td>
                  <td>{formatTime(delivery.timestamp)}</td>
                </tr>
              ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Simulator({
  devices,
  onEmit,
}: {
  devices: Device[];
  onEmit: (id: string, scenario: string) => void;
}) {
  const simulators = devices.filter((device) => device.driver === "simulator");
  return (
    <div className="page-stack">
      <div className="simulator-hero">
        <div className="simulator-rings">
          <Radio className="size-7" />
        </div>
        <div>
          <h2>Profile-driven Equipment Simulator</h2>
          <p>
            Scenarios can reverse-map canonical events into S6F11, or declare
            arbitrary inbound/outbound SxFy templates such as S5F1, S2F41 and
            S7F3.
          </p>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        {simulators.map((device) => (
          <Card key={device.id}>
            <CardHeader>
              <div className="flex items-center justify-between">
                <CardTitle>{device.id}</CardTitle>
                <Badge variant="success">simulator</Badge>
              </div>
              <CardDescription>
                {device.model} · {(device.scenarios ?? []).length} declared
                scenarios
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div className="space-y-2">
                {(device.scenarios ?? []).map((scenario) => (
                  <div className="scenario-row" key={scenario.id}>
                    <div>
                      <p>{scenario.displayName}</p>
                      <span>
                        {scenario.event
                          ? `${scenario.event} → Profile → S6F11`
                          : `${scenario.direction.toUpperCase()} · S${scenario.stream}F${scenario.function}`}
                      </span>
                    </div>
                    <Button
                      size="sm"
                      onClick={() => onEmit(device.id, scenario.id)}
                    >
                      <Play className="size-3.5" />
                      Emit
                    </Button>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        ))}
        {!simulators.length && (
          <Card className="col-span-2">
            <CardContent className="p-5 text-sm text-muted-foreground">
              No simulator-backed devices are configured. Use a real device's
              Host → Equipment panel to send Profile commands or raw SML.
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  );
}

function AlarmsPage({
  alarms,
  pageSize,
}: {
  alarms: AlarmRecord[];
  pageSize: PageSize;
}) {
  const [state, setState] = useState("ALL");
  const [severity, setSeverity] = useState("ALL");
  const [equipment, setEquipment] = useState("ALL");
  const [alarmID, setAlarmID] = useState("ALL");
  const [page, setPage] = useState(1);
  const equipmentIDs = [...new Set(alarms.map((alarm) => alarm.equipmentId))];
  const alarmIDs = [...new Set(alarms.map((alarm) => alarm.alarmId))];
  const filtered = alarms.filter(
    (alarm) =>
      (state === "ALL" || alarm.state === state) &&
      (severity === "ALL" || alarm.severity === severity) &&
      (equipment === "ALL" || alarm.equipmentId === equipment) &&
      (alarmID === "ALL" || alarm.alarmId === alarmID),
  );
  const active = alarms.filter((alarm) => alarm.state === "active").length;
  const critical = alarms.filter(
    (alarm) => alarm.state === "active" && alarm.severity === "critical",
  ).length;
  useEffect(() => setPage(1), [pageSize]);
  return (
    <div className="page-stack">
      <div className="section-heading !mt-0">
        <div>
          <h3>Equipment alarms</h3>
          <p>S5F1 alarm reports projected from the immutable event history.</p>
        </div>
        <div className="filter-bar">
          <select
            className="filter-select"
            value={equipment}
            onChange={(event) => {
              setEquipment(event.target.value);
              setPage(1);
            }}
          >
            <option value="ALL">ALL EQUIPMENT</option>
            {equipmentIDs.map((id) => (
              <option key={id}>{id}</option>
            ))}
          </select>
          <select
            className="filter-select"
            value={alarmID}
            onChange={(event) => {
              setAlarmID(event.target.value);
              setPage(1);
            }}
          >
            <option value="ALL">ALL ALARMS</option>
            {alarmIDs.map((id) => (
              <option key={id}>{id}</option>
            ))}
          </select>
          <select
            className="filter-select"
            value={state}
            onChange={(event) => {
              setState(event.target.value);
              setPage(1);
            }}
          >
            <option>ALL</option>
            <option value="active">ACTIVE</option>
            <option value="cleared">CLEARED</option>
          </select>
          <select
            className="filter-select"
            value={severity}
            onChange={(event) => {
              setSeverity(event.target.value);
              setPage(1);
            }}
          >
            <option>ALL</option>
            <option value="critical">CRITICAL</option>
            <option value="warning">WARNING</option>
            <option value="info">INFO</option>
          </select>
        </div>
      </div>
      <div className="alarm-summary">
        <div>
          <span>Active</span>
          <b>{active}</b>
        </div>
        <div>
          <span>Critical</span>
          <b>{critical}</b>
        </div>
        <div>
          <span>History</span>
          <b>{alarms.length}</b>
        </div>
        <div>
          <span>Visible</span>
          <b>{filtered.length}</b>
        </div>
      </div>
      <div className="table-shell">
        <table>
          <thead>
            <tr>
              <th>State</th>
              <th>Alarm</th>
              <th>Equipment</th>
              <th>Severity</th>
              <th>Raised</th>
              <th>Cleared</th>
            </tr>
          </thead>
          <tbody>
            {paginate(filtered, page, pageSize).map((alarm) => (
              <tr key={`${alarm.equipmentId}-${alarm.alarmId}`}>
                <td>
                  <Badge
                    variant={alarm.state === "active" ? "warning" : "outline"}
                  >
                    {alarm.state}
                  </Badge>
                </td>
                <td>
                  <div className="font-medium">
                    {alarm.alarmId} · {alarm.text}
                  </div>
                  <div className="text-[9px] text-muted-foreground">
                    code {alarm.code} · {alarm.correlationId}
                  </div>
                </td>
                <td>{alarm.equipmentId}</td>
                <td>{alarm.severity}</td>
                <td>{formatTime(alarm.raisedAt)}</td>
                <td>{alarm.clearedAt ? formatTime(alarm.clearedAt) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {filtered.length === 0 && (
          <div className="empty-panel !min-h-32">
            No alarms match the current filters.
          </div>
        )}
      </div>
      <Pager
        page={page}
        total={filtered.length}
        pageSize={pageSize}
        onChange={setPage}
      />
    </div>
  );
}

function deviceToDraft(device: Device): EquipmentDraft {
  return {
    key: device.id,
    id: device.id,
    badge: device.badge || compactDeviceLabel(device.id),
    name: device.name,
    profile: device.profile,
    adapter: device.adapter || "generic-gem",
    driver: device.driver || "simulator",
    autoConnect: device.autoConnect,
    protocol: device.protocol || "hsms-ss",
    mode: device.mode || "active",
    host: device.host,
    port: device.port,
    sessionId: device.sessionId,
  };
}

function ConfigDiffRow({
  label,
  values,
}: {
  label: string;
  values?: string[] | null;
}) {
  return (
    <div className="config-diff-row">
      <span>{label}</span>
      <div>
        {values?.length ? (
          values.map((value) => (
            <Badge key={value} variant="warning">
              {value}
            </Badge>
          ))
        ) : (
          <small>None</small>
        )}
      </div>
    </div>
  );
}

function SettingsPage({
  devices,
  storage,
  pageSize,
  onPageSizeChange,
  onDeviceOrderChange,
}: {
  devices: Device[];
  storage: StudioSnapshot["storage"];
  pageSize: PageSize;
  onPageSizeChange: (value: PageSize) => void;
  onDeviceOrderChange: (order: string[]) => void;
}) {
  const [theme, setTheme] = useState(
    () => localStorage.getItem("eapstudio.theme") ?? "dark",
  );
  const [profiles, setProfiles] = useState<AIProfile[]>(initialAIProfiles);
  const [defaultAIID, setDefaultAIID] = useState("local");
  const [selectedAIID, setSelectedAIID] = useState("local");
  const [saved, setSaved] = useState(false);
  const [equipment, setEquipment] = useState<EquipmentDraft[]>(() =>
    devices.map(deviceToDraft),
  );
  const [selectedEquipmentKey, setSelectedEquipmentKey] = useState(
    () => devices[0]?.id ?? "",
  );
  const [draggedEquipmentKey, setDraggedEquipmentKey] = useState("");
  const [equipmentPath, setEquipmentPath] = useState("");
  const [fileSinkPath, setFileSinkPath] = useState("");
  const [equipmentStatus, setEquipmentStatus] = useState("");
  const [configComparison, setConfigComparison] =
    useState<ConfigComparison | null>(null);
  const [configStatus, setConfigStatus] = useState("");
  const [restartRequired, setRestartRequired] = useState(false);
  const [retentionDays, setRetentionDays] = useState(() => {
    const value = Number(
      localStorage.getItem("eapstudio.historyRetentionDays"),
    );
    return [7, 30, 90, 365].includes(value) ? value : 90;
  });
  const [retentionStatus, setRetentionStatus] = useState("");
  const [permissionPolicy, setPermissionPolicy] = useState({
    mode: "ask",
    equipment: ["*"],
    commands: ["*"],
    ttlMinutes: 5,
  });
  const [permissionStatus, setPermissionStatus] = useState("");
  const selectedAI =
    profiles.find((profile) => profile.id === selectedAIID) ?? profiles[0];
  const selectedEquipment =
    equipment.find((item) => item.key === selectedEquipmentKey) ?? equipment[0];
  const liveDeviceOrder = devices.map((device) => device.id);
  const liveDeviceOrderSignature = liveDeviceOrder.join("\u0000");
  useEffect(() => {
    localStorage.setItem("eapstudio.theme", theme);
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => {
    StudioService.EquipmentConfigPath()
      .then(setEquipmentPath)
      .catch(() => undefined);
    StudioService.FileSinkPath()
      .then(setFileSinkPath)
      .catch(() => undefined);
    StudioService.CompareEquipmentConfig()
      .then(setConfigComparison)
      .catch(() => undefined);
    StudioService.ListAIProfiles()
      .then((stored) => {
        if (!stored?.length) return;
        const loaded = stored.map((profile) => ({
          id: profile.id,
          name: profile.name,
          provider: profile.provider as AIProfile["provider"],
          baseURL: profile.baseURL,
          model: profile.model,
          apiKey: "",
          hasApiKey: profile.hasApiKey,
          isDefault: profile.isDefault,
        }));
        const active = loaded.find((profile) => profile.isDefault) ?? loaded[0];
        setProfiles(loaded);
        setDefaultAIID(active.id);
        setSelectedAIID((current) =>
          loaded.some((profile) => profile.id === current)
            ? current
            : active.id,
        );
      })
      .catch(() => undefined);
    StudioService.PermissionPolicy()
      .then((value) =>
        setPermissionPolicy({
          mode: value.mode,
          equipment: value.equipment ?? ["*"],
          commands: value.commands ?? ["*"],
          ttlMinutes: value.ttlMinutes,
        }),
      )
      .catch(() => undefined);
  }, []);
  useEffect(() => {
    if (!equipment.length && devices.length) {
      const drafts = devices.map(deviceToDraft);
      setEquipment(drafts);
      setSelectedEquipmentKey(drafts[0].key);
    }
  }, [devices, equipment.length]);
  useEffect(() => {
    const rank = new Map(liveDeviceOrder.map((id, index) => [id, index]));
    setEquipment((current) => {
      const known = current
        .filter((item) => rank.has(item.key))
        .sort((left, right) => rank.get(left.key)! - rank.get(right.key)!);
      const drafts = current.filter((item) => !rank.has(item.key));
      const next = [...known, ...drafts];
      return next.map((item) => item.key).join("\u0000") ===
        current.map((item) => item.key).join("\u0000")
        ? current
        : next;
    });
  }, [liveDeviceOrderSignature]);

  const updateAI = (
    field: "name" | "provider" | "baseURL" | "model" | "apiKey",
    value: string,
  ) => {
    setSaved(false);
    setProfiles((current) =>
      current.map((profile) =>
        profile.id === selectedAIID
          ? ({ ...profile, [field]: value } as AIProfile)
          : profile,
      ),
    );
  };

  const addAI = () => {
    const id = `ai-${Date.now()}`;
    setProfiles((current) => [
      ...current,
      {
        id,
        name: "New AI endpoint",
        provider: "responses",
        baseURL: "https://api.openai.com/v1",
        model: "",
        apiKey: "",
      },
    ]);
    setSelectedAIID(id);
    setSaved(false);
  };

  const removeAI = () => {
    if (profiles.length <= 1) return;
    const remaining = profiles.filter((profile) => profile.id !== selectedAIID);
    const nextDefault =
      defaultAIID === selectedAIID ? remaining[0].id : defaultAIID;
    setProfiles(remaining);
    setDefaultAIID(nextDefault);
    setSelectedAIID(remaining[0].id);
    setSaved(false);
  };

  const saveAI = async () => {
    const active =
      profiles.find((profile) => profile.id === defaultAIID) ?? profiles[0];
    await StudioService.SaveAIProfiles(
      profiles.map((profile) => ({
        id: profile.id,
        name: profile.name,
        provider: profile.provider,
        baseURL: profile.baseURL,
        model: profile.model,
        apiKey: profile.apiKey,
        hasApiKey: profile.hasApiKey ?? false,
        isDefault: profile.id === active.id,
      })),
      active.id,
    );
    const stored = await StudioService.ListAIProfiles();
    setProfiles(
      (stored ?? []).map((profile) => ({
        id: profile.id,
        name: profile.name,
        provider: profile.provider as AIProfile["provider"],
        baseURL: profile.baseURL,
        model: profile.model,
        apiKey: "",
        hasApiKey: profile.hasApiKey,
        isDefault: profile.isDefault,
      })),
    );
    window.dispatchEvent(new Event("eapstudio:ai-config-changed"));
    setSaved(true);
  };

  const updateEquipment = (
    field: keyof Omit<EquipmentDraft, "key">,
    value: string | number | boolean,
  ) => {
    setEquipmentStatus("");
    setEquipment((current) =>
      current.map((item) =>
        item.key === selectedEquipmentKey ? { ...item, [field]: value } : item,
      ),
    );
  };
  const addEquipment = () => {
    const key = `new-${Date.now()}`;
    setEquipment((current) => [
      ...current,
      {
        key,
        id: `EQP-${String(current.length + 1).padStart(2, "0")}`,
        badge: String(current.length + 1).padStart(2, "0"),
        name: "New equipment",
        profile: "profiles/demo/etcher-x100.yaml",
        adapter: "generic-gem",
        driver: "simulator",
        autoConnect: false,
        protocol: "hsms-ss",
        mode: "active",
        host: "127.0.0.1",
        port: 5000 + current.length + 1,
        sessionId: 0,
      },
    ]);
    setSelectedEquipmentKey(key);
    setEquipmentStatus("");
  };
  const removeEquipment = () => {
    if (!selectedEquipment || equipment.length <= 1) return;
    const remaining = equipment.filter(
      (item) => item.key !== selectedEquipment.key,
    );
    setEquipment(remaining);
    setSelectedEquipmentKey(remaining[0].key);
    setEquipmentStatus("");
  };
  const dropEquipment = (targetKey: string, event: ReactDragEvent) => {
    event.preventDefault();
    const sourceKey =
      event.dataTransfer.getData("text/plain") || draggedEquipmentKey;
    if (!sourceKey || sourceKey === targetKey) return;
    const next = [...equipment];
    const sourceIndex = next.findIndex((item) => item.key === sourceKey);
    const targetIndex = next.findIndex((item) => item.key === targetKey);
    if (sourceIndex < 0 || targetIndex < 0) return;
    const [moved] = next.splice(sourceIndex, 1);
    next.splice(targetIndex, 0, moved);
    setEquipment(next);
    const liveIDs = new Set(devices.map((device) => device.id));
    onDeviceOrderChange(
      next.map((item) => item.key).filter((key) => liveIDs.has(key)),
    );
    setDraggedEquipmentKey("");
  };
  const saveEquipment = async () => {
    try {
      const result = await StudioService.SaveEquipmentConfig({
        devices: equipment.map((item) => ({
          id: item.id.trim(),
          badge: item.badge.trim(),
          name: item.name.trim(),
          profile: item.profile.trim(),
          adapter: item.adapter.trim(),
          driver: item.driver,
          autoConnect: item.autoConnect,
          connection: {
            protocol: item.protocol.trim(),
            mode: item.mode.trim(),
            host: item.host.trim(),
            port: Number(item.port),
            sessionId: Number(item.sessionId),
          },
        })),
      });
      setEquipmentPath(result.path);
      setConfigComparison(await StudioService.CompareEquipmentConfig());
      setEquipmentStatus(
        result.restartRequired ? "Saved · restart required" : "Saved",
      );
      setRestartRequired((current) => current || result.restartRequired);
    } catch (reason) {
      setEquipmentStatus(`Save failed · ${String(reason)}`);
    }
  };

  const mergePackagedDevices = async () => {
    setConfigStatus("Merging…");
    try {
      const result = await StudioService.MergePackagedDemoDevices();
      const comparison = await StudioService.CompareEquipmentConfig();
      setConfigComparison(comparison);
      setEquipmentPath(result.path);
      setConfigStatus(
        result.added?.length
          ? `Added and hot applied ${result.added.join(", ")}`
          : "Runtime already contains every packaged demo",
      );
      if (result.restartRequired) setRestartRequired(true);
    } catch (reason) {
      setConfigStatus(`Merge failed · ${String(reason)}`);
    }
  };

  const saveRetention = async () => {
    setRetentionStatus("Pruning history…");
    try {
      const result = await StudioService.ApplyHistoryRetention(retentionDays);
      localStorage.setItem(
        "eapstudio.historyRetentionDays",
        String(retentionDays),
      );
      const deleted =
        result.traceDeleted +
        result.eventDeleted +
        result.commandDeleted +
        result.alarmDeleted +
        result.copilotDeleted;
      setRetentionStatus(
        `Saved · removed ${deleted.toLocaleString()} records · ${formatBytes(result.databaseBytes)}`,
      );
    } catch (reason) {
      setRetentionStatus(`Retention failed · ${String(reason)}`);
    }
  };
  const savePermissionPolicy = async () => {
    try {
      await StudioService.SavePermissionPolicy(permissionPolicy);
      setPermissionStatus("Permission policy saved");
    } catch (reason) {
      setPermissionStatus(`Save failed · ${String(reason)}`);
    }
  };

  return (
    <div className="page-stack settings-page">
      <div className="section-heading !mt-0">
        <div>
          <h3>Studio settings</h3>
          <p>
            Appearance, pagination, AI endpoints, and the equipment inventory
            for this desktop runtime.
          </p>
        </div>
        <Badge variant="outline">{equipment.length} configured devices</Badge>
      </div>
      <div className="settings-grid">
        <Card>
          <CardHeader>
            <CardTitle>Interface</CardTitle>
            <CardDescription>
              Appearance and list density preferences.
            </CardDescription>
          </CardHeader>
          <CardContent className="settings-form">
            <label>
              Theme
              <select
                value={theme}
                onChange={(event) => setTheme(event.target.value)}
              >
                <option value="dark">Dark</option>
                <option value="light">Light</option>
                <option value="system">System</option>
              </select>
            </label>
            <label>
              Records per page
              <select
                value={pageSize}
                onChange={(event) =>
                  onPageSizeChange(Number(event.target.value) as PageSize)
                }
              >
                {pageSizes.map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </select>
            </label>
            <p>Applied to Messages, Events, Commands, and Alarms.</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Runtime safety</CardTitle>
            <CardDescription>
              Equipment write actions remain permission-gated.
            </CardDescription>
          </CardHeader>
          <CardContent className="settings-form">
            <div className="settings-safety">
              <ShieldCheck className="size-5" />
              <div>
                <b>Permission cards enabled</b>
                <p>
                  Profile commands, raw SML, and AI writes require an explicit
                  Allow once or Deny decision.
                </p>
              </div>
            </div>
            <label>
              Equipment write policy
              <select
                value={permissionPolicy.mode}
                onChange={(event) =>
                  setPermissionPolicy((value) => ({
                    ...value,
                    mode: event.target.value,
                  }))
                }
              >
                <option value="ask">Allowlist + ask every time</option>
                <option value="deny">Deny all equipment write actions</option>
              </select>
            </label>
            <label>
              Equipment allowlist (glob, comma separated)
              <input
                value={permissionPolicy.equipment.join(", ")}
                onChange={(event) =>
                  setPermissionPolicy((value) => ({
                    ...value,
                    equipment: event.target.value
                      .split(",")
                      .map((item) => item.trim())
                      .filter(Boolean),
                  }))
                }
                placeholder="ETCHER-*, AOI-01"
              />
            </label>
            <label>
              Command allowlist (glob, comma separated)
              <input
                value={permissionPolicy.commands.join(", ")}
                onChange={(event) =>
                  setPermissionPolicy((value) => ({
                    ...value,
                    commands: event.target.value
                      .split(",")
                      .map((item) => item.trim())
                      .filter(Boolean),
                  }))
                }
                placeholder="send.*, request.review"
              />
            </label>
            <label>
              Approval expiry
              <select
                value={permissionPolicy.ttlMinutes}
                onChange={(event) =>
                  setPermissionPolicy((value) => ({
                    ...value,
                    ttlMinutes: Number(event.target.value),
                  }))
                }
              >
                {[1, 5, 10, 30, 60].map((value) => (
                  <option key={value} value={value}>
                    {value} minutes
                  </option>
                ))}
              </select>
            </label>
            <Button
              size="sm"
              variant="outline"
              onClick={() => void savePermissionPolicy()}
            >
              Save permission policy
            </Button>
            {permissionStatus && <p>{permissionStatus}</p>}
            <p>
              API Keys are AES-256-GCM encrypted in SQLite. The encryption key
              is derived with SHA-256 from a per-install random secret and is
              never written to localStorage. <code>EAPSTUDIO_AI_API_KEY</code>{" "}
              remains an environment fallback.
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Configuration source</CardTitle>
            <CardDescription>
              Compare packaged demos with the writable runtime inventory.
            </CardDescription>
          </CardHeader>
          <CardContent className="settings-form">
            <div className="config-counts">
              <Badge variant="outline">
                packaged {configComparison?.packagedCount ?? "—"}
              </Badge>
              <Badge variant="outline">
                runtime {configComparison?.runtimeCount ?? "—"}
              </Badge>
            </div>
            <ConfigDiffRow label="Missing" values={configComparison?.missing} />
            <ConfigDiffRow label="Changed" values={configComparison?.changed} />
            <ConfigDiffRow
              label="Runtime only"
              values={configComparison?.extra}
            />
            <Button
              size="sm"
              variant="outline"
              disabled={!configComparison?.missing?.length}
              onClick={mergePackagedDevices}
            >
              Merge missing packaged demos
            </Button>
            {configStatus && <p>{configStatus}</p>}
            {restartRequired && (
              <div className="config-restart-notice">
                <RefreshCw className="size-4" />
                <div>
                  <b>Restart required by this configuration change</b>
                  <p>
                    This notice is reserved for changes that cannot be hot
                    applied.
                  </p>
                </div>
              </div>
            )}
            <p className="config-path">
              {configComparison?.runtimePath || equipmentPath}
            </p>
            <label>
              Canonical event File Sink
              <input value={fileSinkPath} readOnly />
            </label>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>History retention</CardTitle>
            <CardDescription>
              SQLite stores both fast-path traces and async events/commands for
              correlation and audit.
            </CardDescription>
          </CardHeader>
          <CardContent className="settings-form">
            <div className="history-stats">
              <span>{storage.traceCount.toLocaleString()} traces</span>
              <span>{storage.eventCount.toLocaleString()} events</span>
              <span>{storage.commandCount.toLocaleString()} commands</span>
              <b>{formatBytes(storage.databaseBytes)}</b>
            </div>
            <label>
              Keep history for
              <select
                value={retentionDays}
                onChange={(event) =>
                  setRetentionDays(Number(event.target.value))
                }
              >
                <option value={7}>7 days</option>
                <option value={30}>30 days</option>
                <option value={90}>90 days</option>
                <option value={365}>1 year</option>
              </select>
            </label>
            <Button size="sm" variant="outline" onClick={saveRetention}>
              Save policy and prune now
            </Button>
            <p>
              The saved policy runs again at app startup. Active alarms are
              always retained.
            </p>
            {retentionStatus && <p>{retentionStatus}</p>}
          </CardContent>
        </Card>
        <Card className="settings-ai">
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>AI configurations</CardTitle>
              <CardDescription>
                Maintain multiple endpoints and choose one runtime default.
              </CardDescription>
            </div>
            <Button size="sm" variant="outline" onClick={addAI}>
              <Plus className="size-3.5" />
              Add AI
            </Button>
          </CardHeader>
          <CardContent className="settings-ai-layout">
            <div className="ai-profile-list">
              {profiles.map((profile) => (
                <div
                  key={profile.id}
                  className={cn(
                    "ai-profile-row",
                    selectedAIID === profile.id && "ai-profile-row-active",
                  )}
                >
                  <input
                    type="radio"
                    name="default-ai"
                    aria-label={`Use ${profile.name} as default`}
                    checked={defaultAIID === profile.id}
                    onChange={() => {
                      setDefaultAIID(profile.id);
                      setSelectedAIID(profile.id);
                      setSaved(false);
                    }}
                  />
                  <button onClick={() => setSelectedAIID(profile.id)}>
                    <b>{profile.name}</b>
                    <span>
                      {profile.provider} · {profile.model || "no model"}
                    </span>
                  </button>
                  {defaultAIID === profile.id && (
                    <Badge variant="success">default</Badge>
                  )}
                </div>
              ))}
            </div>
            {selectedAI && (
              <div className="settings-form ai-profile-editor">
                <div className="ai-editor-heading">
                  <div>
                    <b>Edit configuration</b>
                    <span>{selectedAI.id}</span>
                  </div>
                  <Button
                    size="icon"
                    variant="outline"
                    disabled={profiles.length <= 1}
                    onClick={removeAI}
                    aria-label="Delete AI configuration"
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
                <label>
                  Name
                  <input
                    value={selectedAI.name}
                    onChange={(event) => updateAI("name", event.target.value)}
                  />
                </label>
                <label>
                  API adapter
                  <select
                    value={selectedAI.provider}
                    onChange={(event) =>
                      updateAI("provider", event.target.value)
                    }
                  >
                    <option value="local">
                      Local grounded (offline rules)
                    </option>
                    <option value="responses">Responses API</option>
                    <option value="chat">Chat Completions</option>
                  </select>
                </label>
                {selectedAI.provider === "local" && (
                  <div className="ai-local-explanation">
                    <b>Offline rule-based assistant</b>
                    <span>
                      Uses local Runtime snapshots, SQLite history, Profile,
                      Router and Automation configuration. It does not call a
                      model, use an API key, read attachments, or answer broad
                      general-knowledge questions.
                    </span>
                  </div>
                )}
                {selectedAI.provider !== "local" && (
                  <>
                    <label>
                      Base URL
                      <input
                        value={selectedAI.baseURL}
                        onChange={(event) =>
                          updateAI("baseURL", event.target.value)
                        }
                        placeholder="https://api.openai.com/v1"
                      />
                    </label>
                    <label>
                      Model
                      <input
                        value={selectedAI.model}
                        onChange={(event) =>
                          updateAI("model", event.target.value)
                        }
                        placeholder="gpt model"
                      />
                    </label>
                    <label>
                      API Key
                      <input
                        type="password"
                        autoComplete="off"
                        value={selectedAI.apiKey}
                        onChange={(event) =>
                          updateAI("apiKey", event.target.value)
                        }
                        placeholder={
                          selectedAI.hasApiKey
                            ? "Encrypted key stored"
                            : "Enter API key"
                        }
                        aria-describedby="api-key-storage-note"
                      />
                      <small id="api-key-storage-note">
                        {selectedAI.hasApiKey
                          ? "An encrypted key is stored. Leave empty to keep it, or enter a replacement."
                          : "Stored encrypted in SQLite after saving."}
                      </small>
                    </label>
                  </>
                )}
                <div className="ai-save-row">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={defaultAIID === selectedAI.id}
                    onClick={() => {
                      setDefaultAIID(selectedAI.id);
                      setSaved(false);
                    }}
                  >
                    {defaultAIID === selectedAI.id
                      ? "Current default"
                      : "Set as default"}
                  </Button>
                  <Button size="sm" onClick={saveAI}>
                    Save list and default
                  </Button>
                  {saved && <Badge variant="success">saved</Badge>}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
        <Card className="settings-devices">
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>Equipment configuration</CardTitle>
              <CardDescription>
                Edit runtime IDs, UI badges, Profiles, Adapters, and HSMS
                endpoints.
              </CardDescription>
            </div>
            <Button size="sm" variant="outline" onClick={addEquipment}>
              <Plus className="size-3.5" />
              Add equipment
            </Button>
          </CardHeader>
          <CardContent className="settings-equipment-layout">
            <div className="equipment-config-list">
              {equipment.map((item) => {
                const live = devices.find((device) => device.id === item.id);
                return (
                  <button
                    key={item.key}
                    draggable
                    onDragStart={(event) => {
                      event.dataTransfer.effectAllowed = "move";
                      event.dataTransfer.setData("text/plain", item.key);
                      setDraggedEquipmentKey(item.key);
                    }}
                    onDragEnd={() => setDraggedEquipmentKey("")}
                    onDragOver={(event) => {
                      event.preventDefault();
                      event.dataTransfer.dropEffect = "move";
                    }}
                    onDrop={(event) => dropEquipment(item.key, event)}
                    className={cn(
                      "equipment-config-row",
                      selectedEquipment?.key === item.key &&
                        "equipment-config-row-active",
                      draggedEquipmentKey === item.key &&
                        "equipment-config-row-dragging",
                    )}
                    onClick={() => setSelectedEquipmentKey(item.key)}
                  >
                    <span
                      className={cn(
                        "device-monogram",
                        live?.state === "selected" && "device-monogram-online",
                      )}
                    >
                      {item.badge || compactDeviceLabel(item.id)}
                    </span>
                    <span>
                      <b>{item.id || "Untitled"}</b>
                      <small>
                        {item.profile.split(/[\\/]/).pop()} · {item.adapter}
                      </small>
                    </span>
                    <Badge
                      variant={
                        live?.state === "selected" ? "success" : "outline"
                      }
                    >
                      {live?.state ?? "pending"}
                    </Badge>
                    <GripVertical className="equipment-config-drag-handle size-3.5" />
                  </button>
                );
              })}
            </div>
            {selectedEquipment && (
              <div className="settings-form equipment-editor">
                <div className="ai-editor-heading">
                  <div>
                    <b>Edit equipment</b>
                    <span>{selectedEquipment.key}</span>
                  </div>
                  <Button
                    size="icon"
                    variant="outline"
                    disabled={equipment.length <= 1}
                    onClick={removeEquipment}
                    aria-label="Delete equipment"
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
                <div className="equipment-field-grid">
                  <label>
                    Equipment ID
                    <input
                      value={selectedEquipment.id}
                      onChange={(event) =>
                        updateEquipment("id", event.target.value)
                      }
                    />
                  </label>
                  <label>
                    Badge
                    <input
                      maxLength={4}
                      value={selectedEquipment.badge}
                      onChange={(event) =>
                        updateEquipment("badge", event.target.value)
                      }
                    />
                  </label>
                  <label className="field-span-2">
                    Display name
                    <input
                      value={selectedEquipment.name}
                      onChange={(event) =>
                        updateEquipment("name", event.target.value)
                      }
                    />
                  </label>
                  <label className="field-span-2">
                    Profile path
                    <input
                      value={selectedEquipment.profile}
                      onChange={(event) =>
                        updateEquipment("profile", event.target.value)
                      }
                    />
                  </label>
                  <label>
                    Adapter
                    <input
                      value={selectedEquipment.adapter}
                      onChange={(event) =>
                        updateEquipment("adapter", event.target.value)
                      }
                      placeholder="generic-gem or registered adapter"
                    />
                  </label>
                  <label>
                    Driver
                    <select
                      value={selectedEquipment.driver}
                      onChange={(event) =>
                        updateEquipment("driver", event.target.value)
                      }
                    >
                      <option value="simulator">simulator</option>
                      <option value="go-secs">go-secs</option>
                    </select>
                  </label>
                  <label>
                    Protocol
                    <input
                      value={selectedEquipment.protocol}
                      onChange={(event) =>
                        updateEquipment("protocol", event.target.value)
                      }
                    />
                  </label>
                  <label>
                    Mode
                    <select
                      value={selectedEquipment.mode}
                      onChange={(event) =>
                        updateEquipment("mode", event.target.value)
                      }
                    >
                      <option value="active">active</option>
                      <option value="passive">passive</option>
                    </select>
                  </label>
                  <label>
                    Host
                    <input
                      value={selectedEquipment.host}
                      onChange={(event) =>
                        updateEquipment("host", event.target.value)
                      }
                    />
                  </label>
                  <label>
                    Port
                    <input
                      type="number"
                      value={selectedEquipment.port}
                      onChange={(event) =>
                        updateEquipment("port", Number(event.target.value))
                      }
                    />
                  </label>
                  <label>
                    Session ID
                    <input
                      type="number"
                      value={selectedEquipment.sessionId}
                      onChange={(event) =>
                        updateEquipment("sessionId", Number(event.target.value))
                      }
                    />
                  </label>
                  <label className="equipment-check">
                    <input
                      type="checkbox"
                      checked={selectedEquipment.autoConnect}
                      onChange={(event) =>
                        updateEquipment("autoConnect", event.target.checked)
                      }
                    />
                    Auto connect
                  </label>
                </div>
                <div className="equipment-save-row">
                  <Button size="sm" onClick={saveEquipment}>
                    Save devices.yaml
                  </Button>
                  {equipmentStatus && (
                    <Badge variant="warning">{equipmentStatus}</Badge>
                  )}
                </div>
                <p className="config-path">
                  {equipmentPath ||
                    "Runtime config path is resolved by the backend."}
                </p>
                <p>
                  Equipment and Profile changes are hot applied by replacing
                  only affected DeviceRuntime instances. Custom Adapter names
                  must still be registered by the backend.
                </p>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function ProfileWorkbench() {
  type ProfileItem = NonNullable<
    Awaited<ReturnType<typeof StudioService.ListProfiles>>
  >[number];
  const [profiles, setProfiles] = useState<ProfileItem[]>([]);
  const [selectedPath, setSelectedPath] = useState("");
  const [yaml, setYAML] = useState("");
  const [eventName, setEventName] = useState("material.arrived");
  const [eventData, setEventData] = useState(
    '{\n  "materialId": "MAT-001",\n  "recipeId": "ETCH-A",\n  "portId": "LP01"\n}',
  );
  const [status, setStatus] = useState("");
  const [preview, setPreview] = useState<unknown>(null);
  const loadList = () =>
    StudioService.ListProfiles().then((items) => {
      const values = items ?? [];
      setProfiles(values);
      if (!selectedPath && values.length) setSelectedPath(values[0].path);
    });
  useEffect(() => void loadList(), []);
  useEffect(() => {
    if (!selectedPath) return;
    StudioService.ReadProfile(selectedPath).then((value) => {
      setYAML(value.yaml);
      setStatus("");
      setPreview(null);
    });
  }, [selectedPath]);
  const validate = async () => {
    const value = await StudioService.ValidateProfileYAML(selectedPath, yaml);
    setStatus(
      value.valid
        ? `Valid · ${value.summary.vendor} ${value.summary.model}${value.warnings?.length ? ` · ${value.warnings.join(" · ")}` : ""}`
        : `Invalid · ${value.error}`,
    );
  };
  const save = async () => {
    try {
      const result = await StudioService.SaveProfile(selectedPath, yaml);
      setStatus(
        `Saved · hot reloaded ${result.reloadedDevices?.join(", ") || "no active devices"}`,
      );
      await loadList();
    } catch (reason) {
      setStatus(`Save failed · ${String(reason)}`);
    }
  };
  const runPreview = async () => {
    try {
      const data = JSON.parse(eventData) as Record<string, unknown>;
      setPreview(
        await StudioService.PreviewProfileEvent(yaml, eventName, data),
      );
      setStatus("Round-trip preview succeeded");
    } catch (reason) {
      setStatus(`Preview failed · ${String(reason)}`);
    }
  };
  return (
    <div className="page-stack workbench-page">
      <div className="section-heading !mt-0">
        <div>
          <h3>Profile / Adapter Workbench</h3>
          <p>
            Validate YAML, preview canonical ↔ SECS conversion, and hot reload
            affected runtimes.
          </p>
        </div>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => void validate()}>
            Validate
          </Button>
          <Button size="sm" onClick={() => void save()}>
            Save & hot reload
          </Button>
        </div>
      </div>
      <div className="workbench-grid">
        <Card className="workbench-list">
          <CardHeader>
            <CardTitle>Profiles</CardTitle>
            <CardDescription>
              {profiles.length} runtime definitions
            </CardDescription>
          </CardHeader>
          <CardContent>
            {profiles.map((item) => (
              <button
                key={item.path}
                className={cn(
                  "workbench-profile",
                  selectedPath === item.path && "active",
                )}
                onClick={() => setSelectedPath(item.path)}
              >
                <b>{item.name || item.path}</b>
                <span>
                  {item.vendor} {item.model} · {item.adapter}
                </span>
                <Badge variant={item.valid ? "success" : "warning"}>
                  {item.valid ? "valid" : "invalid"}
                </Badge>
              </button>
            ))}
          </CardContent>
        </Card>
        <Card className="workbench-editor">
          <CardHeader>
            <CardTitle>{selectedPath}</CardTitle>
            <CardDescription>Runtime YAML source</CardDescription>
          </CardHeader>
          <CardContent>
            <textarea
              value={yaml}
              onChange={(event) => setYAML(event.target.value)}
              spellCheck={false}
            />
          </CardContent>
        </Card>
        <Card className="workbench-preview">
          <CardHeader>
            <CardTitle>Round-trip preview</CardTitle>
            <CardDescription>
              Canonical event → Adapter.BuildEvent → Adapter.Parse
            </CardDescription>
          </CardHeader>
          <CardContent className="settings-form">
            <label>
              Event name
              <input
                value={eventName}
                onChange={(event) => setEventName(event.target.value)}
              />
            </label>
            <label>
              Canonical data
              <textarea
                value={eventData}
                onChange={(event) => setEventData(event.target.value)}
                spellCheck={false}
              />
            </label>
            <Button
              size="sm"
              variant="outline"
              onClick={() => void runPreview()}
            >
              <FlaskConical className="size-3.5" />
              Run preview
            </Button>
            {preview !== null && (
              <pre className="workbench-result">
                {JSON.stringify(preview, null, 2)}
              </pre>
            )}
          </CardContent>
        </Card>
      </div>
      {status && <div className="workbench-status">{status}</div>}
    </div>
  );
}

function readAttachment(file: File): Promise<CopilotAttachment> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () =>
      reject(reader.error ?? new Error("Unable to read attachment"));
    reader.onload = () =>
      resolve({
        name: file.name,
        mediaType: file.type || "application/octet-stream",
        dataURL: String(reader.result),
        size: file.size,
      });
    reader.readAsDataURL(file);
  });
}

function Copilot({
  devices,
  collapsed,
}: {
  devices: Device[];
  collapsed: boolean;
}) {
  type ChatMessage = {
    id: string;
    from: "user" | "assistant";
    text: string;
    attachments?: CopilotAttachment[];
    evidence?: string[];
    tools?: NonNullable<
      Awaited<ReturnType<typeof StudioService.AskCopilot>>["tools"]
    >;
    permission?: PermissionRequest;
    permissionStatus?: "pending" | "allowed" | "denied" | "expired";
  };
  type CopilotSession = NonNullable<
    Awaited<ReturnType<typeof StudioService.ListCopilotSessions>>
  >[number];
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [prompt, setPrompt] = useState("");
  const [attachments, setAttachments] = useState<CopilotAttachment[]>([]);
  const [attachmentError, setAttachmentError] = useState("");
  const [loading, setLoading] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [profiles, setProfiles] = useState<AIProfile[]>([]);
  const [activeAIID, setActiveAIID] = useState("");
  const [sessions, setSessions] = useState<CopilotSession[]>([]);
  const [activeSessionID, setActiveSessionID] = useState("");
  const [scope, setScope] = useState("ALL");
  const [sessionSearch, setSessionSearch] = useState("");
  const [showSessions, setShowSessions] = useState(false);
  const [deleteCandidate, setDeleteCandidate] = useState<CopilotSession | null>(
    null,
  );
  const [composerHeight, setComposerHeight] = useState(
    () =>
      Number(localStorage.getItem("eapstudio.copilotComposerHeight")) || 112,
  );
  const activeSessionRef = useRef("");
  const activeProfile = profiles.find((item) => item.id === activeAIID);

  useEffect(() => {
    localStorage.setItem(
      "eapstudio.copilotComposerHeight",
      String(composerHeight),
    );
  }, [composerHeight]);

  const beginComposerResize = (event: ReactPointerEvent) => {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = composerHeight;
    const move = (moveEvent: PointerEvent) => {
      const maximum = Math.max(160, Math.floor(window.innerHeight * 0.55));
      setComposerHeight(
        Math.min(
          maximum,
          Math.max(76, startHeight + startY - moveEvent.clientY),
        ),
      );
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
  };

  const refreshSessions = async (search = sessionSearch) => {
    const values = (await StudioService.ListCopilotSessions(search)) ?? [];
    setSessions(values);
    return values;
  };

  useEffect(() => {
    const refreshProfiles = () => {
      Promise.all([
        StudioService.ListAIProfiles(),
        StudioService.ActiveAIProfileID(),
      ]).then(([items, activeID]) => {
        setProfiles((items ?? []) as AIProfile[]);
        setActiveAIID(activeID);
      });
    };
    refreshProfiles();
    window.addEventListener("eapstudio:ai-config-changed", refreshProfiles);
    return () =>
      window.removeEventListener(
        "eapstudio:ai-config-changed",
        refreshProfiles,
      );
  }, []);

  useEffect(() => {
    let active = true;
    StudioService.ListCopilotSessions("").then(async (items) => {
      let values = items ?? [];
      if (!values.length) {
        values = [await StudioService.CreateCopilotSession("ALL")];
      }
      if (!active) return;
      setSessions(values);
      const first = values[0];
      setActiveSessionID(first.id);
      setScope(first.scope);
    });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!showSessions) return;
    const timer = window.setTimeout(() => {
      void refreshSessions(sessionSearch);
    }, 180);
    return () => window.clearTimeout(timer);
  }, [sessionSearch, showSessions]);

  useEffect(() => {
    activeSessionRef.current = activeSessionID;
    setLoading(false);
    if (!activeSessionID) {
      setMessages([]);
      return;
    }
    let active = true;
    setHistoryLoading(true);
    StudioService.CopilotHistory(activeSessionID)
      .then((history) => {
        if (!active || activeSessionRef.current !== activeSessionID) return;
        setMessages(
          (history ?? []).map((message) => ({
            id: message.id,
            from: message.role === "user" ? "user" : "assistant",
            text: message.text,
            attachments: (message.attachments ?? []) as CopilotAttachment[],
            evidence: message.evidence ?? [],
            permission: message.permission
              ? (message.permission as PermissionRequest)
              : undefined,
            permissionStatus: message.permission
              ? message.permissionStatus === "allowed" ||
                message.permissionStatus === "denied"
                ? message.permissionStatus
                : "expired"
              : undefined,
          })),
        );
      })
      .catch((reason) => setAttachmentError(`History: ${String(reason)}`))
      .finally(() => active && setHistoryLoading(false));
    return () => {
      active = false;
    };
  }, [activeSessionID]);

  useEffect(() => {
    const cancel = Events.On("studio:copilot-stream", (event) => {
      const value = event.data as CopilotStreamEvent;
      if (value.sessionId !== activeSessionRef.current) return;
      const messageID = `assistant-${value.requestId}`;
      setMessages((current) =>
        current.map((message) =>
          message.id === messageID
            ? {
                ...message,
                text: value.done
                  ? (value.reply?.answer ?? message.text)
                  : message.text + (value.delta ?? ""),
                evidence: value.done
                  ? (value.reply?.evidence ?? [])
                  : message.evidence,
                permission: value.done
                  ? (value.reply?.permission ?? undefined)
                  : message.permission,
                tools: value.done ? (value.reply?.tools ?? []) : message.tools,
                permissionStatus:
                  value.done && value.reply?.permission
                    ? "pending"
                    : message.permissionStatus,
              }
            : message,
        ),
      );
      if (value.done) {
        setLoading(false);
        void refreshSessions("");
      }
    });
    return () => cancel();
  }, []);

  const createSession = async () => {
    const created = await StudioService.CreateCopilotSession("ALL");
    setSessionSearch("");
    setSessions((current) => [created, ...current]);
    setActiveSessionID(created.id);
    setScope(created.scope);
    setShowSessions(false);
  };

  const selectSession = (session: CopilotSession) => {
    setActiveSessionID(session.id);
    setScope(session.scope);
    setShowSessions(false);
  };

  const deleteSession = async () => {
    if (!deleteCandidate) return;
    const deletingActive = deleteCandidate.id === activeSessionID;
    await StudioService.DeleteCopilotSession(deleteCandidate.id);
    setDeleteCandidate(null);
    let values = await refreshSessions("");
    if (!values.length) {
      values = [await StudioService.CreateCopilotSession("ALL")];
      setSessions(values);
    }
    if (deletingActive) {
      setActiveSessionID(values[0].id);
      setScope(values[0].scope);
    }
  };

  const changeScope = async (next: string) => {
    if (!activeSessionID || loading) return;
    await StudioService.UpdateCopilotSessionScope(activeSessionID, next);
    setScope(next);
    setSessions((current) =>
      current.map((item) =>
        item.id === activeSessionID ? { ...item, scope: next } : item,
      ),
    );
  };

  const activateProfile = async (id: string) => {
    await StudioService.ActivateAIProfile(id);
    setActiveAIID(id);
  };

  const selectFiles = async (files: FileList | null) => {
    if (!files?.length) return;
    setAttachmentError("");
    const selected = Array.from(files).slice(
      0,
      Math.max(0, 4 - attachments.length),
    );
    const oversized = selected.find((file) => file.size > 5 * 1024 * 1024);
    if (oversized) {
      setAttachmentError(`${oversized.name} exceeds the 5 MB limit.`);
      return;
    }
    try {
      const loaded = await Promise.all(selected.map(readAttachment));
      setAttachments((current) => [...current, ...loaded].slice(0, 4));
    } catch (reason) {
      setAttachmentError(String(reason));
    }
  };
  const ask = async (event: FormEvent) => {
    event.preventDefault();
    const question =
      prompt.trim() ||
      (attachments.length ? "请分析这些附件，并结合当前设备状态回答。" : "");
    if (!question || !activeSessionID) return;
    const outgoing = [...attachments];
    setPrompt("");
    setAttachments([]);
    setAttachmentError("");
    const requestID = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    setMessages((value) => [
      ...value,
      {
        id: `user-${requestID}`,
        from: "user",
        text: question,
        attachments: outgoing,
      },
      {
        id: `assistant-${requestID}`,
        from: "assistant",
        text: "",
      },
    ]);
    setLoading(true);
    try {
      await StudioService.AskCopilotStream(
        requestID,
        activeSessionID,
        question,
        scope,
        outgoing,
      );
    } catch (reason) {
      setMessages((value) =>
        value.map((message) =>
          message.id === `assistant-${requestID}`
            ? {
                ...message,
                text: `AI 调用失败：${String(reason)}`,
                evidence: [(activeProfile?.provider ?? "AI") + " adapter"],
              }
            : message,
        ),
      );
      setLoading(false);
    }
  };
  const resolvePermission = async (
    messageID: string,
    permissionID: string,
    allow: boolean,
  ) => {
    setMessages((value) =>
      value.map((message) =>
        message.id === messageID
          ? { ...message, permissionStatus: allow ? "allowed" : "denied" }
          : message,
      ),
    );
    const reply = await StudioService.ResolveAIAction(permissionID, allow);
    setMessages((value) => [
      ...value,
      {
        id: `assistant-${Date.now()}`,
        from: "assistant",
        text: reply.answer,
        evidence: reply.evidence ?? [],
      },
    ]);
  };
  return (
    <aside
      aria-hidden={collapsed}
      className={cn("copilot-panel", collapsed && "copilot-collapsed")}
    >
      <div className="copilot-header">
        <div className="copilot-icon">
          <Bot className="size-4" />
        </div>
        <div className="copilot-label">
          <h3>Equipment Copilot</h3>
          <p>{sessions.find((item) => item.id === activeSessionID)?.title}</p>
        </div>
        <select
          className="copilot-profile-select"
          value={activeAIID}
          onChange={(event) => void activateProfile(event.target.value)}
          aria-label="Active AI configuration"
          title="Switch AI configuration for this runtime"
        >
          {profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name}
            </option>
          ))}
        </select>
        <Button
          size="icon"
          variant="outline"
          className="copilot-header-action"
          onClick={() => setShowSessions((value) => !value)}
          title="Conversation history"
          aria-label="Conversation history"
        >
          <MessageSquareText className="size-3.5" />
        </Button>
        <Button
          size="icon"
          variant="outline"
          className="copilot-header-action"
          onClick={() => void createSession()}
          title="New conversation"
          aria-label="New conversation"
        >
          <Plus className="size-3.5" />
        </Button>
      </div>
      {showSessions && (
        <div className="copilot-sessions">
          <label className="copilot-session-search">
            <Search className="size-3.5" />
            <input
              value={sessionSearch}
              onChange={(event) => setSessionSearch(event.target.value)}
              placeholder="Search conversations…"
              autoFocus
            />
          </label>
          <div className="copilot-session-list">
            {sessions.length ? (
              sessions.map((session) => (
                <div
                  key={session.id}
                  className={cn(
                    "copilot-session-row",
                    session.id === activeSessionID &&
                      "copilot-session-row-active",
                  )}
                >
                  <button onClick={() => selectSession(session)}>
                    <b>{session.title}</b>
                    <span>
                      {session.scope === "ALL" ? "All studio" : session.scope} ·{" "}
                      {session.messageCount} messages
                    </span>
                  </button>
                  <Button
                    size="icon"
                    variant="ghost"
                    disabled={loading && session.id === activeSessionID}
                    onClick={() => setDeleteCandidate(session)}
                    title="Delete conversation"
                    aria-label={`Delete ${session.title}`}
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
              ))
            ) : (
              <p className="copilot-session-empty">No matching conversations</p>
            )}
          </div>
        </div>
      )}
      <div className="copilot-body">
        <label className="copilot-scope">
          <Search className="size-3.5" />
          <select
            value={scope}
            disabled={loading}
            onChange={(event) => void changeScope(event.target.value)}
          >
            <option value="ALL">All equipment & studio knowledge</option>
            {devices.map((item) => (
              <option key={item.id} value={item.id}>
                {item.id} · {item.name}
              </option>
            ))}
          </select>
        </label>
        <Conversation>
          <ConversationContent>
            {messages.length === 0 ? (
              <ConversationEmptyState
                icon={<Sparkles className="size-7 text-primary" />}
                title="Ask Copilot"
                description={
                  historyLoading
                    ? "Loading conversation history…"
                    : "Inspect state, explain messages, trace events, or attach equipment documents and images."
                }
              />
            ) : (
              messages.map((message) => (
                <AIMessage key={message.id} from={message.from}>
                  <MessageContent>
                    {message.attachments?.length ? (
                      <div className="message-attachments">
                        {message.attachments.map((item) => (
                          <div key={item.name}>
                            {item.mediaType.startsWith("image/") &&
                            item.dataURL ? (
                              <img src={item.dataURL} alt={item.name} />
                            ) : (
                              <FileText className="size-4" />
                            )}
                            <span>{item.name}</span>
                          </div>
                        ))}
                      </div>
                    ) : null}
                    <MessageResponse>{message.text || "…"}</MessageResponse>
                    {message.evidence?.length ? (
                      <Tool className="mt-3">
                        <ToolHeader title="Runtime context" />
                        <ToolContent>
                          {message.evidence.join(" · ")}
                        </ToolContent>
                      </Tool>
                    ) : null}
                    {message.tools?.map((tool, index) => (
                      <Tool className="mt-2" key={`${tool.name}-${index}`}>
                        <ToolHeader title={tool.name} />
                        <ToolContent>
                          <pre className="copilot-tool-result">
                            {JSON.stringify(tool.result, null, 2)}
                          </pre>
                        </ToolContent>
                      </Tool>
                    ))}
                    {message.permission && (
                      <PermissionCard
                        permission={message.permission}
                        status={message.permissionStatus ?? "pending"}
                        onAllow={() =>
                          resolvePermission(
                            message.id,
                            message.permission!.id,
                            true,
                          )
                        }
                        onDeny={() =>
                          resolvePermission(
                            message.id,
                            message.permission!.id,
                            false,
                          )
                        }
                      />
                    )}
                  </MessageContent>
                </AIMessage>
              ))
            )}
          </ConversationContent>
        </Conversation>
        <div
          className="copilot-composer-shell"
          style={{ height: composerHeight }}
        >
          <div
            className="copilot-composer-resizer"
            role="separator"
            aria-label="Resize message input"
            aria-orientation="horizontal"
            tabIndex={0}
            onPointerDown={beginComposerResize}
            onDoubleClick={() => setComposerHeight(112)}
            onKeyDown={(event) => {
              if (event.key === "ArrowUp")
                setComposerHeight((value) => Math.min(480, value + 16));
              if (event.key === "ArrowDown")
                setComposerHeight((value) => Math.max(76, value - 16));
            }}
          >
            <span />
          </div>
          {attachments.length > 0 && (
            <div className="attachment-tray">
              {attachments.map((item) => (
                <div key={item.name}>
                  {item.mediaType.startsWith("image/") ? (
                    <ImageIcon className="size-3.5" />
                  ) : (
                    <FileText className="size-3.5" />
                  )}
                  <span>{item.name}</span>
                  <button
                    onClick={() =>
                      setAttachments((current) =>
                        current.filter((value) => value !== item),
                      )
                    }
                    aria-label={`Remove ${item.name}`}
                  >
                    <X className="size-3" />
                  </button>
                </div>
              ))}
            </div>
          )}
          {attachmentError && (
            <p className="attachment-error">{attachmentError}</p>
          )}
          <PromptInput className="copilot-composer" onSubmit={ask}>
            <label className="attachment-button" title="Attach image or file">
              <Paperclip className="size-4" />
              <input
                type="file"
                multiple
                accept="image/*,.pdf,.txt,.md,.csv,.json,.xml,.log"
                onChange={(event) => {
                  void selectFiles(event.target.files);
                  event.target.value = "";
                }}
              />
            </label>
            <PromptInputTextarea
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              onKeyDown={(event) => {
                if (
                  event.key === "Enter" &&
                  !event.shiftKey &&
                  !event.nativeEvent.isComposing
                ) {
                  event.preventDefault();
                  event.currentTarget.form?.requestSubmit();
                }
              }}
              placeholder={
                scope === "ALL"
                  ? "Ask across all devices or general topics…"
                  : `Ask about ${scope}…`
              }
            />
            <PromptInputSubmit
              loading={loading}
              disabled={!prompt.trim() && attachments.length === 0}
            />
          </PromptInput>
        </div>
      </div>
      {deleteCandidate && (
        <div className="copilot-dialog-backdrop">
          <div className="copilot-dialog" role="alertdialog" aria-modal="true">
            <b>Delete conversation?</b>
            <p>
              “{deleteCandidate.title}” and all of its messages will be removed.
            </p>
            <div>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setDeleteCandidate(null)}
              >
                Cancel
              </Button>
              <Button size="sm" onClick={() => void deleteSession()}>
                Delete
              </Button>
            </div>
          </div>
        </div>
      )}
    </aside>
  );
}

function PermissionCard({
  permission,
  status,
  onAllow,
  onDeny,
}: {
  permission: PermissionRequest;
  status: "pending" | "allowed" | "denied" | "expired";
  onAllow: () => void;
  onDeny: () => void;
}) {
  return (
    <div className={cn("permission-card", `permission-${status}`)}>
      <div className="permission-heading">
        <ShieldCheck className="size-4" />
        <div>
          <b>Permission required</b>
          <span>{permission.tool}</span>
        </div>
        <Badge
          variant={
            status === "allowed"
              ? "success"
              : status === "denied" || status === "expired"
                ? "outline"
                : "warning"
          }
        >
          {status}
        </Badge>
      </div>
      <p>{permission.summary}</p>
      <pre>
        {JSON.stringify(
          {
            equipment: permission.equipmentId,
            command: permission.command,
            parameters: permission.parameters,
          },
          null,
          2,
        )}
      </pre>
      {Object.keys(permission.parameterDiff ?? {}).length > 0 && (
        <div className="permission-diff">
          <b>Parameter changes</b>
          {Object.entries(permission.parameterDiff ?? {}).map(
            ([key, change]) => (
              <span key={key}>
                {key}: {JSON.stringify(change?.before ?? null)} →{" "}
                {JSON.stringify(change?.after)}
              </span>
            ),
          )}
        </div>
      )}
      <div className="permission-risk">{permission.risk}</div>
      {status === "pending" && (
        <div className="permission-actions">
          <Button size="sm" variant="outline" onClick={onDeny}>
            <ShieldX className="size-3.5" />
            Deny
          </Button>
          <Button size="sm" onClick={onAllow}>
            <ShieldCheck className="size-3.5" />
            Allow once
          </Button>
        </div>
      )}
    </div>
  );
}

function paginate<T>(items: T[], page: number, pageSize: number) {
  return items.slice(
    (Math.max(1, page) - 1) * pageSize,
    Math.max(1, page) * pageSize,
  );
}
function Pager({
  page,
  total,
  pageSize,
  onChange,
}: {
  page: number;
  total: number;
  pageSize: number;
  onChange: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  return (
    <div className="pager">
      <Button
        size="sm"
        variant="outline"
        disabled={page <= 1}
        onClick={() => onChange(page - 1)}
      >
        Previous
      </Button>
      <span>
        Page {Math.min(page, pages)} of {pages} · {total} records
      </span>
      <Button
        size="sm"
        variant="outline"
        disabled={page >= pages}
        onClick={() => onChange(page + 1)}
      >
        Next
      </Button>
    </div>
  );
}

function MessageTable({ messages }: { messages: SecsMessage[] }) {
  return (
    <div className="table-shell">
      <table>
        <thead>
          <tr>
            <th>Direction</th>
            <th>Message</th>
            <th>Equipment</th>
            <th>System bytes</th>
            <th>Time</th>
          </tr>
        </thead>
        <tbody>
          {messages.map((message, index) => (
            <tr key={`${message.id}-${message.direction}-${index}`}>
              <td>
                <Badge
                  variant={message.direction === "IN" ? "success" : "default"}
                >
                  {message.direction}
                </Badge>
              </td>
              <td className="font-mono font-medium">
                S{message.stream}F{message.function}
                {message.wait ? " W" : ""}
              </td>
              <td>{message.equipmentId}</td>
              <td className="font-mono text-muted-foreground">
                {message.systemBytes}
              </td>
              <td>{formatTime(message.timestamp)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
function formatTime(value: string) {
  return new Date(value).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const exponent = Math.min(
    Math.floor(Math.log(value) / Math.log(1024)),
    units.length - 1,
  );
  return `${(value / 1024 ** exponent).toFixed(exponent ? 1 : 0)} ${units[exponent]}`;
}

export default App;

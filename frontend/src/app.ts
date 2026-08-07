import type {
  AgentServices,
  AgentSummary,
  AnnotationInput,
  Exposure,
  ServiceView,
  SessionResponse,
} from "./api.generated.js";

type PageID = "overview" | "hosts" | "services" | "security" | "operations";
type ServiceExposure = Exposure | "none";
type ServiceStateFilter = "all" | "attention" | "failed" | "web";
type SignalTone = "critical" | "warning" | "info" | "calm";

interface FleetState {
  agents: AgentSummary[];
  groups: AgentServices[];
  agentsLoaded: boolean;
  servicesLoaded: boolean;
}

interface ServiceEntry {
  agent: AgentServices["agent"];
  service: ServiceView;
}

interface WebTarget {
  agentID: string;
  agentName: string;
  serviceKey: string;
  serviceName: string;
  exposure: ServiceExposure;
  url: string;
  label: string;
}

interface Signal {
  tone: SignalTone;
  label: string;
  title: string;
  detail: string;
  agentID?: string;
}

interface EditingService {
  agentID: string;
  agentName: string;
  service: ServiceView;
}

const exposureLabel: Record<ServiceExposure, string> = {
  public: "公网",
  tailnet: "Tailnet",
  local: "本机",
  other: "待确认",
  none: "无监听",
};

const exposureOrder: Record<ServiceExposure, number> = {
  public: 0,
  other: 1,
  tailnet: 2,
  local: 3,
  none: 4,
};

const pageMeta: Record<PageID, { eyebrow: string; title: string }> = {
  overview: { eyebrow: "FLEET OVERVIEW", title: "全局状态" },
  hosts: { eyebrow: "HOST INVENTORY", title: "主机目录" },
  services: { eyebrow: "SERVICE CATALOG", title: "服务目录" },
  security: { eyebrow: "SECURITY POSTURE", title: "安全态势" },
  operations: { eyebrow: "CONTROLLED ACTIONS", title: "运维中心" },
};

const state: FleetState = {
  agents: [],
  groups: [],
  agentsLoaded: false,
  servicesLoaded: false,
};

let authed = false;
let csrfToken = "";
let activePage: PageID = "overview";
let refreshTimer: number | null = null;
let refreshing = false;
let editingService: EditingService | null = null;

function byID<T extends HTMLElement = HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`missing required element #${id}`);
  return node as T;
}

function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className = "",
  text?: string | number,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = String(text);
  return node;
}

function replaceChildren(node: HTMLElement, children: Node[]): void {
  node.replaceChildren(...children);
}

function isPageID(value: string): value is PageID {
  return value in pageMeta;
}

function safeWebURL(raw?: string): string | null {
  if (!raw) return null;
  try {
    const parsed = new URL(raw);
    const webScheme = parsed.protocol === "http:" || parsed.protocol === "https:";
    return webScheme && !parsed.username && !parsed.password ? parsed.href : null;
  } catch {
    return null;
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "unknown error";
}

function serviceExposure(service: ServiceView): ServiceExposure {
  return (service.ports ?? []).length ? service.maxExposure : "none";
}

function serviceDisplayName(service: ServiceView): string {
  return service.alias?.trim() || service.name;
}

function serviceRuntime(service: ServiceView): string {
  if (service.composeProject) {
    return `${service.composeProject}/${service.composeService || service.name}`;
  }
  return service.kind;
}

function isFailed(service: ServiceView): boolean {
  const status = service.status.toLowerCase();
  const health = (service.health ?? "").toLowerCase();
  return status.includes("failed") || status.includes("dead") || health === "unhealthy";
}

function needsAttention(service: ServiceView): boolean {
  return isFailed(service) || Boolean(service.unidentified);
}

function formatLastSeen(raw?: string): string {
  if (!raw) return "尚未同步";
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return raw;
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date);
}

async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const method = (options.method ?? "GET").toUpperCase();
  const headers = new Headers(options.headers);
  headers.set("Accept", "application/json");
  if (!["GET", "HEAD", "OPTIONS"].includes(method) && csrfToken) {
    headers.set("X-CSRF-Token", csrfToken);
  }
  const response = await fetch(path, { ...options, method, headers });
  if (response.status === 401) {
    authed = false;
    csrfToken = "";
    showLogin();
    throw new Error("unauthorized");
  }
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return (await response.json()) as T;
}

function setNotice(message: string | null): void {
  const notice = byID("notice");
  if (!message) {
    notice.classList.add("hidden");
    byID("noticeText").textContent = "";
    return;
  }
  byID("noticeText").textContent = message;
  notice.classList.remove("hidden");
}

function setRefreshing(value: boolean): void {
  refreshing = value;
  const button = byID<HTMLButtonElement>("refreshBtn");
  button.disabled = value;
  button.textContent = value ? "更新中" : "刷新";
  button.classList.toggle("is-busy", value);
}

function showLogin(): void {
  byID("login").classList.remove("hidden");
  byID("app").classList.add("hidden");
  setNotice(null);
  if (refreshTimer !== null) window.clearInterval(refreshTimer);
  refreshTimer = null;
}

function showDashboard(): void {
  byID("login").classList.add("hidden");
  byID("app").classList.remove("hidden");
  setPageFromHash();
  if (!state.agentsLoaded && !state.servicesLoaded) renderLoadingState();
}

function startRefresh(): void {
  if (refreshTimer !== null) window.clearInterval(refreshTimer);
  refreshTimer = window.setInterval(() => void refresh(), 15_000);
}

async function loadSession(): Promise<SessionResponse> {
  const session = await api<SessionResponse>("/api/session");
  authed = session.authed;
  csrfToken = session.csrfToken ?? "";
  return session;
}

async function boot(): Promise<void> {
  try {
    const session = await loadSession();
    if (!session.authed) {
      showLogin();
      return;
    }
    showDashboard();
    await refresh();
    startRefresh();
  } catch {
    showLogin();
  }
}

async function login(): Promise<void> {
  const button = byID<HTMLButtonElement>("loginBtn");
  byID("loginErr").classList.add("hidden");
  button.disabled = true;
  button.textContent = "验证中";
  try {
    await api<{ authed: boolean }>("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: byID<HTMLInputElement>("pw").value }),
    });
    const session = await loadSession();
    if (!session.authed) throw new Error("unauthorized");
    byID<HTMLInputElement>("pw").value = "";
    showDashboard();
    await refresh();
    startRefresh();
  } catch {
    byID("loginErr").classList.remove("hidden");
  } finally {
    button.disabled = false;
    button.textContent = "进入控制台";
  }
}

async function logout(): Promise<void> {
  try {
    await api<{ authed: boolean }>("/api/logout", { method: "POST" });
  } finally {
    authed = false;
    csrfToken = "";
    showLogin();
  }
}

async function refresh(): Promise<void> {
  if (!authed || refreshing) return;
  setRefreshing(true);
  const results = await Promise.allSettled([
    api<AgentSummary[]>("/api/agents"),
    api<AgentServices[]>("/api/services"),
  ]);
  if (!authed) {
    setRefreshing(false);
    return;
  }

  const failures: string[] = [];
  const agentsResult = results[0];
  const servicesResult = results[1];
  if (agentsResult.status === "fulfilled") {
    state.agents = agentsResult.value;
    state.agentsLoaded = true;
  } else {
    failures.push(`主机：${errorMessage(agentsResult.reason)}`);
  }
  if (servicesResult.status === "fulfilled") {
    state.groups = servicesResult.value;
    state.servicesLoaded = true;
  } else {
    failures.push(`服务：${errorMessage(servicesResult.reason)}`);
  }

  renderAll();
  updateConnectionState(failures.length > 0);
  if (failures.length) {
    const noUsableData = !state.agentsLoaded && !state.servicesLoaded;
    setNotice(noUsableData
      ? `控制台数据加载失败，请检查 Hub 状态后重试。${failures.join("；")}`
      : `部分数据更新失败，页面保留最近一次成功结果。${failures.join("；")}`);
  } else {
    setNotice(null);
  }
  setRefreshing(false);
}

function setPage(page: PageID, updateHash = true): void {
  activePage = page;
  document.querySelectorAll<HTMLElement>("[data-page-panel]").forEach((panel) => {
    panel.classList.toggle("hidden", panel.dataset.pagePanel !== page);
  });
  document.querySelectorAll<HTMLButtonElement>("[data-page]").forEach((item) => {
    const selected = item.dataset.page === page;
    item.classList.toggle("active", selected);
    if (selected) item.setAttribute("aria-current", "page");
    else item.removeAttribute("aria-current");
  });
  byID("pageEyebrow").textContent = pageMeta[page].eyebrow;
  byID("pageTitle").textContent = pageMeta[page].title;
  document.title = `Lodge · ${pageMeta[page].title}`;
  if (updateHash && window.location.hash !== `#${page}`) window.location.hash = page;
  if (page === "services") byID<HTMLInputElement>("serviceSearch").focus({ preventScroll: true });
  window.scrollTo({ top: 0, behavior: "instant" });
}

function setPageFromHash(): void {
  const requested = window.location.hash.slice(1);
  setPage(isPageID(requested) ? requested : "overview", false);
}

function updateConnectionState(partialFailure: boolean): void {
  const online = state.agents.filter((agent) => agent.online).length;
  const total = state.agents.length;
  const dot = byID("connectionDot");
  if (!state.agentsLoaded) {
    dot.className = "status-dot offline";
    byID("connectionText").textContent = "主机同步失败";
    byID("updated").textContent = `尝试于 ${new Date().toLocaleTimeString("zh-CN", { hour12: false })}`;
    const fleetStatus = byID("fleetStatus");
    fleetStatus.textContent = "主机数据暂不可用";
    fleetStatus.className = "fleet-status attention";
    return;
  }
  dot.className = `status-dot ${partialFailure ? "warning" : online === total && total > 0 ? "online" : "offline"}`;
  byID("connectionText").textContent = partialFailure
    ? "部分同步失败"
    : total > 0
      ? `${online}/${total} 节点在线`
      : "暂无节点";
  byID("updated").textContent = `更新于 ${new Date().toLocaleTimeString("zh-CN", { hour12: false })}`;
  const fleetStatus = byID("fleetStatus");
  fleetStatus.textContent = partialFailure
    ? "数据可能不是最新"
    : online === total && total > 0
      ? "舰队运行正常"
      : `${total - online} 个节点离线`;
  fleetStatus.className = `fleet-status ${partialFailure || online !== total ? "attention" : "healthy"}`;
}

function renderLoadingState(): void {
  const skeletons = Array.from({ length: 4 }, () => element("div", "metric-card skeleton"));
  replaceChildren(byID("overviewMetrics"), skeletons);
  replaceChildren(byID("riskSignals"), [emptyState("正在读取风险信号…", "loading")]);
  replaceChildren(byID("quickLinks"), [emptyState("正在发现 Web 入口…", "loading")]);
  replaceChildren(byID("hostPreview"), [emptyState("正在连接节点…", "loading")]);
}

function renderAll(): void {
  syncAgentFilter();
  renderOverview();
  renderHosts();
  renderServices();
  renderSecurity();
  renderOperations();
}

function allServiceEntries(): ServiceEntry[] {
  return state.groups.flatMap((group) =>
    group.services.map((service) => ({ agent: group.agent, service })),
  );
}

function serviceWebTargets(entry: ServiceEntry): WebTarget[] {
  const candidates = [entry.service.url, ...(entry.service.routes ?? []).map((route) => route.url)];
  const seen = new Set<string>();
  const targets: WebTarget[] = [];
  candidates.forEach((candidate) => {
    const url = safeWebURL(candidate);
    if (!url || seen.has(url)) return;
    seen.add(url);
    const parsed = new URL(url);
    const path = parsed.pathname === "/" ? "" : parsed.pathname.replace(/\/$/, "");
    targets.push({
      agentID: entry.agent.id,
      agentName: entry.agent.name,
      serviceKey: entry.service.key,
      serviceName: serviceDisplayName(entry.service),
      exposure: serviceExposure(entry.service),
      url,
      label: `${parsed.host}${path}`,
    });
  });
  return targets;
}

function allWebTargets(includeHidden = false): WebTarget[] {
  const seen = new Set<string>();
  return allServiceEntries()
    .filter((entry) => includeHidden || !entry.service.hidden)
    .flatMap((entry) => serviceWebTargets(entry))
    .filter((target) => {
      if (seen.has(target.url)) return false;
      seen.add(target.url);
      return true;
    });
}

function metricCard(label: string, value: string | number, detail: string, tone = ""): HTMLElement {
  const card = element("article", `metric-card ${tone}`.trim());
  card.append(
    element("span", "metric-label", label),
    element("strong", "metric-value", value),
    element("span", "metric-detail", detail),
  );
  return card;
}

function renderOverview(): void {
  const entries = allServiceEntries();
  const onlineHosts = state.agents.filter((agent) => agent.online).length;
  const publicServices = entries.filter((entry) => serviceExposure(entry.service) === "public").length;
  const failedServices = entries.filter((entry) => isFailed(entry.service)).length;
  const unidentified = entries.filter((entry) => entry.service.unidentified).length;
  const pressureHosts = state.agents.filter(
    (agent) => (agent.memUsedPct ?? 0) >= 80 || (agent.diskUsedPct ?? 0) >= 85,
  ).length;
  const attentionCount = state.agents.length - onlineHosts + failedServices + unidentified + pressureHosts;
  const targets = allWebTargets();

  replaceChildren(byID("overviewMetrics"), [
    state.agentsLoaded
      ? metricCard("在线主机", `${onlineHosts}/${state.agents.length}`, "Agent 实时连接", onlineHosts === state.agents.length ? "good" : "critical")
      : metricCard("在线主机", "N/A", "主机数据暂不可用", "critical"),
    state.servicesLoaded
      ? metricCard("工作负载", entries.length, "已发现并归因的服务")
      : metricCard("工作负载", "N/A", "服务数据暂不可用", "critical"),
    state.servicesLoaded
      ? metricCard("Web 入口", targets.length, "已发现的 http(s) 链接", "accent")
      : metricCard("Web 入口", "N/A", "服务数据暂不可用", "critical"),
    state.agentsLoaded || state.servicesLoaded
      ? metricCard("需要关注", attentionCount, attentionCount ? "离线、失败或资源压力" : "当前没有高优先级信号", attentionCount ? "warning" : "good")
      : metricCard("需要关注", "N/A", "等待数据恢复", "critical"),
  ]);

  if (state.agentsLoaded || state.servicesLoaded) renderRiskSignals();
  else replaceChildren(byID("riskSignals"), [emptyState("风险信号暂时不可用，请刷新重试。", "error")]);
  if (state.servicesLoaded) renderQuickLinks(targets);
  else replaceChildren(byID("quickLinks"), [emptyState("Web 入口数据暂时不可用。", "error")]);
  if (state.agentsLoaded) {
    const previewCards = state.agents.slice(0, 6).map((agent) => hostCard(agent, false));
    replaceChildren(byID("hostPreview"), previewCards.length ? previewCards : [emptyState("尚未纳管主机。")]);
  } else {
    replaceChildren(byID("hostPreview"), [emptyState("主机数据暂时不可用。", "error")]);
  }
}

function collectSignals(): Signal[] {
  const signals: Signal[] = [];
  state.agents.forEach((agent) => {
    if (!agent.online) {
      signals.push({
        tone: "critical",
        label: "离线",
        title: agent.name,
        detail: agent.lastError || `最后同步 ${formatLastSeen(agent.lastSeen)}`,
        agentID: agent.id,
      });
      return;
    }
    if ((agent.diskUsedPct ?? 0) >= 85) {
      signals.push({ tone: "warning", label: "磁盘", title: agent.name, detail: `磁盘使用率 ${agent.diskUsedPct}%`, agentID: agent.id });
    }
    if ((agent.memUsedPct ?? 0) >= 80) {
      signals.push({ tone: "warning", label: "内存", title: agent.name, detail: `内存使用率 ${agent.memUsedPct}%`, agentID: agent.id });
    }
  });
  allServiceEntries().forEach((entry) => {
    if (isFailed(entry.service)) {
      signals.push({
        tone: "critical",
        label: "失败",
        title: serviceDisplayName(entry.service),
        detail: `${entry.agent.name} · ${entry.service.status}`,
        agentID: entry.agent.id,
      });
    } else if (entry.service.unidentified) {
      signals.push({
        tone: "warning",
        label: "待归因",
        title: serviceDisplayName(entry.service),
        detail: `${entry.agent.name} · 需要确认来源`,
        agentID: entry.agent.id,
      });
    }
  });
  return signals;
}

function renderRiskSignals(): void {
  const signals = collectSignals();
  if (!signals.length) {
    replaceChildren(byID("riskSignals"), [emptyState("没有离线、失败或高资源压力信号。", "success")]);
    return;
  }
  const nodes: Node[] = signals.slice(0, 6).map((signal) => {
    const row = element("button", `signal-row ${signal.tone}`);
    row.type = "button";
    row.append(
      element("span", "signal-label", signal.label),
      element("span", "signal-copy"),
    );
    const copy = row.lastElementChild;
    if (copy) copy.append(element("strong", "", signal.title), element("small", "", signal.detail));
    row.addEventListener("click", () => {
      if (signal.agentID) byID<HTMLSelectElement>("agentFilter").value = signal.agentID;
      byID<HTMLSelectElement>("stateFilter").value = "attention";
      renderServices();
      setPage("services");
    });
    return row;
  });
  if (signals.length > nodes.length) nodes.push(element("p", "more-note", `另有 ${signals.length - nodes.length} 项需要关注`));
  replaceChildren(byID("riskSignals"), nodes);
}

function renderQuickLinks(targets: WebTarget[]): void {
  if (!targets.length) {
    replaceChildren(byID("quickLinks"), [emptyState("尚未发现可安全打开的 Web 入口。")]);
    return;
  }
  const nodes = targets.slice(0, 7).map((target) => {
    const link = element("a", "quick-link");
    link.href = target.url;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    link.append(
      element("span", `scope-mark ${target.exposure}`, exposureLabel[target.exposure]),
      element("span", "quick-link-copy"),
      element("span", "open-label", "打开"),
    );
    const copy = link.children.item(1);
    if (copy) copy.append(element("strong", "", target.serviceName), element("small", "", `${target.agentName} · ${target.label}`));
    return link;
  });
  replaceChildren(byID("quickLinks"), nodes);
}

function emptyState(message: string, tone = ""): HTMLElement {
  return element("div", `empty-state ${tone}`.trim(), message);
}

function usageTone(value?: number): string {
  if ((value ?? 0) >= 90) return "critical";
  if ((value ?? 0) >= 75) return "warning";
  return "normal";
}

function utilization(label: string, value?: number): HTMLElement {
  const actual = Math.max(0, Math.min(value ?? 0, 100));
  const row = element("div", "utilization");
  const heading = element("span", "utilization-label");
  heading.append(element("span", "", label), element("strong", "", value === undefined ? "N/A" : `${value}%`));
  const track = element("span", "utilization-track");
  const bar = element("span", `utilization-bar ${usageTone(value)}`);
  bar.style.width = `${actual}%`;
  track.append(bar);
  row.append(heading, track);
  return row;
}

function hostCard(agent: AgentSummary, expanded: boolean): HTMLElement {
  const card = element("article", `host-card ${agent.online ? "" : "offline"}`.trim());
  const heading = element("div", "host-heading");
  const identity = element("div", "host-identity");
  identity.append(
    element("span", `status-dot ${agent.online ? "online" : "offline"}`),
    element("span", "host-name"),
  );
  const name = identity.lastElementChild;
  if (name) name.append(element("strong", "", agent.name), element("small", "", agent.online ? "在线" : "离线"));
  const button = element("button", "inline-link", "查看服务");
  button.type = "button";
  button.addEventListener("click", () => {
    byID<HTMLSelectElement>("agentFilter").value = agent.id;
    byID<HTMLSelectElement>("stateFilter").value = "all";
    renderServices();
    setPage("services");
  });
  heading.append(identity, button);

  const summary = element("div", "host-summary");
  summary.append(
    element("span", "", `${agent.serviceCount} 服务`),
    element("span", agent.publicCount ? "critical-text" : "", `${agent.publicCount} 公网`),
    element("span", "", `${(agent.load1 ?? 0).toFixed(2)} / ${agent.cpus ?? "?"} 负载`),
  );
  card.append(heading, summary, utilization("内存", agent.memUsedPct), utilization("磁盘", agent.diskUsedPct));
  if (expanded) {
    const group = state.groups.find((item) => item.agent.id === agent.id);
    const details = element("div", "host-details");
    details.append(
      element("span", "", `Agent ${group?.agent.agentVersion || "未知版本"}`),
      element("span", "", `最后同步 ${formatLastSeen(agent.lastSeen || group?.agent.lastSeen)}`),
    );
    card.append(details);
  }
  if (!agent.online && agent.lastError) card.append(element("p", "host-error", agent.lastError.slice(0, 180)));
  return card;
}

function renderHosts(): void {
  if (!state.agentsLoaded) {
    replaceChildren(byID("hostDirectory"), [emptyState("主机数据暂时不可用，请刷新重试。", "error")]);
    return;
  }
  const sorted = [...state.agents].sort((left, right) => Number(right.online) - Number(left.online));
  const cards = sorted.map((agent) => hostCard(agent, true));
  replaceChildren(byID("hostDirectory"), cards.length ? cards : [emptyState("尚未纳管主机。")]);
}

function syncAgentFilter(): void {
  const select = byID<HTMLSelectElement>("agentFilter");
  const current = select.value;
  const options: HTMLOptionElement[] = [];
  const all = element("option", "", "全部主机");
  all.value = "all";
  options.push(all);
  state.groups.forEach((group) => {
    const option = element("option", "", group.agent.name);
    option.value = group.agent.id;
    options.push(option);
  });
  replaceChildren(select, options);
  select.value = options.some((option) => option.value === current) ? current : "all";
}

function serviceSearchText(entry: ServiceEntry): string {
  const ports = (entry.service.ports ?? []).map((port) => `${port.bind} ${port.port} ${port.proto}`).join(" ");
  const routes = serviceWebTargets(entry).map((target) => target.label).join(" ");
  return [
    entry.agent.name,
    entry.service.name,
    entry.service.alias,
    entry.service.notes,
    entry.service.status,
    entry.service.kind,
    entry.service.composeProject,
    entry.service.composeService,
    ports,
    routes,
  ].filter(Boolean).join(" ").toLocaleLowerCase("zh-CN");
}

function filteredServices(): ServiceEntry[] {
  const query = byID<HTMLInputElement>("serviceSearch").value.trim().toLocaleLowerCase("zh-CN");
  const agent = byID<HTMLSelectElement>("agentFilter").value;
  const exposure = byID<HTMLSelectElement>("exposureFilter").value;
  const serviceState = byID<HTMLSelectElement>("stateFilter").value as ServiceStateFilter;
  return allServiceEntries()
    .filter((entry) => agent === "all" || entry.agent.id === agent)
    .filter((entry) => exposure === "all" || serviceExposure(entry.service) === exposure)
    .filter((entry) => !query || serviceSearchText(entry).includes(query))
    .filter((entry) => {
      if (serviceState === "failed") return isFailed(entry.service);
      if (serviceState === "attention") return needsAttention(entry.service) || !entry.agent.online;
      if (serviceState === "web") return serviceWebTargets(entry).length > 0;
      return true;
    })
    .sort((left, right) => {
      const attentionDifference = Number(needsAttention(right.service)) - Number(needsAttention(left.service));
      if (attentionDifference) return attentionDifference;
      const exposureDifference = exposureOrder[serviceExposure(left.service)] - exposureOrder[serviceExposure(right.service)];
      if (exposureDifference) return exposureDifference;
      return serviceDisplayName(left.service).localeCompare(serviceDisplayName(right.service), "zh-CN");
    });
}

function renderServices(): void {
  if (!state.servicesLoaded) {
    byID("serviceResultCount").textContent = "N/A";
    replaceChildren(byID("serviceDirectory"), [emptyState("服务数据暂时不可用，请刷新重试。", "error")]);
    return;
  }
  const entries = filteredServices();
  byID("serviceResultCount").textContent = `${entries.length} / ${allServiceEntries().length} 项`;
  if (!entries.length) {
    replaceChildren(byID("serviceDirectory"), [emptyState("没有符合当前条件的服务。请调整搜索或筛选条件。")]);
    return;
  }

  const nodes: Node[] = [];
  const heading = element("div", "service-table-head");
  ["范围", "服务", "主机", "状态", "访问入口", ""].forEach((label) => heading.append(element("span", "", label)));
  nodes.push(heading);
  entries.forEach((entry) => nodes.push(serviceRow(entry)));
  replaceChildren(byID("serviceDirectory"), nodes);
}

function serviceRow(entry: ServiceEntry): HTMLElement {
  const exposure = serviceExposure(entry.service);
  const row = element("article", `service-row ${needsAttention(entry.service) ? "attention" : ""} ${entry.service.hidden ? "is-hidden" : ""}`.trim());
  row.append(element("span", `scope-badge ${exposure}`, exposureLabel[exposure]));

  const identity = element("div", "service-identity");
  identity.append(element("strong", "", serviceDisplayName(entry.service)));
  const runtime = serviceRuntime(entry.service);
  const identityMeta = [runtime, entry.service.alias ? entry.service.name : "", entry.service.hidden ? "已隐藏" : ""].filter(Boolean).join(" · ");
  identity.append(element("small", "", identityMeta));
  if (entry.service.notes) identity.append(element("span", "service-note", entry.service.notes));
  row.append(identity);

  const host = element("div", "service-host");
  host.append(element("strong", "", entry.agent.name), element("small", "", entry.agent.online ? "在线" : "离线"));
  row.append(host);

  const status = element("div", "service-state");
  const statusClass = isFailed(entry.service) ? "critical" : entry.service.unidentified ? "warning" : "healthy";
  status.append(element("span", `state-label ${statusClass}`, isFailed(entry.service) ? "失败" : entry.service.unidentified ? "待归因" : "正常"));
  status.append(element("small", "", entry.service.status || "未知状态"));
  row.append(status);

  const access = element("div", "service-access");
  const targets = serviceWebTargets(entry);
  if (targets.length) {
    const links = element("div", "access-links");
    targets.slice(0, 2).forEach((target) => {
      const link = element("a", "access-link", target.label);
      link.href = target.url;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      links.append(link);
    });
    if (targets.length > 2) links.append(element("span", "more-pill", `+${targets.length - 2}`));
    access.append(links);
  }
  const ports = entry.service.ports ?? [];
  if (ports.length) {
    const portList = element("div", "compact-ports");
    ports.slice(0, 2).forEach((port) => {
      const chip = element("span", "port-chip", `${port.port}/${port.proto}`);
      chip.title = `${port.bind}:${port.port} · ${exposureLabel[port.exposure]}`;
      portList.append(chip);
    });
    if (ports.length > 2) {
      const rest = element("span", "more-pill", `+${ports.length - 2} 端口`);
      rest.title = ports.map((port) => `${port.bind}:${port.port}/${port.proto}`).join("\n");
      portList.append(rest);
    }
    access.append(portList);
  }
  if (!targets.length && !ports.length) access.append(element("span", "muted-copy", "无入口"));
  row.append(access);

  const manage = element("button", "button button-quiet", "管理");
  manage.type = "button";
  manage.addEventListener("click", () => openAnnotation(entry));
  row.append(manage);
  return row;
}

function renderSecurity(): void {
  const entries = allServiceEntries();
  const publicEntries = entries.filter((entry) => serviceExposure(entry.service) === "public");
  const publicWithWeb = publicEntries.filter((entry) => serviceWebTargets(entry).length > 0);
  const unknown = entries.filter((entry) => entry.service.unidentified).length;
  const offline = state.agents.filter((agent) => !agent.online).length;
  replaceChildren(byID("securityMetrics"), [
    state.servicesLoaded
      ? metricCard("公网服务", publicEntries.length, "监听暴露范围为公网", publicEntries.length ? "warning" : "good")
      : metricCard("公网服务", "N/A", "服务数据暂不可用", "critical"),
    state.servicesLoaded
      ? metricCard("公网 Web", publicWithWeb.length, "发现 http(s) 链接")
      : metricCard("公网 Web", "N/A", "服务数据暂不可用", "critical"),
    state.servicesLoaded
      ? metricCard("待归因", unknown, "来源尚未确认", unknown ? "warning" : "good")
      : metricCard("待归因", "N/A", "服务数据暂不可用", "critical"),
    state.agentsLoaded
      ? metricCard("离线节点", offline, "无法取得实时状态", offline ? "critical" : "good")
      : metricCard("离线节点", "N/A", "主机数据暂不可用", "critical"),
  ]);

  if (!state.servicesLoaded) {
    replaceChildren(byID("publicSurface"), [emptyState("公网暴露面数据暂时不可用。", "error")]);
    return;
  }
  if (!publicEntries.length) {
    replaceChildren(byID("publicSurface"), [emptyState("当前没有检测到公网监听服务。", "success")]);
    return;
  }
  const rows = publicEntries.slice(0, 12).map((entry) => {
    const row = element("button", "surface-row");
    row.type = "button";
    row.append(
      element("span", "surface-name", serviceDisplayName(entry.service)),
      element("span", "surface-meta", `${entry.agent.name} · ${(entry.service.ports ?? []).length} 端口`),
      element("span", "surface-action", "查看"),
    );
    row.addEventListener("click", () => {
      byID<HTMLSelectElement>("agentFilter").value = entry.agent.id;
      byID<HTMLSelectElement>("exposureFilter").value = "public";
      byID<HTMLSelectElement>("stateFilter").value = "all";
      renderServices();
      setPage("services");
    });
    return row;
  });
  replaceChildren(byID("publicSurface"), rows);
}

function renderOperations(): void {
  if (!state.agentsLoaded) {
    replaceChildren(byID("syncSummary"), [emptyState("资产同步状态暂时不可用。", "error")]);
    return;
  }
  if (!state.agents.length) {
    replaceChildren(byID("syncSummary"), [emptyState("尚未纳管主机。")]);
    return;
  }
  const rows = state.agents.map((agent) => {
    const group = state.groups.find((item) => item.agent.id === agent.id);
    const row = element("div", "sync-row");
    row.append(
      element("span", `status-dot ${agent.online ? "online" : "offline"}`),
      element("span", "sync-copy"),
      element("span", "sync-version", group?.agent.agentVersion ? `Agent ${group.agent.agentVersion}` : "版本未知"),
    );
    const copy = row.children.item(1);
    if (copy) copy.append(element("strong", "", agent.name), element("small", "", formatLastSeen(agent.lastSeen || group?.agent.lastSeen)));
    return row;
  });
  replaceChildren(byID("syncSummary"), rows);
}

function openAnnotation(entry: ServiceEntry): void {
  editingService = { agentID: entry.agent.id, agentName: entry.agent.name, service: entry.service };
  byID("annotationTitle").textContent = `管理 ${serviceDisplayName(entry.service)}`;
  byID("annotationContext").textContent = `${entry.agent.name} · ${entry.service.key}`;
  byID<HTMLInputElement>("annotationAlias").value = entry.service.alias ?? "";
  byID<HTMLInputElement>("annotationURL").value = entry.service.url ?? "";
  byID<HTMLTextAreaElement>("annotationNotes").value = entry.service.notes ?? "";
  byID<HTMLInputElement>("annotationHidden").checked = entry.service.hidden;
  byID("annotationError").classList.add("hidden");
  byID<HTMLInputElement>("annotationURL").setCustomValidity("");
  byID<HTMLDialogElement>("annotationDialog").showModal();
  byID<HTMLInputElement>("annotationAlias").focus();
}

function closeAnnotation(): void {
  editingService = null;
  byID<HTMLDialogElement>("annotationDialog").close();
}

async function saveAnnotation(event: SubmitEvent): Promise<void> {
  event.preventDefault();
  if (!editingService) return;
  const alias = byID<HTMLInputElement>("annotationAlias").value.trim();
  const urlInput = byID<HTMLInputElement>("annotationURL");
  const url = urlInput.value.trim();
  const notes = byID<HTMLTextAreaElement>("annotationNotes").value.trim();
  if (url && !safeWebURL(url)) {
    urlInput.setCustomValidity("请输入不含用户名或密码的 http(s) 地址。");
    urlInput.reportValidity();
    return;
  }
  urlInput.setCustomValidity("");

  const input: AnnotationInput = {
    alias,
    url,
    notes,
    hidden: byID<HTMLInputElement>("annotationHidden").checked,
  };
  const saveButton = byID<HTMLButtonElement>("saveAnnotationBtn");
  const error = byID("annotationError");
  error.classList.add("hidden");
  saveButton.disabled = true;
  saveButton.textContent = "保存中";
  try {
    await api<{ ok: boolean }>(
      `/api/annotation?agent=${encodeURIComponent(editingService.agentID)}&key=${encodeURIComponent(editingService.service.key)}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      },
    );
    closeAnnotation();
    await refresh();
  } catch (saveError) {
    error.textContent = `保存失败：${errorMessage(saveError)}`;
    error.classList.remove("hidden");
  } finally {
    saveButton.disabled = false;
    saveButton.textContent = "保存配置";
  }
}

byID<HTMLButtonElement>("loginBtn").addEventListener("click", () => void login());
byID<HTMLButtonElement>("logoutBtn").addEventListener("click", () => void logout());
byID<HTMLButtonElement>("refreshBtn").addEventListener("click", () => void refresh());
byID<HTMLButtonElement>("dismissNotice").addEventListener("click", () => setNotice(null));
byID<HTMLInputElement>("pw").addEventListener("keydown", (event) => {
  if (event.key === "Enter") void login();
});

document.querySelectorAll<HTMLButtonElement>("[data-page]").forEach((item) => {
  item.addEventListener("click", () => {
    const page = item.dataset.page;
    if (page && isPageID(page)) setPage(page);
  });
});

document.querySelectorAll<HTMLButtonElement>("[data-navigate]").forEach((item) => {
  item.addEventListener("click", () => {
    const page = item.dataset.navigate;
    const serviceState = item.dataset.serviceState;
    if (serviceState) byID<HTMLSelectElement>("stateFilter").value = serviceState;
    if (page && isPageID(page)) {
      renderServices();
      setPage(page);
    }
  });
});

["serviceSearch", "agentFilter", "exposureFilter", "stateFilter"].forEach((id) => {
  byID<HTMLInputElement | HTMLSelectElement>(id).addEventListener("input", renderServices);
});

byID<HTMLFormElement>("annotationForm").addEventListener("submit", (event) => void saveAnnotation(event));
byID<HTMLButtonElement>("closeDialogBtn").addEventListener("click", closeAnnotation);
byID<HTMLButtonElement>("cancelDialogBtn").addEventListener("click", closeAnnotation);
byID<HTMLInputElement>("annotationURL").addEventListener("input", (event) => {
  (event.currentTarget as HTMLInputElement).setCustomValidity("");
});
byID<HTMLDialogElement>("annotationDialog").addEventListener("click", (event) => {
  if (event.target === event.currentTarget) closeAnnotation();
});
byID<HTMLDialogElement>("annotationDialog").addEventListener("close", () => {
  editingService = null;
});
window.addEventListener("hashchange", setPageFromHash);

void boot();

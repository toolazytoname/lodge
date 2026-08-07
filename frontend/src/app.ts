import type {
  AgentServices,
  AgentSummary,
  AnnotationInput,
  Exposure,
  ServiceView,
  SessionResponse,
} from "./api.generated.js";

type ServiceExposure = Exposure | "none";

const exposureLabel: Record<ServiceExposure, string> = {
  public: "公网",
  tailnet: "内网",
  local: "本机",
  other: "待定",
  none: "无监听",
};
const exposureOrder: Record<ServiceExposure, number> = { public: 0, other: 1, tailnet: 2, local: 3, none: 4 };

let authed = false;
let csrfToken = "";
let refreshTimer: number | null = null;

function byID<T extends HTMLElement = HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`missing required element #${id}`);
  return node as T;
}

const serviceExposure = (service: ServiceView): ServiceExposure =>
  (service.ports ?? []).length ? service.maxExposure : "none";

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

function showLogin(): void {
  byID("login").classList.remove("hidden");
  byID("dash").classList.add("hidden");
  byID("logoutBtn").classList.add("hidden");
  byID("updated").textContent = "";
  byID("updated").classList.remove("err");
  if (refreshTimer !== null) window.clearInterval(refreshTimer);
  refreshTimer = null;
}

function showDashboard(): void {
  byID("login").classList.add("hidden");
  byID("dash").classList.remove("hidden");
  byID("logoutBtn").classList.remove("hidden");
}

function startRefresh(): void {
  if (refreshTimer !== null) window.clearInterval(refreshTimer);
  refreshTimer = window.setInterval(() => void refresh(), 10_000);
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
  byID("loginErr").classList.add("hidden");
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
  if (!authed) return;
  try {
    const [agents, groups] = await Promise.all([
      api<AgentSummary[]>("/api/agents"),
      api<AgentServices[]>("/api/services"),
    ]);
    renderMachines(agents);
    renderServices(groups);
    byID("updated").textContent = `更新于 ${new Date().toLocaleTimeString("zh-CN")}`;
    byID("updated").classList.remove("err");
  } catch (error) {
    byID("updated").textContent = `加载失败：${errorMessage(error)}`;
    byID("updated").classList.add("err");
  }
}

function renderMachines(agents: AgentSummary[]): void {
  const cards = agents.map((agent) => {
    const card = element("article", "card");
    const name = element("div", "card-name");
    name.append(element("span", `dot ${agent.online ? "on" : ""}`), document.createTextNode(agent.name));
    card.append(name);

    const stats = element("div", "stats");
    const values: Array<[string, string]> = [
      ["负载", `${(agent.load1 ?? 0).toFixed(2)} / ${agent.cpus ?? "-"}`],
      ["内存", `${agent.memUsedPct ?? 0}%`],
      ["磁盘", `${agent.diskUsedPct ?? 0}%`],
      ["服务", String(agent.serviceCount)],
    ];
    values.forEach(([label, value], index) => {
      const row = element("span", "", `${label} `);
      row.append(element("b", "", value));
      if (index === 3 && agent.publicCount) row.append(element("span", "warn", ` · ${agent.publicCount} 公网`));
      stats.append(row);
    });
    card.append(stats);

    if (!agent.online && agent.lastError) {
      card.append(element("p", "offline-error err", agent.lastError.slice(0, 160)));
    }
    return card;
  });
  replaceChildren(byID("machines"), cards);
}

function renderServices(groups: AgentServices[]): void {
  const sections: Node[] = [];
  groups.forEach((group) => {
    const title = element("h2", "group-title", group.agent.name);
    if (!group.agent.online) title.append(element("span", "err", " · 离线"));
    sections.push(title);

    const container = element("div", "card section-card");
    const services = [...group.services].sort(
      (left, right) => exposureOrder[serviceExposure(left)] - exposureOrder[serviceExposure(right)],
    );
    if (!services.length) {
      container.append(element("div", "empty", "无服务"));
    } else {
      services.forEach((service) => container.append(serviceRow(group.agent.id, service)));
    }
    sections.push(container);
  });
  replaceChildren(byID("services"), sections);
}

function serviceRow(agentID: string, service: ServiceView): HTMLDivElement {
  const servicePorts = service.ports ?? [];
  const serviceRoutes = service.routes ?? [];
  const exposure = serviceExposure(service);
  const row = element("div", `svc-row ${exposure === "public" ? "public" : ""}`);
  row.append(element("span", `badge ${exposure}`, exposureLabel[exposure]));

  const name = element("span", "svc-name");
  const displayName = service.alias || service.name;
  const href = safeWebURL(service.url);
  if (href) {
    const link = element("a", "", `${displayName} ↗`);
    link.href = href;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    name.append(link);
  } else {
    name.textContent = displayName;
  }
  if (service.unidentified) name.append(element("span", "err", " ?"));
  const runtime = service.composeProject
    ? `compose · ${service.composeProject}/${service.composeService || service.name}`
    : service.kind;
  const state = service.status ? ` · ${service.status}` : "";
  row.append(name, element("span", "svc-kind", `${runtime}${state}`));

  const details = element("span", "service-details");
  if (serviceRoutes.length) {
    const routes = element("span", "routes");
    serviceRoutes.forEach((route) => {
      const routeHref = safeWebURL(route.url);
      if (routeHref) {
        const parsed = new URL(routeHref);
        const label = `${parsed.host}${parsed.pathname === "/" ? "" : parsed.pathname}`;
        const link = element("a", "route", `${label} ↗`);
        link.href = routeHref;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        if ((route.upstreams ?? []).length) link.title = `上游：${route.upstreams?.join(", ")}`;
        routes.append(link);
      } else {
        routes.append(element("span", "route muted", `${route.scheme} :${route.port}${route.path || "/"}`));
      }
    });
    details.append(routes);
  }
  const ports = element("span", "ports");
  servicePorts.forEach((port) => ports.append(element("span", "port", `${port.bind}:${port.port}`)));
  details.append(ports);
  row.append(details);

  const edit = element("button", "edit", "编辑");
  edit.type = "button";
  edit.title = "设置访问链接";
  edit.addEventListener("click", () => void editURL(agentID, service.key, service.url ?? ""));
  row.append(edit);
  return row;
}

async function editURL(agentID: string, serviceKey: string, current: string): Promise<void> {
  const next = window.prompt(`设置「${serviceKey}」的访问链接（留空清除）：`, current);
  if (next === null) return;
  const input = { url: next.trim() } satisfies AnnotationInput;
  try {
    await api<{ ok: boolean }>(
      `/api/annotation?agent=${encodeURIComponent(agentID)}&key=${encodeURIComponent(serviceKey)}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      },
    );
    await refresh();
  } catch (error) {
    byID("updated").textContent = `保存失败：${errorMessage(error)}`;
    byID("updated").classList.add("err");
  }
}

byID<HTMLButtonElement>("loginBtn").addEventListener("click", () => void login());
byID<HTMLButtonElement>("logoutBtn").addEventListener("click", () => void logout());
byID<HTMLInputElement>("pw").addEventListener("keydown", (event) => {
  if (event.key === "Enter") void login();
});

void boot();

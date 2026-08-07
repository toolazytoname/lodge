const exposureLabel = {
    public: "公网",
    tailnet: "Tailnet",
    local: "本机",
    other: "待确认",
    none: "无监听",
};
const exposureOrder = {
    public: 0,
    other: 1,
    tailnet: 2,
    local: 3,
    none: 4,
};
const pageMeta = {
    overview: { eyebrow: "FLEET OVERVIEW", title: "全局状态" },
    hosts: { eyebrow: "HOST INVENTORY", title: "主机目录" },
    services: { eyebrow: "SERVICE CATALOG", title: "服务目录" },
    security: { eyebrow: "SECURITY POSTURE", title: "安全态势" },
    operations: { eyebrow: "CONTROLLED ACTIONS", title: "运维中心" },
};
const state = {
    agents: [],
    groups: [],
    events: { events: [] },
    linkChecks: {
        checks: [],
        summary: { total: 0, reachable: 0, degraded: 0, unreachable: 0 },
    },
    operations: { operations: [] },
    agentsLoaded: false,
    servicesLoaded: false,
    eventsLoaded: false,
    eventsError: "",
    linkChecksLoaded: false,
    operationsLoaded: false,
    operationsError: "",
};
let authed = false;
let csrfToken = "";
let activePage = "overview";
let refreshTimer = null;
let refreshing = false;
let editingService = null;
let selectedHistoryAgent = "";
let selectedActionAgent = "";
let pendingAction = null;
let pendingDeployment = null;
let actionExecuting = false;
const historyByAgent = new Map();
const actionsByAgent = new Map();
const deploymentsByAgent = new Map();
const trackedDeploymentOperations = new Set();
function byID(id) {
    const node = document.getElementById(id);
    if (!node)
        throw new Error(`missing required element #${id}`);
    return node;
}
function element(tag, className = "", text) {
    const node = document.createElement(tag);
    if (className)
        node.className = className;
    if (text !== undefined)
        node.textContent = String(text);
    return node;
}
function replaceChildren(node, children) {
    node.replaceChildren(...children);
}
function isPageID(value) {
    return value in pageMeta;
}
function safeWebURL(raw) {
    if (!raw)
        return null;
    try {
        const parsed = new URL(raw);
        const webScheme = parsed.protocol === "http:" || parsed.protocol === "https:";
        return webScheme && !parsed.username && !parsed.password ? parsed.href : null;
    }
    catch {
        return null;
    }
}
class APIRequestError extends Error {
    status;
    payload;
    constructor(status, payload) {
        super(`HTTP ${status}`);
        this.status = status;
        this.payload = payload;
    }
}
function errorMessage(error) {
    return error instanceof Error ? error.message : "unknown error";
}
function serviceExposure(service) {
    return (service.ports ?? []).length ? service.maxExposure : "none";
}
function serviceDisplayName(service) {
    return service.alias?.trim() || service.name;
}
function serviceRuntime(service) {
    if (service.composeProject) {
        return `${service.composeProject}/${service.composeService || service.name}`;
    }
    return service.kind;
}
function isFailed(service) {
    const status = service.status.toLowerCase();
    const health = (service.health ?? "").toLowerCase();
    return status.includes("failed") || status.includes("dead") || health === "unhealthy";
}
function needsAttention(service) {
    return isFailed(service) || Boolean(service.unidentified);
}
function formatLastSeen(raw) {
    if (!raw)
        return "尚未同步";
    const date = new Date(raw);
    if (Number.isNaN(date.getTime()))
        return raw;
    return new Intl.DateTimeFormat("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
    }).format(date);
}
async function api(path, options = {}) {
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
    if (!response.ok) {
        let payload;
        try {
            payload = await response.json();
        }
        catch {
            payload = null;
        }
        throw new APIRequestError(response.status, payload);
    }
    return (await response.json());
}
function setNotice(message) {
    const notice = byID("notice");
    if (!message) {
        notice.classList.add("hidden");
        byID("noticeText").textContent = "";
        return;
    }
    byID("noticeText").textContent = message;
    notice.classList.remove("hidden");
}
function setRefreshing(value) {
    refreshing = value;
    const button = byID("refreshBtn");
    button.disabled = value;
    button.textContent = value ? "更新中" : "刷新";
    button.classList.toggle("is-busy", value);
}
function showLogin() {
    byID("login").classList.remove("hidden");
    byID("app").classList.add("hidden");
    setNotice(null);
    if (refreshTimer !== null)
        window.clearInterval(refreshTimer);
    refreshTimer = null;
}
function showDashboard() {
    byID("login").classList.add("hidden");
    byID("app").classList.remove("hidden");
    setPageFromHash();
    if (!state.agentsLoaded && !state.servicesLoaded)
        renderLoadingState();
}
function startRefresh() {
    if (refreshTimer !== null)
        window.clearInterval(refreshTimer);
    refreshTimer = window.setInterval(() => void refresh(), 15_000);
}
async function loadSession() {
    const session = await api("/api/session");
    authed = session.authed;
    csrfToken = session.csrfToken ?? "";
    return session;
}
async function boot() {
    try {
        const session = await loadSession();
        if (!session.authed) {
            showLogin();
            return;
        }
        showDashboard();
        await refresh();
        startRefresh();
    }
    catch {
        showLogin();
    }
}
async function login() {
    const button = byID("loginBtn");
    byID("loginErr").classList.add("hidden");
    button.disabled = true;
    button.textContent = "验证中";
    try {
        await api("/api/login", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ password: byID("pw").value }),
        });
        const session = await loadSession();
        if (!session.authed)
            throw new Error("unauthorized");
        byID("pw").value = "";
        showDashboard();
        await refresh();
        startRefresh();
    }
    catch {
        byID("loginErr").classList.remove("hidden");
    }
    finally {
        button.disabled = false;
        button.textContent = "进入控制台";
    }
}
async function logout() {
    try {
        await api("/api/logout", { method: "POST" });
    }
    finally {
        authed = false;
        csrfToken = "";
        showLogin();
    }
}
async function refresh() {
    if (!authed || refreshing)
        return;
    setRefreshing(true);
    const results = await Promise.allSettled([
        api("/api/agents"),
        api("/api/services"),
        api("/api/link-checks"),
        api("/api/events?limit=100"),
        api("/api/operations?limit=100"),
    ]);
    if (!authed) {
        setRefreshing(false);
        return;
    }
    const failures = [];
    const agentsResult = results[0];
    const servicesResult = results[1];
    const linkChecksResult = results[2];
    const eventsResult = results[3];
    const operationsResult = results[4];
    if (agentsResult.status === "fulfilled") {
        state.agents = agentsResult.value;
        state.agentsLoaded = true;
    }
    else {
        failures.push(`主机：${errorMessage(agentsResult.reason)}`);
    }
    if (servicesResult.status === "fulfilled") {
        state.groups = servicesResult.value;
        state.servicesLoaded = true;
    }
    else {
        failures.push(`服务：${errorMessage(servicesResult.reason)}`);
    }
    if (linkChecksResult.status === "fulfilled") {
        state.linkChecks = linkChecksResult.value;
        state.linkChecksLoaded = true;
    }
    else {
        failures.push(`入口检查：${errorMessage(linkChecksResult.reason)}`);
    }
    if (eventsResult.status === "fulfilled") {
        state.events = eventsResult.value;
        state.eventsLoaded = true;
        state.eventsError = "";
    }
    else {
        state.eventsError = errorMessage(eventsResult.reason);
        failures.push(`事件：${state.eventsError}`);
    }
    if (operationsResult.status === "fulfilled") {
        state.operations = operationsResult.value;
        state.operationsLoaded = true;
        state.operationsError = "";
    }
    else {
        state.operationsError = errorMessage(operationsResult.reason);
        failures.push(`操作记录：${state.operationsError}`);
    }
    renderAll();
    if (activePage === "security")
        void loadSelectedHistory(true);
    if (activePage === "operations")
        void loadSelectedCapabilities(true);
    updateConnectionState(failures.length > 0);
    if (failures.length) {
        const noUsableData = !state.agentsLoaded && !state.servicesLoaded;
        setNotice(noUsableData
            ? `控制台数据加载失败，请检查 Hub 状态后重试。${failures.join("；")}`
            : `部分数据更新失败，页面保留最近一次成功结果。${failures.join("；")}`);
    }
    else {
        setNotice(null);
    }
    setRefreshing(false);
}
function setPage(page, updateHash = true) {
    activePage = page;
    document.querySelectorAll("[data-page-panel]").forEach((panel) => {
        panel.classList.toggle("hidden", panel.dataset.pagePanel !== page);
    });
    document.querySelectorAll("[data-page]").forEach((item) => {
        const selected = item.dataset.page === page;
        item.classList.toggle("active", selected);
        if (selected)
            item.setAttribute("aria-current", "page");
        else
            item.removeAttribute("aria-current");
    });
    byID("pageEyebrow").textContent = pageMeta[page].eyebrow;
    byID("pageTitle").textContent = pageMeta[page].title;
    document.title = `Lodge · ${pageMeta[page].title}`;
    if (updateHash && window.location.hash !== `#${page}`)
        window.location.hash = page;
    if (page === "services")
        byID("serviceSearch").focus({ preventScroll: true });
    if (page === "security") {
        syncHistoryAgentSelect();
        renderHistory();
        void loadSelectedHistory(false);
    }
    if (page === "operations") {
        syncActionAgentSelect();
        renderOperations();
        void loadSelectedCapabilities(false);
    }
    window.scrollTo({ top: 0, behavior: "instant" });
}
function setPageFromHash() {
    const requested = window.location.hash.slice(1);
    setPage(isPageID(requested) ? requested : "overview", false);
}
function updateConnectionState(partialFailure) {
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
function renderLoadingState() {
    const skeletons = Array.from({ length: 4 }, () => element("div", "metric-card skeleton"));
    replaceChildren(byID("overviewMetrics"), skeletons);
    replaceChildren(byID("riskSignals"), [emptyState("正在读取风险信号…", "loading")]);
    replaceChildren(byID("quickLinks"), [emptyState("正在发现 Web 入口…", "loading")]);
    replaceChildren(byID("hostPreview"), [emptyState("正在连接节点…", "loading")]);
}
function renderAll() {
    syncAgentFilter();
    renderOverview();
    renderHosts();
    renderServices();
    renderSecurity();
    renderOperations();
}
function allServiceEntries() {
    return state.groups.flatMap((group) => group.services.map((service) => ({ agent: group.agent, service })));
}
function serviceWebTargets(entry) {
    const candidates = [entry.service.url, ...(entry.service.routes ?? []).map((route) => route.url)];
    const seen = new Set();
    const targets = [];
    candidates.forEach((candidate) => {
        const url = safeWebURL(candidate);
        if (!url || seen.has(url))
            return;
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
function allWebTargets(includeHidden = false) {
    const seen = new Set();
    return allServiceEntries()
        .filter((entry) => includeHidden || !entry.service.hidden)
        .flatMap((entry) => serviceWebTargets(entry))
        .filter((target) => {
        if (seen.has(target.url))
            return false;
        seen.add(target.url);
        return true;
    });
}
function linkCheckFor(target) {
    return state.linkChecks.checks.find((check) => check.agentId === target.agentID
        && check.serviceKey === target.serviceKey
        && safeWebURL(check.url) === target.url);
}
function linkCheckLabel(check) {
    if (!check)
        return "未检查";
    if (check.state === "reachable")
        return "Hub 可达";
    if (check.state === "degraded")
        return `HTTP ${check.httpStatus ?? "5xx"}`;
    return "Hub 不可达";
}
function linkCheckTitle(check) {
    if (!check)
        return "尚未从 Hub 发起主动检查";
    const status = check.httpStatus ? `HTTP ${check.httpStatus}` : check.errorKind || "network";
    return `Hub 主动检查：${status} · ${check.latencyMs}ms · ${formatLastSeen(check.checkedAt)}`;
}
function webLinkMetricDetail() {
    const summary = state.linkChecks.summary;
    if (!state.linkChecksLoaded || summary.total === 0) {
        return { detail: "尚未从 Hub 主动检查", tone: "accent" };
    }
    const detail = `${summary.reachable}/${summary.total} Hub 可达`;
    if (summary.unreachable > 0)
        return { detail, tone: "critical" };
    if (summary.degraded > 0)
        return { detail, tone: "warning" };
    return { detail, tone: "good" };
}
function metricCard(label, value, detail, tone = "") {
    const card = element("article", `metric-card ${tone}`.trim());
    card.append(element("span", "metric-label", label), element("strong", "metric-value", value), element("span", "metric-detail", detail));
    return card;
}
function renderOverview() {
    const entries = allServiceEntries();
    const onlineHosts = state.agents.filter((agent) => agent.online).length;
    const publicServices = entries.filter((entry) => serviceExposure(entry.service) === "public").length;
    const failedServices = entries.filter((entry) => isFailed(entry.service)).length;
    const unidentified = entries.filter((entry) => entry.service.unidentified).length;
    const pressureHosts = state.agents.filter((agent) => (agent.memUsedPct ?? 0) >= 80 || (agent.diskUsedPct ?? 0) >= 85).length;
    const attentionCount = state.agents.length - onlineHosts + failedServices + unidentified + pressureHosts;
    const targets = allWebTargets();
    const linkMetric = webLinkMetricDetail();
    replaceChildren(byID("overviewMetrics"), [
        state.agentsLoaded
            ? metricCard("在线主机", `${onlineHosts}/${state.agents.length}`, "Agent 实时连接", onlineHosts === state.agents.length ? "good" : "critical")
            : metricCard("在线主机", "N/A", "主机数据暂不可用", "critical"),
        state.servicesLoaded
            ? metricCard("工作负载", entries.length, "已发现并归因的服务")
            : metricCard("工作负载", "N/A", "服务数据暂不可用", "critical"),
        state.servicesLoaded
            ? metricCard("Web 入口", targets.length, linkMetric.detail, linkMetric.tone)
            : metricCard("Web 入口", "N/A", "服务数据暂不可用", "critical"),
        state.agentsLoaded || state.servicesLoaded
            ? metricCard("需要关注", attentionCount, attentionCount ? "离线、失败或资源压力" : "当前没有高优先级信号", attentionCount ? "warning" : "good")
            : metricCard("需要关注", "N/A", "等待数据恢复", "critical"),
    ]);
    if (state.agentsLoaded || state.servicesLoaded)
        renderRiskSignals();
    else
        replaceChildren(byID("riskSignals"), [emptyState("风险信号暂时不可用，请刷新重试。", "error")]);
    if (state.servicesLoaded)
        renderQuickLinks(targets);
    else
        replaceChildren(byID("quickLinks"), [emptyState("Web 入口数据暂时不可用。", "error")]);
    if (state.agentsLoaded) {
        const previewCards = state.agents.slice(0, 6).map((agent) => hostCard(agent, false));
        replaceChildren(byID("hostPreview"), previewCards.length ? previewCards : [emptyState("尚未纳管主机。")]);
    }
    else {
        replaceChildren(byID("hostPreview"), [emptyState("主机数据暂时不可用。", "error")]);
    }
}
function collectSignals() {
    const signals = [];
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
        }
        else if (entry.service.unidentified) {
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
function renderRiskSignals() {
    const signals = collectSignals();
    if (!signals.length) {
        replaceChildren(byID("riskSignals"), [emptyState("没有离线、失败或高资源压力信号。", "success")]);
        return;
    }
    const nodes = signals.slice(0, 6).map((signal) => {
        const row = element("button", `signal-row ${signal.tone}`);
        row.type = "button";
        row.append(element("span", "signal-label", signal.label), element("span", "signal-copy"));
        const copy = row.lastElementChild;
        if (copy)
            copy.append(element("strong", "", signal.title), element("small", "", signal.detail));
        row.addEventListener("click", () => {
            if (signal.agentID)
                byID("agentFilter").value = signal.agentID;
            byID("stateFilter").value = "attention";
            renderServices();
            setPage("services");
        });
        return row;
    });
    if (signals.length > nodes.length)
        nodes.push(element("p", "more-note", `另有 ${signals.length - nodes.length} 项需要关注`));
    replaceChildren(byID("riskSignals"), nodes);
}
function renderQuickLinks(targets) {
    if (!targets.length) {
        replaceChildren(byID("quickLinks"), [emptyState("尚未发现可安全打开的 Web 入口。")]);
        return;
    }
    const nodes = targets.slice(0, 7).map((target) => {
        const check = linkCheckFor(target);
        const link = element("a", "quick-link");
        link.href = target.url;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        link.append(element("span", `scope-mark ${target.exposure}`, exposureLabel[target.exposure]), element("span", "quick-link-copy"), element("span", `open-label ${check?.state ?? "unknown"}`, linkCheckLabel(check)));
        link.title = linkCheckTitle(check);
        const copy = link.children.item(1);
        if (copy)
            copy.append(element("strong", "", target.serviceName), element("small", "", `${target.agentName} · ${target.label}`));
        return link;
    });
    replaceChildren(byID("quickLinks"), nodes);
}
function emptyState(message, tone = "") {
    return element("div", `empty-state ${tone}`.trim(), message);
}
function usageTone(value) {
    if ((value ?? 0) >= 90)
        return "critical";
    if ((value ?? 0) >= 75)
        return "warning";
    return "normal";
}
function utilization(label, value) {
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
function hostCard(agent, expanded) {
    const card = element("article", `host-card ${agent.online ? "" : "offline"}`.trim());
    const heading = element("div", "host-heading");
    const identity = element("div", "host-identity");
    identity.append(element("span", `status-dot ${agent.online ? "online" : "offline"}`), element("span", "host-name"));
    const name = identity.lastElementChild;
    if (name)
        name.append(element("strong", "", agent.name), element("small", "", agent.online ? "在线" : "离线"));
    const button = element("button", "inline-link", "查看服务");
    button.type = "button";
    button.addEventListener("click", () => {
        byID("agentFilter").value = agent.id;
        byID("stateFilter").value = "all";
        renderServices();
        setPage("services");
    });
    heading.append(identity, button);
    const summary = element("div", "host-summary");
    summary.append(element("span", "", `${agent.serviceCount} 服务`), element("span", agent.publicCount ? "critical-text" : "", `${agent.publicCount} 公网`), element("span", "", `${(agent.load1 ?? 0).toFixed(2)} / ${agent.cpus ?? "?"} 负载`));
    card.append(heading, summary, utilization("内存", agent.memUsedPct), utilization("磁盘", agent.diskUsedPct));
    if (expanded) {
        const group = state.groups.find((item) => item.agent.id === agent.id);
        const details = element("div", "host-details");
        details.append(element("span", "", `Agent ${group?.agent.agentVersion || "未知版本"}`), element("span", "", `最后同步 ${formatLastSeen(agent.lastSeen || group?.agent.lastSeen)}`));
        card.append(details);
    }
    if (!agent.online && agent.lastError)
        card.append(element("p", "host-error", agent.lastError.slice(0, 180)));
    return card;
}
function renderHosts() {
    if (!state.agentsLoaded) {
        replaceChildren(byID("hostDirectory"), [emptyState("主机数据暂时不可用，请刷新重试。", "error")]);
        return;
    }
    const sorted = [...state.agents].sort((left, right) => Number(right.online) - Number(left.online));
    const cards = sorted.map((agent) => hostCard(agent, true));
    replaceChildren(byID("hostDirectory"), cards.length ? cards : [emptyState("尚未纳管主机。")]);
}
function syncAgentFilter() {
    const select = byID("agentFilter");
    const current = select.value;
    const options = [];
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
function serviceSearchText(entry) {
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
function filteredServices() {
    const query = byID("serviceSearch").value.trim().toLocaleLowerCase("zh-CN");
    const agent = byID("agentFilter").value;
    const exposure = byID("exposureFilter").value;
    const serviceState = byID("stateFilter").value;
    return allServiceEntries()
        .filter((entry) => agent === "all" || entry.agent.id === agent)
        .filter((entry) => exposure === "all" || serviceExposure(entry.service) === exposure)
        .filter((entry) => !query || serviceSearchText(entry).includes(query))
        .filter((entry) => {
        if (serviceState === "failed")
            return isFailed(entry.service);
        if (serviceState === "attention")
            return needsAttention(entry.service) || !entry.agent.online;
        if (serviceState === "web")
            return serviceWebTargets(entry).length > 0;
        return true;
    })
        .sort((left, right) => {
        const attentionDifference = Number(needsAttention(right.service)) - Number(needsAttention(left.service));
        if (attentionDifference)
            return attentionDifference;
        const exposureDifference = exposureOrder[serviceExposure(left.service)] - exposureOrder[serviceExposure(right.service)];
        if (exposureDifference)
            return exposureDifference;
        return serviceDisplayName(left.service).localeCompare(serviceDisplayName(right.service), "zh-CN");
    });
}
function renderServices() {
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
    const nodes = [];
    const heading = element("div", "service-table-head");
    ["范围", "服务", "主机", "状态", "访问入口", ""].forEach((label) => heading.append(element("span", "", label)));
    nodes.push(heading);
    entries.forEach((entry) => nodes.push(serviceRow(entry)));
    replaceChildren(byID("serviceDirectory"), nodes);
}
function serviceRow(entry) {
    const exposure = serviceExposure(entry.service);
    const row = element("article", `service-row ${needsAttention(entry.service) ? "attention" : ""} ${entry.service.hidden ? "is-hidden" : ""}`.trim());
    row.append(element("span", `scope-badge ${exposure}`, exposureLabel[exposure]));
    const identity = element("div", "service-identity");
    identity.append(element("strong", "", serviceDisplayName(entry.service)));
    const runtime = serviceRuntime(entry.service);
    const identityMeta = [runtime, entry.service.alias ? entry.service.name : "", entry.service.hidden ? "已隐藏" : ""].filter(Boolean).join(" · ");
    identity.append(element("small", "", identityMeta));
    if (entry.service.notes)
        identity.append(element("span", "service-note", entry.service.notes));
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
            const check = linkCheckFor(target);
            const link = element("a", `access-link ${check?.state ?? "unknown"}`, target.label);
            link.href = target.url;
            link.target = "_blank";
            link.rel = "noopener noreferrer";
            link.title = linkCheckTitle(check);
            links.append(link);
        });
        if (targets.length > 2)
            links.append(element("span", "more-pill", `+${targets.length - 2}`));
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
    if (!targets.length && !ports.length)
        access.append(element("span", "muted-copy", "无入口"));
    row.append(access);
    const manage = element("button", "button button-quiet", "管理");
    manage.type = "button";
    manage.addEventListener("click", () => openAnnotation(entry));
    row.append(manage);
    return row;
}
function syncHistoryAgentSelect() {
    const select = byID("historyAgent");
    const available = new Set(state.agents.map((agent) => agent.id));
    if (!available.has(selectedHistoryAgent))
        selectedHistoryAgent = state.agents[0]?.id ?? "";
    const options = state.agents.map((agent) => {
        const option = element("option", "", agent.name);
        option.value = agent.id;
        return option;
    });
    replaceChildren(select, options);
    select.value = selectedHistoryAgent;
    select.disabled = !selectedHistoryAgent;
}
async function loadSelectedHistory(force) {
    syncHistoryAgentSelect();
    const agentID = selectedHistoryAgent;
    if (!agentID) {
        renderHistory();
        return;
    }
    const current = historyByAgent.get(agentID) ?? { response: null, loading: false, error: "" };
    if (current.loading || (!force && current.response))
        return;
    historyByAgent.set(agentID, { ...current, loading: true, error: "" });
    renderHistory();
    try {
        const response = await api(`/api/history?agent=${encodeURIComponent(agentID)}&limit=120`);
        historyByAgent.set(agentID, { response, loading: false, error: "" });
    }
    catch (historyError) {
        historyByAgent.set(agentID, {
            response: current.response,
            loading: false,
            error: errorMessage(historyError),
        });
    }
    if (selectedHistoryAgent === agentID)
        renderHistory();
}
function svgElement(tag) {
    return document.createElementNS("http://www.w3.org/2000/svg", tag);
}
function historySparkline(values, tone, label) {
    const svg = svgElement("svg");
    svg.classList.add("history-sparkline", tone);
    svg.setAttribute("viewBox", "0 0 240 54");
    svg.setAttribute("role", "img");
    svg.setAttribute("aria-label", label);
    const baseline = svgElement("line");
    baseline.setAttribute("x1", "0");
    baseline.setAttribute("x2", "240");
    baseline.setAttribute("y1", "52");
    baseline.setAttribute("y2", "52");
    baseline.setAttribute("class", "sparkline-baseline");
    svg.append(baseline);
    let segment = [];
    const flush = () => {
        if (!segment.length)
            return;
        if (segment.length === 1)
            segment.push(segment[0] ?? "");
        const polyline = svgElement("polyline");
        polyline.setAttribute("points", segment.join(" "));
        polyline.setAttribute("class", "sparkline-line");
        svg.append(polyline);
        segment = [];
    };
    const denominator = Math.max(1, values.length - 1);
    values.forEach((value, index) => {
        if (value === undefined || !Number.isFinite(value)) {
            flush();
            return;
        }
        const bounded = Math.max(0, Math.min(100, value));
        segment.push(`${(index * 240 / denominator).toFixed(2)},${(52 - bounded * 0.5).toFixed(2)}`);
    });
    flush();
    return svg;
}
function historyTrendCard(label, value, detail, values, tone) {
    const card = element("article", "history-trend-card");
    const heading = element("div", "history-trend-heading");
    heading.append(element("span", "", label), element("strong", "", value));
    card.append(heading, historySparkline(values, tone, `${label}趋势：${detail}`), element("small", "", detail));
    return card;
}
function latestDefined(points, select) {
    for (const point of points) {
        const value = select(point);
        if (value !== undefined)
            return value;
    }
    return undefined;
}
function renderHistory() {
    syncHistoryAgentSelect();
    const summary = byID("historySummary");
    const trends = byID("historyTrends");
    const incidents = byID("historyIncidents");
    if (!state.agentsLoaded) {
        replaceChildren(summary, [emptyState("历史主机列表暂时不可用。", "error")]);
        replaceChildren(trends, []);
        replaceChildren(incidents, []);
        return;
    }
    if (!selectedHistoryAgent) {
        replaceChildren(summary, [emptyState("尚未纳管主机，暂无历史趋势。")]);
        replaceChildren(trends, []);
        replaceChildren(incidents, []);
        return;
    }
    const loaded = historyByAgent.get(selectedHistoryAgent);
    if (!loaded || (loaded.loading && !loaded.response)) {
        replaceChildren(summary, [emptyState("正在读取持久化观测…", "loading")]);
        replaceChildren(trends, []);
        replaceChildren(incidents, []);
        return;
    }
    if (loaded.error && !loaded.response) {
        replaceChildren(summary, [emptyState(`历史数据暂时不可用：${loaded.error}`, "error")]);
        replaceChildren(trends, []);
        replaceChildren(incidents, []);
        return;
    }
    const points = loaded.response?.points ?? [];
    if (!points.length) {
        replaceChildren(summary, [emptyState("这台主机还没有可用的历史观测。")]);
        replaceChildren(trends, []);
        replaceChildren(incidents, []);
        return;
    }
    const chronological = [...points].reverse();
    const onlinePoints = points.filter((point) => point.online).length;
    const availability = onlinePoints * 100 / points.length;
    const oldest = chronological[0];
    const newest = points[0];
    const windowText = oldest && newest
        ? `${formatLastSeen(oldest.observedAt)} → ${formatLastSeen(newest.observedAt)}`
        : "观测窗口未知";
    const summaryLine = element("div", "history-window");
    summaryLine.append(element("strong", "", `${availability.toFixed(1)}% 在线`), element("span", "", `${points.length} 个观测点`), element("span", "", windowText));
    if (loaded.loading)
        summaryLine.append(element("span", "history-refreshing", "正在更新"));
    if (loaded.error)
        summaryLine.append(element("span", "history-error", `最近更新失败：${loaded.error}`));
    replaceChildren(summary, [summaryLine]);
    const memoryLatest = latestDefined(points, (point) => point.memoryUsedPct);
    const diskLatest = latestDefined(points, (point) => point.diskUsedPct);
    const loadLatest = latestDefined(points, (point) => point.load1);
    const cpuLatest = latestDefined(points, (point) => point.cpus);
    const onlineSeries = chronological.map((point) => point.online ? 100 : 0);
    const loadSeries = chronological.map((point) => point.load1 !== undefined && point.cpus ? Math.min(100, point.load1 * 100 / point.cpus) : undefined);
    replaceChildren(trends, [
        historyTrendCard("在线", `${availability.toFixed(1)}%`, `${points.length - onlinePoints} 个离线采样`, onlineSeries, availability === 100 ? "good" : "critical"),
        historyTrendCard("负载", loadLatest === undefined ? "N/A" : loadLatest.toFixed(2), cpuLatest ? `最近 ${cpuLatest} CPU` : "暂无 CPU 数据", loadSeries, "info"),
        historyTrendCard("内存", memoryLatest === undefined ? "N/A" : `${memoryLatest}%`, "最近有效采样", chronological.map((point) => point.memoryUsedPct), memoryLatest !== undefined && memoryLatest >= 80 ? "warning" : "good"),
        historyTrendCard("磁盘", diskLatest === undefined ? "N/A" : `${diskLatest}%`, "根文件系统", chronological.map((point) => point.diskUsedPct), diskLatest !== undefined && diskLatest >= 85 ? "critical" : "calm"),
    ]);
    const failedPeak = Math.max(...points.map((point) => point.failedWorkloadCount));
    const warningPeak = Math.max(...points.map((point) => point.warningCount));
    const wildcardPeak = Math.max(...points.map((point) => point.wildcardEndpointCount));
    replaceChildren(incidents, [
        element("span", failedPeak ? "history-pill critical" : "history-pill", `失败服务峰值 ${failedPeak}`),
        element("span", warningPeak ? "history-pill warning" : "history-pill", `采集警告峰值 ${warningPeak}`),
        element("span", "history-pill", `公网绑定峰值 ${wildcardPeak}`),
    ]);
}
function syncEventAgentSelect() {
    const select = byID("eventAgentFilter");
    const current = select.value || "all";
    const all = element("option", "", "全部主机");
    all.value = "all";
    const options = [all, ...state.agents.map((agent) => {
            const option = element("option", "", agent.name);
            option.value = agent.id;
            return option;
        })];
    replaceChildren(select, options);
    select.value = options.some((option) => option.value === current) ? current : "all";
    select.disabled = !state.agents.length;
}
function eventKindLabel(kind) {
    const labels = {
        "host.offline": "主机离线",
        "resource.memory": "内存压力",
        "resource.disk": "磁盘压力",
        "resource.load": "系统负载",
        "workload.failed": "服务失败",
        "listener.added": "新增监听",
        "ssh.bruteforce": "SSH 爆破",
    };
    return labels[kind] ?? kind;
}
function eventSeverityLabel(event) {
    if (event.severity === "critical")
        return "严重";
    if (event.severity === "warning")
        return "警告";
    return "信息";
}
function eventStateLabel(event) {
    if (event.state === "active")
        return "待确认";
    if (event.state === "acknowledged")
        return "已确认，持续中";
    return "已恢复";
}
function eventHostName(agentID) {
    return state.agents.find((agent) => agent.id === agentID)?.name ?? agentID;
}
function eventDuration(event) {
    const start = new Date(event.firstObservedAt).getTime();
    const finish = new Date(event.resolvedAt || event.lastObservedAt).getTime();
    if (!Number.isFinite(start) || !Number.isFinite(finish) || finish < start)
        return "持续时间未知";
    const minutes = Math.max(0, Math.round((finish - start) / 60_000));
    if (minutes < 60)
        return `持续 ${minutes} 分钟`;
    const hours = Math.round(minutes / 6) / 10;
    if (hours < 24)
        return `持续 ${hours} 小时`;
    return `持续 ${Math.round(hours / 2.4) / 10} 天`;
}
function filteredEvents() {
    const agent = byID("eventAgentFilter").value || "all";
    const lifecycle = byID("eventStateFilter").value || "ongoing";
    return state.events.events.filter((event) => {
        if (agent !== "all" && event.agentId !== agent)
            return false;
        if (lifecycle === "ongoing")
            return event.state !== "resolved";
        if (lifecycle !== "all")
            return event.state === lifecycle;
        return true;
    });
}
function eventRow(event) {
    const row = element("article", `event-row ${event.severity} ${event.state}`);
    const heading = element("div", "event-row-heading");
    heading.append(element("span", `event-severity ${event.severity}`, eventSeverityLabel(event)), element("span", `event-lifecycle ${event.state}`, eventStateLabel(event)));
    const copy = element("div", "event-copy");
    copy.append(element("strong", "", event.title));
    if (event.detail)
        copy.append(element("p", "", event.detail));
    const meta = element("div", "event-meta");
    meta.append(element("span", "", eventHostName(event.agentId)), element("span", "", eventKindLabel(event.kind)), element("span", "", eventDuration(event)), element("span", "", `最近 ${formatLastSeen(event.lastObservedAt)}`));
    copy.append(meta);
    row.append(heading, copy);
    if (event.state === "active") {
        const acknowledge = element("button", "button button-quiet event-ack", "确认");
        acknowledge.type = "button";
        acknowledge.setAttribute("aria-label", `确认事件：${event.title}`);
        acknowledge.addEventListener("click", () => void acknowledgeEvent(event.id, acknowledge));
        row.append(acknowledge);
    }
    else {
        row.append(element("span", "event-state-note", event.state === "acknowledged" ? "等待恢复" : `恢复于 ${formatLastSeen(event.resolvedAt)}`));
    }
    return row;
}
function renderEvents() {
    syncEventAgentSelect();
    const summary = byID("eventSummary");
    const list = byID("eventList");
    if (!state.eventsLoaded) {
        replaceChildren(summary, [emptyState(state.eventsError ? `事件数据暂时不可用：${state.eventsError}` : "正在读取事件…", state.eventsError ? "error" : "loading")]);
        replaceChildren(list, []);
        return;
    }
    const ongoing = state.events.events.filter((event) => event.state !== "resolved");
    const unacknowledged = ongoing.filter((event) => event.state === "active");
    const critical = ongoing.filter((event) => event.severity === "critical");
    const resolved = state.events.events.filter((event) => event.state === "resolved");
    const stats = [
        element("span", ongoing.length ? "event-stat warning" : "event-stat calm", `${ongoing.length} 进行中`),
        element("span", unacknowledged.length ? "event-stat critical" : "event-stat calm", `${unacknowledged.length} 待确认`),
        element("span", critical.length ? "event-stat critical" : "event-stat calm", `${critical.length} 严重`),
        element("span", "event-stat calm", `${resolved.length} 已恢复`),
    ];
    if (state.eventsError)
        stats.push(element("span", "event-summary-error", `最近更新失败：${state.eventsError}`));
    replaceChildren(summary, stats);
    const events = filteredEvents();
    if (!events.length) {
        const message = state.events.events.length
            ? "当前筛选条件下没有事件。"
            : "还没有事件记录。规则会在风险出现时自动建立事件。";
        replaceChildren(list, [emptyState(message, state.events.events.length ? "" : "success")]);
        return;
    }
    const rows = events.slice(0, 20).map(eventRow);
    if (events.length > rows.length)
        rows.push(element("p", "more-note", `另有 ${events.length - rows.length} 条事件，请缩小筛选范围`));
    replaceChildren(list, rows);
}
async function acknowledgeEvent(id, button) {
    button.disabled = true;
    button.textContent = "确认中";
    try {
        const updated = await api(`/api/events/ack?id=${encodeURIComponent(id)}`, { method: "POST" });
        state.events.events = state.events.events.map((event) => event.id === updated.id ? updated : event);
        state.eventsError = "";
        renderSecurity();
        setNotice(`已确认事件“${updated.title}”。风险会保持进行中，直到新观测证明恢复。`);
    }
    catch (acknowledgementError) {
        button.disabled = false;
        button.textContent = "重试确认";
        setNotice(`事件确认失败：${errorMessage(acknowledgementError)}`);
    }
}
function renderSecurity() {
    const entries = allServiceEntries();
    const publicEntries = entries.filter((entry) => serviceExposure(entry.service) === "public");
    const unknown = entries.filter((entry) => entry.service.unidentified).length;
    const activeEvents = state.events.events.filter((event) => event.state === "active");
    const criticalEvents = state.events.events.filter((event) => event.state !== "resolved" && event.severity === "critical");
    replaceChildren(byID("securityMetrics"), [
        state.servicesLoaded
            ? metricCard("公网服务", publicEntries.length, "监听暴露范围为公网", publicEntries.length ? "warning" : "good")
            : metricCard("公网服务", "N/A", "服务数据暂不可用", "critical"),
        state.servicesLoaded
            ? metricCard("待归因", unknown, "来源尚未确认", unknown ? "warning" : "good")
            : metricCard("待归因", "N/A", "服务数据暂不可用", "critical"),
        state.eventsLoaded
            ? metricCard("待确认事件", activeEvents.length, "需要操作者确认", activeEvents.length ? "warning" : "good")
            : metricCard("待确认事件", "N/A", "事件数据暂不可用", "critical"),
        state.eventsLoaded
            ? metricCard("严重进行中", criticalEvents.length, "包含已确认事件", criticalEvents.length ? "critical" : "good")
            : metricCard("严重进行中", "N/A", "事件数据暂不可用", "critical"),
    ]);
    renderEvents();
    if (!state.servicesLoaded) {
        replaceChildren(byID("publicSurface"), [emptyState("公网暴露面数据暂时不可用。", "error")]);
        renderHistory();
        return;
    }
    if (!publicEntries.length) {
        replaceChildren(byID("publicSurface"), [emptyState("当前没有检测到公网监听服务。", "success")]);
        renderHistory();
        return;
    }
    const rows = publicEntries.slice(0, 12).map((entry) => {
        const row = element("button", "surface-row");
        row.type = "button";
        row.append(element("span", "surface-name", serviceDisplayName(entry.service)), element("span", "surface-meta", `${entry.agent.name} · ${(entry.service.ports ?? []).length} 端口`), element("span", "surface-action", "查看"));
        row.addEventListener("click", () => {
            byID("agentFilter").value = entry.agent.id;
            byID("exposureFilter").value = "public";
            byID("stateFilter").value = "all";
            renderServices();
            setPage("services");
        });
        return row;
    });
    replaceChildren(byID("publicSurface"), rows);
    renderHistory();
}
function syncActionAgentSelect() {
    const select = byID("actionAgent");
    const available = new Set(state.agents.map((agent) => agent.id));
    if (!available.has(selectedActionAgent)) {
        selectedActionAgent = state.agents.find((agent) => agent.online)?.id ?? state.agents[0]?.id ?? "";
    }
    const options = state.agents.map((agent) => {
        const option = element("option", "", `${agent.name}${agent.online ? "" : " · 离线"}`);
        option.value = agent.id;
        return option;
    });
    replaceChildren(select, options);
    select.value = selectedActionAgent;
    select.disabled = !selectedActionAgent;
}
async function loadSelectedActions(force) {
    syncActionAgentSelect();
    const agentID = selectedActionAgent;
    if (!agentID) {
        renderOperations();
        return;
    }
    const current = actionsByAgent.get(agentID) ?? { response: null, loading: false, error: "" };
    if (current.loading || (!force && current.response))
        return;
    actionsByAgent.set(agentID, { ...current, loading: true, error: "" });
    renderOperations();
    try {
        const response = await api(`/api/actions?agent=${encodeURIComponent(agentID)}`);
        actionsByAgent.set(agentID, { response, loading: false, error: "" });
    }
    catch (actionError) {
        actionsByAgent.set(agentID, {
            response: current.response,
            loading: false,
            error: errorMessage(actionError),
        });
    }
    if (selectedActionAgent === agentID)
        renderOperations();
}
async function loadSelectedDeployments(force) {
    syncActionAgentSelect();
    const agentID = selectedActionAgent;
    if (!agentID) {
        renderOperations();
        return;
    }
    const current = deploymentsByAgent.get(agentID) ?? { response: null, loading: false, error: "" };
    if (current.loading || (!force && current.response))
        return;
    deploymentsByAgent.set(agentID, { ...current, loading: true, error: "" });
    renderOperations();
    try {
        const response = await api(`/api/deployments?agent=${encodeURIComponent(agentID)}`);
        deploymentsByAgent.set(agentID, { response, loading: false, error: "" });
    }
    catch (deploymentError) {
        deploymentsByAgent.set(agentID, {
            response: current.response,
            loading: false,
            error: errorMessage(deploymentError),
        });
    }
    if (selectedActionAgent === agentID)
        renderOperations();
}
async function loadSelectedCapabilities(force) {
    await Promise.all([
        loadSelectedActions(force),
        loadSelectedDeployments(force),
    ]);
}
function actionKindLabel(kind) {
    const labels = {
        logs: "读取日志",
        start: "启动",
        restart: "重启",
        stop: "停止",
    };
    return labels[kind];
}
function actionRiskLabel(risk) {
    const labels = {
        read: "只读",
        change: "状态变更",
        disruptive: "中断风险",
    };
    return labels[risk];
}
function deploymentKindLabel(kind) {
    return kind === "rollback" ? "回滚" : "部署";
}
function shortImageDigest(image) {
    const marker = "@sha256:";
    const offset = image.lastIndexOf(marker);
    if (offset < 0)
        return "摘要不可用";
    return `sha256:${image.slice(offset + marker.length, offset + marker.length + 12)}…`;
}
function operationStateLabel(stateValue) {
    const labels = {
        requested: "已请求",
        running: "执行中",
        succeeded: "成功",
        failed: "失败",
        rolled_back: "已回滚",
    };
    return labels[stateValue];
}
function operationKindLabel(kind) {
    const labels = {
        logs: "读取日志",
        start: "启动",
        restart: "重启",
        stop: "停止",
        deploy: "部署",
        rollback: "回滚",
    };
    return labels[kind];
}
function operationErrorLabel(errorKind) {
    if (!errorKind)
        return "";
    const labels = {
        agent_unavailable: "Agent 不可用",
        agent_timeout: "Agent 超时",
        agent_auth_failed: "Agent 认证失败",
        agent_incompatible: "Agent 版本不兼容",
        agent_http_error: "Agent 响应异常",
        agent_invalid_response: "Agent 返回不可信",
        command_failed: "动作执行失败",
        state_read_failed: "无法读取目标状态",
        health_verification_failed: "执行后健康验证失败",
        preflight_failed: "发布前检查失败",
        image_prepare_failed: "镜像准备失败",
        current_release_unknown: "无法识别当前版本",
        state_prepare_failed: "发布状态准备失败",
        compose_apply_failed: "Compose 应用失败",
        rollback_failed: "自动回滚失败",
        state_commit_failed: "发布状态持久化失败",
        log_read_failed: "日志读取失败",
        hub_restarted: "Hub 重启，结果不确定且未重放",
    };
    return labels[errorKind] ?? errorKind;
}
function operationDuration(operation) {
    if (!operation.startedAt || !operation.finishedAt)
        return "";
    const duration = new Date(operation.finishedAt).getTime() - new Date(operation.startedAt).getTime();
    if (!Number.isFinite(duration) || duration < 0)
        return "";
    return duration < 1000 ? `${duration} ms` : `${(duration / 1000).toFixed(1)} s`;
}
function renderOperationAudit() {
    const audit = byID("operationAudit");
    if (!state.operationsLoaded) {
        replaceChildren(audit, [emptyState(state.operationsError ? `操作记录暂时不可用：${state.operationsError}` : "正在读取持久化操作记录…", state.operationsError ? "error" : "loading")]);
        return;
    }
    const operations = state.operations.operations;
    if (!operations.length) {
        replaceChildren(audit, [emptyState("还没有受控操作记录。首个动作完成后会在这里留下审计轨迹。")]);
        return;
    }
    const agentNames = new Map(state.agents.map((agent) => [agent.id, agent.name]));
    const rows = operations.map((operation) => {
        const row = element("article", `operation-row ${operation.state}`);
        const stateCell = element("div", "operation-state-cell");
        stateCell.append(element("span", `operation-state ${operation.state}`, operationStateLabel(operation.state)), element("small", "", operationKindLabel(operation.kind)));
        const copy = element("div", "operation-copy");
        copy.append(element("strong", "", operation.targetKey || "未指定目标"), element("p", "", operation.resultSummary || operationErrorLabel(operation.errorKind) || "等待执行结果"));
        const metadata = element("div", "operation-meta");
        metadata.append(element("span", "", agentNames.get(operation.agentId) ?? operation.agentId), element("span", "", formatLastSeen(operation.requestedAt)), element("span", "", operationDuration(operation) || "未完成"), element("span", "operation-requester", operation.requestedBy.startsWith("session:") ? `会话 ${operation.requestedBy.slice(8)}` : operation.requestedBy));
        copy.append(metadata);
        row.append(stateCell, copy);
        return row;
    });
    if (state.operationsError) {
        rows.unshift(element("p", "operation-audit-warning", `最近更新失败，保留上次结果：${state.operationsError}`));
    }
    replaceChildren(audit, rows);
}
function renderDeployments(selectedAgent, inFlight) {
    const status = byID("deploymentCapabilityStatus");
    const list = byID("deploymentList");
    const loaded = selectedActionAgent ? deploymentsByAgent.get(selectedActionAgent) : undefined;
    const deployments = loaded?.response?.deployments ?? [];
    if (!state.agentsLoaded || !selectedAgent) {
        replaceChildren(status, []);
        replaceChildren(list, [emptyState(state.agentsLoaded ? "尚未纳管主机。" : "主机列表暂时不可用。", state.agentsLoaded ? "" : "error")]);
        return;
    }
    if (!loaded || (loaded.loading && !loaded.response)) {
        replaceChildren(status, []);
        replaceChildren(list, [emptyState("正在向 Agent 读取实时发布策略…", "loading")]);
        return;
    }
    if (loaded.error && !loaded.response) {
        replaceChildren(status, []);
        replaceChildren(list, [emptyState(`发布策略暂时不可用：${loaded.error}`, "error")]);
        return;
    }
    const stacks = new Map();
    deployments.forEach((definition) => {
        const existing = stacks.get(definition.stackKey) ?? [];
        existing.push(definition);
        stacks.set(definition.stackKey, existing);
    });
    const statusLine = element("div", "capability-status-line");
    statusLine.append(element("span", `status-dot ${selectedAgent.online ? "online" : "offline"}`), element("strong", "", `${deployments.length} 个发布 / ${stacks.size} 个服务栈`), element("span", "", loaded.loading ? "正在复核策略" : `Agent ${loaded.response?.agentVersion || "版本未知"}`));
    if (loaded.error)
        statusLine.append(element("span", "capability-error", `最近更新失败：${loaded.error}`));
    replaceChildren(status, [statusLine]);
    if (!deployments.length) {
        replaceChildren(list, [emptyState("这台主机没有安装发布策略，部署和回滚保持禁用。", "success")]);
        return;
    }
    const groups = [...stacks.values()].map((definitions) => {
        const first = definitions[0];
        const group = element("article", "deployment-stack");
        const heading = element("div", "deployment-stack-heading");
        const identity = element("div", "deployment-stack-identity");
        identity.append(element("strong", "", first.stackLabel), element("small", "", first.stackKey));
        heading.append(identity, element("span", "deployment-current", `当前 ${first.currentReleaseId || "待识别"}`), element("span", "deployment-previous", `上一个 ${first.previousReleaseId || "无"}`));
        group.append(heading);
        definitions.forEach((definition) => {
            const button = element("button", `deployment-row kind-${definition.kind}`);
            button.type = "button";
            button.setAttribute("aria-label", `${deploymentKindLabel(definition.kind)} ${definition.stackLabel} 到 ${definition.releaseLabel}`);
            const kind = element("span", "deployment-kind", deploymentKindLabel(definition.kind));
            const copy = element("span", "deployment-copy");
            copy.append(element("strong", "", definition.releaseLabel), element("small", "", definition.description));
            const metadata = element("span", "deployment-release-meta");
            metadata.append(element("span", "deployment-release-id", definition.releaseId), element("code", "deployment-digest", shortImageDigest(definition.image)));
            button.append(kind, copy, metadata, element("span", `action-risk ${definition.risk}`, actionRiskLabel(definition.risk)));
            button.disabled = !selectedAgent.online || inFlight > 0 || actionExecuting;
            button.addEventListener("click", () => openDeploymentDialog(selectedAgent, definition));
            group.append(button);
        });
        return group;
    });
    replaceChildren(list, groups);
}
function renderOperations() {
    syncActionAgentSelect();
    const selectedAgent = state.agents.find((agent) => agent.id === selectedActionAgent);
    const loaded = selectedActionAgent ? actionsByAgent.get(selectedActionAgent) : undefined;
    const deploymentLoaded = selectedActionAgent ? deploymentsByAgent.get(selectedActionAgent) : undefined;
    const actions = loaded?.response?.actions ?? [];
    const deployments = deploymentLoaded?.response?.deployments ?? [];
    const targetCount = new Set([
        ...actions.map((action) => action.targetKey),
        ...deployments.map((deployment) => `deployment:${deployment.stackKey}`),
    ]).size;
    const inFlight = state.operations.operations.filter((operation) => operation.state === "requested" || operation.state === "running").length;
    const failed = state.operations.operations.filter((operation) => operation.state === "failed" || operation.state === "rolled_back").length;
    const capabilitiesLoaded = Boolean(loaded?.response && deploymentLoaded?.response);
    replaceChildren(byID("operationsMetrics"), [
        metricCard("受控能力", capabilitiesLoaded ? actions.length + deployments.length : "N/A", selectedAgent ? selectedAgent.name : "未选择主机", actions.length + deployments.length ? "good" : "calm"),
        metricCard("批准目标", capabilitiesLoaded ? targetCount : "N/A", "来自 root-only 策略", targetCount ? "info" : "calm"),
        metricCard("执行中", state.operationsLoaded ? inFlight : "N/A", "全局串行门禁", inFlight ? "warning" : "good"),
        metricCard("失败 / 回滚", state.operationsLoaded ? failed : "N/A", `最近 ${state.operations.operations.length} 条记录`, failed ? "critical" : "good"),
    ]);
    if (!state.agentsLoaded) {
        replaceChildren(byID("syncSummary"), [emptyState("资产同步状态暂时不可用。", "error")]);
    }
    else if (!state.agents.length) {
        replaceChildren(byID("syncSummary"), [emptyState("尚未纳管主机。")]);
    }
    else {
        const rows = state.agents.map((agent) => {
            const group = state.groups.find((item) => item.agent.id === agent.id);
            const row = element("div", "sync-row");
            row.append(element("span", `status-dot ${agent.online ? "online" : "offline"}`), element("span", "sync-copy"), element("span", "sync-version", group?.agent.agentVersion ? `Agent ${group.agent.agentVersion}` : "版本未知"));
            const copy = row.children.item(1);
            if (copy)
                copy.append(element("strong", "", agent.name), element("small", "", formatLastSeen(agent.lastSeen || group?.agent.lastSeen)));
            return row;
        });
        replaceChildren(byID("syncSummary"), rows);
    }
    const status = byID("actionCapabilityStatus");
    const list = byID("actionList");
    if (!state.agentsLoaded || !selectedAgent) {
        replaceChildren(status, []);
        replaceChildren(list, [emptyState(state.agentsLoaded ? "尚未纳管主机。" : "主机列表暂时不可用。", state.agentsLoaded ? "" : "error")]);
    }
    else if (!loaded || (loaded.loading && !loaded.response)) {
        replaceChildren(status, []);
        replaceChildren(list, [emptyState("正在向 Agent 读取实时动作策略…", "loading")]);
    }
    else if (loaded.error && !loaded.response) {
        replaceChildren(status, []);
        replaceChildren(list, [emptyState(`动作策略暂时不可用：${loaded.error}`, "error")]);
    }
    else {
        const statusLine = element("div", "capability-status-line");
        statusLine.append(element("span", `status-dot ${selectedAgent.online ? "online" : "offline"}`), element("strong", "", `${actions.length} 个动作 / ${targetCount} 个目标`), element("span", "", loaded.loading ? "正在复核策略" : `Agent ${loaded.response?.agentVersion || "版本未知"}`));
        if (loaded.error)
            statusLine.append(element("span", "capability-error", `最近更新失败：${loaded.error}`));
        replaceChildren(status, [statusLine]);
        if (!actions.length) {
            replaceChildren(list, [emptyState("这台主机没有安装动作策略，所有写操作保持禁用。", "success")]);
        }
        else {
            const actionRows = actions.map((action) => {
                const button = element("button", `action-row risk-${action.risk}`);
                button.type = "button";
                button.setAttribute("aria-label", `${actionKindLabel(action.kind)} ${action.targetLabel}`);
                const icon = element("span", "action-kind", actionKindLabel(action.kind));
                const copy = element("span", "action-copy");
                copy.append(element("strong", "", action.targetLabel), element("small", "", action.description));
                const risk = element("span", `action-risk ${action.risk}`, actionRiskLabel(action.risk));
                button.append(icon, copy, risk);
                button.disabled = !selectedAgent.online || inFlight > 0 || actionExecuting;
                button.addEventListener("click", () => openActionDialog(selectedAgent, action));
                return button;
            });
            replaceChildren(list, actionRows);
        }
    }
    renderDeployments(selectedAgent, inFlight);
    renderOperationAudit();
}
function openActionDialog(agent, definition) {
    pendingAction = { agentID: agent.id, agentName: agent.name, definition };
    pendingDeployment = null;
    actionExecuting = false;
    byID("actionDialogTitle").textContent = `${actionKindLabel(definition.kind)} ${definition.targetLabel}`;
    byID("actionDialogContext").textContent = `${agent.name} · ${definition.targetKey}`;
    const risk = byID("actionDialogRisk");
    replaceChildren(risk, [
        element("span", `action-risk ${definition.risk}`, actionRiskLabel(definition.risk)),
        element("span", "", definition.kind === "logs" ? "瞬时读取，不写入审计正文" : "执行后验证目标状态"),
    ]);
    byID("actionDialogDescription").textContent = definition.description;
    byID("actionConfirmationPhrase").textContent = definition.confirmation;
    const input = byID("actionConfirmation");
    input.value = "";
    input.disabled = false;
    byID("actionConfirmationFields").classList.remove("hidden");
    byID("actionResult").classList.add("hidden");
    byID("actionLogNotice").classList.add("hidden");
    const logs = byID("actionResultLogs");
    logs.textContent = "";
    logs.classList.add("hidden");
    byID("actionError").classList.add("hidden");
    const execute = byID("executeActionBtn");
    execute.disabled = true;
    execute.classList.remove("hidden");
    execute.textContent = "确认执行";
    const cancel = byID("cancelActionBtn");
    cancel.disabled = false;
    cancel.textContent = "取消";
    byID("closeActionDialogBtn").disabled = false;
    byID("actionDialog").showModal();
    input.focus();
}
function openDeploymentDialog(agent, definition) {
    pendingAction = null;
    pendingDeployment = { agentID: agent.id, agentName: agent.name, definition };
    actionExecuting = false;
    byID("actionDialogTitle").textContent = `${deploymentKindLabel(definition.kind)} ${definition.stackLabel} 到 ${definition.releaseLabel}`;
    byID("actionDialogContext").textContent = `${agent.name} · ${definition.stackKey} · ${shortImageDigest(definition.image)}`;
    replaceChildren(byID("actionDialogRisk"), [
        element("span", `action-risk ${definition.risk}`, actionRiskLabel(definition.risk)),
        element("span", "", "固定摘要 · 后台执行 · 健康失败自动回滚"),
    ]);
    byID("actionDialogDescription").textContent = definition.description;
    byID("actionConfirmationPhrase").textContent = definition.confirmation;
    const input = byID("actionConfirmation");
    input.value = "";
    input.disabled = false;
    byID("actionConfirmationFields").classList.remove("hidden");
    byID("actionResult").classList.add("hidden");
    byID("actionLogNotice").classList.add("hidden");
    const logs = byID("actionResultLogs");
    logs.textContent = "";
    logs.classList.add("hidden");
    byID("actionError").classList.add("hidden");
    const execute = byID("executeActionBtn");
    execute.disabled = true;
    execute.classList.remove("hidden");
    execute.textContent = "确认发布";
    const cancel = byID("cancelActionBtn");
    cancel.disabled = false;
    cancel.textContent = "取消";
    byID("closeActionDialogBtn").disabled = false;
    byID("actionDialog").showModal();
    input.focus();
}
function pendingConfirmation() {
    return pendingAction?.definition.confirmation ?? pendingDeployment?.definition.confirmation;
}
function closeActionDialog() {
    if (actionExecuting)
        return;
    const dialog = byID("actionDialog");
    if (dialog.open)
        dialog.close();
    pendingAction = null;
    pendingDeployment = null;
    byID("actionConfirmation").value = "";
    byID("actionResultLogs").textContent = "";
}
function rememberOperation(operation) {
    state.operations.operations = [
        operation,
        ...state.operations.operations.filter((candidate) => candidate.id !== operation.id),
    ].slice(0, 100);
    state.operationsLoaded = true;
    state.operationsError = "";
}
function executionResponseFromError(error) {
    if (!(error instanceof APIRequestError) || !error.payload || typeof error.payload !== "object")
        return null;
    const candidate = error.payload;
    return candidate.operation && typeof candidate.operation.id === "string"
        ? candidate
        : null;
}
function showActionExecutionResult(response) {
    rememberOperation(response.operation);
    const succeeded = response.operation.state === "succeeded";
    const summary = byID("actionResultSummary");
    replaceChildren(summary, [
        element("strong", succeeded ? "success" : "failure", succeeded ? "动作完成" : "动作失败"),
        element("span", "", response.operation.resultSummary || operationErrorLabel(response.errorKind || response.operation.errorKind)),
    ]);
    const logs = response.result?.logs ?? [];
    const logOutput = byID("actionResultLogs");
    logOutput.textContent = logs.join("\n");
    logOutput.classList.toggle("hidden", !logs.length);
    byID("actionLogNotice").classList.toggle("hidden", !logs.length);
    byID("actionResult").classList.remove("hidden");
    byID("actionConfirmationFields").classList.add("hidden");
    byID("executeActionBtn").classList.add("hidden");
    const cancel = byID("cancelActionBtn");
    cancel.textContent = "关闭";
    renderOperations();
}
function showDeploymentAccepted(response) {
    rememberOperation(response.operation);
    replaceChildren(byID("actionResultSummary"), [
        element("strong", "success", "发布已受理"),
        element("span", "", "Hub 正在后台执行。最终结果会自动写入下方操作记录，关闭窗口不会中断任务。"),
    ]);
    byID("actionResult").classList.remove("hidden");
    byID("actionConfirmationFields").classList.add("hidden");
    byID("actionLogNotice").classList.add("hidden");
    byID("actionResultLogs").classList.add("hidden");
    byID("executeActionBtn").classList.add("hidden");
    byID("cancelActionBtn").textContent = "关闭";
    renderOperations();
}
function operationIsTerminal(operation) {
    return operation.state === "succeeded" || operation.state === "failed" || operation.state === "rolled_back";
}
async function trackDeploymentOperation(operationID, agentName, stackLabel) {
    if (trackedDeploymentOperations.has(operationID))
        return;
    trackedDeploymentOperations.add(operationID);
    try {
        for (let attempt = 0; attempt < 800 && authed; attempt += 1) {
            if (attempt > 0) {
                await new Promise((resolve) => window.setTimeout(resolve, 1_500));
            }
            try {
                state.operations = await api("/api/operations?limit=100");
                state.operationsLoaded = true;
                state.operationsError = "";
                renderOperations();
            }
            catch (pollError) {
                state.operationsError = errorMessage(pollError);
                renderOperations();
                continue;
            }
            const operation = state.operations.operations.find((candidate) => candidate.id === operationID);
            if (!operation || !operationIsTerminal(operation))
                continue;
            if (operation.state === "succeeded") {
                setNotice(`${agentName} · ${stackLabel}：发布成功，健康验证已通过。`);
            }
            else if (operation.state === "rolled_back") {
                setNotice(`${agentName} · ${stackLabel}：发布未通过验证，已自动回滚并写入审计。`);
            }
            else {
                setNotice(`${agentName} · ${stackLabel}：发布失败，${operationErrorLabel(operation.errorKind) || "请查看操作记录"}。`);
            }
            void loadSelectedDeployments(true);
            return;
        }
        if (authed) {
            setNotice(`${agentName} · ${stackLabel}：页面跟踪已超时，请以持久操作记录为准。`);
        }
    }
    finally {
        trackedDeploymentOperations.delete(operationID);
    }
}
async function refreshOperationAudit() {
    try {
        state.operations = await api("/api/operations?limit=100");
        state.operationsLoaded = true;
        state.operationsError = "";
    }
    catch (operationError) {
        state.operationsError = errorMessage(operationError);
    }
    renderOperations();
}
async function executePendingAction(event) {
    event.preventDefault();
    const action = pendingAction;
    const deployment = pendingDeployment;
    if ((!action && !deployment) || actionExecuting)
        return;
    const confirmation = byID("actionConfirmation").value;
    const expectedConfirmation = action?.definition.confirmation ?? deployment?.definition.confirmation;
    if (confirmation !== expectedConfirmation)
        return;
    actionExecuting = true;
    const execute = byID("executeActionBtn");
    const cancel = byID("cancelActionBtn");
    const close = byID("closeActionDialogBtn");
    const error = byID("actionError");
    execute.disabled = true;
    execute.textContent = "执行中";
    cancel.disabled = true;
    close.disabled = true;
    byID("actionConfirmation").disabled = true;
    error.classList.add("hidden");
    try {
        if (action) {
            const input = {
                agentId: action.agentID,
                actionId: action.definition.id,
                confirmation,
            };
            const response = await api("/api/actions/execute", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(input),
            });
            showActionExecutionResult(response);
            setNotice(response.operation.state === "succeeded"
                ? `${action.agentName} · ${action.definition.targetLabel}：动作已完成并写入审计。`
                : `${action.agentName} · ${action.definition.targetLabel}：动作失败，已记录类型化原因。`);
        }
        else if (deployment) {
            const input = {
                agentId: deployment.agentID,
                deploymentId: deployment.definition.id,
                confirmation,
            };
            const response = await api("/api/deployments/execute", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(input),
            });
            showDeploymentAccepted(response);
            setNotice(`${deployment.agentName} · ${deployment.definition.stackLabel}：发布已受理，正在后台执行。`);
            void trackDeploymentOperation(response.operation.id, deployment.agentName, deployment.definition.stackLabel);
        }
    }
    catch (actionError) {
        const audited = action ? executionResponseFromError(actionError) : null;
        if (audited) {
            showActionExecutionResult(audited);
            setNotice(`${action?.agentName ?? "主机"} · ${action?.definition.targetLabel ?? "目标"}：Agent 未完成动作，失败已写入审计。`);
        }
        else {
            error.textContent = `${deployment ? "无法提交发布" : "无法提交动作"}：${errorMessage(actionError)}`;
            error.classList.remove("hidden");
        }
    }
    finally {
        actionExecuting = false;
        close.disabled = false;
        if (!byID("actionConfirmationFields").classList.contains("hidden")) {
            execute.disabled = confirmation !== pendingConfirmation();
            execute.textContent = deployment ? "确认发布" : "确认执行";
            cancel.disabled = false;
            byID("actionConfirmation").disabled = false;
        }
        else {
            cancel.disabled = false;
        }
        void refreshOperationAudit();
        void loadSelectedCapabilities(true);
    }
}
async function probeWebLinks() {
    const button = byID("probeLinksBtn");
    button.disabled = true;
    button.textContent = "检查中";
    button.setAttribute("aria-busy", "true");
    setNotice("正在从 Hub 检查登记的 Web 入口，最多等待 15 秒。");
    try {
        state.linkChecks = await api("/api/link-checks", { method: "POST" });
        state.linkChecksLoaded = true;
        renderAll();
        const summary = state.linkChecks.summary;
        setNotice(summary.total
            ? `Hub 入口检查完成：${summary.reachable}/${summary.total} 可达，${summary.degraded} 个返回 5xx，${summary.unreachable} 个网络不可达。`
            : "当前没有可检查的 Web 入口。");
    }
    catch (probeError) {
        setNotice(`入口检查失败：${errorMessage(probeError)}`);
    }
    finally {
        button.disabled = false;
        button.textContent = "检查入口";
        button.removeAttribute("aria-busy");
    }
}
function openAnnotation(entry) {
    editingService = { agentID: entry.agent.id, agentName: entry.agent.name, service: entry.service };
    byID("annotationTitle").textContent = `管理 ${serviceDisplayName(entry.service)}`;
    byID("annotationContext").textContent = `${entry.agent.name} · ${entry.service.key}`;
    byID("annotationAlias").value = entry.service.alias ?? "";
    byID("annotationURL").value = entry.service.url ?? "";
    byID("annotationNotes").value = entry.service.notes ?? "";
    byID("annotationHidden").checked = entry.service.hidden;
    byID("annotationError").classList.add("hidden");
    byID("annotationURL").setCustomValidity("");
    byID("annotationDialog").showModal();
    byID("annotationAlias").focus();
}
function closeAnnotation() {
    editingService = null;
    byID("annotationDialog").close();
}
async function saveAnnotation(event) {
    event.preventDefault();
    if (!editingService)
        return;
    const alias = byID("annotationAlias").value.trim();
    const urlInput = byID("annotationURL");
    const url = urlInput.value.trim();
    const notes = byID("annotationNotes").value.trim();
    if (url && !safeWebURL(url)) {
        urlInput.setCustomValidity("请输入不含用户名或密码的 http(s) 地址。");
        urlInput.reportValidity();
        return;
    }
    urlInput.setCustomValidity("");
    const input = {
        alias,
        url,
        notes,
        hidden: byID("annotationHidden").checked,
    };
    const saveButton = byID("saveAnnotationBtn");
    const error = byID("annotationError");
    error.classList.add("hidden");
    saveButton.disabled = true;
    saveButton.textContent = "保存中";
    try {
        await api(`/api/annotation?agent=${encodeURIComponent(editingService.agentID)}&key=${encodeURIComponent(editingService.service.key)}`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(input),
        });
        closeAnnotation();
        await refresh();
    }
    catch (saveError) {
        error.textContent = `保存失败：${errorMessage(saveError)}`;
        error.classList.remove("hidden");
    }
    finally {
        saveButton.disabled = false;
        saveButton.textContent = "保存配置";
    }
}
byID("loginBtn").addEventListener("click", () => void login());
byID("logoutBtn").addEventListener("click", () => void logout());
byID("refreshBtn").addEventListener("click", () => void refresh());
byID("probeLinksBtn").addEventListener("click", () => void probeWebLinks());
byID("historyAgent").addEventListener("change", (event) => {
    selectedHistoryAgent = event.currentTarget.value;
    renderHistory();
    void loadSelectedHistory(false);
});
byID("eventAgentFilter").addEventListener("change", renderEvents);
byID("eventStateFilter").addEventListener("change", renderEvents);
byID("actionAgent").addEventListener("change", (event) => {
    selectedActionAgent = event.currentTarget.value;
    renderOperations();
    void loadSelectedCapabilities(false);
});
byID("dismissNotice").addEventListener("click", () => setNotice(null));
byID("pw").addEventListener("keydown", (event) => {
    if (event.key === "Enter")
        void login();
});
document.querySelectorAll("[data-page]").forEach((item) => {
    item.addEventListener("click", () => {
        const page = item.dataset.page;
        if (page && isPageID(page))
            setPage(page);
    });
});
document.querySelectorAll("[data-navigate]").forEach((item) => {
    item.addEventListener("click", () => {
        const page = item.dataset.navigate;
        const serviceState = item.dataset.serviceState;
        if (serviceState)
            byID("stateFilter").value = serviceState;
        if (page && isPageID(page)) {
            renderServices();
            setPage(page);
        }
    });
});
["serviceSearch", "agentFilter", "exposureFilter", "stateFilter"].forEach((id) => {
    byID(id).addEventListener("input", renderServices);
});
byID("annotationForm").addEventListener("submit", (event) => void saveAnnotation(event));
byID("closeDialogBtn").addEventListener("click", closeAnnotation);
byID("cancelDialogBtn").addEventListener("click", closeAnnotation);
byID("annotationURL").addEventListener("input", (event) => {
    event.currentTarget.setCustomValidity("");
});
byID("annotationDialog").addEventListener("click", (event) => {
    if (event.target === event.currentTarget)
        closeAnnotation();
});
byID("annotationDialog").addEventListener("close", () => {
    editingService = null;
});
byID("actionForm").addEventListener("submit", (event) => void executePendingAction(event));
byID("closeActionDialogBtn").addEventListener("click", closeActionDialog);
byID("cancelActionBtn").addEventListener("click", closeActionDialog);
byID("actionConfirmation").addEventListener("input", (event) => {
    const value = event.currentTarget.value;
    byID("executeActionBtn").disabled = actionExecuting || value !== pendingConfirmation();
    byID("actionError").classList.add("hidden");
});
byID("actionDialog").addEventListener("click", (event) => {
    if (event.target === event.currentTarget)
        closeActionDialog();
});
byID("actionDialog").addEventListener("cancel", (event) => {
    if (actionExecuting)
        event.preventDefault();
});
byID("actionDialog").addEventListener("close", () => {
    pendingAction = null;
    pendingDeployment = null;
    byID("actionResultLogs").textContent = "";
});
window.addEventListener("hashchange", setPageFromHash);
void boot();
export {};

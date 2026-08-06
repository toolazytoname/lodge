"use strict";

const exposureLabel = { public: "公网", tailnet: "内网", local: "本机", other: "待定" };
const exposureOrder = { public: 0, other: 1, tailnet: 2, local: 3 };

let authed = false;
let csrfToken = "";
let refreshTimer = null;

const byID = (id) => document.getElementById(id);

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = String(text);
  return node;
}

function replaceChildren(node, children) {
  node.replaceChildren(...children);
}

function safeWebURL(raw) {
  if (!raw) return null;
  try {
    const parsed = new URL(raw);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : null;
  } catch (_) {
    return null;
  }
}

async function api(path, options = {}) {
  const method = (options.method || "GET").toUpperCase();
  const headers = new Headers(options.headers || {});
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
  return response.json();
}

function showLogin() {
  byID("login").classList.remove("hidden");
  byID("dash").classList.add("hidden");
  byID("logoutBtn").classList.add("hidden");
  byID("updated").textContent = "";
  byID("updated").classList.remove("err");
  if (refreshTimer) window.clearInterval(refreshTimer);
  refreshTimer = null;
}

function showDashboard() {
  byID("login").classList.add("hidden");
  byID("dash").classList.remove("hidden");
  byID("logoutBtn").classList.remove("hidden");
}

function startRefresh() {
  if (refreshTimer) window.clearInterval(refreshTimer);
  refreshTimer = window.setInterval(refresh, 10000);
}

async function loadSession() {
  const session = await api("/api/session");
  authed = session.authed;
  csrfToken = session.csrfToken || "";
  return session;
}

async function boot() {
  try {
    const session = await loadSession();
    if (!session.authed) return showLogin();
    showDashboard();
    await refresh();
    startRefresh();
  } catch (_) {
    showLogin();
  }
}

async function login() {
  byID("loginErr").classList.add("hidden");
  try {
    await api("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: byID("pw").value }),
    });
    const session = await loadSession();
    if (!session.authed) throw new Error("unauthorized");
    byID("pw").value = "";
    showDashboard();
    await refresh();
    startRefresh();
  } catch (_) {
    byID("loginErr").classList.remove("hidden");
  }
}

async function logout() {
  try {
    await api("/api/logout", { method: "POST" });
  } finally {
    authed = false;
    csrfToken = "";
    showLogin();
  }
}

async function refresh() {
  if (!authed) return;
  try {
    const [agents, groups] = await Promise.all([api("/api/agents"), api("/api/services")]);
    renderMachines(agents);
    renderServices(groups);
    byID("updated").textContent = `更新于 ${new Date().toLocaleTimeString("zh-CN")}`;
    byID("updated").classList.remove("err");
  } catch (error) {
    byID("updated").textContent = `加载失败：${error.message}`;
    byID("updated").classList.add("err");
  }
}

function renderMachines(agents) {
  const cards = agents.map((agent) => {
    const card = element("article", "card");
    const name = element("div", "card-name");
    name.append(element("span", `dot ${agent.online ? "on" : ""}`), document.createTextNode(agent.name));
    card.append(name);

    const stats = element("div", "stats");
    const values = [
      ["负载", `${Number(agent.load1 || 0).toFixed(2)} / ${agent.cpus || "-"}`],
      ["内存", `${agent.memUsedPct || 0}%`],
      ["磁盘", `${agent.diskUsedPct || 0}%`],
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
      card.append(element("p", "offline-error err", String(agent.lastError).slice(0, 160)));
    }
    return card;
  });
  replaceChildren(byID("machines"), cards);
}

function renderServices(groups) {
  const sections = [];
  groups.forEach((group) => {
    const title = element("h2", "group-title", group.agent.name);
    if (!group.agent.online) title.append(element("span", "err", " · 离线"));
    sections.push(title);

    const container = element("div", "card section-card");
    const services = [...group.services].sort(
      (left, right) => (exposureOrder[left.maxExposure] ?? 9) - (exposureOrder[right.maxExposure] ?? 9),
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

function serviceRow(agentID, service) {
  const row = element("div", `svc-row ${service.maxExposure === "public" ? "public" : ""}`);
  row.append(element("span", `badge ${service.maxExposure}`, exposureLabel[service.maxExposure] || service.maxExposure));

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
  row.append(name, element("span", "svc-kind", service.kind));

  const ports = element("span", "ports");
  (service.ports || []).forEach((port) => ports.append(element("span", "port", `${port.bind}:${port.port}`)));
  row.append(ports);

  const edit = element("button", "edit", "编辑");
  edit.type = "button";
  edit.title = "设置访问链接";
  edit.addEventListener("click", () => editURL(agentID, service.key, service.url || ""));
  row.append(edit);
  return row;
}

async function editURL(agentID, serviceKey, current) {
  const next = window.prompt(`设置「${serviceKey}」的访问链接（留空清除）：`, current);
  if (next === null) return;
  try {
    await api(`/api/annotation?agent=${encodeURIComponent(agentID)}&key=${encodeURIComponent(serviceKey)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url: next.trim() }),
    });
    await refresh();
  } catch (error) {
    byID("updated").textContent = `保存失败：${error.message}`;
    byID("updated").classList.add("err");
  }
}

byID("loginBtn").addEventListener("click", login);
byID("logoutBtn").addEventListener("click", logout);
byID("pw").addEventListener("keydown", (event) => {
  if (event.key === "Enter") login();
});

boot();

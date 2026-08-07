#!/usr/bin/env node
import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const webRoot = path.join(repositoryRoot, "internal", "hub", "web");
const listenHost = process.env.LODGE_FIXTURE_HOST || "127.0.0.1";
const listenPort = Number(process.env.LODGE_FIXTURE_PORT || "4173");

const hostDefinitions = [
  { id: "north", name: "North", cpus: 2, load1: 0.14, memUsedPct: 41, diskUsedPct: 45 },
  { id: "east", name: "East", cpus: 2, load1: 0.37, memUsedPct: 75, diskUsedPct: 73 },
  { id: "south", name: "South", cpus: 4, load1: 0.28, memUsedPct: 26, diskUsedPct: 47 },
  { id: "west", name: "West", cpus: 4, load1: 0.52, memUsedPct: 73, diskUsedPct: 40 },
  { id: "harbor", name: "Harbor", cpus: 2, load1: 0.08, memUsedPct: 65, diskUsedPct: 35 },
];

function makeService(host, hostIndex, serviceIndex) {
  const publicService = serviceIndex === 0 || serviceIndex === 3 || (hostIndex > 2 && serviceIndex === 7);
  const tailnetService = !publicService && serviceIndex % 4 === 1;
  const localService = !publicService && !tailnetService && serviceIndex % 4 === 2;
  const hasPort = publicService || tailnetService || localService;
  const failed = (hostIndex === 1 && serviceIndex === 8) || (hostIndex === 4 && serviceIndex >= 8);
  const gateway = serviceIndex === 0;
  const certbot = hostIndex === 1 && serviceIndex === 8;
  const name = gateway ? "gateway" : certbot ? "certbot" : `service-${hostIndex + 1}-${serviceIndex + 1}`;
  const exposure = publicService ? "public" : tailnetService ? "tailnet" : localService ? "local" : "local";
  const ports = hasPort
    ? Array.from({ length: gateway ? 5 : 1 }, (_, portIndex) => ({
        proto: "tcp",
        port: 8000 + hostIndex * 100 + serviceIndex * 5 + portIndex,
        bind: publicService ? "0.0.0.0" : tailnetService ? "100.64.0.10" : "127.0.0.1",
        exposure,
      }))
    : undefined;
  const route = gateway
    ? {
        scheme: "https",
        host: `${host.id}.fixture.example.test`,
        port: 443,
        path: "/",
        upstreams: [`127.0.0.1:${8000 + hostIndex * 100}`],
        url: `https://${host.id}.fixture.example.test/`,
      }
    : undefined;
  return {
    key: `${gateway ? "docker" : "systemd"}:${name}${gateway ? "" : ".service"}`,
    kind: gateway ? "docker" : "systemd",
    name,
    status: failed ? "failed" : gateway ? "running" : "active/running",
    ...(gateway ? { image: "fixture/gateway:1.0" } : { unit: `${name}.service` }),
    ...(ports ? { ports } : {}),
    ...(route ? { routes: [route], url: route.url } : {}),
    maxExposure: hasPort ? exposure : "local",
    hidden: false,
    ...(serviceIndex === 4 ? { notes: "Fixture maintenance window: Sunday" } : {}),
  };
}

function buildFixture(mode) {
  if (mode === "empty") return { agents: [], groups: [] };
  const groups = hostDefinitions.map((host, hostIndex) => {
    const services = Array.from({ length: 11 }, (_, serviceIndex) => makeService(host, hostIndex, serviceIndex));
    const offline = mode === "offline" && hostIndex === hostDefinitions.length - 1;
    return {
      agent: {
        id: host.id,
        name: host.name,
        online: !offline,
        lastSeen: "2026-08-08T00:00:00Z",
        ...(offline ? { lastError: "fixture: agent connection timed out" } : {}),
        agentVersion: "0.7.0",
      },
      services,
    };
  });
  const agents = groups.map((group, index) => ({
    id: group.agent.id,
    name: group.agent.name,
    online: group.agent.online,
    lastSeen: group.agent.lastSeen,
    ...(group.agent.lastError ? { lastError: group.agent.lastError } : {}),
    cpus: hostDefinitions[index].cpus,
    load1: hostDefinitions[index].load1,
    memUsedPct: hostDefinitions[index].memUsedPct,
    diskUsedPct: hostDefinitions[index].diskUsedPct,
    serviceCount: group.services.length,
    publicCount: group.services.filter((service) => service.maxExposure === "public").length,
  }));
  return { agents, groups };
}

function buildLinkChecks(mode) {
  const fixture = buildFixture(mode);
  const checkedAt = "2026-08-08T00:00:00Z";
  const checks = fixture.groups.flatMap((group) => group.services.flatMap((service) =>
    (service.routes || []).map((route) => {
      const unreachable = mode === "offline" && group.agent.id === "harbor";
      return {
        agentId: group.agent.id,
        serviceKey: service.key,
        url: route.url,
        state: unreachable ? "unreachable" : "reachable",
        ...(unreachable ? { errorKind: "timeout" } : { httpStatus: 204 }),
        latencyMs: unreachable ? 3000 : 18,
        checkedAt,
      };
    }),
  ));
  return {
    checks,
    summary: {
      total: checks.length,
      reachable: checks.filter((check) => check.state === "reachable").length,
      degraded: checks.filter((check) => check.state === "degraded").length,
      unreachable: checks.filter((check) => check.state === "unreachable").length,
      ...(checks.length ? { checkedAt } : {}),
    },
  };
}

function buildHistory(mode, agentID) {
  const fixture = buildFixture(mode);
  const group = fixture.groups.find((candidate) => candidate.agent.id === agentID);
  if (!group) return null;
  const hostIndex = hostDefinitions.findIndex((host) => host.id === agentID);
  const publicBindings = group.services.flatMap((service) => service.ports || [])
    .filter((port) => port.exposure === "public").length;
  const baseTime = Date.parse("2026-08-08T00:00:00Z");
  const points = Array.from({ length: 120 }, (_, index) => {
    const normalGap = agentID === "east" && index >= 36 && index <= 38;
    const currentOffline = mode === "offline" && agentID === "harbor" && index <= 8;
    const online = !normalGap && !currentOffline;
    const wave = Math.sin((119 - index + hostIndex * 7) / 9);
    const memory = Math.max(8, Math.min(94, hostDefinitions[hostIndex].memUsedPct + Math.round(wave * 4)));
    const disk = Math.max(8, Math.min(96, hostDefinitions[hostIndex].diskUsedPct + Math.round(wave * 2)));
    const failed = hostIndex === 1 ? 1 : hostIndex === 4 ? 3 : 0;
    return {
      observedAt: new Date(baseTime - index * 30_000).toISOString(),
      online,
      ...(online ? {
        agentVersion: "0.7.0",
        cpus: hostDefinitions[hostIndex].cpus,
        load1: Number(Math.max(0.01, hostDefinitions[hostIndex].load1 + wave * 0.08).toFixed(2)),
        memoryUsedPct: memory,
        diskUsedPct: disk,
        workloadCount: group.services.length,
        failedWorkloadCount: failed,
        wildcardEndpointCount: publicBindings,
        warningCount: agentID === "south" && index === 27 ? 1 : 0,
      } : {
        lastError: "fixture: agent connection timed out",
        workloadCount: 0,
        failedWorkloadCount: 0,
        wildcardEndpointCount: 0,
        warningCount: 0,
      }),
    };
  });
  return { agentId: agentID, points };
}

const acknowledgedFixtureEvents = new Set();

function seededOperations() {
  return [
    {
      id: "op_fixture_south_deploy_rollback",
      agentId: "south",
      targetKey: "gateway",
      kind: "deploy",
      state: "rolled_back",
      requestedBy: "session:9b1d4e73a2f011c8",
      requestedAt: "2026-08-07T23:54:00Z",
      startedAt: "2026-08-07T23:54:01Z",
      finishedAt: "2026-08-07T23:55:18Z",
      resultSummary: "Version 2 未通过健康验证，已自动恢复 Version 1",
      errorKind: "health_verification_failed",
    },
    {
      id: "op_fixture_west_restart",
      agentId: "west",
      targetKey: "docker:gateway",
      kind: "restart",
      state: "succeeded",
      requestedBy: "session:9b1d4e73a2f011c8",
      requestedAt: "2026-08-07T23:48:00Z",
      startedAt: "2026-08-07T23:48:01Z",
      finishedAt: "2026-08-07T23:48:03Z",
      resultSummary: "Gateway：running → running",
    },
    {
      id: "op_fixture_east_logs",
      agentId: "east",
      targetKey: "docker:gateway",
      kind: "logs",
      state: "succeeded",
      requestedBy: "session:9b1d4e73a2f011c8",
      requestedAt: "2026-08-07T22:31:00Z",
      startedAt: "2026-08-07T22:31:00Z",
      finishedAt: "2026-08-07T22:31:01Z",
      resultSummary: "返回 2 行有界脱敏日志",
    },
    {
      id: "op_fixture_harbor_failed",
      agentId: "harbor",
      targetKey: "docker:gateway",
      kind: "restart",
      state: "failed",
      requestedBy: "session:4a88713bc410e2d4",
      requestedAt: "2026-08-07T20:18:00Z",
      startedAt: "2026-08-07T20:18:01Z",
      finishedAt: "2026-08-07T20:18:11Z",
      errorKind: "health_verification_failed",
    },
    {
      id: "op_fixture_north_start",
      agentId: "north",
      targetKey: "docker:gateway",
      kind: "start",
      state: "succeeded",
      requestedBy: "session:4a88713bc410e2d4",
      requestedAt: "2026-08-07T18:02:00Z",
      startedAt: "2026-08-07T18:02:00Z",
      finishedAt: "2026-08-07T18:02:01Z",
      resultSummary: "Gateway：running → running",
    },
  ];
}

let fixtureOperations = seededOperations();
let fixtureDeploymentPending = new Map();

function buildActions(mode, agentID) {
  const agent = buildFixture(mode).agents.find((candidate) => candidate.id === agentID);
  if (!agent) return null;
  const common = {
    targetKey: "docker:gateway",
    targetLabel: "Gateway",
    targetKind: "docker",
  };
  return {
    agentId: agent.id,
    agentName: agent.name,
    agentVersion: "0.7.0",
    actions: [
      {
        ...common,
        id: "logs:docker:gateway",
        kind: "logs",
        description: "读取最多 200 行脱敏日志：Gateway",
        confirmation: "确认读取日志 Gateway",
        risk: "read",
      },
      {
        ...common,
        id: "start:docker:gateway",
        kind: "start",
        description: "启动并验证运行状态：Gateway",
        confirmation: "确认启动 Gateway",
        risk: "change",
      },
      {
        ...common,
        id: "restart:docker:gateway",
        kind: "restart",
        description: "重启并验证恢复运行：Gateway",
        confirmation: "确认重启 Gateway",
        risk: "disruptive",
      },
    ],
  };
}

function buildDeployments(mode, agentID) {
  const agent = buildFixture(mode).agents.find((candidate) => candidate.id === agentID);
  if (!agent) return null;
  const digest = (digit) => `registry.fixture.example.test/gateway@sha256:${digit.repeat(64)}`;
  return {
    agentId: agent.id,
    agentName: agent.name,
    agentVersion: "0.7.0",
    deployments: [
      {
        id: "deploy:gateway:v2",
        stackKey: "gateway",
        stackLabel: "Gateway",
        kind: "deploy",
        releaseId: "v2",
        releaseLabel: "Version 2",
        image: digest("2"),
        currentReleaseId: "v1",
        previousReleaseId: "v0",
        description: "部署不可变镜像并验证；失败自动回滚：Gateway / Version 2",
        confirmation: "确认部署 Gateway 到 Version 2",
        risk: "disruptive",
      },
      {
        id: "rollback:gateway",
        stackKey: "gateway",
        stackLabel: "Gateway",
        kind: "rollback",
        releaseId: "v0",
        releaseLabel: "Version 0",
        image: digest("0"),
        currentReleaseId: "v1",
        previousReleaseId: "v0",
        description: "回滚到上一个已验证版本：Gateway / Version 0",
        confirmation: "确认回滚 Gateway 到 Version 0",
        risk: "disruptive",
      },
    ],
  };
}

function settleFixtureDeployments() {
  for (const [operationID, polls] of fixtureDeploymentPending) {
    if (polls < 1) {
      fixtureDeploymentPending.set(operationID, polls + 1);
      continue;
    }
    fixtureOperations = fixtureOperations.map((operation) => operation.id === operationID
      ? {
          ...operation,
          state: "succeeded",
          finishedAt: "2026-08-08T00:01:18Z",
          resultSummary: "Gateway 已部署 Version 2，健康验证通过",
        }
      : operation);
    fixtureDeploymentPending.delete(operationID);
  }
}

async function readJSONBody(request) {
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 8192) throw new Error("fixture request too large");
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

function buildEvents(mode, agentID = "") {
  if (mode === "empty") return { events: [] };
  const events = [
    {
      id: "evt_fixture_east_certbot",
      agentId: "east",
      kind: "workload.failed",
      severity: "critical",
      state: "active",
      title: "服务失败：certbot",
      detail: "systemd unit entered failed state",
      firstObservedAt: "2026-08-07T23:41:00Z",
      lastObservedAt: "2026-08-08T00:00:00Z",
    },
    {
      id: "evt_fixture_harbor_nine",
      agentId: "harbor",
      kind: "workload.failed",
      severity: "critical",
      state: "active",
      title: "服务失败：service-5-9",
      detail: "health check reports unhealthy",
      firstObservedAt: "2026-08-07T22:52:00Z",
      lastObservedAt: "2026-08-08T00:00:00Z",
    },
    {
      id: "evt_fixture_harbor_ten",
      agentId: "harbor",
      kind: "workload.failed",
      severity: "critical",
      state: "acknowledged",
      title: "服务失败：service-5-10",
      detail: "systemd unit entered failed state",
      firstObservedAt: "2026-08-07T21:18:00Z",
      lastObservedAt: "2026-08-08T00:00:00Z",
      acknowledgedAt: "2026-08-07T23:12:00Z",
    },
    {
      id: "evt_fixture_harbor_ssh",
      agentId: "harbor",
      kind: "ssh.bruteforce",
      severity: "critical",
      state: "active",
      title: "SSH 认证失败突增",
      detail: "10 分钟内 SSH 认证失败 71 次；主要来源 203.0.113.44 × 61、2001:db8::7 × 10",
      firstObservedAt: "2026-08-07T23:36:00Z",
      lastObservedAt: "2026-08-08T00:00:00Z",
    },
    {
      id: "evt_fixture_west_listener",
      agentId: "west",
      kind: "listener.added",
      severity: "warning",
      state: "resolved",
      title: "新增公网绑定：8443/tcp",
      detail: "docker:gateway on 0.0.0.0",
      firstObservedAt: "2026-08-07T18:10:00Z",
      lastObservedAt: "2026-08-07T18:23:00Z",
      resolvedAt: "2026-08-07T18:24:00Z",
    },
    {
      id: "evt_fixture_south_memory",
      agentId: "south",
      kind: "resource.memory",
      severity: "warning",
      state: "resolved",
      title: "内存压力",
      detail: "内存使用率 87%",
      firstObservedAt: "2026-08-07T16:05:00Z",
      lastObservedAt: "2026-08-07T16:19:00Z",
      resolvedAt: "2026-08-07T16:20:00Z",
    },
  ];
  if (mode === "offline") {
    events.unshift({
      id: "evt_fixture_harbor_offline",
      agentId: "harbor",
      kind: "host.offline",
      severity: "critical",
      state: "active",
      title: "主机离线",
      detail: "fixture: agent connection timed out",
      firstObservedAt: "2026-08-07T23:55:30Z",
      lastObservedAt: "2026-08-08T00:00:00Z",
    });
  }
  const projected = events.map((event) => acknowledgedFixtureEvents.has(event.id) && event.state === "active"
    ? { ...event, state: "acknowledged", acknowledgedAt: "2026-08-08T00:00:01Z" }
    : event);
  return { events: agentID ? projected.filter((event) => event.agentId === agentID) : projected };
}

function fixtureMode(requestURL, cookieHeader = "") {
  const requested = requestURL.searchParams.get("fixture");
  if (requested) return requested;
  const cookie = cookieHeader.split(";").map((part) => part.trim()).find((part) => part.startsWith("lodge_fixture="));
  return cookie ? decodeURIComponent(cookie.slice("lodge_fixture=".length)) : "normal";
}

function sendJSON(response, statusCode, value) {
  const body = JSON.stringify(value);
  response.writeHead(statusCode, {
    "Cache-Control": "no-store",
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": Buffer.byteLength(body),
  });
  response.end(body);
}

async function sendAsset(response, pathname) {
  const relative = pathname === "/" ? "index.html" : pathname.slice(1);
  if (!new Set(["index.html", "app.css", "app.js"]).has(relative)) {
    response.writeHead(404).end("not found");
    return;
  }
  const filePath = path.join(webRoot, relative);
  const fileStat = await stat(filePath);
  const contentType = relative.endsWith(".html")
    ? "text/html; charset=utf-8"
    : relative.endsWith(".css")
      ? "text/css; charset=utf-8"
      : "application/javascript; charset=utf-8";
  response.writeHead(200, {
    "Cache-Control": "no-store",
    "Content-Type": contentType,
    "Content-Length": fileStat.size,
  });
  createReadStream(filePath).pipe(response);
}

const server = createServer(async (request, response) => {
  try {
    const requestURL = new URL(request.url || "/", `http://${listenHost}:${listenPort}`);
    const mode = fixtureMode(requestURL, request.headers.cookie);
    if (requestURL.pathname === "/" && requestURL.searchParams.has("fixture")) {
      response.setHeader("Set-Cookie", `lodge_fixture=${encodeURIComponent(mode)}; Path=/; SameSite=Strict`);
    }
    if (requestURL.pathname === "/api/session") {
      sendJSON(response, 200, { authed: true, csrfToken: "fixture-csrf" });
      return;
    }
    if (requestURL.pathname === "/__fixture/reset") {
      if (request.method !== "POST") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
      } else {
        acknowledgedFixtureEvents.clear();
        fixtureOperations = seededOperations();
        fixtureDeploymentPending = new Map();
        sendJSON(response, 200, { ok: true });
      }
      return;
    }
    if (requestURL.pathname === "/api/agents") {
      if (mode === "error") sendJSON(response, 503, { error: "fixture agents unavailable" });
      else sendJSON(response, 200, buildFixture(mode).agents);
      return;
    }
    if (requestURL.pathname === "/api/services") {
      if (mode === "error" || mode === "partial") sendJSON(response, 503, { error: "fixture services unavailable" });
      else sendJSON(response, 200, buildFixture(mode).groups);
      return;
    }
    if (requestURL.pathname === "/api/link-checks") {
      if (request.method === "POST" && request.headers["x-csrf-token"] !== "fixture-csrf") {
        sendJSON(response, 403, { error: "fixture csrf" });
      } else if (request.method !== "GET" && request.method !== "POST") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
      } else if (mode === "error") {
        sendJSON(response, 503, { error: "fixture link checks unavailable" });
      } else {
        sendJSON(response, 200, buildLinkChecks(mode));
      }
      return;
    }
    if (requestURL.pathname === "/api/history") {
      if (mode === "error" || mode === "history-error") {
        sendJSON(response, 503, { error: "fixture history unavailable" });
      } else {
        const history = buildHistory(mode, requestURL.searchParams.get("agent") || "");
        if (!history) sendJSON(response, 404, { error: "fixture unknown agent" });
        else sendJSON(response, 200, history);
      }
      return;
    }
    if (requestURL.pathname === "/api/events") {
      if (request.method !== "GET") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
      } else if (mode === "error" || mode === "events-error") {
        sendJSON(response, 503, { error: "fixture events unavailable" });
      } else {
        sendJSON(response, 200, buildEvents(mode, requestURL.searchParams.get("agent") || ""));
      }
      return;
    }
    if (requestURL.pathname === "/api/events/ack") {
      if (request.method !== "POST") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
        return;
      }
      if (request.headers["x-csrf-token"] !== "fixture-csrf") {
        sendJSON(response, 403, { error: "fixture csrf" });
        return;
      }
      const id = requestURL.searchParams.get("id") || "";
      const event = buildEvents(mode).events.find((candidate) => candidate.id === id);
      if (!event) {
        sendJSON(response, 404, { error: "fixture unknown event" });
      } else if (event.state === "resolved") {
        sendJSON(response, 409, { error: "fixture event resolved" });
      } else {
        acknowledgedFixtureEvents.add(id);
        const updated = buildEvents(mode).events.find((candidate) => candidate.id === id);
        sendJSON(response, 200, updated);
      }
      return;
    }
    if (requestURL.pathname === "/api/actions") {
      if (request.method !== "GET") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
      } else if (mode === "error" || mode === "actions-error") {
        sendJSON(response, 503, { error: "fixture actions unavailable" });
      } else {
        const actions = buildActions(mode, requestURL.searchParams.get("agent") || "");
        if (!actions) sendJSON(response, 404, { error: "fixture unknown agent" });
        else sendJSON(response, 200, actions);
      }
      return;
    }
    if (requestURL.pathname === "/api/actions/execute") {
      if (request.method !== "POST") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
        return;
      }
      if (request.headers["x-csrf-token"] !== "fixture-csrf") {
        sendJSON(response, 403, { error: "fixture csrf" });
        return;
      }
      const input = await readJSONBody(request);
      const capabilities = buildActions(mode, input.agentId);
      const action = capabilities?.actions.find((candidate) => candidate.id === input.actionId);
      if (!action) {
        sendJSON(response, 404, { error: "fixture action not approved" });
        return;
      }
      if (input.confirmation !== action.confirmation) {
        sendJSON(response, 422, { error: "fixture confirmation mismatch" });
        return;
      }
      const finishedAt = new Date(Date.parse("2026-08-08T00:00:02Z") + fixtureOperations.length * 1000).toISOString();
      const operation = {
        id: `op_fixture_live_${fixtureOperations.length + 1}`,
        agentId: input.agentId,
        targetKey: action.targetKey,
        kind: action.kind,
        state: "succeeded",
        requestedBy: "session:fixture123456789",
        requestedAt: "2026-08-08T00:00:00Z",
        startedAt: "2026-08-08T00:00:01Z",
        finishedAt,
        resultSummary: action.kind === "logs" ? "返回 2 行有界脱敏日志" : "Gateway：running → running",
      };
      fixtureOperations.unshift(operation);
      sendJSON(response, 200, {
        operation,
        result: {
          ok: true,
          actionId: action.id,
          targetKey: action.targetKey,
          kind: action.kind,
          ...(action.kind === "logs"
            ? { summary: operation.resultSummary, logs: ["2026-08-08T00:00:00Z gateway ready", "2026-08-08T00:00:01Z health check passed"] }
            : { stateBefore: "running", stateAfter: "running", summary: operation.resultSummary }),
        },
      });
      return;
    }
    if (requestURL.pathname === "/api/deployments") {
      if (request.method !== "GET") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
      } else if (mode === "error" || mode === "deployments-error") {
        sendJSON(response, 503, { error: "fixture deployments unavailable" });
      } else {
        const deployments = buildDeployments(mode, requestURL.searchParams.get("agent") || "");
        if (!deployments) sendJSON(response, 404, { error: "fixture unknown agent" });
        else sendJSON(response, 200, deployments);
      }
      return;
    }
    if (requestURL.pathname === "/api/deployments/execute") {
      if (request.method !== "POST") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
        return;
      }
      if (request.headers["x-csrf-token"] !== "fixture-csrf") {
        sendJSON(response, 403, { error: "fixture csrf" });
        return;
      }
      const input = await readJSONBody(request);
      const capabilities = buildDeployments(mode, input.agentId);
      const deployment = capabilities?.deployments.find((candidate) => candidate.id === input.deploymentId);
      if (!deployment) {
        sendJSON(response, 404, { error: "fixture deployment not approved" });
        return;
      }
      if (input.confirmation !== deployment.confirmation) {
        sendJSON(response, 422, { error: "fixture confirmation mismatch" });
        return;
      }
      const operation = {
        id: `op_fixture_deployment_${fixtureOperations.length + 1}`,
        agentId: input.agentId,
        targetKey: deployment.stackKey,
        kind: deployment.kind,
        state: "running",
        requestedBy: "session:fixture123456789",
        requestedAt: "2026-08-08T00:00:00Z",
        startedAt: "2026-08-08T00:00:01Z",
      };
      fixtureOperations.unshift(operation);
      fixtureDeploymentPending.set(operation.id, 0);
      sendJSON(response, 202, { operation });
      return;
    }
    if (requestURL.pathname === "/api/operations") {
      if (request.method !== "GET") {
        sendJSON(response, 405, { error: "fixture method not allowed" });
      } else if (mode === "error" || mode === "operations-error") {
        sendJSON(response, 503, { error: "fixture operations unavailable" });
      } else {
        settleFixtureDeployments();
        const agentID = requestURL.searchParams.get("agent") || "";
        sendJSON(response, 200, {
          ...(agentID ? { agentId: agentID } : {}),
          operations: mode === "empty"
            ? []
            : agentID
              ? fixtureOperations.filter((operation) => operation.agentId === agentID)
              : fixtureOperations,
        });
      }
      return;
    }
    if (requestURL.pathname === "/api/annotation" && request.method === "POST") {
      sendJSON(response, 200, { ok: true });
      return;
    }
    if (requestURL.pathname === "/api/logout" && request.method === "POST") {
      sendJSON(response, 200, { authed: false });
      return;
    }
    await sendAsset(response, requestURL.pathname);
  } catch (error) {
    sendJSON(response, 500, { error: error instanceof Error ? error.message : "fixture failure" });
  }
});

server.listen(listenPort, listenHost, () => {
  process.stdout.write(`Lodge Web fixture listening on http://${listenHost}:${listenPort}\n`);
});

function shutdown() {
  server.close(() => process.exit(0));
}

process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);

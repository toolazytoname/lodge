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
        agentVersion: "0.4.1",
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

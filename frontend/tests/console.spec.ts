import { expect, test, type Page } from "@playwright/test";

const browserErrors = new WeakMap<Page, string[]>();

async function expectNoHorizontalOverflow(page: Page): Promise<void> {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth);
}

async function openServices(page: Page): Promise<void> {
  await page.getByRole("button", { name: "服务 Services", exact: true }).click();
  await expect(page).toHaveURL(/#services$/);
}

test.describe("Lodge Web console", () => {
  test.beforeEach(async ({ page }) => {
    const errors: string[] = [];
    browserErrors.set(page, errors);
    page.on("console", (message) => {
      const expectedHTTPFailure = message.text().includes("status of 503 (Service Unavailable)")
        || message.text().includes("status of 401 (Unauthorized)");
      if (message.type() === "error" && !expectedHTTPFailure) errors.push(message.text());
    });
    page.on("pageerror", (error) => errors.push(error.message));
    await page.clock.setFixedTime(new Date("2026-08-08T00:00:00+08:00"));
    const reset = await page.request.post("/__fixture/reset");
    expect(reset.ok()).toBeTruthy();
  });

  test.afterEach(async ({ page }) => {
    expect(browserErrors.get(page) ?? []).toEqual([]);
  });

  test("desktop inventory, risk, search, and safe annotation flow", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/?fixture=normal");

    await expect(page.getByRole("heading", { name: "全局状态" })).toBeVisible();
    await expect(page.locator("#hostPreview .host-card")).toHaveCount(5);
    await expect(page.locator("#overviewMetrics .metric-value").nth(1)).toHaveText("55");
    await expect(page.locator("#overviewMetrics .metric-detail").nth(2)).toHaveText("7/8 Hub 可达");
    await expect(page.locator("#riskSignals .signal-row")).toHaveCount(4);
    await expectNoHorizontalOverflow(page);

    await page.getByRole("button", { name: "检查入口", exact: true }).click();
    await expect(page.locator("#notice")).toContainText("7/8 可达");

    await openServices(page);
    await expect(page.locator(".service-row")).toHaveCount(55);
    await expect(page.locator(".service-row.attention")).toHaveCount(4);
    await expect(page.locator(".service-row").nth(3)).toHaveClass(/attention/);
    await expect(page.locator(".service-row").nth(4)).not.toHaveClass(/attention/);

    const search = page.getByLabel("搜索");
    await search.fill("certbot");
    await expect(page.locator(".service-row")).toHaveCount(1);
    await expect(page.locator("#serviceResultCount")).toHaveText("1 / 55 项");

    await page.getByRole("button", { name: "编辑资料", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "编辑 certbot 资料" });
    await expect(dialog).toBeVisible();
    await expect(page.locator(":focus")).toHaveAttribute("id", "annotationAlias");
    await page.getByLabel("首选 Web 入口").fill("ssh://example.test");
    await page.getByRole("button", { name: "保存配置", exact: true }).click();
    await expect(page.getByLabel("首选 Web 入口")).toHaveJSProperty(
      "validationMessage",
      "请输入不含用户名或密码的 http(s) 地址。",
    );
    await page.getByRole("button", { name: "取消", exact: true }).click();
    await search.fill("");
    await expect(page.locator(".service-row")).toHaveCount(55);

    await search.fill("quota.example.test");
    await expect(page.locator(".service-row")).toHaveCount(1);
    await page.getByText("全部 4 个入口", { exact: true }).click();
    await expect(page.locator(".route-list")).toContainText("quota.example.test");
    await expect(page.locator(".route-list")).toContainText("静态站点");
    await expect(page.locator(".route-list")).toContainText("反向代理");
    await expect(page.locator(".route-list")).toContainText("受保护");
    await expect(page.locator(".route-item")).toHaveCount(4);
    await search.fill("");
    await expectNoHorizontalOverflow(page);
    await expect(page).toHaveScreenshot("services-1280.png", { fullPage: true });
  });

  test("390px mobile keeps all five pages and a usable service catalog", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/?fixture=normal#services");

    await expect(page.locator("[data-page]")).toHaveCount(5);
    await expect(page.locator(".service-row")).toHaveCount(55);
    await expect(page.locator('[data-page="operations"]')).toBeAttached();
    await expectNoHorizontalOverflow(page);
    await expect(page).toHaveScreenshot("services-390.png", { fullPage: true });
  });

  test("1920px overview remains dense without stretching beyond its frame", async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.goto("/?fixture=normal#overview");

    await expect(page.locator("#overviewMetrics .metric-card")).toHaveCount(4);
    await expect(page.locator("#quickLinks .quick-link")).toHaveCount(7);
    await expectNoHorizontalOverflow(page);
    await expect(page).toHaveScreenshot("overview-1920.png", { fullPage: true });
  });

  test("security page renders bounded durable history and host switching", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("/?fixture=normal#security");

    await expect(page.getByRole("heading", { name: "最近观测趋势" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "SSH 与防护基线" })).toBeVisible();
    await expect(page.locator("#accessPosture .security-posture-card")).toHaveCount(5);
    await expect(page.locator("#accessPosture")).toContainText("密码登录");
    await expect(page.locator("#historyTrends .history-trend-card")).toHaveCount(4);
    await expect(page.locator("#historySummary")).toContainText("100.0% 在线");
    await expect(page.locator("#historySummary")).toContainText("120 个观测点");
    await expect(page.getByRole("heading", { name: "事件中心" })).toBeVisible();
    await expect(page.locator("#eventList .event-row")).toHaveCount(4);
    await expect(page.locator("#eventSummary")).toContainText("4 进行中");
    await expect(page.locator("#eventSummary")).toContainText("3 待确认");
    await expect(page.locator("#eventSummary")).toContainText("已加载 4 / 4");
    await expect(page.locator("#eventList")).toContainText("203.0.113.44 × 61");
    await expect(page.locator("#eventList")).toContainText("SSH 爆破");

    await page.getByRole("button", { name: "确认事件：服务失败：certbot" }).click();
    await expect(page.locator("#notice")).toContainText("风险会保持进行中");
    await expect(page.locator("#eventSummary")).toContainText("2 待确认");
    await expect(page.locator("#eventList .event-row.acknowledged")).toHaveCount(2);

    const eventRequests: string[] = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname === "/api/events") eventRequests.push(`${url.pathname}${url.search}`);
    });
    await page.locator("#eventStateFilter").selectOption("resolved");
    await expect.poll(() => eventRequests.some((path) => path.includes("state=resolved"))).toBeTruthy();
    await expect(page.locator("#eventList .event-row")).toHaveCount(2);
    await expect(page.locator("#eventList")).toContainText("新增公网绑定：8443/tcp");
    await expect(page.locator("#eventSummary")).toContainText("4 进行中");
    await page.locator("#eventStateFilter").selectOption("ongoing");

    await page.locator("#historyAgent").selectOption("east");
    await expect(page.locator("#historySummary")).toContainText("97.5% 在线");
    await expect(page.locator("#historyIncidents")).toContainText("失败服务峰值 1");
    await expectNoHorizontalOverflow(page);
    await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
    await expect(page).toHaveScreenshot("security-history-1280.png", { fullPage: true });

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator("#historyTrends .history-trend-card")).toHaveCount(4);
    await expectNoHorizontalOverflow(page);
    // Linux and macOS round the complete posture-card stack differently. Pin
    // only the full-page canvas so both still compare every rendered pixel;
    // this never crops content or relaxes the visual-diff threshold.
    await page.addStyleTag({ content: "html, body { min-height: 3323px !important; }" });
    await expect(page).toHaveScreenshot("security-history-390.png", { fullPage: true });
  });

  test("operations page gates actions and asynchronous immutable releases", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const operationAuditRequests: string[] = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname === "/api/operations") operationAuditRequests.push(`${url.pathname}${url.search}`);
    });
    await page.goto("/?fixture=normal#operations");

    await expect(page.getByRole("heading", { name: "已批准动作" })).toBeVisible();
    await expect(page.locator("#actionList .action-row")).toHaveCount(3);
    await expect(page.getByRole("heading", { name: "已批准发布" })).toBeVisible();
    await expect(page.locator("#deploymentList .deployment-row")).toHaveCount(2);
    await expect(page.locator("#deploymentList")).toContainText("sha256:222222222222…");
    await expect(page.getByRole("heading", { name: "North 操作记录" })).toBeVisible();
    await expect(page.locator("#operationAudit .operation-row")).toHaveCount(1);
    await expect(page.locator("#operationsMetrics .metric-value")).toHaveText(["5", "2", "0", "0"]);
    await page.getByRole("button", { name: "读取日志 Gateway", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "读取日志 Gateway" });
    await expect(dialog).toBeVisible();
    await expect(dialog.locator("#actionConfirmationPhrase")).toHaveText("确认读取日志 Gateway");
    const confirmation = dialog.getByLabel("确认短语");
    const execute = dialog.getByRole("button", { name: "确认执行", exact: true });
    await expect(execute).toBeDisabled();
    await confirmation.fill("确认读取日志");
    await expect(execute).toBeDisabled();
    await confirmation.fill("确认读取日志 Gateway");
    await expect(execute).toBeEnabled();
    await execute.click();

    await expect(dialog.locator("#actionResultSummary")).toContainText("动作完成");
    await expect(dialog.locator("#actionResultLogs")).toContainText("gateway ready");
    await expect(dialog.locator("#actionLogNotice")).toContainText("关闭后即清除");
    await expect(page.locator("#operationAudit .operation-row")).toHaveCount(2);
    await expect(page.locator("#operationAudit .operation-row").first()).toContainText("读取日志");
    await expectNoHorizontalOverflow(page);
    // Preserve the complete page while neutralizing platform font-metric
    // rounding (Linux is six pixels shorter than macOS for this fixture).
    await page.addStyleTag({ content: "html, body { min-height: 1765px !important; }" });
    await expect(page).toHaveScreenshot("operations-result-1280.png", { fullPage: true });

    await dialog.locator("#cancelActionBtn").click();
    await expect(dialog).not.toBeVisible();
    await expect(page.locator("#actionResultLogs")).toHaveText("");

    await page.getByRole("button", { name: "部署 Gateway 到 Version 2", exact: true }).click();
    const deploymentDialog = page.getByRole("dialog", { name: "部署 Gateway 到 Version 2" });
    await expect(deploymentDialog.locator("#actionConfirmationPhrase")).toHaveText("确认部署 Gateway 到 Version 2");
    const deploymentConfirmation = deploymentDialog.getByLabel("确认短语");
    const deploy = deploymentDialog.getByRole("button", { name: "确认发布", exact: true });
    await deploymentConfirmation.fill("确认部署 Gateway");
    await expect(deploy).toBeDisabled();
    await deploymentConfirmation.fill("确认部署 Gateway 到 Version 2");
    await expect(deploy).toBeEnabled();
    await deploy.click();
    await expect(deploymentDialog.locator("#actionResultSummary")).toContainText("发布成功");
    await expect(page.locator("#operationAudit .operation-row").first()).toContainText("部署");
    await expect(page.locator("#operationAudit .operation-row").first()).toContainText("成功");
    await expect(page.locator("#operationAudit .operation-row").first()).toContainText("sha256:222222222222…");
    await expect(page.locator("#notice")).toContainText("健康验证已通过");
    await expect(page.locator("#operationAudit .operation-row")).toHaveCount(3);
    await deploymentDialog.locator("#cancelActionBtn").click();
    await expect(deploymentDialog).not.toBeVisible();

    await page.locator("#actionAgent").selectOption("south");
    await expect(page.getByRole("heading", { name: "South 操作记录" })).toBeVisible();
    await expect(page.locator("#operationAudit .operation-row")).toHaveCount(1);
    await expect(page.locator("#operationAudit")).toContainText("已回滚");
    await expect(page.locator("#operationsMetrics .metric-value")).toHaveText(["5", "2", "0", "1"]);
    await expect.poll(() => operationAuditRequests).toContain("/api/operations?agent=south&limit=100");
    await page.locator("#actionAgent").selectOption("north");
    await expect(page.locator("#operationAudit .operation-row")).toHaveCount(3);
    await expect.poll(() => operationAuditRequests).toContain("/api/operations?agent=north&limit=100");

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator("#actionList .action-row")).toHaveCount(3);
    await expectNoHorizontalOverflow(page);
    await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
    await page.addStyleTag({ content: "html, body { min-height: 2548px !important; }" });
    await expect(page).toHaveScreenshot("operations-390.png", { fullPage: true });
  });

  test("empty, offline, partial, and total-error fixtures stay truthful", async ({ page }) => {
    const failedAPIs: string[] = [];
    page.on("response", (response) => {
      if (response.status() === 503) failedAPIs.push(new URL(response.url()).pathname);
    });
    await page.setViewportSize({ width: 1280, height: 800 });

    await page.goto("/?fixture=empty#overview");
    await expect(page.locator("#overviewMetrics .metric-value").first()).toHaveText("0/0");
    await expect(page.locator("#hostPreview")).toContainText("尚未纳管主机");
    await openServices(page);
    await expect(page.locator("#serviceResultCount")).toHaveText("0 / 0 项");
    await expect(page.locator("#serviceDirectory")).toContainText("没有符合当前条件的服务");

    await page.goto("/?fixture=offline#overview");
    await expect(page.locator("#overviewMetrics .metric-value").first()).toHaveText("4/5");
    await expect(page.locator("#riskSignals")).toContainText("fixture: agent connection timed out");
    await expect(page).toHaveScreenshot("overview-offline-1280.png", { fullPage: true });

    await page.goto("/?fixture=partial#overview");
    await expect(page.locator("#notice")).toContainText("部分数据更新失败");
    await expect(page.locator("#hostPreview .host-card")).toHaveCount(5);
    await expect(page.locator("#overviewMetrics .metric-value").nth(1)).toHaveText("N/A");
    await openServices(page);
    await expect(page.locator("#serviceDirectory")).toContainText("服务数据暂时不可用");

    await page.goto("/?fixture=error#overview");
    await expect(page.locator("#notice")).toContainText("控制台数据加载失败");
    await expect(page.locator("#overviewMetrics .metric-value")).toHaveText(["N/A", "N/A", "N/A", "N/A"]);
    await expect(page.locator("#hostPreview")).toContainText("主机数据暂时不可用");
    await expect(page).toHaveScreenshot("overview-error-1280.png", { fullPage: true });

    await page.goto("/?fixture=events-error#security");
    await expect(page.locator("#eventSummary")).toContainText("事件数据暂时不可用");
    await expect(page.locator("#historyTrends .history-trend-card")).toHaveCount(4);
    await expect(page.locator("#publicSurface .surface-row")).toHaveCount(12);

    await page.goto("/?fixture=history-error#security");
    await expect(page.locator("#historySummary")).toContainText("历史数据暂时不可用");
    await expect(page.locator("#historyTrends .history-trend-card")).toHaveCount(0);
    await expect(page.locator("#eventList .event-row")).toHaveCount(4);

    await page.goto("/?fixture=deployments-error#operations");
    await expect(page.locator("#actionList .action-row")).toHaveCount(3);
    await expect(page.locator("#deploymentList")).toContainText("发布策略暂时不可用");
    await expect(page.locator("#operationAudit .operation-row")).toHaveCount(1);
    expect(failedAPIs.sort()).toEqual(["/api/agents", "/api/deployments", "/api/events", "/api/events", "/api/history", "/api/link-checks", "/api/operations", "/api/services", "/api/services"]);
  });

  test("expired session closes an open operation dialog before showing login", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/?fixture=normal#operations");
    await page.getByRole("button", { name: "读取日志 Gateway", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "读取日志 Gateway" });
    await expect(dialog).toBeVisible();
    await dialog.getByLabel("确认短语").fill("确认读取日志 Gateway");
    await page.route("**/api/actions/execute", async (route) => {
      await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "unauthorized" }) });
    });
    await dialog.getByRole("button", { name: "确认执行", exact: true }).click();
    await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
    await expect(dialog).not.toBeVisible();
  });

  test("event filters ignore stale overlapping responses", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/?fixture=normal#security");
    await expect(page.locator("#eventList .event-row")).toHaveCount(4);
    let releaseOld: () => void = () => {};
    let markOld: () => void = () => {};
    const held = new Promise<void>((resolve) => { releaseOld = resolve; });
    const seen = new Promise<void>((resolve) => { markOld = resolve; });
    await page.route("**/api/events?**", async (route) => {
      const url = new URL(route.request().url());
      const response = await route.fetch();
      if (url.searchParams.get("state") === "resolved") {
        markOld();
        await held;
      }
      await route.fulfill({ response });
    });
    await page.locator("#eventStateFilter").selectOption("resolved");
    await seen;
    await page.locator("#eventStateFilter").selectOption("ongoing");
    await expect(page.locator("#eventList .event-row")).toHaveCount(4);
    releaseOld();
    await page.waitForTimeout(200);
    await expect(page.locator("#eventStateFilter")).toHaveValue("ongoing");
    await expect(page.locator("#eventList .event-row.resolved")).toHaveCount(0);
    await expect(page.locator("#eventList .event-row")).toHaveCount(4);
  });

  test("event list can page through more than twenty matching incidents", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("/?fixture=many-events#security");
    await expect(page.locator("#eventSummary")).toContainText("125 进行中");
    await expect(page.locator("#eventSummary")).toContainText("已加载 50 / 125");
    await expect(page.locator("#eventList .event-row")).toHaveCount(50);
    await page.getByRole("button", { name: "加载更多", exact: true }).click();
    await expect(page.locator("#eventList .event-row")).toHaveCount(100);
    await page.getByRole("button", { name: "加载更多", exact: true }).click();
    await expect(page.locator("#eventList .event-row")).toHaveCount(125);
    await expect(page.locator("#eventSummary")).toContainText("已加载 125 / 125");
    await expect(page.getByRole("button", { name: "加载更多", exact: true })).toHaveCount(0);
    const titles = await page.locator("#eventList .event-row strong").allTextContents();
    expect(new Set(titles).size).toBe(125);
    await page.getByRole("button", { name: "确认事件：服务失败：bulk-0" }).click();
    await expect(page.locator("#notice")).toContainText("风险会保持进行中");
  });

  test("interrupted load-more recovers and can finish the list", async ({ page }) => {
    const scenarios: Array<{ name: string; interrupt: (tab: Page) => Promise<void> }> = [
      {
        name: "refresh",
        interrupt: async (tab) => {
          await tab.locator("#refreshBtn").click();
          await expect(tab.locator("#refreshBtn")).not.toBeDisabled();
        },
      },
      {
        name: "filter",
        interrupt: async (tab) => {
          await tab.locator("#eventStateFilter").selectOption("active");
          await expect(tab.locator("#eventStateFilter")).toHaveValue("active");
          await expect(tab.locator("#eventList .event-row")).toHaveCount(50);
        },
      },
      {
        name: "ack",
        interrupt: async (tab) => {
          await tab.getByRole("button", { name: "确认事件：服务失败：bulk-0" }).click();
          await expect(tab.locator("#notice")).toContainText("风险会保持进行中");
        },
      },
    ];
    for (const scenario of scenarios) {
      const reset = await page.request.post("/__fixture/reset");
      expect(reset.ok()).toBeTruthy();
      const tab = await page.context().newPage();
      await tab.setViewportSize({ width: 1280, height: 900 });
      await tab.clock.setFixedTime(new Date("2026-08-08T00:00:00+08:00"));
      await tab.goto("/?fixture=many-events#security");
      await expect(tab.locator("#eventList .event-row")).toHaveCount(50);
      let releaseAppend: () => void = () => {};
      let markAppend: () => void = () => {};
      const held = new Promise<void>((resolve) => { releaseAppend = resolve; });
      const seen = new Promise<void>((resolve) => { markAppend = resolve; });
      await tab.route("**/api/events?**", async (route) => {
        const url = new URL(route.request().url());
        const response = await route.fetch();
        if (url.searchParams.get("after")) {
          markAppend();
          await held;
        }
        await route.fulfill({ response });
      });
      await tab.getByRole("button", { name: "加载更多", exact: true }).click();
      await seen;
      await scenario.interrupt(tab);
      await expect(tab.getByRole("button", { name: "加载更多", exact: true })).toBeEnabled();
      releaseAppend();
      await tab.waitForTimeout(200);
      await expect(tab.getByRole("button", { name: "加载更多", exact: true })).toBeEnabled();
      await tab.getByRole("button", { name: "加载更多", exact: true }).click();
      await expect(tab.locator("#eventList .event-row")).toHaveCount(100);
      await tab.getByRole("button", { name: "加载更多", exact: true }).click();
      await expect(tab.locator("#eventList .event-row")).toHaveCount(125);
      await expect(tab.getByRole("button", { name: "加载更多", exact: true })).toHaveCount(0);
      await tab.close();
    }
  });

  test("switching event filters does not append the previous page", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("/?fixture=many-events#security");
    await expect(page.locator("#eventList .event-row")).toHaveCount(50);
    const requests: string[] = [];
    let releaseFilter: () => void = () => {};
    let markFilter: () => void = () => {};
    const held = new Promise<void>((resolve) => { releaseFilter = resolve; });
    const seen = new Promise<void>((resolve) => { markFilter = resolve; });
    await page.route("**/api/events?**", async (route) => {
      const url = new URL(route.request().url());
      requests.push(url.search);
      const response = await route.fetch();
      if (url.searchParams.get("agent") === "east" && !url.searchParams.get("after") && !url.searchParams.get("snapshot")) {
        markFilter();
        await held;
      }
      await route.fulfill({ response });
    });
    await page.locator("#eventAgentFilter").selectOption("east");
    await seen;
    await expect(page.getByRole("button", { name: "加载更多", exact: true })).toHaveCount(0);
    expect(requests.filter((search) => search.includes("agent=east") && (search.includes("after=") || search.includes("offset=")))).toEqual([]);
    releaseFilter();
    await expect(page.locator("#eventSummary")).toContainText("已加载 0 / 0");
    await expect(page.locator("#eventList .event-row")).toHaveCount(0);
    await expect(page.locator("#eventAgentFilter")).toHaveValue("east");
  });

  test("refreshing more than five hundred events uses legal page sizes", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const requestedLimits: number[] = [];
    const catalog = Array.from({ length: 550 }, (_, index) => ({
      id: `bulk_${index}`,
      agentId: "harbor",
      kind: "workload.failed",
      severity: "critical",
      state: "active",
      title: `Bulk ${index}`,
      detail: "bulk incident",
      firstObservedAt: "2026-08-07T23:00:00Z",
      lastObservedAt: "2026-08-08T00:00:00Z",
    }));
    const snapshots = new Map<string, string[]>();
    let snapshotSeq = 0;
    await page.route("**/api/events?**", async (route) => {
      const url = new URL(route.request().url());
      const limit = Number(url.searchParams.get("limit") || 50);
      requestedLimits.push(limit);
      if (limit > 500) {
        await route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({ error: "event limit must be between 1 and 500" }),
        });
        return;
      }
      let snapshot = url.searchParams.get("snapshot") || "";
      const after = url.searchParams.get("after") || "";
      let ids = snapshot ? snapshots.get(snapshot) : undefined;
      if (!ids) {
        snapshotSeq += 1;
        snapshot = `snap_${snapshotSeq.toString(16).padStart(32, "0")}`;
        ids = catalog.map((event) => event.id);
        snapshots.set(snapshot, ids);
      }
      const byID = new Map(catalog.map((event) => [event.id, event]));
      let start = 0;
      if (after) {
        const index = ids.indexOf(after);
        start = index >= 0 ? index + 1 : ids.length;
      }
      const pageIDs = ids.slice(start, start + limit);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          events: pageIDs.map((id) => byID.get(id)),
          ongoingCount: 550,
          activeCount: 550,
          criticalCount: 550,
          resolvedCount: 0,
          matchedCount: ids.length,
          snapshot,
          hasMore: start + pageIDs.length < ids.length,
          offset: start,
          limit,
        }),
      });
    });
    await page.goto("/?fixture=normal#security");
    await expect(page.locator("#eventList .event-row")).toHaveCount(50);
    for (let count = 100; count <= 550; count += 50) {
      await page.getByRole("button", { name: "加载更多", exact: true }).click();
      await expect(page.locator("#eventList .event-row")).toHaveCount(count);
    }
    await expect(page.locator("#eventSummary")).toContainText("已加载 550 / 550");
    await page.locator("#refreshBtn").click();
    await expect(page.locator("#eventSummary")).toContainText("已加载 550 / 550");
    await expect(page.locator("#eventSummary")).not.toContainText("最近更新失败");
    await expect(page.locator("#eventList .event-row")).toHaveCount(550);
    expect(requestedLimits.length).toBeGreaterThan(0);
    expect(requestedLimits.every((limit) => limit >= 1 && limit <= 500)).toBeTruthy();
  });

  test("late action results cannot restore logs after logout or relogin", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/?fixture=normal#operations");
    let releaseAction: () => void = () => {};
    let markAction: () => void = () => {};
    const held = new Promise<void>((resolve) => { releaseAction = resolve; });
    const seen = new Promise<void>((resolve) => { markAction = resolve; });
    await page.route("**/api/actions/execute", async (route) => {
      const response = await route.fetch();
      markAction();
      await held;
      await route.fulfill({ response });
    });
    await page.getByRole("button", { name: "读取日志 Gateway", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "读取日志 Gateway" });
    await dialog.getByLabel("确认短语").fill("确认读取日志 Gateway");
    await dialog.getByRole("button", { name: "确认执行", exact: true }).click();
    await seen;
    await page.route("**/api/events?**", (route) => route.fulfill({
      status: 401, contentType: "application/json", body: JSON.stringify({ error: "unauthorized" }),
    }));
    await page.evaluate(() => (document.querySelector("#refreshBtn") as HTMLButtonElement).click());
    await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
    await expect(dialog).not.toBeVisible();
    await expect(page.locator("#actionResultLogs")).toHaveText("");
    await expect(page.locator("#hostPreview .host-card")).toHaveCount(0);
    await page.unroute("**/api/events?**");
    await page.getByLabel("访问密码").fill("fixture");
    await page.getByRole("button", { name: "进入控制台" }).click();
    await expect(page.locator("#login")).toHaveClass(/hidden/);
    await expect(page.getByRole("heading", { name: "运维中心" })).toBeVisible();
    releaseAction();
    await page.waitForTimeout(200);
    await expect(page.locator("#actionResultLogs")).toHaveText("");
    await expect(page.getByRole("dialog", { name: "读取日志 Gateway" })).not.toBeVisible();
  });

  test("stale action responses cannot unlock a newer execution", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("/?fixture=normal#operations");
    let releaseFirst: () => void = () => {};
    let releaseSecond: () => void = () => {};
    let markFirst: () => void = () => {};
    let markSecond: () => void = () => {};
    const firstSeen = new Promise<void>((resolve) => { markFirst = resolve; });
    const secondSeen = new Promise<void>((resolve) => { markSecond = resolve; });
    const firstHeld = new Promise<void>((resolve) => { releaseFirst = resolve; });
    const secondHeld = new Promise<void>((resolve) => { releaseSecond = resolve; });
    let calls = 0;
    await page.route("**/api/actions/execute", async (route) => {
      const index = calls;
      calls += 1;
      const response = await route.fetch();
      if (index === 0) {
        markFirst();
        await firstHeld;
      } else if (index === 1) {
        markSecond();
        await secondHeld;
      }
      await route.fulfill({ response });
    });
    async function submit(): Promise<void> {
      await page.getByRole("button", { name: "读取日志 Gateway", exact: true }).click();
      await page.locator("#actionConfirmation").fill("确认读取日志 Gateway");
      await page.locator("#executeActionBtn").click();
    }
    await submit();
    await firstSeen;
    await page.route("**/api/events?**", (route) => route.fulfill({
      status: 401, contentType: "application/json", body: JSON.stringify({ error: "unauthorized" }),
    }));
    await page.evaluate(() => (document.querySelector("#refreshBtn") as HTMLButtonElement).click());
    await expect(page.locator("#login")).not.toHaveClass(/hidden/);
    await expect(page.locator("#refreshBtn")).not.toBeDisabled();
    await page.unroute("**/api/events?**");
    await page.getByLabel("访问密码").fill("fixture");
    await page.getByRole("button", { name: "进入控制台" }).click();
    await expect(page.locator("#login")).toHaveClass(/hidden/);
    await expect(page.getByRole("heading", { name: "运维中心" })).toBeVisible();
    await expect(page.getByRole("button", { name: "读取日志 Gateway", exact: true })).toBeVisible();
    await submit();
    await secondSeen;
    await expect(page.locator("#executeActionBtn")).toBeDisabled();
    await expect(page.locator("#executeActionBtn")).toHaveText("执行中");
    releaseFirst();
    await page.waitForTimeout(200);
    await expect(page.locator("#executeActionBtn")).toBeDisabled();
    await expect(page.locator("#executeActionBtn")).toHaveText("执行中");
    await expect(page.locator("#actionResult")).toHaveClass(/hidden/);
    releaseSecond();
    await expect(page.locator("#actionResult")).not.toHaveClass(/hidden/);
    await expect(page.locator("#actionResultSummary")).toContainText("动作完成");
  });
});

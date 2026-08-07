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
      const expectedHTTPFailure = message.text().includes("status of 503 (Service Unavailable)");
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
    await expect(page.locator("#overviewMetrics .metric-detail").nth(2)).toHaveText("5/5 Hub 可达");
    await expect(page.locator("#riskSignals .signal-row")).toHaveCount(4);
    await expectNoHorizontalOverflow(page);

    await page.getByRole("button", { name: "检查入口", exact: true }).click();
    await expect(page.locator("#notice")).toContainText("5/5 可达");

    await openServices(page);
    await expect(page.locator(".service-row")).toHaveCount(55);
    await expect(page.locator(".service-row.attention")).toHaveCount(4);
    await expect(page.locator(".service-row").nth(3)).toHaveClass(/attention/);
    await expect(page.locator(".service-row").nth(4)).not.toHaveClass(/attention/);

    const search = page.getByLabel("搜索");
    await search.fill("certbot");
    await expect(page.locator(".service-row")).toHaveCount(1);
    await expect(page.locator("#serviceResultCount")).toHaveText("1 / 55 项");

    await page.getByRole("button", { name: "管理", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "管理 certbot" });
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
    await expect(page.locator("#quickLinks .quick-link")).toHaveCount(5);
    await expectNoHorizontalOverflow(page);
    await expect(page).toHaveScreenshot("overview-1920.png", { fullPage: true });
  });

  test("security page renders bounded durable history and host switching", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("/?fixture=normal#security");

    await expect(page.getByRole("heading", { name: "最近观测趋势" })).toBeVisible();
    await expect(page.locator("#historyTrends .history-trend-card")).toHaveCount(4);
    await expect(page.locator("#historySummary")).toContainText("100.0% 在线");
    await expect(page.locator("#historySummary")).toContainText("120 个观测点");
    await expect(page.getByRole("heading", { name: "事件中心" })).toBeVisible();
    await expect(page.locator("#eventList .event-row")).toHaveCount(4);
    await expect(page.locator("#eventSummary")).toContainText("4 进行中");
    await expect(page.locator("#eventSummary")).toContainText("3 待确认");

    await page.getByRole("button", { name: "确认事件：服务失败：certbot" }).click();
    await expect(page.locator("#notice")).toContainText("风险会保持进行中");
    await expect(page.locator("#eventSummary")).toContainText("2 待确认");
    await expect(page.locator("#eventList .event-row.acknowledged")).toHaveCount(2);

    await page.locator("#eventStateFilter").selectOption("resolved");
    await expect(page.locator("#eventList .event-row")).toHaveCount(2);
    await expect(page.locator("#eventList")).toContainText("新增公网绑定：8443/tcp");
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
    // Linux and macOS round the final mobile text line one pixel differently.
    // Pin only the visual-regression canvas so the snapshot still compares the
    // complete page without turning host font metrics into a false failure.
    await page.addStyleTag({ content: "html, body { min-height: 2211px !important; }" });
    await expect(page).toHaveScreenshot("security-history-390.png", { fullPage: true });
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
    expect(failedAPIs.sort()).toEqual(["/api/agents", "/api/events", "/api/events", "/api/history", "/api/link-checks", "/api/services", "/api/services"]);
  });
});

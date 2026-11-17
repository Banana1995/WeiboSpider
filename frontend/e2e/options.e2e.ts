import {
  test,
  expect,
  type Page,
  type APIRequestContext,
} from "@playwright/test";
test.describe.configure({ mode: "serial" });
const endpoint = "/api/platform/options";
const fixture = "http://127.0.0.1:17053";
const calls = async (request: APIRequestContext) =>
  (await (await request.get(`${fixture}/test/counters`)).json())
    .quote_calls as number;
const body = async (request: APIRequestContext, path: string) => {
  const response = await request.get(endpoint + path);
  expect(response.ok()).toBeTruthy();
  return response.json();
};
async function close(page: Page) {
  await page.getByRole("button", { name: "关闭弹窗" }).click();
}
async function add(page: Page, query: string, name: string) {
  await page.getByRole("button", { name: "＋ 添加股票" }).click();
  await page.getByLabel("搜索股票").fill(query);
  await page.locator("[data-search-result]").filter({ hasText: name }).click();
}
async function refresh(page: Page) {
  await page
    .getByRole("button", { name: "更新行情", exact: true })
    .first()
    .click();
  await expect(page.getByText("行情更新完成", { exact: false })).toBeVisible();
}

test("manual quotes, exact fee inputs, comparison and frozen persistent favorites", async ({
  page,
  request,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/options");
  await expect(page.getByText("先添加愿意持有的股票")).toBeVisible();
  expect(await calls(request)).toBe(0);
  await add(page, "苹果", "苹果");
  await expect(
    page.getByRole("heading", { name: "AAPL · 苹果" }),
  ).toBeVisible();
  await expect(page.getByText("还没有这只股票的行情")).toBeVisible();
  await page
    .getByRole("button", { name: "重置筛选", exact: true })
    .first()
    .click();
  expect(await calls(request)).toBe(0);
  await refresh(page);
  // The zero-bid and expired rows never reach the chain.
  await expect(page.locator(".opt-table tbody tr")).toHaveCount(4);
  const initial = await body(
    request,
    "/candidates?symbol=US.AAPL&sort=strike&direction=desc",
  );
  const row = initial.items.find((x: any) => x.quote.contract.strike === "200");
  expect(row.result.income).toBe("345.00");
  expect(row.result.capital).toBe("20000.00");
  const target = page.getByRole("row", {
    name: `查看 ${row.quote.contract.code}`,
    exact: true,
  });
  await target
    .getByRole("button", {
      name: `收藏 ${row.quote.contract.code}`,
      exact: true,
    })
    .click();
  await expect(page.locator(".opt-header-actions")).toContainText("收藏 1");
  let saved = await body(request, "/favorites");
  const frozen = saved.items[0];
  expect(frozen.candidate.result.income).toBe("345.00");
  await page
    .getByRole("button", {
      name: `查看收藏 ${row.quote.contract.code}`,
      exact: true,
    })
    .click();
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("345.00");
  await close(page);
  await page.getByRole("button", { name: "净投入", exact: true }).click();
  await expect(
    page.getByRole("button", {
      name: `查看收藏 ${row.quote.contract.code}`,
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByLabel("已确认我的费用方案，计入预计费用").check();
  for (const [label, value] of [
    ["每份佣金", "0.5"],
    ["每单最低佣金", "2"],
    ["每单固定／平台费", "1"],
    ["每份监管／交易所费", "0.1"],
  ])
    await page.getByLabel(label!, { exact: true }).fill(value!);
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await expect(page.getByText("设置已保存", { exact: false })).toBeVisible();
  const settings = await body(request, "/settings");
  expect(settings.fees.open.per_contract).toBe("0.5");
  await target.click();
  await page.getByLabel("卖出份数").fill("2");
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("686.80");
  await page.getByLabel("成交价格假设").selectOption("custom");
  await page.getByLabel("每股权利金（USD）", { exact: true }).fill("4.125");
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("821.80");
  await page.getByLabel("另外测算现金利息").check();
  await page.getByLabel("假设年利率（%）").fill("3");
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("预计利息");
  await page.getByText("到期接货与提前平仓情景，选看", { exact: true }).click();
  await page.getByLabel("启用独立情景").check();
  await page.getByLabel("假设到期股价（USD）", { exact: true }).fill("190");
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("-1,178.20");
  await page.getByRole("button", { name: "收藏此记录", exact: true }).click();
  await expect(page.locator("dialog")).toContainText("已收藏");
  await close(page);
  saved = await body(request, "/favorites");
  expect(saved.total).toBe(2);
  expect(saved.items.find((x: any) => x.id === frozen.id)).toEqual(frozen);
  const checkboxes = page.locator(".opt-table input[type=checkbox]:enabled");
  await checkboxes.nth(0).check();
  await checkboxes.nth(1).check();
  await page
    .getByRole("button", { name: "比较 2 份合约", exact: true })
    .click();
  await expect(page.locator(".opt-compare-grid>section")).toHaveCount(2);
  await close(page);
  const before = await calls(request);
  // Day-range inputs sit in the first filter row and filter server-side.
  await page.getByLabel("最多剩余天数").fill("40");
  await expect(page.locator(".opt-table tbody tr")).toHaveCount(2);
  await page.getByLabel("最少剩余天数").fill("40");
  await expect(page.getByText("没有符合当前条件的合约")).toBeVisible();
  await page
    .getByRole("button", { name: "重置筛选", exact: true })
    .first()
    .click();
  await page.getByLabel("最低权利金年化（%）").fill("999");
  await expect(page.getByText("没有符合当前条件的合约")).toBeVisible();
  await page
    .getByRole("button", { name: "重置筛选", exact: true })
    .first()
    .click();
  await page.clock.install();
  await page.clock.fastForward(6 * 60 * 1000);
  expect(await calls(request)).toBe(before);
  await request.post(`${fixture}/test/control`, { data: { shift: 1 } });
  await refresh(page);
  const changed = await body(request, "/candidates?symbol=US.AAPL");
  expect(
    changed.items.find((x: any) => x.quote.contract.strike === "200").quote.bid,
  ).toBe("4.45");
  expect(await body(request, `/favorites/${frozen.id}`)).toEqual(frozen);
  await page.reload();
  await expect(page.locator(".opt-header-actions")).toContainText("收藏 2");
  expect(errors).toEqual([]);
});

test("mobile Chinese search, focus style, zero bid and independent favorites after stock removal", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/options");
  await expect(page.locator(".opt-mobile-cards article")).toHaveCount(4);
  const before = await calls(request);
  // Temporary conditions must not leak across underlyings.
  await page.getByLabel("行权价上限（USD）").fill("360");
  await page.getByLabel("最低权利金年化（%）").fill("12");
  await page.getByText("更多条件与费用", { exact: true }).click();
  await page.getByLabel("价内／价外").selectOption("out");
  await page.getByRole("button", { name: "＋ 添加股票" }).click();
  const search = page.getByLabel("搜索股票");
  await search.fill("英伟达");
  await expect(page.locator("[data-search-result]")).toHaveCount(1);
  await search.focus();
  const focus = await search.evaluate((el) => ({
    border: getComputedStyle(el).borderTopWidth,
    outline: getComputedStyle(el).outlineStyle,
    shadow: getComputedStyle(el).boxShadow,
  }));
  expect(focus).toEqual({ border: "1px", outline: "none", shadow: "none" });
  await search.dispatchEvent("compositionstart");
  await search.press("Enter");
  await expect(page.getByRole("dialog")).toBeVisible();
  await search.dispatchEvent("compositionend");
  await page.locator("[data-search-result]").click();
  await expect(
    page.getByRole("heading", { name: "NVDA · 英伟达" }),
  ).toBeVisible();
  expect(await calls(request)).toBe(before);
  await expect(page.getByLabel("行权价上限（USD）")).toHaveValue("");
  await expect(page.getByLabel("最低权利金年化（%）")).toHaveValue("");
  await expect(page.getByLabel("价内／价外")).toHaveValue("");
  await refresh(page);
  // Expired and zero-bid contracts are excluded, but counted for audit.
  await expect(
    page.locator(".opt-contract-card").filter({ hasText: "140 Put" }),
  ).toHaveCount(0);
  await page.getByText("查看筛除原因", { exact: true }).click();
  await expect(page.getByText(/Bid 不可用：\d+ 份/)).toBeVisible();
  const tradeable = page.locator(".opt-contract-card").first();
  await tradeable.getByRole("button", { name: "☆ 收藏", exact: true }).click();
  await expect(
    tradeable.getByRole("button", { name: "★ 已收藏", exact: true }),
  ).toBeVisible();
  const advanced = page.locator(".opt-filters details.opt-details").first();
  if (!(await advanced.evaluate((el) => (el as HTMLDetailsElement).open))) {
    await advanced.locator("summary").click();
  }
  await page.getByRole("button", { name: "股票设置", exact: true }).click();
  await page.getByRole("button", { name: "移出股票池", exact: true }).click();
  await page.getByRole("button", { name: /^收藏/ }).first().click();
  await expect(
    page.locator(".opt-saved-item").filter({ hasText: "NVDA" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  expect(
    await page
      .locator("dialog")
      .evaluate((el) => el.scrollWidth <= el.clientWidth),
  ).toBeTruthy();
});

test("partial failure preserves previous snapshot, restart persists state and quote cache", async ({
  page,
  request,
}) => {
  await page.goto("/options");
  await add(page, "微软", "微软");
  await refresh(page);
  const before = (await body(request, "/candidates?symbol=US.MSFT"))
    .snapshots[0].id;
  await request.post(`${fixture}/test/control`, {
    data: { fail_symbol: "US.MSFT", shift: 2 },
  });
  await page.getByRole("button", { name: /全部自选/ }).click();
  await page
    .getByRole("button", { name: "更新行情", exact: true })
    .first()
    .click();
  await expect(page.getByText("本轮更新结束", { exact: false })).toBeVisible();
  expect((await body(request, "/status")).run.state).toBe("partial");
  expect(
    (await body(request, "/candidates?symbol=US.MSFT")).snapshots[0].id,
  ).toBe(before);
  const count = await calls(request),
    saved = await body(request, "/favorites");
  await request.post("http://127.0.0.1:17052/restart");
  await expect
    .poll(async () => {
      try {
        return (await request.get("http://127.0.0.1:17051/healthz")).ok();
      } catch {
        return false;
      }
    })
    .toBeTruthy();
  await page.reload();
  await expect(page.locator(".opt-header-actions")).toContainText(
    `收藏 ${saved.total}`,
  );
  expect(await body(request, "/favorites")).toEqual(saved);
  expect(await calls(request)).toBe(count);
  expect(
    (await body(request, "/candidates?symbol=US.MSFT")).snapshots[0].id,
  ).toBe(before);
});

test("covered Call view has independent quotes, capital, payoff and frozen favorites", async ({
  page,
  request,
}) => {
  await request.post(`${fixture}/test/control`, {
    data: { shift: 0, fail_symbol: "", fail_type: "" },
  });
  await page.goto("/options");
  await expect(
    page.getByRole("heading", { name: "AAPL · 苹果" }),
  ).toBeVisible();
  const putBefore = await body(
    request,
    "/candidates?symbol=US.AAPL&fee_version=0",
  );
  const savedBefore = await body(request, "/favorites");
  const countBefore = await calls(request);
  const countersBefore = await (
    await request.get(`${fixture}/test/counters`)
  ).json();
  await page.getByLabel("行权价上限（USD）").fill("200");
  await page.getByLabel("最低权利金年化（%）").fill("1");
  await page.getByRole("button", { name: "卖 Call", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "卖 Call", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByText("还没有这只股票的行情")).toBeVisible();
  await expect(page.getByLabel("行权价下限（USD）")).toHaveValue("");
  await expect(page.getByLabel("最低权利金年化（%）")).toHaveValue("");
  expect(await calls(request)).toBe(countBefore);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "卖 Call", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByText("还没有这只股票的行情")).toBeVisible();
  expect(await calls(request)).toBe(countBefore);
  await page.getByText("更多条件与费用", { exact: true }).click();
  await page.getByLabel("计入预计开仓费用", { exact: true }).uncheck();
  await refresh(page);
  await expect(page.locator(".opt-table tbody tr")).toHaveCount(4);
  const countersAfter = await (
    await request.get(`${fixture}/test/counters`)
  ).json();
  expect(countersAfter.call_chain_calls).toBeGreaterThan(0);
  expect(countersAfter.put_chain_calls).toBe(countersBefore.put_chain_calls);
  const callsPage = await body(
    request,
    "/candidates?symbol=US.AAPL&option_type=CALL&fee_version=0",
  );
  expect(
    callsPage.items.every((v: any) => v.quote.contract.type === "CALL"),
  ).toBeTruthy();
  const otm = callsPage.items.find(
    (v: any) => v.quote.contract.strike === "220",
  );
  const itm = callsPage.items.find(
    (v: any) => v.quote.contract.strike === "200",
  );
  expect(otm.result.income).toBe("300.00");
  expect(otm.result.capital).toBe("21000.00");
  expect(otm.result.strike_proceeds).toBe("22000.00");
  expect(otm.result.net_sale_price).toBe("223.000000");
  expect(otm.result.intrinsic).toBe("0.00");
  expect(itm.result.intrinsic).toBe("1000.00");
  expect(itm.result.time_value).toBe("200.00");
  expect(
    (await body(request, "/candidates?symbol=US.AAPL")).snapshots[0].id,
  ).toBe(putBefore.snapshots[0].id);

  await page.getByLabel("价内／价外", { exact: true }).selectOption("out");
  await expect(page.locator(".opt-table tbody tr")).toHaveCount(2);
  await expect(page.locator(".opt-table tbody .opt-flag")).toHaveCount(0);
  const checkboxes = page.locator(".opt-table input[type=checkbox]:enabled");
  await checkboxes.nth(0).check();
  await checkboxes.nth(1).check();
  await expect(page.locator(".opt-selected-tags")).toContainText("Call");
  await page
    .getByRole("button", { name: "比较 2 份合约", exact: true })
    .click();
  await expect(page.locator(".opt-compare-grid>section")).toHaveCount(2);
  await expect(page.locator(".opt-compare-grid")).toContainText(
    "备兑股票参考市值",
  );
  await expect(page.locator(".opt-compare-grid")).toContainText(
    "含权利金卖出价",
  );
  await close(page);
  await page
    .getByRole("row", { name: `查看 ${otm.quote.contract.code}`, exact: true })
    .click();
  await expect(page.locator(".opt-dialog-head h2")).toContainText("Call");
  await expect(
    page.locator("dialog").getByText("备兑所需股数", { exact: true }),
  ).toBeVisible();
  await page.getByLabel("卖出份数").fill("2");
  await page.getByLabel("自定义本单开仓费用").check();
  await page.getByLabel("开仓费用（USD）", { exact: true }).fill("5");
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("595.00");
  await expect(page.locator("dialog .opt-hero-caption")).toContainText(
    "卖出 2 份",
  );
  await expect(page.locator("dialog .opt-hero-caption")).toContainText(
    "200 股",
  );
  await page.getByText("到期交股与提前平仓情景，选看", { exact: true }).click();
  await page.getByLabel("启用独立情景").check();
  await page.getByLabel("假设到期股价（USD）", { exact: true }).fill("250");
  await page.getByLabel("假设持股成本／股（USD）", { exact: true }).fill("200");
  await page.getByLabel("指派等额外费用（USD）", { exact: true }).fill("0");
  await page.getByLabel("情景期间利息（USD）", { exact: true }).fill("0");
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("4,595.00");
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("-5,405.00");
  await page.getByRole("button", { name: "收藏此记录", exact: true }).click();
  await expect(page.locator("dialog")).toContainText("已收藏");
  await close(page);
  const saved = await body(request, "/favorites");
  expect(saved.total).toBe(savedBefore.total + 1);
  const frozen = saved.items.find(
    (v: any) => v.candidate.quote.contract.type === "CALL",
  );
  expect(frozen.candidate.input.scenario.stock_cost).toBe("200");
  expect(frozen.candidate.result.covered_pnl).toBe("4595.00");
  for (const original of savedBefore.items)
    expect(saved.items.find((v: any) => v.id === original.id)).toEqual(
      original,
    );
  await request.post(`${fixture}/test/control`, { data: { shift: 1 } });
  await refresh(page);
  expect(await body(request, `/favorites/${frozen.id}`)).toEqual(frozen);
  await page.getByLabel("行权价下限（USD）").fill("215");
  await page.getByRole("button", { name: "保存筛选方案", exact: true }).click();
  await expect(page.getByText("卖 Call 的筛选和资金口径已保存")).toBeVisible();
  const settings = await body(request, "/settings");
  expect(settings.call_filter.min_strike).toBe("215");
  expect(settings.filter.max_strike).toBe("");
  await page.getByRole("button", { name: "卖 Put", exact: true }).click();
  await expect(page.getByLabel("行权价上限（USD）")).toHaveValue("");
  await expect(page.locator(".opt-table tbody tr")).toHaveCount(4);
  expect(
    (await body(request, "/candidates?symbol=US.AAPL")).snapshots[0].id,
  ).toBe(putBefore.snapshots[0].id);
  await page.getByRole("button", { name: "卖 Call", exact: true }).click();
  await expect(page.getByLabel("行权价下限（USD）")).toHaveValue("");
  const beforeRestart = await calls(request);
  const callSnapshot = (
    await body(request, "/candidates?option_type=CALL&symbol=US.AAPL")
  ).snapshots[0].id;
  await request.post("http://127.0.0.1:17052/restart");
  await expect
    .poll(async () => {
      try {
        return (await request.get("http://127.0.0.1:17051/healthz")).ok();
      } catch {
        return false;
      }
    })
    .toBeTruthy();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "卖 Call", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("行权价下限（USD）")).toHaveValue("215");
  expect(await calls(request)).toBe(beforeRestart);
  expect(
    (await body(request, "/candidates?option_type=CALL&symbol=US.AAPL"))
      .snapshots[0].id,
  ).toBe(callSnapshot);
  expect(await body(request, `/favorites/${frozen.id}`)).toEqual(frozen);
});

test("mobile Call view and failed update preserve both sides", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/options?strategy=CALL");
  await expect(
    page.getByRole("button", { name: "卖 Call", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".opt-contract-card")).toHaveCount(2);
  await expect(page.locator(".opt-contract-title").first()).toContainText(
    "Call",
  );
  await page.locator(".opt-contract-title").first().click();
  await expect(page.locator("dialog")).toContainText("备兑所需股数");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  expect(
    await page
      .locator("dialog")
      .evaluate((el) => el.scrollWidth <= el.clientWidth),
  ).toBeTruthy();
  await close(page);
  const put = await body(request, "/candidates?symbol=US.AAPL");
  const call = await body(
    request,
    "/candidates?symbol=US.AAPL&option_type=CALL",
  );
  await request.post(`${fixture}/test/control`, {
    data: { fail_symbol: "US.AAPL", fail_type: "CALL" },
  });
  await page
    .getByRole("button", { name: "更新行情", exact: true })
    .first()
    .click();
  await expect(page.getByText("本轮更新结束", { exact: false })).toBeVisible();
  expect((await body(request, "/status")).run.option_type).toBe("CALL");
  expect((await body(request, "/status")).run.state).toBe("failed");
  expect(
    (await body(request, "/candidates?symbol=US.AAPL&option_type=CALL"))
      .snapshots[0].id,
  ).toBe(call.snapshots[0].id);
  expect(
    (await body(request, "/candidates?symbol=US.AAPL")).snapshots[0].id,
  ).toBe(put.snapshots[0].id);
  await page.getByRole("button", { name: /^收藏/ }).first().click();
  const row = page
    .locator(".opt-saved-item")
    .filter({ hasText: "Call" })
    .first();
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "查看", exact: true }).click();
  await expect(
    page.locator("dialog [data-testid=calculation-result]"),
  ).toContainText("4,595.00");
  await expect(page.locator("dialog input")).toHaveCount(0);
});

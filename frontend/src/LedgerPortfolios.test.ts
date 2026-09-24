// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { defineComponent } from "vue";
import Ledger from "./Ledger.vue";
import LedgerPortfolios from "./LedgerPortfolios.vue";
import LedgerPortfolioDialog from "./LedgerPortfolioDialog.vue";
import { type Account } from "./ledger";
import {
  validatePortfolioBasis,
  type Portfolio,
  type PortfolioBasis,
} from "./ledgerPortfolios";
import { todayShanghai } from "./ledgerView";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { ReturnMetric } from "./ledgerReturns";

// Synthetic whole-year, no-flow examples: A 100 -> 120; B 200 -> 210.
// The combined 10% is deliberately different from averaging 20% and 5%.
const stamp = "2026-09-01T00:00:00Z";
const accounts: Account[] = ["a", "b", "usd"].map((id) => ({
  id,
  name: id === "a" ? "股票账户 A" : id === "b" ? "股票账户 B" : "美元账户",
  currency: id === "usd" ? "USD" : "CNY",
  opening_date: "2023-01-01",
  opening_cash: null,
  version: "1",
  current_holdings_input: "manual_snapshot",
}));
const family: Portfolio = {
  id: "family",
  name: "家庭股票",
  currency: "CNY",
  account_ids: ["a", "b"],
  version: "1",
  created_at: stamp,
  updated_at: stamp,
};
const mine: Portfolio = {
  ...family,
  id: "mine",
  name: "我的投资",
  account_ids: ["a"],
};
const metric = (
  value: number,
  rate = true,
  reference = false,
): ReturnMetric => ({
  value: value.toFixed(rate ? 12 : 2),
  percentage: rate ? (value * 100).toFixed(2) : null,
  reason: "",
  status: reference ? "reference" : "available",
});

function fixture(
  p: Portfolio,
  from = "",
  to = todayShanghai(),
): PortfolioBasis {
  const openingDate = from
    ? new Date(Date.parse(`${from}T00:00:00Z`) - 86400000)
        .toISOString()
        .slice(0, 10)
    : "2023-01-01";
  const memberValues = p.account_ids.map((id) =>
    id === "a" ? [100, 120] : [200, 210],
  );
  const openingAssets = memberValues.reduce((sum, pair) => sum + pair[0]!, 0);
  const closingAssets = memberValues.reduce((sum, pair) => sum + pair[1]!, 0);
  const dates = [openingDate, "2023-12-31", "2024-01-01"];
  const points = dates.map((date, i) => ({
    date,
    record_id: `portfolio-${date}`,
    sequence: String(i + 1),
    version: "1",
    assets: (i === 2 ? closingAssets : openingAssets).toFixed(2),
    flow: i === 0 && !from ? openingAssets.toFixed(2) : null,
    status: "reported",
    selected: true,
    source_id: `portfolio-${date}`,
    source_version: "1",
    source_date: date,
  }));
  const days = (Date.parse("2024-01-01") - Date.parse(openingDate)) / 86400000;
  const growth = closingAssets / openingAssets - 1;
  const profit = closingAssets - openingAssets;
  const b: PortfolioBasis = {
    fx: [
      ...new Set(
        p.account_ids.map((id) => accounts.find((a) => a.id === id)!.currency),
      ),
    ]
      .filter((currency) => currency !== p.currency)
      .map((currency) => ({
        base: currency,
        quote: p.currency,
        mode: "latest",
        rate: "1.00000000",
        date: "2026-09-01",
        source: "Synthetic",
        fetched_at: stamp,
        quoted_at: stamp,
        requested_date: "2026-09-01",
      })),
    account_id: p.id,
    currency: p.currency,
    from: from || "0001-01-01",
    to,
    timezone: "Asia/Shanghai",
    revision: "a".repeat(64),
    change_revision: "0",
    status: "current",
    points: from ? points.slice(1) : points,
    opening: from ? points[0]! : null,
    closing: points[2]!,
    net_flow: from ? "0.00" : openingAssets.toFixed(2),
    previous_basis_affected: false,
    changes: [],
    portfolio: p,
    carried: true,
    entries: from
      ? []
      : p.account_ids.map((id, i) => ({
          id: `opening-${id}`,
          account_id: id,
          record_id: "",
          date: "2023-01-01",
          kind: "opening",
          amount: memberValues[i]![0]!.toFixed(2),
        })),
    members: p.account_ids.map((id, i) => {
      const [start, end] = memberValues[i]! as [number, number];
      const rate = end / start - 1;
      return {
        account_id: id,
        name: accounts.find((a) => a.id === id)!.name,
        currency: accounts.find((a) => a.id === id)!.currency,
        state: "active",
        first_date: "2023-01-01",
        initial_assets: start.toFixed(2),
        source_date: "2024-01-01",
        from: openingDate,
        to: "2024-01-01",
        assets: end.toFixed(2),
        asset_share: metric(end / closingAssets),
        profit: metric(end - start, false),
        modified_dietz: metric(rate),
        xirr: metric(Math.pow(1 + rate, 365 / days) - 1),
        twr: metric(rate),
        twr_annualized: metric(Math.pow(1 + rate, 365 / days) - 1),
        carried: !!from,
      };
    }),
    returns: {
      revision: "a".repeat(64),
      requested_from: from,
      requested_to: to,
      start_mode: from ? "custom" : "baseline",
      effective_from: openingDate,
      effective_to: "2024-01-01",
      days,
      period_days: days + 1,
      opening: points[0]!,
      closing: points[2]!,
      net_flow: "0.00",
      denominator: String(openingAssets),
      profit: metric(profit, false, true),
      modified_dietz: metric(growth, true, true),
      xirr: metric(Math.pow(1 + growth, 365 / days) - 1, true, true),
      twr: metric(growth, true, true),
      twr_annualized: metric(Math.pow(1 + growth, 365 / days) - 1, true, true),
      curve: points.map((point, i) => ({
        date: point.date,
        record_id: point.record_id,
        baseline: i === 0,
        profit: metric(i === 2 ? profit : 0, false, true),
        modified_dietz: metric(i === 2 ? growth : 0, true, true),
        twr: metric(i === 2 ? growth : 0, true, true),
      })),
      warnings: ["portfolio_carried_assets"],
      flows: [],
      investor_flows: [
        { date: openingDate, amount: (-openingAssets).toFixed(2) },
        { date: "2024-01-01", amount: closingAssets.toFixed(2) },
      ],
    },
  };
  return b;
}

const Chart = defineComponent({
  name: "LedgerReturnChart",
  props: ["mode", "samples", "flows", "benchmarks", "markers", "seriesName"],
  emits: ["locate"],
  template: '<div data-test="portfolio-chart" />',
});
let wrapper: VueWrapper;
let groups: Portfolio[];
let reads: string[];
let writes: {
  path: string;
  method: string;
  body: Record<string, unknown>;
  key: string;
}[];
let failWrite: boolean;
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  groups = structuredClone([family, mine]);
  reads = [];
  writes = [];
  failWrite = false;
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute("open");
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, options: RequestInit = {}) => {
      const url = new URL(input, "http://localhost");
      const path = url.pathname.replace("/api/platform/ledger", "");
      const method = options.method ?? "GET";
      if (method !== "GET") {
        const body = JSON.parse(String(options.body));
        const key = new Headers(options.headers).get("Idempotency-Key")!;
        writes.push({ path, method, body, key });
        if (failWrite) {
          failWrite = false;
          throw new Error("synthetic disconnect");
        }
        if (
          method !== "POST" &&
          groups.find((p) => p.id === path.split("/").at(-1))?.version !==
            body.expected_version
        )
          return response(
            { code: "version_conflict", message: "stale draft" },
            409,
          );
        if (method === "DELETE") {
          const id = path.split("/").at(-1)!;
          groups = groups.filter((p) => p.id !== id);
          return response({ portfolio_id: id, deleted: true });
        }
        const id = method === "POST" ? body.id : path.split("/").at(-1)!;
        const p: Portfolio = {
          ...family,
          id,
          name: body.name,
          currency: body.currency ?? family.currency,
          account_ids: [...body.account_ids].sort(),
          version:
            method === "POST" ? "1" : String(Number(body.expected_version) + 1),
        };
        groups = [...groups.filter((g) => g.id !== id), p];
        return response(p, method === "POST" ? 201 : 200);
      }
      reads.push(path + url.search);
      if (path === "/portfolios") return response({ items: groups });
      if (path === "/accounts") return response({ items: accounts });
      if (path === "/instruments") return response({ items: [] });
      if (path.endsWith("/analysis-basis")) {
        const id = path.split("/").at(-2)!;
        const p = groups.find((p) => p.id === id) ?? { ...mine, id };
        return response(
          fixture(
            p,
            url.searchParams.get("from") ?? "",
            url.searchParams.get("to") ?? todayShanghai(),
          ),
        );
      }
      if (path.endsWith("/effective-summary"))
        return response({
          row_count: 2,
          asset_count: 2,
          flow_count: 0,
          log_count: 0,
          voided_count: 0,
          from: "2023-01-01",
          to: "2024-01-01",
          total_in: "0.00",
          total_out: "0.00",
          latest_assets: "120.00",
          latest_asset_date: "2024-01-01",
          latest_asset_count: 1,
        });
      throw new Error(`Unexpected test request ${path}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

function render() {
  let refreshKey = 0;
  const workspace = createLedgerWorkspace(
    () =>
      void wrapper.setProps({
        refreshKey: ++refreshKey,
      }),
  );
  wrapper = mount(LedgerPortfolios, {
    attachTo: document.body,
    props: { accounts, refreshKey: 0 },
    global: {
      provide: { [ledgerWorkspaceKey as symbol]: workspace },
      stubs: { LedgerReturnChart: Chart, LedgerAnnualReturns: true },
    },
  });
  return workspace;
}
async function click(text: string) {
  const button = wrapper.findAll("button").find((b) => b.text() === text);
  expect(button, text).toBeDefined();
  await button!.trigger("click");
  await flushPromises();
}

it("validates combined transport and rejects altered members, totals, profits and stale definitions", () => {
  expect(
    validatePortfolioBasis(fixture(family), family, "", todayShanghai()).returns
      .modified_dietz.percentage,
  ).toBe("10.00");
  for (const alter of [
    (b: PortfolioBasis) => {
      b.members[0]!.assets = "121.00";
    },
    (b: PortfolioBasis) => {
      b.members[0]!.profit.value = "21.00";
    },
    (b: PortfolioBasis) => {
      b.members[1]!.account_id = "a";
    },
    (b: PortfolioBasis) => {
      b.portfolio = { ...b.portfolio, version: "2" };
    },
    (b: PortfolioBasis) => {
      b.entries[0]!.record_id = "invented-record";
    },
  ]) {
    const b = fixture(family);
    alter(b);
    expect(() =>
      validatePortfolioBasis(b, family, "", todayShanghai()),
    ).toThrow();
  }
});

it("uses visible keyboard-accessible portfolio tabs and switches the existing chart", async () => {
  render();
  await flushPromises();
  expect(wrapper.findAll('[role="tab"]').map((b) => b.text())).toEqual([
    "家庭股票",
    "我的投资",
  ]);
  expect(wrapper.find("select").exists()).toBe(false);
  expect(wrapper.findComponent(Chart).props("seriesName")).toBe("组合");
  expect(wrapper.text()).toContain("10.00%");
  expect(reads.some((path) => path.includes("/annual-returns"))).toBe(false);
  await wrapper
    .get('[role="tab"][aria-selected="true"]')
    .trigger("keydown", { key: "ArrowRight" });
  await flushPromises();
  expect(wrapper.get('[aria-selected="true"]').text()).toBe("我的投资");
  expect(wrapper.text()).toContain("20.00%");
  expect(
    reads.some((path) => path.startsWith("/portfolios/mine/analysis-basis")),
  ).toBe(true);
  await click("资产曲线");
  expect(wrapper.findComponent(Chart).props("mode")).toBe("assets");
});

it("shows member contributions on demand and keeps the chosen date range", async () => {
  render();
  await flushPromises();
  expect(wrapper.find('[data-test="portfolio-contributions"]').exists()).toBe(
    false,
  );
  await click("自定义");
  const dates = wrapper.findAll('input[type="date"]');
  await dates[0]!.setValue("2023-07-01");
  await dates[1]!.setValue("2024-01-01");
  await flushPromises();
  await click("账户贡献");
  const table = wrapper.get('[data-test="portfolio-contributions"]');
  expect(table.text()).toContain("36.36%");
  expect(table.text()).toContain("63.64%");
  expect(table.text()).toContain("30.00");
  expect(wrapper.findComponent(Chart).exists()).toBe(false);
  await table.get(".lp-contribution-name").trigger("click");
  expect(table.text()).toContain("实际统计区间");
  await table.get('[aria-label="查看股票账户 A"]').trigger("click");
  expect(wrapper.emitted("account")?.[0]).toEqual([
    { id: "a", portfolio: "家庭股票" },
  ]);
  await click("整体表现");
  expect(
    (wrapper.findAll('input[type="date"]')[0]!.element as HTMLInputElement)
      .value,
  ).toBe("2023-07-01");
  expect(reads.at(-1)).toContain("from=2023-07-01&to=2024-01-01");
});

it("expands several member contributions at the same time", async () => {
  render();
  await flushPromises();
  await click("账户贡献");
  const table = wrapper.get('[data-test="portfolio-contributions"]');
  const names = table.findAll(".lp-contribution-name");
  expect(names).toHaveLength(2);
  expect(table.findAll(".lp-contribution-detail")).toHaveLength(0);
  await names[0]!.trigger("click");
  expect(table.findAll(".lp-contribution-detail")).toHaveLength(1);
  await names[1]!.trigger("click");
  expect(table.findAll(".lp-contribution-detail")).toHaveLength(2);
  expect(names.every((b) => b.attributes("aria-expanded") === "true")).toBe(
    true,
  );
  await names[0]!.trigger("click");
  expect(table.findAll(".lp-contribution-detail")).toHaveLength(1);
  expect(names[0]!.attributes("aria-expanded")).toBe("false");
  expect(names[1]!.attributes("aria-expanded")).toBe("true");
});

it("creates a named selection, allows mixed currencies, and replays an uncertain write with the same key", async () => {
  const workspace = render();
  await flushPromises();
  await click("＋ 新建组合");
  const dialog = wrapper.findComponent(LedgerPortfolioDialog);
  await dialog
    .get('input[placeholder="例如：家庭股票投资"]')
    .setValue("夫妻股票");
  await dialog.get('input[value="a"]').setValue(true);
  expect(
    (dialog.get('input[value="usd"]').element as HTMLInputElement).disabled,
  ).toBe(false);
  await dialog.get('input[value="b"]').setValue(true);
  await dialog.get('input[value="usd"]').setValue(true);
  await dialog.get('[data-test="portfolio-currency"]').setValue("HKD");
  failWrite = true;
  await dialog.get("form").trigger("submit");
  await flushPromises();
  expect(workspace.locked.value).toBe(true);
  expect(writes).toHaveLength(1);
  await click("按原请求重试确认");
  expect(writes).toHaveLength(2);
  expect(writes[0]!.key).toBe(writes[1]!.key);
  expect(writes[0]!.body).toEqual(writes[1]!.body);
  expect(writes[1]!.body.account_ids).toEqual(["a", "b", "usd"]);
  expect(writes[1]!.body.currency).toBe("HKD");
  expect(workspace.locked.value).toBe(false);
  expect(wrapper.findComponent(LedgerPortfolioDialog).exists()).toBe(false);
  expect(wrapper.get('[aria-selected="true"]').text()).toBe("夫妻股票");
  expect(wrapper.get('[data-test="portfolio-fx"]').text()).toContain("HKD");
  expect(writes.every((w) => w.path.startsWith("/portfolios"))).toBe(true);
});

it("updates members with an expected version and deletes only the portfolio", async () => {
  render();
  await flushPromises();
  await click("调整成员");
  let dialog = wrapper.findComponent(LedgerPortfolioDialog);
  await dialog.get('input[value="b"]').setValue(false);
  await dialog.get('[data-test="portfolio-currency"]').setValue("USD");
  await dialog
    .get('input[placeholder="例如：家庭股票投资"]')
    .setValue("家庭股票新版");
  await dialog.get("form").trigger("submit");
  await flushPromises();
  expect(writes[0]!.body).toEqual({
    name: "家庭股票新版",
    currency: "USD",
    account_ids: ["a"],
    expected_version: "1",
  });
  expect(wrapper.text()).toContain("20.00%");
  await click("删除组合");
  dialog = wrapper.findComponent(LedgerPortfolioDialog);
  await dialog.get(".lp-danger-button").trigger("click");
  await flushPromises();
  expect(writes[1]).toMatchObject({
    method: "DELETE",
    path: "/portfolios/family",
    body: { expected_version: "2" },
  });
  expect(wrapper.get('[aria-selected="true"]').text()).toBe("我的投资");
  expect(accounts).toHaveLength(3);
});

it("keeps the draft's original version when a list refresh arrives during editing", async () => {
  render();
  await flushPromises();
  await click("调整成员");
  const dialog = wrapper.findComponent(LedgerPortfolioDialog);
  await dialog
    .get('input[placeholder="例如：家庭股票投资"]')
    .setValue("本地草稿");
  groups = [{ ...family, name: "其他访客的新名称", version: "2" }, mine];
  await wrapper.setProps({ refreshKey: 1 });
  await flushPromises();
  await dialog.get("form").trigger("submit");
  await flushPromises();
  expect(writes[0]!.body.expected_version).toBe("1");
  expect(groups[0]!.name).toBe("其他访客的新名称");
  expect(wrapper.findComponent(LedgerPortfolioDialog).exists()).toBe(true);
  expect(dialog.text()).toContain("记录已被修改");
});

it("returns from a member account to the retained portfolio and contribution view", async () => {
  wrapper = mount(Ledger, {
    attachTo: document.body,
    global: {
      stubs: {
        LedgerReturnChart: Chart,
        LedgerAnnualReturns: true,
        LedgerHoldings: true,
        AccountRecords: true,
        LedgerManagement: true,
      },
    },
  });
  await flushPromises();
  await click("账户组合");
  await click("账户贡献");
  await wrapper.get('[aria-label="查看股票账户 A"]').trigger("click");
  await flushPromises();
  expect(wrapper.get('.lp-workspace-switch [aria-pressed="true"]').text()).toBe(
    "单账户",
  );
  await click("返回「家庭股票」组合");
  expect(wrapper.get('.lp-workspace-switch [aria-pressed="true"]').text()).toBe(
    "账户组合",
  );
  expect(wrapper.get('.lp-analysis-tabs [aria-pressed="true"]').text()).toBe(
    "账户贡献",
  );
  expect(wrapper.get('[data-test="portfolio-contributions"]').isVisible()).toBe(
    true,
  );
});

it("closes the portfolio actions menu on an outside click and on Escape", async () => {
  render();
  await flushPromises();
  const menu = wrapper.get<HTMLDetailsElement>("details.lp-portfolio-menu");
  const details = menu.element;
  details.open = true;
  document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await flushPromises();
  expect(details.open).toBe(false);
  details.open = true;
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  await flushPromises();
  expect(details.open).toBe(false);
});

it("auto-dismisses the success notice a few seconds after a write", async () => {
  vi.useFakeTimers();
  try {
    wrapper = mount(Ledger, {
      attachTo: document.body,
      global: {
        stubs: {
          LedgerReturnChart: Chart,
          LedgerAnnualReturns: true,
          LedgerHoldings: true,
          AccountRecords: true,
          LedgerManagement: true,
        },
      },
    });
    await flushPromises();
    await click("账户组合");
    await click("＋ 新建组合");
    const dialog = wrapper.findComponent(LedgerPortfolioDialog);
    await dialog
      .get('input[placeholder="例如：家庭股票投资"]')
      .setValue("夫妻股票");
    await dialog.get('input[value="a"]').setValue(true);
    await dialog.get("form").trigger("submit");
    await flushPromises();
    expect(wrapper.get(".lp-notice").text()).toContain("创建组合成功");
    await vi.advanceTimersByTimeAsync(5000);
    expect(wrapper.find(".lp-notice").exists()).toBe(false);
  } finally {
    vi.useRealTimers();
  }
});

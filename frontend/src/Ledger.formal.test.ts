// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { defineComponent, nextTick } from "vue";
import { readFileSync } from "node:fs";
import Ledger from "./Ledger.vue";
import LedgerOverview from "./LedgerOverview.vue";
import { PendingWrite, type Account } from "./ledger";
import type { AccountRecord, EffectiveSummary } from "./accountRecords";
import type { BasisPoint } from "./ledgerChart";
import {
  returnReasons,
  validReturns,
  type ReturnMetric,
} from "./ledgerReturns";
import { todayShanghai, validateBasis, type AnalysisBasis } from "./ledgerView";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
  useLedgerWorkspace,
} from "./useLedgerWorkspace";
const styles = readFileSync("src/ledger.css", "utf8");

const account: Account = {
  id: "a",
  name: "合成账户甲",
  currency: "CNY",
  opening_date: "2020-01-01",
  opening_cash: null,
  version: "1",
  accounting_mode: "reported",
  current_holdings_input: "manual_snapshot",
};
const accounts = [
  account,
  { ...account, id: "b", name: "名称很长但仍然完整可访问的合成账户乙" },
  { ...account, id: "c", name: "合成账户丙" },
];
const summary: EffectiveSummary = {
  row_count: 6,
  asset_count: 4,
  flow_count: 1,
  log_count: 1,
  voided_count: 0,
  from: "2020-01-01",
  to: "2021-09-01",
  total_in: "100.00",
  total_out: "0.00",
  latest_assets: "90071992547409.03",
  latest_asset_date: "2021-09-01",
  latest_asset_count: 1,
};
const metric = (
  status: ReturnMetric["status"] = "available",
  reason = "",
): ReturnMetric => ({
  value: status === "unavailable" ? null : "0.260000000000",
  percentage: status === "unavailable" ? null : "26.00",
  status,
  reason,
});

// Synthetic transport facts, not a client-side financial calculator.
function fixture(id = "a", to = todayShanghai()): AnalysisBasis {
  const dates = [
    "2020-01-01",
    "2020-02-01",
    "2020-03-01",
    "2020-04-01",
    "2021-05-02",
    "2021-09-01",
  ];
  const points: BasisPoint[] = dates.map((date, i) => {
    const record: AccountRecord = {
      id: `sample-${i}`,
      account_id: id,
      date,
      sequence: `${i + 1}`,
      version: "1",
      kind: i === 4 ? "cash_flow" : i === 3 ? "log" : "asset",
      flow: i === 4 ? "100.00" : null,
      total_assets: i === 3 ? null : "90071992547409.03",
      note: "Synthetic",
      origin: "manual",
      original: null,
      voided: false,
      created_at: `${date}T00:00:00Z`,
      updated_at: `${date}T00:00:00Z`,
    };
    return {
      date,
      record_id: record.id,
      sequence: record.sequence!,
      version: "1",
      selected: true,
      assets: record.total_assets,
      flow: record.flow,
      status:
        i === 2 || i === 5 ? "carried" : i === 3 ? "unavailable" : "reported",
      source_id: i === 3 ? "" : `sample-${i === 2 || i === 5 ? i - 1 : i}`,
      source_version: i === 3 ? "" : "1",
      source_date: i === 3 ? "" : dates[i === 2 || i === 5 ? i - 1 : i]!,
      record,
    };
  });
  const curve = points.map((p, i) => {
    const m = metric(
      i === 2 || i === 5 ? "reference" : i === 3 ? "unavailable" : "available",
      i === 3 ? "missing_closing" : "",
    );
    return {
      date: p.date,
      record_id: p.record_id,
      baseline: i === 0,
      modified_dietz: m,
      twr: m,
      profit: {
        ...m,
        value: m.value === null ? null : "90071992547409.03",
        percentage: null,
      },
    };
  });
  const last = curve.at(-1)!;
  return {
    account_id: id,
    currency: "CNY",
    timezone: "Asia/Shanghai",
    from: "0001-01-01",
    to,
    revision: "a".repeat(64),
    change_revision: "1",
    status: "current",
    points,
    opening: points[0]!,
    closing: points.at(-1)!,
    net_flow: "100.00",
    previous_basis_affected: false,
    changes: [],
    returns: {
      revision: "a".repeat(64),
      requested_from: "",
      requested_to: to,
      start_mode: "baseline",
      effective_from: dates[0]!,
      effective_to: dates.at(-1)!,
      days: 609,
      period_days: 610,
      opening: points[0]!,
      closing: points.at(-1)!,
      net_flow: "100.00",
      denominator: "100",
      profit: last.profit,
      modified_dietz: last.modified_dietz,
      twr: last.twr,
      xirr: metric("unavailable", "possible_multiple_roots"),
      twr_annualized: metric("reference"),
      curve,
      warnings: ["carried_assets_unchanged"],
      flows: [
        {
          date: "2021-05-02",
          record_id: "sample-4",
          version: "1",
          flow: "100.00",
          weight_days: 122,
          period_days: 610,
        },
      ],
      investor_flows: [],
    },
  };
}
const response = (data: unknown) =>
  new Response(JSON.stringify(data), {
    headers: { "Content-Type": "application/json" },
  });
let wrapper: VueWrapper;
let basis: AnalysisBasis;
let calls: string[];
let accountList: Account[];
let workspace: ReturnType<typeof createLedgerWorkspace>;
const manager = defineComponent({
  setup() {
    workspace = useLedgerWorkspace();
    return () => null;
  },
});
beforeEach(() => {
  basis = fixture();
  calls = [];
  accountList = accounts;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const url = new URL(input, "http://localhost");
      calls.push(url.pathname + url.search);
      if (url.pathname.endsWith("/accounts"))
        return response({ items: accountList });
      if (url.pathname.endsWith("/instruments")) return response({ items: [] });
      if (url.pathname.endsWith("/effective-summary")) return response(summary);
      if (url.pathname.endsWith("/analysis-basis")) {
        const id = url.pathname.split("/").at(-2)!;
        return response(
          id === "a" ? basis : fixture(id, url.searchParams.get("to")!),
        );
      }
      if (url.pathname.endsWith("/records"))
        return response({ items: [basis.points[4]!.record] });
      throw new Error(`Unexpected test request: ${input}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function overview() {
  wrapper = mount(LedgerOverview, {
    props: { account, refreshKey: 0 },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
}
async function ledger() {
  wrapper = mount(Ledger, {
    attachTo: document.body,
    global: { stubs: { LedgerManagement: manager } },
  });
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
}

it("validates the inclusive 610-day contract with unchanged 122-day flow weight", () => {
  expect(validateBasis(basis, account, "", basis.to)).toBe(basis);
  const r = basis.returns;
  expect(r.days).toBe(609);
  expect(r.flows[0]!.weight_days).toBe(122);
  expect(r.flows[0]!.period_days).toBe(610);
  for (const period_days of [undefined, 609, -1, 610.5, "610", 0]) {
    expect(
      validReturns({ ...r, period_days } as typeof r, r.revision, "", basis.to),
    ).toBe(false);
  }
  for (const flow of [
    { ...r.flows[0]!, period_days: 609 },
    { ...r.flows[0]!, weight_days: 610 },
    { ...r.flows[0]!, weight_days: -1 },
  ]) {
    expect(
      validReturns({ ...r, flows: [flow] }, r.revision, "", basis.to),
    ).toBe(false);
  }
});

it("keeps a same-day inclusive duration of one, but requires zero when the closing endpoint is missing", () => {
  const r = basis.returns;
  Object.assign(r, {
    effective_to: r.effective_from,
    days: 0,
    period_days: 1,
    closing: r.opening,
    curve: [r.curve[0]!],
    flows: [],
  });
  for (const key of [
    "profit",
    "modified_dietz",
    "xirr",
    "twr",
    "twr_annualized",
  ] as const)
    r[key] = metric("unavailable", "no_interval");
  expect(validReturns(r, r.revision, "", basis.to)).toBe(true);
  Object.assign(r, { effective_to: "", period_days: 0, closing: null });
  for (const key of [
    "profit",
    "modified_dietz",
    "xirr",
    "twr",
    "twr_annualized",
  ] as const)
    r[key] = metric("unavailable", "missing_closing");
  expect(validReturns(r, r.revision, "", basis.to)).toBe(true);
  expect(validReturns({ ...r, period_days: 1 }, r.revision, "", basis.to)).toBe(
    false,
  );
});

it("joins numeric samples with one solid path and filled points without changing exact values or provenance", async () => {
  const original = structuredClone(basis);
  await overview();
  expect(wrapper.get('[data-test="period-days"]').text()).toBe(
    "统计时长 610 天",
  );
  const svg = wrapper.get('svg[aria-label="账户收益曲线"]');
  expect(svg.findAll(".lp-point-available, .lp-point-reference")).toHaveLength(0);
  expect(svg.findAll("circle")).toHaveLength(5);
  expect(
    svg
      .get("path.lp-curve")
      .attributes("d")!
      .match(/M/g),
  ).toHaveLength(1);
  expect(svg.get("path.lp-curve").attributes("d")!.match(/L/g)).toHaveLength(4);
  expect(svg.findAll("path.lp-curve")).toHaveLength(1);
  expect(svg.find(".lp-reference-line").exists()).toBe(false);
  expect(svg.find('.lp-point[aria-label^="2020-04-01"]').exists()).toBe(false);
  expect(wrapper.find('[data-test="curve-gaps"]').exists()).toBe(false);
  expect(wrapper.find('[aria-label="收益状态图例"]').exists()).toBe(false);
  const reference = svg.get('.lp-point[aria-label^="2020-03-01"]');
  expect(reference.attributes("tabindex")).toBe("0");
  await reference.trigger("focus");
  expect(wrapper.get(".lp-chart-readout").text()).toContain("26.00%");
  expect(wrapper.get(".lp-chart-readout").text()).toContain(
    "沿用 2020-02-01 总资产原值",
  );
  await reference.trigger("blur", {
    relatedTarget: wrapper.get(".lp-chart-readout").element,
  });
  expect(wrapper.get(".lp-chart-readout").text()).toContain(
    "沿用 2020-02-01 总资产原值",
  );
  expect(reference.attributes("aria-label")).not.toContain("sample-");
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "收益金额")!
    .trigger("click");
  expect(wrapper.get(".lp-chart-readout").text()).toContain(
    "90,071,992,547,409.03",
  );
  expect(calls).toHaveLength(2);
  expect(styles).not.toContain(".lp-reference-line");
  expect(styles).toMatch(
    /\.lp-point\s*\{[^}]*fill: var\(--lp-primary\)/,
  );
  expect(styles).toMatch(/\.lp-account-tabs\s*\{[^}]*overflow-x: auto/);
  expect(basis).toEqual(original);
});

it.each([0, 1])("does not fabricate values when only %i numeric samples exist", async (count) => {
  for (const p of basis.returns.curve) p.modified_dietz = metric("unavailable", "missing_flow_boundary");
  if (count) basis.returns.curve[0]!.modified_dietz = {
    ...metric(), value: "0.000000000000", percentage: "0.00",
  };
  basis.returns.modified_dietz = basis.returns.curve.at(-1)!.modified_dietz;
  const original = structuredClone(basis);
  await overview();
  expect(wrapper.findAll(".lp-point")).toHaveLength(count);
  if (count) {
    const path = wrapper.get("path.lp-curve").attributes("d")!;
    expect(path).toMatch(/^M/);
    expect(path).not.toContain("L");
    expect(wrapper.get(".lp-point").attributes("aria-label")).toContain("0.00%");
  } else {
    expect(wrapper.find("path.lp-curve").exists()).toBe(false);
    expect(wrapper.text()).toContain("这个区间还画不出曲线");
  }
  expect(basis).toEqual(original);
});

it("separates manager estimate provenance and earlier boundaries from unchanged personal and profit values", async () => {
  const estimated = basis.returns.curve[2]!;
  estimated.twr_estimate = {
    assets: "90071992547509.03",
    source_record_id: "sample-1",
    source_date: "2020-02-01",
    net_flow: "100.00",
  };
  estimated.twr = { ...metric("reference"), value: "0.000000000000", percentage: "0.00" };
  const explicit = basis.points.at(-1)!;
  Object.assign(explicit, { status: "reported", source_id: explicit.record_id, source_date: explicit.date });
  basis.returns.warnings.push("twr_estimated_assets");
  const original = structuredClone(basis);
  await overview();
  const readout = () => wrapper.get(".lp-chart-readout").text();
  const point = () => wrapper.get('.lp-point[aria-label^="2020-03-01"]');
  await point().trigger("focus");
  expect(readout()).toContain("26.00%");
  expect(readout()).toContain("沿用 2020-02-01 总资产原值，未增加资金流");
  expect(readout()).not.toContain("90071992547509.03");
  expect(wrapper.get(".lp-reference").text()).not.toContain("TWR");

  await wrapper.findAll("button").find(b => b.text() === "基金经理视角")!.trigger("click");
  expect(readout()).toContain("0.00%");
  expect(readout()).toContain("TWR 估算资产 90071992547509.03 CNY");
  expect(readout()).toContain("基于 2020-02-01 最后明确总资产");
  expect(readout()).toContain("累计净流入 100.00 CNY");
  expect(readout()).toContain("来源记录 sample-1");
  expect(readout()).not.toContain("未增加资金流");
  expect(point().attributes("aria-label")).toContain("来源记录 sample-1");
  expect(point().get("title").text()).toContain("累计净流入 100.00");
  expect(wrapper.get(".lp-reference").text()).toContain("TWR 仅供参考");
  expect(wrapper.get(".lp-reference").text()).toContain("收益金额及个人视角：");
  await wrapper.get('.lp-point[aria-label^="2021-09-01"]').trigger("focus");
  expect(readout()).toContain("TWR 包含较早的估算边界");
  expect(readout()).not.toContain("沿用 2021-05-02");
  expect(readout()).not.toContain("TWR 估算资产");

  await point().trigger("focus");
  await wrapper.findAll("button").find(b => b.text() === "收益金额")!.trigger("click");
  expect(readout()).toContain("90,071,992,547,409.03");
  expect(readout()).toContain("沿用 2020-02-01 总资产原值，未增加资金流");
  expect(readout()).not.toContain("TWR 估算资产");
  expect(wrapper.get(".lp-assets strong").text()).toBe("90,071,992,547,409.03");
  await wrapper.findAll("button").find(b => b.text() === "个人视角")!.trigger("click");
  await wrapper.findAll("button").find(b => b.text() === "收益率")!.trigger("click");
  expect(readout()).toContain("26.00%");
  expect(wrapper.get('[data-test="annual-return"]').text()).toContain(returnReasons.possible_multiple_roots);
  expect(wrapper.get(".lp-reference").text()).not.toContain("TWR");
  expect(basis).toEqual(original);
  expect(calls).toHaveLength(2);
});

it.each([
  "possible_multiple_roots",
  "precision_unresolved",
  "out_of_solver_range",
])(
  "explains %s immediately beside XIRR instead of inventing missing principal",
  async (reason) => {
    basis.returns.xirr = metric("unavailable", reason);
    await overview();
    const annual = wrapper.get('[data-test="annual-return"]');
    expect(annual.text()).toContain("XIRR");
    expect(annual.text()).toContain(returnReasons[reason]);
    expect(annual.text()).not.toContain("缺少期初");
    expect(annual.get("strong").text()).toBe("—");
    if (reason === "out_of_solver_range")
      expect(annual.text()).not.toContain("唯一根");
  },
);

it("does not invent duration or assets for missing endpoints", async () => {
  const unavailable = metric("unavailable", "missing_opening");
  Object.assign(basis, { points: [], opening: null, closing: null });
  Object.assign(basis.returns, {
    days: 0,
    period_days: 0,
    effective_from: "",
    effective_to: "",
    opening: null,
    closing: null,
    curve: [],
    flows: [],
    investor_flows: [],
    profit: unavailable,
    modified_dietz: unavailable,
    twr: unavailable,
    twr_annualized: unavailable,
    xirr: unavailable,
  });
  await overview();
  expect(wrapper.find('[data-test="period-days"]').exists()).toBe(false);
  expect(wrapper.get('[data-test="annual-return"]').text()).toContain(
    "缺少期初资产",
  );
  expect(wrapper.find('svg[aria-label="账户收益曲线"]').exists()).toBe(false);
});

it("uses accessible account tabs, roving keyboard focus and keeps focus after reads", async () => {
  await ledger();
  const tabs = wrapper.findAll('[role="tab"]');
  expect(tabs).toHaveLength(3);
  expect(wrapper.find("select#ledger-account").exists()).toBe(false);
  for (const tab of tabs) {
    expect(
      wrapper
        .get(`#${tab.attributes("aria-controls")}`)
        .attributes("aria-labelledby"),
    ).toBe(tab.attributes("id"));
  }
  expect(tabs[0]!.attributes("tabindex")).toBe("0");
  expect(tabs[1]!.attributes("title")).toBe(accounts[1]!.name);
  (tabs[0]!.element as HTMLElement).focus();
  await tabs[0]!.trigger("keydown", { key: "ArrowRight" });
  await flushPromises();
  expect(tabs[1]!.attributes("aria-selected")).toBe("true");
  expect(document.activeElement).toBe(tabs[1]!.element);
  expect(wrapper.get("#account-panel-a").attributes("hidden")).toBeDefined();
  await tabs[1]!.trigger("keydown", { key: "End" });
  await flushPromises();
  expect(document.activeElement).toBe(tabs[2]!.element);
  await tabs[2]!.trigger("keydown", { key: "ArrowRight" });
  await flushPromises();
  expect(document.activeElement).toBe(tabs[0]!.element);
  (tabs[1]!.element as HTMLButtonElement).disabled = true;
  await tabs[0]!.trigger("keydown", { key: "ArrowRight" });
  await flushPromises();
  expect(document.activeElement).toBe(tabs[2]!.element);
  await tabs[2]!.trigger("keydown", { key: "Home" });
  await flushPromises();
  expect(tabs[0]!.attributes("aria-selected")).toBe("true");
  expect(
    calls.some((path) => path.includes("/accounts/b/analysis-basis")),
  ).toBe(true);
  expect(calls.some((path) => path.includes("/records"))).toBe(false);
});

it("keeps a single account as a labelled tab", async () => {
  accountList = [account];
  await ledger();
  expect(wrapper.get('[role="tab"]').text()).toBe(account.name);
});

it("clears stale account metrics and does not move focus when an old request finishes", async () => {
  await ledger();
  const fetch = globalThis.fetch;
  let resolve!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string, options: RequestInit) =>
      input.includes("/accounts/b/analysis-basis")
        ? new Promise<Response>((done) => {
            resolve = done;
          })
        : fetch(input, options),
    ),
  );
  await wrapper.get("#account-tab-a").trigger("keydown", { key: "ArrowRight" });
  expect(wrapper.get(".lp-chart-empty").text()).toContain("正在读取收益记录");
  expect(wrapper.find(".lp-chart circle").exists()).toBe(false);
  await wrapper.get("#account-tab-b").trigger("keydown", { key: "ArrowRight" });
  await flushPromises();
  resolve(response(fixture("b")));
  await flushPromises();
  expect(document.activeElement).toBe(wrapper.get("#account-tab-c").element);
  expect(wrapper.get("#account-tab-c").attributes("aria-selected")).toBe(
    "true",
  );
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(wrapper.get('[data-test="period-days"]').text()).toBe(
    "统计时长 610 天",
  );
});

it("locks account switching and navigation while a write is pending", async () => {
  await ledger();
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "管理账户")!
    .trigger("click");
  workspace.pending.value = new PendingWrite("/accounts/a/records", "POST", {});
  await nextTick();
  expect(
    wrapper
      .findAll('[role="tab"]')
      .every((tab) => tab.attributes("disabled") !== undefined),
  ).toBe(true);
  await wrapper.get("#account-tab-b").trigger("click");
  await wrapper.get("#account-tab-a").trigger("keydown", { key: "End" });
  expect(wrapper.get('[role="tab"]').attributes("aria-selected")).toBe("true");
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  const event = new MouseEvent("click", { bubbles: true, cancelable: true });
  wrapper.get('a[href="/liquor"]').element.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true);
});

it("locks the account tabs for a record modal even before any write is pending", async () => {
  const show = HTMLDialogElement.prototype.showModal;
  const close = HTMLDialogElement.prototype.close;
  HTMLDialogElement.prototype.showModal = vi.fn();
  HTMLDialogElement.prototype.close = vi.fn();
  try {
    await ledger();
    await wrapper.get("[data-ledger-focus]").trigger("click");
    expect(wrapper.find("dialog").exists()).toBe(true);
    expect(
      wrapper
        .findAll('[role="tab"]')
        .every((tab) => tab.attributes("disabled") !== undefined),
    ).toBe(true);
    await wrapper.get("#account-tab-b").trigger("click");
    expect(wrapper.get("#account-tab-a").attributes("aria-selected")).toBe(
      "true",
    );
    await wrapper.get('[aria-label="关闭弹窗"]').trigger("click");
    await flushPromises();
    expect(wrapper.find("dialog").exists()).toBe(false);
    expect(
      wrapper.get("#account-tab-b").attributes("disabled"),
    ).toBeUndefined();
  } finally {
    HTMLDialogElement.prototype.showModal = show;
    HTMLDialogElement.prototype.close = close;
  }
});

it("opens the folded record table from a real chart event without resetting overview", async () => {
  const original = HTMLElement.prototype.scrollIntoView;
  HTMLElement.prototype.scrollIntoView = vi.fn();
  try {
    await ledger();
    const records = wrapper.get("details.lp-records");
    expect((records.element as HTMLDetailsElement).open).toBe(false);
    await wrapper.get(".lp-event").trigger("click");
    await records.trigger("toggle");
    await flushPromises();
    expect((records.element as HTMLDetailsElement).open).toBe(true);
    expect(wrapper.get('[data-record-id="sample-4"]').classes()).toContain(
      "lp-highlighted",
    );
    expect(wrapper.get('[data-test="period-days"]').text()).toBe(
      "统计时长 610 天",
    );
    expect(calls.filter((path) => path.includes("/records?"))).toHaveLength(1);
    expect(
      calls.filter((path) => path.includes("/analysis-basis?")),
    ).toHaveLength(1);
    await wrapper.get("#account-tab-b").trigger("click");
    await flushPromises();
    expect(
      (wrapper.get("details.lp-records").element as HTMLDetailsElement).open,
    ).toBe(false);
    expect(wrapper.find('[data-record-id="sample-4"]').exists()).toBe(false);
  } finally {
    HTMLElement.prototype.scrollIntoView = original;
  }
});

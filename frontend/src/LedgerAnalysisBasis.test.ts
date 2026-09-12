// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import LedgerAnalysisBasis from "./LedgerAnalysisBasis.vue";

// Rendering/lifecycle are exercised separately with a mocked ECharts instance.
vi.mock("./LedgerReturnTrend.vue", () => ({
  __esModule: true,
  default: { template: "<div />" },
}));
vi.mock("./LedgerAssetChart.vue", () => ({
  __esModule: true,
  default: { template: "<div />" },
}));

const sample = (account = "a") => ({
  returns: {
    revision: "synthetic-revision",
    requested_from: "",
    requested_to: "",
    start_mode: "baseline",
    effective_from: "",
    effective_to: "",
    days: 0,
    period_days: 0,
    opening: null,
    closing: null,
    net_flow: "0.00",
    denominator: null,
    profit: {
      value: null,
      percentage: null,
      status: "unavailable",
      reason: "missing_opening",
    },
    modified_dietz: {
      value: null,
      percentage: null,
      status: "unavailable",
      reason: "missing_opening",
    },
    xirr: {
      value: null,
      percentage: null,
      status: "unavailable",
      reason: "missing_opening",
    },
    warnings: [],
    twr: {
      value: null,
      percentage: null,
      status: "unavailable",
      reason: "missing_opening",
    },
    twr_annualized: {
      value: null,
      percentage: null,
      status: "unavailable",
      reason: "missing_opening",
    },
    curve: [],
    flows: [],
    investor_flows: [],
  },
  account_id: account,
  currency: "CNY",
  from: "0001-01-01",
  to: "2026-09-06",
  revision: "synthetic-revision",
  change_revision: "4",
  status: "current",
  net_flow: "90071992547409.01",
  opening: null,
  closing: {
    date: "2020-01-02",
    sequence: "2",
    assets: "90071992547409.01",
    status: "carried",
    source_id: "manual-a",
    source_version: "1",
    source_date: "2020-01-01",
  },
  points: [],
  changes: [],
  previous_basis_affected: false,
});

it("rejects a mismatched requested range and oversized snapshots without rendering partial chart data", async () => {
  const fetch = vi.fn().mockResolvedValue(response(sample()));
  vi.stubGlobal("fetch", fetch);
  const w = mount(LedgerAnalysisBasis, {
    props: { accountId: "a", refreshKey: 0 },
  });
  await w.get('input[name="basis_from"]').setValue("2020-01-01");
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.find('[role="alert"]').exists()).toBe(true);
  expect(w.text()).not.toContain("90071992547409.01");
  await w.get('input[name="basis_from"]').setValue("");
  fetch.mockResolvedValue(
    response({ ...sample(), points: Array(10001).fill(null) }),
  );
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.find('[role="alert"]').exists()).toBe(true);
  const source = w.get('[data-test="analysis-source"]');
  expect(source.get("summary").text()).toBe("查看数据来源与计算依据");
  expect((source.element as HTMLDetailsElement).open).toBe(false);
  (source.element as HTMLDetailsElement).open = true;
  await source.trigger("toggle");
  expect(source.text()).toContain("10,000");
  expect(w.text()).not.toContain("90071992547409.01");
  w.unmount();
});

it("ignores late refresh responses and rejects wrong embedded record identity", async () => {
  let finish!: (r: Response) => void;
  const fetch = vi.fn().mockImplementationOnce(
    () =>
      new Promise<Response>((r) => {
        finish = r;
      }),
  );
  vi.stubGlobal("fetch", fetch);
  const w = mount(LedgerAnalysisBasis, {
    props: { accountId: "a", holdings: true, refreshKey: 0 },
  });
  await w.get("form").trigger("submit");
  fetch.mockResolvedValue(
    response({ ...sample("a"), net_flow: "7.00", closing: null }),
  );
  await w.setProps({ refreshKey: 1 });
  await flushPromises();
  finish(response(sample()));
  await flushPromises();
  expect(w.text()).toContain("7.00");
  expect(w.text()).not.toContain("90071992547409.01");
  fetch.mockResolvedValue(
    response({
      ...sample(),
      points: [
        {
          date: "2020-01-01",
          record_id: "manual-a",
          selected: true,
          status: "reported",
          assets: "1.00",
          flow: null,
          record: { account_id: "wrong", id: "manual-a", date: "2020-01-01" },
        },
      ],
    }),
  );
  await w.setProps({ refreshKey: 2 });
  await flushPromises();
  expect(w.find('[role="alert"]').exists()).toBe(true);
  expect(w.text()).not.toContain("90071992547409.01");
  w.unmount();
});
const response = (data: unknown) =>
  new Response(JSON.stringify(data), {
    headers: { "Content-Type": "application/json" },
  });
afterEach(() => vi.unstubAllGlobals());

it("requires returns from the same revision and clears results immediately when dates change", async () => {
  const fetch = vi.fn().mockResolvedValue(response(sample()));
  vi.stubGlobal("fetch", fetch);
  const w = mount(LedgerAnalysisBasis, {
    props: { accountId: "a", refreshKey: 0 },
  });
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.find('[data-test="ledger-returns"]').exists()).toBe(true);
  expect(fetch).toHaveBeenCalledTimes(1);
  await w.get('input[name="basis_to"]').setValue("2020-01-01");
  expect(w.find('[data-test="ledger-returns"]').exists()).toBe(false);
  await w.get('input[name="basis_to"]').setValue("");
  fetch.mockResolvedValue(
    response({
      ...sample(),
      returns: { ...sample().returns, revision: "wrong-revision" },
    }),
  );
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.find('[role="alert"]').exists()).toBe(true);
  expect(w.find('[data-test="ledger-returns"]').exists()).toBe(false);
  w.unmount();
});

it("reads only on demand, explains exact carry and missing opening, and refreshes with scoped watermark", async () => {
  const fetch = vi.fn().mockResolvedValue(response(sample()));
  vi.stubGlobal("fetch", fetch);
  const w = mount(LedgerAnalysisBasis, {
    props: { accountId: "a", refreshKey: 0 },
  });
  expect(fetch).not.toHaveBeenCalled();
  await w.get("form").trigger("submit");
  await flushPromises();
  const summary = w.get('[data-test="analysis-summary"]');
  expect(summary.text()).toContain("90071992547409.01");
  expect(summary.text()).toContain("基准模式：以首个明确日终资产起算");
  expect(summary.text()).not.toContain("manual-a");
  const source = w.get('[data-test="analysis-source"]');
  expect(source.get("summary").text()).toBe("查看数据来源与计算依据");
  expect((source.element as HTMLDetailsElement).open).toBe(false);
  (source.element as HTMLDetailsElement).open = true;
  await source.trigger("toggle");
  expect(source.text()).toContain("最近明确资产加后续净转入，仅供参考");
  expect(source.text()).toContain("manual-a / 来源版本 1");
  fetch.mockResolvedValue(
    response({
      ...sample(),
      previous_basis_affected: true,
      change_revision: "5",
    }),
  );
  await w.setProps({ refreshKey: 1 });
  await flushPromises();
  expect(fetch.mock.calls[1][0]).toContain("since_revision=4");
  const refreshedSource = w.get('[data-test="analysis-source"]');
  (refreshedSource.element as HTMLDetailsElement).open = true;
  await refreshedSource.trigger("toggle");
  expect(refreshedSource.text()).toContain("上次读取依据已受变更影响");
  w.unmount();
});

it("uses one account basis without a track selector and refuses mismatched responses", async () => {
  const fetch = vi.fn().mockResolvedValue(response(sample()));
  vi.stubGlobal("fetch", fetch);
  const w = mount(LedgerAnalysisBasis, {
    props: { accountId: "a", holdings: true, refreshKey: 0 },
  });
  await w.get("form").trigger("submit");
  await flushPromises();
  fetch.mockResolvedValue(
    response({ ...sample("a"), status: "pending_recalculation" }),
  );
  expect(w.find('select[name="track"]').exists()).toBe(false);
  await w.setProps({ refreshKey: 1 });
  await flushPromises();
  expect(fetch.mock.calls[1][0]).not.toContain("track=");
  expect(fetch.mock.calls[1][0]).toContain("since_revision=4");
  const source = w.get('[data-test="analysis-source"]');
  expect((source.element as HTMLDetailsElement).open).toBe(false);
  (source.element as HTMLDetailsElement).open = true;
  await source.trigger("toggle");
  expect(source.text()).toContain("需要核实或修正");
  fetch.mockResolvedValue(response(sample("wrong")));
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.find('[role="alert"]').exists()).toBe(true);
  expect(w.text()).not.toContain("90071992547409.01");
  w.unmount();
});

it("discards late results after switching accounts and hides former basis on read failure", async () => {
  let resolve!: (value: Response) => void;
  const fetch = vi.fn().mockImplementationOnce(
    () =>
      new Promise<Response>((r) => {
        resolve = r;
      }),
  );
  vi.stubGlobal("fetch", fetch);
  const w = mount(LedgerAnalysisBasis, {
    props: { accountId: "a", refreshKey: 0 },
  });
  await w.get("form").trigger("submit");
  await flushPromises();
  await w.setProps({ accountId: "b" });
  resolve(response(sample()));
  await flushPromises();
  expect(w.text()).not.toContain("90071992547409.01");
  fetch.mockResolvedValueOnce(response(sample("b")));
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.text()).toContain("90071992547409.01");
  fetch.mockRejectedValueOnce(new TypeError("synthetic"));
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.find('[role="alert"]').exists()).toBe(true);
  expect(w.text()).not.toContain("90071992547409.01");
  w.unmount();
});

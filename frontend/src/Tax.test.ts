// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Tax from "./Tax.vue";
import {
  taxExact,
  taxKey,
  taxNumber,
  taxMoney,
  taxPrice,
  securityIdentity,
  type TaxRecord,
  type TaxState,
  type TaxReport,
} from "./tax";

const row: TaxRecord = {
  date: "20250101",
  account: "合成账户",
  account_id: "001",
  kind: "资金进出",
  code: "",
  market: "",
  direction: "IN",
  currency: "HKD",
  quantity: "",
  price: "",
  amount: "",
  fee: "0",
  change: "10.00",
  note: "合成凭据",
  reason: "股息收入",
  source: "example.csv",
  line: 2,
};
let wrapper: VueWrapper;
let state: TaxState;
let report: TaxReport;
let failImport: boolean;
let fetcher: ReturnType<typeof vi.fn>;
let imports: number;
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });
const buttons = (text: string) =>
  wrapper.findAll("button").find((b) => b.text() === text)!;
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
  });
  imports = 0;
  report = {
    rows: [],
    matches: [],
    entries: [],
    missing: [],
    method: "fifo-cost-basis-v2",
  };
  failImport = false;
  state = {
    records: [],
    imports: [],
    rates: [
      {
        year: 2025,
        currency: "HKD",
        value: "1",
        date: "2025-12-30",
        source: "synthetic",
      },
    ],
  };
  fetcher = vi.fn(async (url: string, init: RequestInit = {}) => {
    expect(new Headers(init.headers).get("X-Tax-Key")).toMatch(
      /^[a-f0-9]{64}$/,
    );
    const u = new URL(url, "http://localhost");
    if (u.pathname.endsWith("/state")) return response(state);
    if (u.pathname.endsWith("/report")) return response(report);
    if (u.pathname.endsWith("/names"))
      return response({ names: { hk00325: "布鲁可", usNIO: "蔚来" } });
    if (u.pathname.endsWith("/preview"))
      return response({
        digest: "d".repeat(64),
        count: 1,
        added: state.records.length ? 0 : 1,
        rows: [row],
      });
    if (u.pathname.endsWith("/imports")) {
      imports++;
      expect((init.body as FormData).get("digest")).toBe("d".repeat(64));
      if (failImport) {
        failImport = false;
        return response({ message: "模拟网络错误" }, 503);
      }
      const added = state.records.length ? 0 : 1;
      state.records = [row];
      state.imports = [
        {
          id: 1,
          name: "example.csv",
          digest: "d",
          count: 1,
          created: "2026-01-01",
        },
      ];
      return response({ added });
    }
    if (init.method === "DELETE") {
      state.records = [];
      state.imports = [];
      return response({ ok: true });
    }
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});

it("previews a real File, preserves import errors and retries without a second committed record", async () => {
  wrapper = mount(Tax);
  await flushPromises();
  const input = wrapper.get('input[type="file"]');
  Object.defineProperty(input.element, "files", {
    configurable: true,
    value: [new File(["csv"], "example.csv", { type: "text/csv" })],
  });
  await input.trigger("change");
  await flushPromises();
  expect(wrapper.text()).toContain("1 条，预计新增 1 条");
  expect(imports).toBe(0);
  failImport = true;
  await buttons("确认导入").trigger("click");
  await flushPromises();
  expect(wrapper.get('[role="alert"]').text()).toContain("模拟网络错误");
  await buttons("确认导入").trigger("click");
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(wrapper.text()).toContain("本次新增 1 条记录");
  await buttons("对账单").trigger("click");
  expect(wrapper.get("#tax-view-records").isVisible()).toBe(true);
  expect(wrapper.get(".tax-records tbody").text()).toContain("合成凭据");
  expect(state.records).toHaveLength(1);
  expect(imports).toBe(2);
});

it("filters dates through the report API and removes an imported batch", async () => {
  state.records = [row];
  state.imports = [
    {
      id: 1,
      name: "example.csv",
      digest: "d",
      count: 1,
      created: "2026-01-01",
    },
  ];
  wrapper = mount(Tax);
  await flushPromises();
  await wrapper.get('.tax-filters input[type="date"]').setValue("2025-06-01");
  await flushPromises();
  expect(
    fetcher.mock.calls.some((c) => String(c[0]).includes("from=2025-06-01")),
  ).toBe(true);
  await wrapper
    .findAll("button")
    .find((b) => b.text().startsWith("导入记录"))!
    .trigger("click");
  await buttons("移除批次").trigger("click");
  await flushPromises();
  expect(state.records).toHaveLength(0);
  expect(wrapper.text()).toContain("已移除导入批次");
});

it("shows the yearly rates read-only without editing controls", async () => {
  state.rates = [
    {
      year: 2025,
      currency: "HKD",
      value: "0.90322",
      date: "2025-12-31",
      source: "https://example.invalid/rate",
    },
    {
      year: 2025,
      currency: "USD",
      value: "7.0288",
      date: "2025-12-31",
      source: "synthetic",
    },
  ];
  wrapper = mount(Tax);
  await flushPromises();
  await buttons("汇率").trigger("click");
  const view = wrapper.get("#tax-view-rates");
  expect(view.text()).toContain("0.90322");
  expect(view.text()).toContain("7.0288");
  expect(view.get("thead").text()).toContain("港币（HKD）");
  expect(view.get("thead").text()).toContain("美元（USD）");
  expect(view.findAll("tbody tr")).toHaveLength(1);
  expect(view.findAll("a")).toHaveLength(0);
  expect(view.text()).not.toContain("synthetic");
  expect(view.text()).not.toContain("来源");
  expect(wrapper.get(".tax-filters").isVisible()).toBe(false);
  expect(view.find("form").exists()).toBe(false);
  expect(view.findAll("button")).toHaveLength(0);
  expect(view.findAll("input, select")).toHaveLength(0);
  expect(fetcher.mock.calls.some((c) => String(c[0]).includes("/rates"))).toBe(
    false,
  );
});

it("formats money with explicit units without floating point rounding", () => {
  expect(taxNumber("9007199254740993.12345678")).toBe(
    "9,007,199,254,740,993.12345678",
  );
  expect(taxExact("0.000000000001")).toBe("0.000000000001");
  expect(taxExact("1.23000000")).toBe("1.23");
  expect(taxMoney("9007199254740993.125", "HKD")).toBe(
    "9,007,199,254,740,993.13 港币",
  );
  expect(taxMoney("-0.005", "USD")).toBe("-0.01 美元");
  expect(taxMoney("0", "CNY")).toBe("0.00 人民币");
  expect(taxPrice("19.79770000", "HKD")).toBe("19.7977 港币");
  expect(
    securityIdentity({ ...row, kind: "证券", market: "SEHK", code: "325" })
      .code,
  ).toBe("00325");
  expect(
    securityIdentity({ ...row, kind: "证券", market: "US", code: "BRK.B" })
      .code,
  ).toBe("BRK.B");
  expect(
    securityIdentity({ ...row, kind: "基金", market: "FD", code: "880002" })
      .key,
  ).toBe("");
  const key = taxKey();
  expect(taxKey()).toBe(key);
  expect(key).toHaveLength(64);
});

it("numbers statement rows continuously, resolves names, and hides source metadata and exports", async () => {
  state.records = Array.from({ length: 51 }, (_, i) => ({
    ...row,
    kind: "证券",
    code: "325",
    market: "SEHK",
    quantity: "100",
    price: "60.95863333",
    amount: "-1000",
    fee: "1",
    change: "-1001",
    line: i + 2,
  }));
  wrapper = mount(Tax);
  await flushPromises();
  await buttons("对账单").trigger("click");
  const table = wrapper.get(".tax-records");
  expect(table.findAll("tbody tr")[0]!.findAll("td")[0]!.text()).toBe("1");
  expect(table.text()).toContain("00325");
  expect(table.text()).toContain("布鲁可");
  expect(table.text()).toContain("60.95863333 港币");
  expect(table.text()).toContain("-1,001.00 港币");
  expect(table.text()).not.toContain("example.csv");
  expect(table.text()).not.toContain("日精度");
  expect(wrapper.text()).not.toContain("导出");
  await buttons("下一页").trigger("click");
  expect(table.findAll("tbody tr")[0]!.findAll("td")[0]!.text()).toBe("51");
  await wrapper.get('input[type="search"]').setValue("布鲁可");
  expect(table.text()).toContain("00325");
});

it("opens an annual modal with opening lots before closing, full accounts and cash dividends", async () => {
  const opening = {
    ...row,
    kind: "证券",
    code: "325",
    market: "SEHK",
    direction: "买入开仓",
    quantity: "100",
    price: "10",
    amount: "-1000",
    fee: "10",
    change: "-1010",
    account: "开仓账户",
    account_id: "original-001",
    date: "20240101",
  };
  const closing = {
    ...opening,
    account: "平仓账户",
    account_id: "new-002",
    date: "20250102",
    direction: "卖出平仓",
    quantity: "-100",
    amount: "1200",
    fee: "2",
    change: "1198",
    price: "12",
  };
  state.records = [opening, closing, row];
  report.rows = [
    {
      year: 2025,
      currency: "CNY",
      sell_close: "1200",
      buy_open: "1000",
      buy_close: "0",
      sell_open: "0",
      redemption: "0",
      subscription: "0",
      trade_fees: "12",
      incurred_trade_fees: "2",
      realized_open_fees: "10",
      realized_close_fees: "2",
      deferred_open_fees: "0",
      unmatched_close_fees: "0",
      other_fees: "0",
      fees: "12",
      net: "188",
      dividends: "10",
      profit_tax: "37.6",
      dividend_tax: "2",
      tax: "39.6",
      rate: null,
      complete: true,
      missing_rate: false,
    },
  ];
  report.entries = [
    {
      record: closing,
      openings: [
        { record: opening, quantity: "100", amount: "1000", fee: "10" },
      ],
      has_close: true,
      fees: "12",
      capitalized_fee: "0",
      profit: "188",
      dividends: "0",
      complete: true,
    },
    {
      record: row,
      openings: [],
      has_close: false,
      fees: "0",
      capitalized_fee: "0",
      profit: "0",
      dividends: "10",
      complete: true,
    },
  ];
  wrapper = mount(Tax, { attachTo: document.body });
  await flushPromises();
  const button = buttons("详情");
  (button.element as HTMLButtonElement).focus();
  await button.trigger("click");
  await flushPromises();
  const modal = document.querySelector("dialog")!;
  expect(modal.open).toBe(true);
  expect(document.body.style.overflow).toBe("hidden");
  expect(modal.textContent).toContain("original-001");
  expect(modal.textContent).toContain("new-002");
  expect(modal.textContent).toContain("布鲁可");
  expect(modal.textContent).toContain("00325");
  expect(modal.textContent).toContain("188.00 港币");
  expect(modal.textContent).toContain("12.00 港币");
  expect(modal.textContent!.indexOf("开仓信息")).toBeLessThan(
    modal.textContent!.indexOf("平仓信息"),
  );
  expect(modal.querySelector(".tax-cash-entry")!.textContent).toContain(
    "10.00 港币",
  );
  expect(modal.textContent).not.toContain("example.csv");
  expect(modal.textContent).not.toContain("导出");
  modal.dispatchEvent(new Event("cancel", { cancelable: true }));
  await flushPromises();
  expect(document.querySelector("dialog")).toBeNull();
  expect(document.body.style.overflow).toBe("");
  expect(document.activeElement).toBe(button.element);
});

it("distinguishes incurred opening fees, recognized expense and carryforward in an unsold year", async () => {
  report.rows = [
    {
      year: 2024,
      currency: "HKD",
      sell_close: "0",
      buy_open: "0",
      buy_close: "0",
      sell_open: "0",
      redemption: "0",
      subscription: "0",
      trade_fees: "0",
      incurred_trade_fees: "12",
      realized_open_fees: "0",
      realized_close_fees: "0",
      deferred_open_fees: "12",
      unmatched_close_fees: "0",
      other_fees: "0",
      fees: "0",
      net: "0",
      dividends: "0",
      profit_tax: "0",
      dividend_tax: "0",
      tax: "0",
      rate: "1",
      complete: true,
      missing_rate: false,
    },
  ];
  wrapper = mount(Tax);
  await flushPromises();
  const table = wrapper.get(".tax-summary");
  expect(table.text()).toContain("计入损益交易费");
  expect(table.text()).toContain("期内发生 12.00 港币");
  expect(table.get("tbody .tax-deferred-fee").text()).toBe("12.00 港币");
  expect(table.get("tbody .tax-emphasis").text()).toContain("0.00 港币");
});

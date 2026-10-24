// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Tax from "./Tax.vue";
import {
  taxCSV,
  taxExact,
  taxKey,
  taxNumber,
  type TaxRecord,
  type TaxState,
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
let failImport: boolean;
let fetcher: ReturnType<typeof vi.fn>;
let imports: number;
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });
const buttons = (text: string) =>
  wrapper.findAll("button").find((b) => b.text() === text)!;
beforeEach(() => {
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
  });
  imports = 0;
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
    if (u.pathname.endsWith("/report"))
      return response({
        rows: [],
        matches: [],
        missing: [],
        method: "lixinger-compatible-v1",
      });
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
  expect(view.text()).toContain("查看历史中间价来源");
  expect(view.text()).toContain("synthetic");
  expect(view.find("form").exists()).toBe(false);
  expect(view.findAll("button")).toHaveLength(0);
  expect(view.findAll("input, select")).toHaveLength(0);
  expect(fetcher.mock.calls.some((c) => String(c[0]).includes("/rates"))).toBe(
    false,
  );
});

it("keeps financial formatting exact and protects spreadsheet text cells", () => {
  expect(taxNumber("9007199254740993.12345678")).toBe(
    "9,007,199,254,740,993.12345678",
  );
  expect(taxExact("0.000000000001")).toBe("0.000000000001");
  expect(taxExact("1.23000000")).toBe("1.23");
  const csv = taxCSV([["=SUM(1,2)", "-1.25", 'a"b\nc']]);
  expect(csv).toContain('"\'=SUM(1,2)"');
  expect(csv).toContain('"-1.25"');
  expect(csv).toContain('"a""b\nc"');
  const key = taxKey();
  expect(taxKey()).toBe(key);
  expect(key).toHaveLength(64);
});

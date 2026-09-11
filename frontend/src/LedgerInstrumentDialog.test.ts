// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import LedgerInstrumentDialog from "./LedgerInstrumentDialog.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { Instrument } from "./ledger";

let wrapper: VueWrapper;
let fetcher: ReturnType<typeof vi.fn>;
let workspace: ReturnType<typeof createLedgerWorkspace>;
const stock = {
  name: "贵州茅台",
  market: "SH",
  code: "600519",
  currency: "CNY",
};
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  fetcher = vi.fn(async (_path: string, options?: RequestInit) =>
    response(
      options?.method === "POST"
        ? JSON.parse(options.body as string)
        : { items: [stock] },
    ),
  );
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function start(instruments: Instrument[] = []) {
  workspace = createLedgerWorkspace(() => {});
  wrapper = mount(LedgerInstrumentDialog, {
    props: { instruments },
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace } },
  });
}
const field = (name: string) => wrapper.get(`[name="security_${name}"]`);
const value = (name: string) => (field(name).element as HTMLInputElement).value;
async function lookup(code = "600519") {
  await field("search").setValue(code);
  await field("search").trigger("keydown", { key: "Enter" });
  await flushPromises();
}
it("fills exact identity by code without a write, then registers on explicit submit", async () => {
  start();
  await lookup("SH600519");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]![0]).toBe(
    "/api/platform/ledger/instruments/search?code=sh600519",
  );
  expect([
    value("name"),
    value("market"),
    value("code"),
    value("currency"),
  ]).toEqual(["贵州茅台", "SH", "600519", "CNY"]);
  expect(wrapper.text()).toContain("已按腾讯查询结果回填");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  const options = fetcher.mock.calls[1]![1] as RequestInit;
  expect(options.method).toBe("POST");
  expect(JSON.parse(options.body as string)).toMatchObject(stock);
  expect(wrapper.emitted("registered")).toHaveLength(1);
});
it.each([
  { name: "腾讯控股", market: "HK", code: "00700", currency: "HKD" },
  { name: "腾讯控股-R", market: "HK", code: "80700", currency: "CNY" },
  { name: "云赛B股", market: "SH", code: "900901", currency: "USD" },
  { name: "南玻B", market: "SZ", code: "200012", currency: "HKD" },
])("preserves queried currency and leading zeros for $code", async (item) => {
  fetcher.mockResolvedValueOnce(response({ items: [item] }));
  start();
  await lookup(item.code);
  expect(value("currency")).toBe(item.currency);
  expect(value("code")).toBe(item.code);
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(
    JSON.parse((fetcher.mock.calls[1]![1] as RequestInit).body as string),
  ).toMatchObject(item);
});
it("requires selection for multiple exact markets instead of choosing the first", async () => {
  const second = { ...stock, market: "SZ", name: "合成另一市场证券" };
  fetcher.mockResolvedValueOnce(response({ items: [stock, second] }));
  start();
  await lookup();
  expect(value("name")).toBe("");
  expect(wrapper.text()).toContain("找到多个市场的证券");
  await wrapper
    .get(".lp-search-results")
    .findAll("button")[1]!
    .trigger("click");
  expect(value("market")).toBe("SZ");
  expect(value("name")).toBe(second.name);
});
it.each([
  { result: { items: [] }, status: 200, message: "未找到支持的股票" },
  {
    result: { code: "instrument_search_timeout" },
    status: 504,
    message: "证券查询超时",
  },
  {
    result: { code: "instrument_search_unavailable" },
    status: 502,
    message: "证券查询服务暂不可用",
  },
  {
    result: { items: [{ ...stock, code: "000001" }] },
    status: 200,
    message: "invalid_response",
  },
  {
    result: { items: [{ ...stock, currency: "EUR" }] },
    status: 200,
    message: "invalid_response",
  },
])(
  "allows manual entry after empty or failed lookup: $message",
  async ({ result, status, message }) => {
    fetcher.mockResolvedValueOnce(response(result, status));
    start();
    await lookup();
    expect(value("name")).toBe("");
    expect(wrapper.text()).toContain(message);
    await field("name").setValue("手工证券");
    await field("code").setValue("600519");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(wrapper.emitted("registered")).toHaveLength(1);
  },
);
it("rejects incomplete codes before network access", async () => {
  start();
  await lookup("700");
  expect(fetcher).not.toHaveBeenCalled();
  expect(wrapper.text()).toContain("请输入完整代码");
});
it("ignores a superseded response even when the transport ignores cancellation", async () => {
  let finish!: (response: Response) => void;
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  start();
  await lookup();
  expect((field("name").element as HTMLInputElement).matches(":disabled")).toBe(
    true,
  );
  const signal = (fetcher.mock.calls[0]![1] as RequestInit).signal!;
  await field("search").setValue("00700");
  expect(signal.aborted).toBe(true);
  const hk = { name: "腾讯控股", market: "HK", code: "00700", currency: "HKD" };
  fetcher.mockResolvedValueOnce(response({ items: [hk] }));
  await lookup("00700");
  finish(response({ items: [stock] }));
  await flushPromises();
  expect(value("name")).toBe(hk.name);
  await field("search").setValue("000001");
  expect(value("name")).toBe("");
  expect(value("code")).toBe("");
});
it("prevents duplicate registration and preserves the original idempotent write on retry", async () => {
  start([{ ...stock, id: "existing" } as Instrument]);
  await lookup();
  await wrapper.get("form").trigger("submit");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(wrapper.text()).toContain("该证券已登记");
  await wrapper.setProps({ instruments: [] });
  fetcher.mockRejectedValueOnce(new Error("lost response"));
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(workspace.locked.value).toBe(true);
  expect(
    (field("search").element as HTMLInputElement).matches(":disabled"),
  ).toBe(true);
  const first = fetcher.mock.calls[1]![1] as RequestInit;
  await workspace.retry();
  const retry = fetcher.mock.calls[2]![1] as RequestInit;
  expect(retry.body).toBe(first.body);
  expect(retry.headers).toEqual(first.headers);
  expect(wrapper.emitted("registered")).toHaveLength(1);
});

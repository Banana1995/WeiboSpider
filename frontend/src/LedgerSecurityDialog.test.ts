// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import LedgerSecurityDialog from "./LedgerSecurityDialog.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";

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
  fetcher = vi.fn(async () => response({ items: [stock] }));
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function start() {
  workspace = createLedgerWorkspace(() => {});
  wrapper = mount(LedgerSecurityDialog, {
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace } },
  });
}
const searchInput = () => wrapper.get('[name="security_search"]');
async function lookup(value: string) {
  await searchInput().setValue(value);
  await searchInput().trigger("keydown", { key: "Enter" });
  await flushPromises();
}
const results = () =>
  wrapper.findAll(".lp-search-results button").map((b) => b.text());

it("searches the same box by code and selects a result immediately", async () => {
  start();
  await lookup("600519");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]![0]).toBe(
    "/api/platform/ledger/instruments/search?q=600519",
  );
  expect(results()[0]).toContain("贵州茅台");
  await wrapper.findAll(".lp-search-results button")[0]!.trigger("click");
  await flushPromises();
  expect(wrapper.emitted("selected")?.[0]?.[0]).toMatchObject(stock);
  expect(
    typeof (wrapper.emitted("selected")?.[0]?.[0] as { id: string }).id,
  ).toBe("string");
});

it("searches the same box by security name", async () => {
  start();
  await lookup("茅台");
  expect(fetcher.mock.calls[0]![0]).toBe(
    "/api/platform/ledger/instruments/search?q=%E8%8C%85%E5%8F%B0",
  );
  expect(results()[0]).toContain("贵州茅台");
});

it("shows every matched identity and never auto-selects the first", async () => {
  const second = { ...stock, market: "SZ", name: "合成另一市场证券" };
  fetcher.mockResolvedValueOnce(response({ items: [stock, second] }));
  start();
  await lookup("600519");
  expect(results()).toHaveLength(2);
  expect(wrapper.emitted("selected")).toBeUndefined();
  await wrapper.findAll(".lp-search-results button")[1]!.trigger("click");
  await flushPromises();
  expect(wrapper.emitted("selected")?.[0]?.[0]).toMatchObject(second);
});

it.each([
  { result: { items: [] }, status: 200 },
  { result: { code: "instrument_search_timeout" }, status: 504 },
  { result: { code: "instrument_search_unavailable" }, status: 502 },
  { result: { items: [{ ...stock, market: "US" }] }, status: 200 },
  { result: { items: [{ ...stock, currency: "EUR" }] }, status: 200 },
])(
  "offers no selectable result for an empty or invalid response",
  async ({ result, status }) => {
    fetcher.mockResolvedValueOnce(response(result, status));
    start();
    await lookup("600519");
    expect(wrapper.findAll(".lp-search-results button")).toHaveLength(0);
    await wrapper.get("input").trigger("keydown", { key: "Enter" });
    await flushPromises();
    expect(wrapper.emitted("selected")).toBeUndefined();
  },
);

it("ignores a superseded response even when the transport ignores cancellation", async () => {
  let finish!: (response: Response) => void;
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  start();
  await lookup("600519");
  const signal = (fetcher.mock.calls[0]![1] as RequestInit).signal!;
  await searchInput().setValue("00700");
  expect(signal.aborted).toBe(true);
  const hk = { name: "腾讯控股", market: "HK", code: "00700", currency: "HKD" };
  fetcher.mockResolvedValueOnce(response({ items: [hk] }));
  await lookup("00700");
  finish(response({ items: [stock] }));
  await flushPromises();
  expect(results()[0]).toContain("腾讯控股");
  await wrapper.findAll(".lp-search-results button")[0]!.trigger("click");
  await flushPromises();
  expect(wrapper.emitted("selected")?.[0]?.[0]).toMatchObject(hk);
});

it("protects the typed draft and exposes no manual identity fields", async () => {
  start();
  await searchInput().setValue("600519");
  expect(wrapper.emitted("dirty")?.at(-1)).toEqual([true]);
  expect(wrapper.find('[name="security_name"]').exists()).toBe(false);
  expect(wrapper.find('[name="security_market"]').exists()).toBe(false);
  expect(wrapper.find('[name="security_currency"]').exists()).toBe(false);
  expect(
    wrapper.findAll("button").some((b) => b.text().includes("使用此证券")),
  ).toBe(false);
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "取消")!
    .trigger("click");
  expect(wrapper.emitted("close")).toHaveLength(1);
});

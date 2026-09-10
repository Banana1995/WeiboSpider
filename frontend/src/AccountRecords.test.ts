// @vitest-environment jsdom
// Updated for the permanent production record table; not executed in this change.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import AccountRecords from "./AccountRecords.vue";
import type { Account, Page } from "./ledger";
import type { AccountRecord } from "./accountRecords";
import { money, recordPage } from "./ledgerView";
import { createLedgerWorkspace, ledgerWorkspaceKey } from "./useLedgerWorkspace";

const account: Account = { id: "a", name: "合成账户", currency: "CNY", opening_date: "2020-01-01", opening_cash: null, version: "1", accounting_mode: "reported", current_holdings_input: "manual_snapshot" };
const sample = (overrides: Partial<AccountRecord> = {}): AccountRecord => ({
  id: "manual-test", account_id: "a", sequence: "9007199254740993", kind: "cash_flow", date: "2020-01-02", flow: "10.01", total_assets: "90071992547409.01", note: "Synthetic note",
  version: "9007199254740993", origin: "manual", original: null, voided: false, created_at: "2020-01-02T00:00:00Z", updated_at: "2020-01-02T00:00:00Z", ...overrides,
});
let wrapper: VueWrapper;
let calls: { path: string; method: string }[];
let responsePage: Page<AccountRecord>;
let failed: boolean;
const workspace = () => createLedgerWorkspace(() => {});
beforeEach(() => {
  calls = []; responsePage = { items: [sample()] }; failed = false;
  vi.stubGlobal("fetch", vi.fn(async (url: string, options: RequestInit = {}) => {
    calls.push({ path: String(url), method: options.method ?? "GET" });
    return new Response(JSON.stringify(failed ? { code: "storage_busy" } : responsePage), { status: failed ? 503 : 200, headers: { "Content-Type": "application/json" } });
  }));
});
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals(); });
async function start() {
  wrapper = mount(AccountRecords, { props: { account, refreshKey: 0 }, global: { provide: { [ledgerWorkspaceKey as symbol]: workspace() } } });
  await flushPromises();
}
it("reads active records immediately and preserves both money columns exactly", async () => {
  await start();
  expect(calls).toHaveLength(1);
  expect(calls[0]!.method).toBe("GET");
  expect(calls[0]!.path).toContain("status=active");
  const cells = wrapper.get("tbody tr").findAll("td");
  expect(cells[2]!.text()).toBe("10.01");
  expect(cells[3]!.text()).toBe("—");
  expect(cells[4]!.text()).toBe("90,071,992,547,409.01");
  expect(wrapper.text()).not.toContain("9007199254740993");
  expect(wrapper.find("pre").exists()).toBe(false);
});
it("shows a positive withdrawal and a dash for absent assets", async () => {
  responsePage = { items: [sample({ flow: "-10.01", total_assets: null })] };
  await start();
  const cells = wrapper.get("tbody tr").findAll("td");
  expect(cells[1]!.text()).toBe("转出");
  expect(cells[2]!.text()).toBe("—");
  expect(cells[3]!.text()).toBe("10.01");
  expect(cells[4]!.text()).toBe("—");
});
it("delegates source-managed edits to the original transaction without writing", async () => {
  responsePage = { items: [sample({ origin: "operation", operation_id: "op-original" })] };
  await start();
  const buttons = wrapper.get("tbody").findAll("button");
  expect(buttons.some(b => b.text() === "编辑")).toBe(false);
  await buttons.find(b => b.text() === "在持仓交易中修改")!.trigger("click");
  expect(wrapper.emitted("operation")).toEqual([["op-original"]]);
  expect(calls.every(c => c.method === "GET")).toBe(true);
});
it("clears old rows on failed refresh instead of showing stale financial facts", async () => {
  await start(); failed = true;
  await wrapper.setProps({ refreshKey: 1 }); await flushPromises();
  expect(wrapper.find("tbody").exists()).toBe(false);
  expect(wrapper.get('[role="alert"]').text()).toContain("读取失败");
});
it("uses server cursors unchanged and returns through cursor history", async () => {
  const cursor = "2020-01-02:9007199254740993";
  responsePage = { items: [sample()], next_cursor: cursor };
  await start();
  responsePage = { items: [sample({ id: "manual-earlier", sequence: "9007199254740992" })] };
  await wrapper.findAll("button").find(b => b.text() === "下一页")!.trigger("click"); await flushPromises();
  expect(new URL(calls.at(-1)!.path, "http://localhost").searchParams.get("cursor")).toBe(cursor);
  responsePage = { items: [sample()], next_cursor: cursor };
  await wrapper.findAll("button").find(b => b.text() === "上一页")!.trigger("click"); await flushPromises();
  expect(new URL(calls.at(-1)!.path, "http://localhost").searchParams.has("cursor")).toBe(false);
});
it("rejects cross-account, out-of-filter and misordered pages without coercing IDs", () => {
  expect(() => recordPage({ items: [sample({ account_id: "b" })] }, "a", "", "", "active")).toThrow();
  expect(() => recordPage({ items: [sample({ voided: true })] }, "a", "", "", "active")).toThrow();
  expect(() => recordPage({ items: [sample()] }, "a", "2021-01-01", "", "active")).toThrow();
  expect(() => recordPage({ items: [sample({ sequence: "9007199254740992" }), sample()] }, "a", "", "", "active")).toThrow();
  expect(money("0.00")).toBe("0.00");
  expect(money(null)).toBe("—");
});

// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import AccountRecords from "./AccountRecords.vue";
import type { Account, Page } from "./ledger";
import type { AccountRecord } from "./accountRecords";
import { money, recordPage } from "./ledgerView";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";

const account: Account = {
  id: "a",
  name: "合成账户",
  currency: "CNY",
  opening_date: "2020-01-01",
  opening_cash: null,
  version: "1",
  current_holdings_input: "manual_snapshot",
};
const sample = (overrides: Partial<AccountRecord> = {}): AccountRecord => ({
  id: "manual-test",
  account_id: "a",
  sequence: "9007199254740993",
  kind: "cash_flow",
  date: "2020-01-02",
  flow: "10.01",
  total_assets: "90071992547409.01",
  note: "Synthetic note",
  version: "9007199254740993",
  origin: "manual",
  original: null,
  voided: false,
  created_at: "2020-01-02T00:00:00Z",
  updated_at: "2020-01-02T00:00:00Z",
  ...overrides,
});
let wrapper: VueWrapper;
let calls: { path: string; method: string }[];
let responsePage: Page<AccountRecord>;
let failed: boolean;
const workspace = () => createLedgerWorkspace(() => {});
beforeEach(() => {
  calls = [];
  responsePage = { items: [sample()] };
  failed = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, options: RequestInit = {}) => {
      calls.push({ path: String(url), method: options.method ?? "GET" });
      return new Response(
        JSON.stringify(failed ? { code: "storage_busy" } : responsePage),
        {
          status: failed ? 503 : 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
async function start(open = true) {
  wrapper = mount(AccountRecords, {
    props: { account, refreshKey: 0 },
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace() } },
  });
  await flushPromises();
  if (open) {
    await wrapper.get(".lp-record-summary").trigger("click");
    await wrapper.get("details.lp-records").trigger("toggle");
    await flushPromises();
  }
}
it("defers the first records read until expanded and preserves exact money columns", async () => {
  await start(false);
  expect(
    (wrapper.get("details.lp-records").element as HTMLDetailsElement).open,
  ).toBe(false);
  expect(calls).toHaveLength(0);
  expect(
    wrapper.get(".lp-record-summary").findAll("button, input, select"),
  ).toHaveLength(0);
  await wrapper.get(".lp-record-summary").trigger("click");
  await wrapper.get("details.lp-records").trigger("toggle");
  await flushPromises();
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
it("keeps the fold state through refresh but closes and clears it for a new account", async () => {
  await start();
  await wrapper.setProps({ refreshKey: 1 });
  await flushPromises();
  expect(
    (wrapper.get("details.lp-records").element as HTMLDetailsElement).open,
  ).toBe(true);
  expect(calls).toHaveLength(2);
  await wrapper.get(".lp-record-summary").trigger("click");
  await wrapper.get("details.lp-records").trigger("toggle");
  await wrapper.setProps({ refreshKey: 2 });
  await flushPromises();
  expect(calls).toHaveLength(2);
  expect(
    (wrapper.get("details.lp-records").element as HTMLDetailsElement).open,
  ).toBe(false);
  await wrapper.setProps({ account: { ...account, id: "b" } });
  expect(wrapper.find("tbody").exists()).toBe(false);
  expect(
    (wrapper.get("details.lp-records").element as HTMLDetailsElement).open,
  ).toBe(false);
});
it("opens before locating a collapsed record and ignores the ensuing native toggle", async () => {
  await start(false);
  const scroll = vi.fn();
  const original = HTMLElement.prototype.scrollIntoView;
  HTMLElement.prototype.scrollIntoView = scroll;
  try {
    const locating = (
      wrapper.vm as unknown as {
        locate: (e: {
          id: string;
          date: string;
          accountId: string;
        }) => Promise<void>;
      }
    ).locate({ id: "manual-test", date: "2020-01-02", accountId: "a" });
    expect(
      (wrapper.get("details.lp-records").element as HTMLDetailsElement).open,
    ).toBe(true);
    await wrapper.get("details.lp-records").trigger("toggle");
    await locating;
    await flushPromises();
    expect(calls).toHaveLength(1);
    expect(calls[0]!.path).toContain("from=2020-01-02&to=2020-01-02");
    expect(wrapper.get("tbody tr").classes()).toContain("lp-highlighted");
    expect(scroll).toHaveBeenCalledOnce();
  } finally {
    HTMLElement.prototype.scrollIntoView = original;
  }
});
it("fences a delayed locate after switching accounts", async () => {
  await start(false);
  let resolve!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((done) => {
          resolve = done;
        }),
    ),
  );
  const locating = (
    wrapper.vm as unknown as {
      locate: (e: {
        id: string;
        date: string;
        accountId: string;
      }) => Promise<void>;
    }
  ).locate({ id: "manual-test", date: "2020-01-02", accountId: "a" });
  await wrapper.setProps({ account: { ...account, id: "b" } });
  resolve(
    new Response(JSON.stringify(responsePage), {
      headers: { "Content-Type": "application/json" },
    }),
  );
  await locating;
  await flushPromises();
  expect(wrapper.find("tbody").exists()).toBe(false);
  expect(wrapper.text()).not.toContain("已定位资金记录");
  expect(
    (wrapper.get("details.lp-records").element as HTMLDetailsElement).open,
  ).toBe(false);
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
it("rejects retired operation-origin records rather than offering a dead transaction link", async () => {
  responsePage = {
    items: [sample({ origin: "operation" as never })],
  };
  await start();
  expect(wrapper.text()).toContain("invalid_response");
  expect(wrapper.find("tbody").exists()).toBe(false);
  expect(wrapper.text()).not.toContain("在持仓交易中修改");
  expect(calls.every((c) => c.method === "GET")).toBe(true);
});
it("clears old rows on failed refresh instead of showing stale financial facts", async () => {
  await start();
  failed = true;
  await wrapper.setProps({ refreshKey: 1 });
  await flushPromises();
  expect(wrapper.find("tbody").exists()).toBe(false);
  expect(wrapper.get('[role="alert"]').text()).toContain("读取失败");
});
it("uses server cursors unchanged and returns through cursor history", async () => {
  const cursor = "2020-01-02:9007199254740993";
  responsePage = { items: [sample()], next_cursor: cursor };
  await start();
  responsePage = {
    items: [sample({ id: "manual-earlier", sequence: "9007199254740992" })],
  };
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "下一页")!
    .trigger("click");
  await flushPromises();
  expect(
    new URL(calls.at(-1)!.path, "http://localhost").searchParams.get("cursor"),
  ).toBe(cursor);
  responsePage = { items: [sample()], next_cursor: cursor };
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "上一页")!
    .trigger("click");
  await flushPromises();
  expect(
    new URL(calls.at(-1)!.path, "http://localhost").searchParams.has("cursor"),
  ).toBe(false);
});
it("rejects cross-account, out-of-filter and misordered pages without coercing IDs", () => {
  expect(() =>
    recordPage({ items: [sample({ account_id: "b" })] }, "a", "", "", "active"),
  ).toThrow();
  expect(() =>
    recordPage({ items: [sample({ voided: true })] }, "a", "", "", "active"),
  ).toThrow();
  expect(() =>
    recordPage({ items: [sample()] }, "a", "2021-01-01", "", "active"),
  ).toThrow();
  expect(() =>
    recordPage(
      { items: [sample({ sequence: "9007199254740992" }), sample()] },
      "a",
      "",
      "",
      "active",
    ),
  ).toThrow();
  expect(money("0.00")).toBe("0.00");
  expect(money(null)).toBe("—");
});

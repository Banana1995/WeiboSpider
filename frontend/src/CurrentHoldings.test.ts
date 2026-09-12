// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Panel from "./CurrentHoldings.vue";
import {
  validateCurrentHoldings,
  type CurrentHoldings,
} from "./currentHoldings";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { Instrument } from "./ledger";

const stock: Instrument = {
  id: "existing",
  market: "SH",
  code: "600000",
  name: "Synthetic",
  currency: "CNY",
};
const empty: CurrentHoldings = {
  account_id: "a",
  audit_id: "",
  snapshot: null,
};
let current: CurrentHoldings;
let wrapper: VueWrapper;
let fetcher: ReturnType<typeof vi.fn>;
let workspace: ReturnType<typeof createLedgerWorkspace>;
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  current = structuredClone(empty);
  fetcher = vi.fn(async (url: string, init: RequestInit = {}) => {
    if (url.includes("/instruments/search"))
      return response({
        items: [
          {
            name: stock.name,
            market: stock.market,
            code: stock.code,
            currency: stock.currency,
          },
        ],
      });
    if (init.method === "PUT") {
      const input = JSON.parse(init.body as string);
      current = {
        account_id: "a",
        audit_id: "3",
        snapshot: {
          version: String(BigInt(input.expected_version) + 1n),
          saved_at: "2026-09-08T00:00:00Z",
          cash: input.cash,
          positions: input.positions.toSorted(
            (a: { instrument_id: string }, b: { instrument_id: string }) =>
              a.instrument_id.localeCompare(b.instrument_id),
          ),
        },
      };
    }
    return response(current);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function start() {
  workspace = createLedgerWorkspace(() => {});
  wrapper = mount(Panel, {
    props: {
      accountId: "a",
      currency: "CNY",
      instruments: [stock],
      disabled: false,
      refreshKey: 0,
    },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: workspace,
      },
    },
  });
  await flushPromises();
}
async function button(text: string) {
  const found = wrapper.findAll("button").find((b) => b.text() === text);
  expect(found, text).toBeTruthy();
  await found!.trigger("click");
  await flushPromises();
}
async function selectStock() {
  await wrapper.get('[name="security_search"]').setValue("600000");
  await button("查询证券");
  await wrapper.get('[data-test="instrument-form"]').trigger("submit");
  await flushPromises();
}
async function save() {
  await wrapper.get('[data-test="current-holdings-form"]').trigger("submit");
  await flushPromises();
}
it("adds a queried identity and quantity atomically without standalone registration", async () => {
  await start();
  await button("添加持仓");
  await selectStock();
  await wrapper
    .get('[name="current_quantity_0"]')
    .setValue("9007199254.740993");
  await wrapper.get('[name="current_cash"]').setValue("90071992547409.01");
  await save();
  const writes = fetcher.mock.calls.filter(([, i]) => i?.method === "PUT");
  expect(writes).toHaveLength(1);
  const input = JSON.parse(writes[0]![1]!.body as string);
  expect(input.positions[0]).toEqual({
    instrument_id: input.securities[0].id,
    quantity: "9007199254.740993",
  });
  expect(input.securities[0]).toMatchObject({
    market: "SH",
    code: "600000",
    name: "Synthetic",
    currency: "CNY",
  });
  expect(input).not.toHaveProperty("baseline_date");
  expect(fetcher.mock.calls.some(([, i]) => i?.method === "POST")).toBe(false);
  expect(wrapper.emitted("saved")).toHaveLength(1);
  expect(wrapper.text()).toContain("历史记录不变");
  expect(wrapper.find("dialog").exists()).toBe(false);
});
it("rejects duplicate market/code on add and edit even with different IDs", async () => {
  current = {
    account_id: "a",
    audit_id: "1",
    snapshot: {
      version: "1",
      saved_at: "2026-09-08T00:00:00Z",
      cash: "0.00",
      positions: [{ instrument_id: stock.id, quantity: "1.000000" }],
    },
  };
  await start();
  await button("添加持仓");
  await selectStock();
  expect(wrapper.text()).toContain("本账户已持有该市场和代码");
  expect(wrapper.findAll(".lp-security-cell")).toHaveLength(1);
  expect(wrapper.findAll('[name^="current_quantity_"]')).toHaveLength(0);
  expect(fetcher.mock.calls.some(([, i]) => i?.method === "PUT")).toBe(false);
});
it("edits quantity and deletes holdings inline without creating asset or flow rows", async () => {
  current = {
    account_id: "a",
    audit_id: "1",
    snapshot: {
      version: "1",
      saved_at: "2026-09-08T00:00:00Z",
      cash: "10.00",
      positions: [{ instrument_id: stock.id, quantity: "1.000000" }],
    },
  };
  await start();
  await button("编辑");
  await wrapper.get('[name="current_quantity_0"]').setValue("2.5");
  await save();
  expect(current.snapshot?.positions[0]).toEqual({
    instrument_id: stock.id,
    quantity: "2.5",
  });
  await button("删除");
  expect(current.snapshot?.positions).toHaveLength(1);
  await save();
  expect(current.snapshot?.positions).toEqual([]);
  expect(current.snapshot?.cash).toBe("10.00");
  expect(
    fetcher.mock.calls.every(([url]) => url.endsWith("/current-holdings")),
  ).toBe(true);
});
it("retries uncertain writes with the same body and idempotency key", async () => {
  await start();
  await button("设置现金");
  fetcher.mockRejectedValueOnce(new TypeError("response lost"));
  await save();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  await button("重试保存持仓");
  const writes = fetcher.mock.calls.filter(([, i]) => i?.method === "PUT");
  expect(writes).toHaveLength(2);
  expect(writes[0]![1]?.body).toBe(writes[1]![1]?.body);
  expect(writes[0]![1]?.headers).toEqual(writes[1]![1]?.headers);
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([false]);
});
it("retains draft after version conflict and requires explicit reload", async () => {
  await start();
  await button("设置现金");
  await wrapper.get('[name="current_cash"]').setValue("15");
  fetcher.mockResolvedValueOnce(response({ code: "version_conflict" }, 409));
  await save();
  expect(wrapper.text()).toContain("记录已被修改");
  expect(wrapper.text()).not.toContain("version_conflict");
  expect(
    (wrapper.get('[name="current_cash"]').element as HTMLInputElement).value,
  ).toBe("15");
  expect(wrapper.emitted("saved")).toBeUndefined();
});
it("distinguishes missing input from explicit zero and rejects malformed quantities", () => {
  expect(validateCurrentHoldings(empty, "a").snapshot).toBeNull();
  const value: CurrentHoldings = {
    account_id: "a",
    audit_id: "1",
    snapshot: {
      version: "1",
      saved_at: "2026-09-08T00:00:00Z",
      cash: "0.00",
      positions: [],
    },
  };
  expect(validateCurrentHoldings(value, "a").snapshot?.cash).toBe("0.00");
  value.snapshot!.positions.push({ instrument_id: "i", quantity: "-1" });
  expect(() => validateCurrentHoldings(value, "a")).toThrow();
});

it("protects inline cash drafts without locking their own save, and clears the navigation guard on cancel", async () => {
  await start();
  await button("设置现金");
  expect(workspace.navigationLocked.value).toBe(true);
  expect(workspace.locked.value).toBe(false);
  await wrapper.get('[name="current_cash"]').setValue("125.25");
  await button("取消");
  expect(wrapper.text()).toContain("放弃尚未保存的修改");
  expect(wrapper.find("dialog").exists()).toBe(false);
  await button("继续编辑");
  expect(
    (wrapper.get('[name="current_cash"]').element as HTMLInputElement).value,
  ).toBe("125.25");
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true);
  await button("取消");
  await button("放弃修改");
  expect(workspace.navigationLocked.value).toBe(false);
  expect(fetcher.mock.calls.some(([, init]) => init.method === "PUT")).toBe(
    false,
  );
});

it("protects a securities search draft without opening any dialog", async () => {
  await start();
  await button("添加持仓");
  await wrapper.get('[name="security_search"]').setValue("600000");
  await button("取消");
  expect(wrapper.text()).toContain("放弃尚未保存的修改");
  expect(wrapper.find("dialog").exists()).toBe(false);
  await button("继续编辑");
  expect(
    (wrapper.get('[name="security_search"]').element as HTMLInputElement).value,
  ).toBe("600000");
});

it("allows a security held elsewhere when this account has no duplicate", async () => {
  await start();
  await button("添加持仓");
  await selectStock();
  await wrapper.get('[name="current_quantity_0"]').setValue("2");
  await save();
  expect(current.snapshot?.positions).toHaveLength(1);
  expect(wrapper.emitted("saved")).toHaveLength(1);
});

it("does not show quote values from another holdings version or while refreshing", async () => {
  current = {
    account_id: "a",
    audit_id: "1",
    snapshot: {
      version: "2",
      cash: "10.00",
      positions: [{ instrument_id: stock.id, quantity: "2.500000" }],
      saved_at: "2026-09-12T00:00:00Z",
    },
  };
  await start();
  const valuation = {
    account_id: "a",
    currency: "CNY",
    source: "manual_snapshot",
    as_of: "2026-09-12",
    ledger_at: "2026-09-12T00:00:00Z",
    revision: "a".repeat(64),
    manual_version: "1",
    configured: true,
    cash: "10.00",
    complete: true,
    total_assets: "321.00",
    items: [],
  };
  await wrapper.setProps({ valuation });
  expect(wrapper.text()).not.toContain("321.00");
  expect(wrapper.text()).toContain("持仓已变化");
  await wrapper.setProps({ valuation: { ...valuation, manual_version: "2" } });
  expect(wrapper.text()).toContain("321.00");
  await wrapper.setProps({ valuationLoading: true });
  expect(wrapper.text()).not.toContain("321.00");
  expect(wrapper.text()).toContain("2.5");
  expect(wrapper.text()).not.toContain("2.500000");
});

it("requires an explicit discard before reloading a conflicting draft", async () => {
  await start();
  await button("设置现金");
  await wrapper.get('[name="current_cash"]').setValue("15");
  fetcher.mockResolvedValueOnce(response({ code: "version_conflict" }, 409));
  await save();
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  const count = fetcher.mock.calls.length;
  await button("重新读取最新持仓");
  expect(fetcher).toHaveBeenCalledTimes(count);
  confirm.mockReturnValue(true);
  await button("重新读取最新持仓");
  expect(wrapper.find("form").exists()).toBe(false);
  expect(workspace.navigationLocked.value).toBe(false);
});

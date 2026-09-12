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
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
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
  expect(wrapper.text()).toContain("历史总资产、资金流和收益不变");
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
  expect(wrapper.findAll('[name^="current_quantity_"]')).toHaveLength(1);
  expect(fetcher.mock.calls.some(([, i]) => i?.method === "PUT")).toBe(false);
});
it("edits identity and quantity and deletes holdings without creating asset or flow rows", async () => {
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
  await button("编辑现金与持仓");
  await button("修改证券");
  await wrapper.get('[name="security_name"]').setValue("Changed");
  await wrapper.get('[name="security_code"]').setValue("600001");
  await wrapper.get('[data-test="instrument-form"]').trigger("submit");
  await flushPromises();
  await wrapper.get('[name="current_quantity_0"]').setValue("2.5");
  await save();
  expect(current.snapshot?.positions[0]?.instrument_id).not.toBe(stock.id);
  await button("编辑现金与持仓");
  await button("移除此证券");
  await save();
  expect(current.snapshot?.positions).toEqual([]);
  expect(current.snapshot?.cash).toBe("10.00");
  expect(
    fetcher.mock.calls.every(([url]) => url.endsWith("/current-holdings")),
  ).toBe(true);
});
it("retries uncertain writes with the same body and idempotency key", async () => {
  await start();
  await button("编辑现金与持仓");
  fetcher.mockRejectedValueOnce(new TypeError("response lost"));
  await save();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  await button("按原请求重试确认持仓");
  const writes = fetcher.mock.calls.filter(([, i]) => i?.method === "PUT");
  expect(writes).toHaveLength(2);
  expect(writes[0]![1]?.body).toBe(writes[1]![1]?.body);
  expect(writes[0]![1]?.headers).toEqual(writes[1]![1]?.headers);
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([false]);
});
it("retains draft after version conflict and requires explicit reload", async () => {
  await start();
  await button("编辑现金与持仓");
  await wrapper.get('[name="current_cash"]').setValue("15");
  fetcher.mockResolvedValueOnce(response({ code: "version_conflict" }, 409));
  await save();
  expect(wrapper.text()).toContain("version_conflict");
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

// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Panel from "./LedgerHoldings.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { Account } from "./ledger";
import { emptyStockBook, stockItem } from "./stockBook.testHelpers";

let wrapper: VueWrapper;
const account: Account = {
  id: "a",
  name: "Synthetic",
  currency: "CNY",
  opening_date: "2020-01-01",
  opening_cash: null,
  version: "1",
  current_holdings_input: "manual_snapshot",
};
const props = {
  account,
  accounts: [account],
  instruments: [],
  instrumentsError: "",
  instrumentsLoading: false,
  refreshKey: 0,
};
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
function start() {
  wrapper = mount(Panel, {
    props,
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
}
it("reads a single stock book, shows cash and refreshes without writing account assets", async () => {
  const fetcher = vi.fn(
    async () =>
      new Response(JSON.stringify({ ...emptyStockBook(), cash: "123.00" })),
  );
  vi.stubGlobal("fetch", fetcher);
  start();
  await flushPromises();
  expect(wrapper.text()).toContain("我的持仓");
  expect(wrapper.get(".stock-cash").text()).toContain("123.00");
  expect(wrapper.findAll(".stock-cash")).toHaveLength(1);
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "刷新")!
    .trigger("click");
  await flushPromises();
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(
    fetcher.mock.calls.every(
      (args) => !(args as unknown as [string, RequestInit])[1]?.method,
    ),
  ).toBe(true);
});
it("opens each stock with its costs and profits, preserving a visibly stale list after a failed refresh", async () => {
  let failed = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      failed
        ? new Response(JSON.stringify({ code: "storage_busy" }), {
            status: 503,
          })
        : new Response(
            JSON.stringify({
              ...emptyStockBook(),
              version: "1",
              items: [stockItem()],
            }),
          ),
    ),
  );
  start();
  await flushPromises();
  expect(wrapper.findAll(".stock-row")).toHaveLength(1);
  await wrapper.get(".stock-row").trigger("click");
  expect(wrapper.text()).toContain("摊薄成本");
  expect(wrapper.text()).toContain("380");
  expect(wrapper.text()).toContain("个股累计盈亏");
  expect(wrapper.text()).toContain("86,720.00");
  failed = true;
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "刷新")!
    .trigger("click");
  await flushPromises();
  expect(wrapper.get('[role="alert"]').text()).toContain("可能已过期");
  expect(wrapper.text()).toContain("合成腾讯");
  expect(
    wrapper
      .findAll("button")
      .find((b) => b.text() === "＋ 加仓")!
      .attributes("disabled"),
  ).toBeDefined();
});
it("shows total funds and position weight, and sorts by weight on header click", async () => {
  const base = stockItem();
  const low = {
    ...base,
    instrument: {
      ...base.instrument,
      id: "low",
      code: "000001",
      name: "合成低仓",
    },
    weight: "10.00",
    market_value: "10000.00",
  };
  const high = {
    ...base,
    instrument: {
      ...base.instrument,
      id: "high",
      code: "600519",
      name: "合成高仓",
    },
    weight: "90.00",
    market_value: "90000.00",
  };
  const book = {
    ...emptyStockBook(),
    version: "1",
    positions_value: "100000.00",
    total_assets: "100000.00",
    items: [low, high],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(book))),
  );
  start();
  await flushPromises();
  expect(wrapper.text()).toContain("总资金");
  expect(wrapper.text()).toContain("100,000.00");
  expect(wrapper.text()).toContain("90.00%");
  expect(wrapper.findAll(".stock-row")[0]!.text()).toContain("合成低仓");
  const header = wrapper
    .findAll(".stock-list-labels button")
    .find((b) => b.text().includes("持仓比例"))!;
  expect(header).toBeTruthy();
  await header.trigger("click");
  expect(wrapper.findAll(".stock-row")[0]!.text()).toContain("合成高仓");
  await header.trigger("click");
  expect(wrapper.findAll(".stock-row")[0]!.text()).toContain("合成低仓");
});
it("offers account creation when no account exists", () => {
  wrapper = mount(Panel, {
    props: { ...props, account: undefined, accounts: [] },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  expect(wrapper.text()).toContain("新建账户");
});

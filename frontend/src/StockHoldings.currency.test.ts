// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import StockHoldings from "./StockHoldings.vue";
import { emptyStockBook, stockItem } from "./stockBook.testHelpers";
import type { StockBook, StockItem } from "./stockBook";
import type { Account, FXQuote } from "./ledger";
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
const fx: FXQuote = {
  base: "HKD",
  quote: "CNY",
  mode: "latest",
  requested_date: "2026-09-25",
  rate: "0.85000000",
  date: "2026-09-24",
  source: "Synthetic FX",
  fetched_at: "2026-09-25T01:00:00Z",
};
function foreign(): StockItem {
  const item = stockItem();
  return {
    ...item,
    instrument: { ...item.instrument, name: "合成港币股" },
    quantity: "10.000000",
    price: "120.000000",
    market_value: "1200.00",
    account_market_value: "1020.00",
    quote: { ...item.quote!, price: "120.000000" },
    fx: { ...fx },
  };
}
function domestic(): StockItem {
  const item = foreign();
  return {
    ...item,
    instrument: {
      id: "cny",
      market: "SH",
      code: "600000",
      name: "合成人民币股",
      currency: "CNY",
    },
    price: "110.000000",
    market_value: "1100.00",
    account_market_value: "1100.00",
    quote: {
      ...item.quote!,
      symbol: "sh600000",
      currency: "CNY",
      price: "110.000000",
    },
    fx: null,
  };
}
function book(items: StockItem[]): StockBook {
  return { ...emptyStockBook(), version: "1", items };
}
let wrapper: VueWrapper;
let fetcher: ReturnType<typeof vi.fn>;
async function start(value: StockBook, selectedAccount = account) {
  fetcher = vi.fn(async () => new Response(JSON.stringify(value)));
  vi.stubGlobal("fetch", fetcher);
  wrapper = mount(StockHoldings, {
    attachTo: document.body,
    props: { account: selectedAccount, refreshKey: 0 },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  await flushPromises();
}
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
  document.body.innerHTML = "";
});

it("shows native and account currency market values without duplicating domestic amounts", async () => {
  await start(book([foreign(), domestic()]));
  const [hk, cny] = wrapper.findAll(".stock-row");
  expect(hk!.get(".stock-market-value strong").text()).toMatch(
    /1,200\.00\s*HKD/,
  );
  expect(hk!.get(".stock-value-converted").text()).toMatch(/≈ 1,020\.00\s*CNY/);
  expect(hk!.get(".stock-market-value").text()).toContain("10 股");
  expect(cny!.get(".stock-market-value strong").text()).toMatch(
    /1,100\.00\s*CNY/,
  );
  expect(cny!.find(".stock-value-converted").exists()).toBe(false);
  await hk!.trigger("click");
  const detail = wrapper.get("dialog.stock-detail-dialog");
  expect(detail.get(".stock-market-value strong").text()).toMatch(
    /1,200\.00\s*HKD/,
  );
  expect(detail.get(".stock-value-converted").text()).toMatch(
    /≈ 1,020\.00\s*CNY/,
  );
  expect(detail.get(".stock-detail-fx").text()).toContain("0.8500 CNY");
  expect(detail.get(".stock-detail-fx").text()).toContain("2026-09-24");
  expect(detail.get(".stock-detail-fx").text()).toContain("Synthetic FX");
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("sorts mixed currencies by account value and keeps unavailable conversions last in both directions", async () => {
  const missing: StockItem = {
    ...foreign(),
    instrument: {
      ...foreign().instrument,
      id: "missing",
      code: "00883",
      name: "合成缺汇率",
    },
    market_value: "2000.00",
    account_market_value: null,
    fx: null,
  };
  const zero: StockItem = {
    ...foreign(),
    instrument: {
      ...foreign().instrument,
      id: "zero",
      code: "01030",
      name: "合成已清仓",
    },
    quantity: "0.000000",
    market_value: "0.00",
    account_market_value: "0.00",
    price: null,
    quote: null,
    quote_status: "closed",
    fx: null,
  };
  await start({
    ...book([foreign(), domestic(), missing, zero]),
    complete: false,
    total_assets: null,
  });
  await wrapper.get(".stock-list-toolbar select").setValue("all");
  const sort = wrapper
    .findAll(".stock-list-labels button")
    .find((b) => b.text().includes("市值 / 数量"))!;
  const names = () =>
    wrapper.findAll(".stock-identity strong").map((el) => el.text());
  await sort.trigger("click");
  expect(names()).toEqual([
    "合成人民币股",
    "合成港币股",
    "合成已清仓",
    "合成缺汇率",
  ]);
  await sort.trigger("click");
  expect(names()).toEqual([
    "合成已清仓",
    "合成港币股",
    "合成人民币股",
    "合成缺汇率",
  ]);
  const rows = wrapper.findAll(".stock-row");
  expect(rows[0]!.get(".stock-value-converted").text()).toContain("0.00 CNY");
  expect(rows[3]!.get(".stock-value-converted").text()).toBe("人民币估值暂缺");
  await rows[3]!.trigger("click");
  const detail = wrapper.get("dialog.stock-detail-dialog");
  expect(detail.get(".stock-value-converted").text()).toBe("人民币估值暂缺");
  expect(detail.find(".stock-detail-fx").exists()).toBe(false);
});

it("deduplicates shared FX references and exposes their actual date and source on hover, focus or tap", async () => {
  const second = {
    ...foreign(),
    instrument: { ...foreign().instrument, id: "second", code: "00883" },
  };
  await start(book([foreign(), second]));
  expect(wrapper.findAll(".stock-fx-trigger")).toHaveLength(1);
  const trigger = wrapper.get(".stock-fx-trigger");
  expect(trigger.text()).toContain("1 HKD = 0.8500 CNY");
  expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);
  await trigger.trigger("mouseenter");
  await flushPromises();
  expect(wrapper.get('[role="tooltip"]').text()).toContain(
    "汇率日期 2026-09-24",
  );
  expect(wrapper.get('[role="tooltip"]').text()).toContain(
    "来源：Synthetic FX",
  );
  expect(trigger.attributes("aria-describedby")).toBe(
    wrapper.get('[role="tooltip"]').attributes("id"),
  );
  await wrapper.get(".stock-fx-reference").trigger("mouseleave");
  await trigger.trigger("focus");
  await flushPromises();
  expect(trigger.attributes("aria-expanded")).toBe("true");
  await trigger.trigger("keydown", { key: "Escape" });
  expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);
  await trigger.trigger("click");
  await flushPromises();
  expect(wrapper.get('[role="tooltip"]').text()).toContain(
    "非历史成交结算汇率",
  );
  window.dispatchEvent(new Event("resize"));
  await flushPromises();
  expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("uses the account's currency and retains precise backend values instead of assuming CNY", async () => {
  const usd: Account = { ...account, currency: "USD" };
  const item = foreign();
  item.account_market_value = "153.87";
  item.fx = { ...fx, quote: "USD", rate: "0.12822500" };
  await start({ ...book([item]), currency: "USD" }, usd);
  expect(wrapper.get(".stock-value-converted").text()).toContain("153.87 USD");
  expect(wrapper.get(".stock-fx-trigger").text()).toContain("0.128225 USD");
});

// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import StockHoldings from "./StockHoldings.vue";
import { emptyStockBook, stockItem } from "./stockBook.testHelpers";
import type { ClosedItem, StockBook, StockItem } from "./stockBook";
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
  const allButton = wrapper
    .findAll(".stock-filter button")
    .find((b) => b.text() === "全部")!;
  await allButton.trigger("click");
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

it("shows lifetime trade fees alongside dividends in the stock's own currency", async () => {
  const hk = foreign(),
    cn = domestic();
  hk.metrics = { ...hk.metrics, fees: "1234.56", tax: "0.20" };
  await start(book([hk, cn]));
  const open = async (name: string) => {
    await wrapper
      .findAll(".stock-row")
      .find((row) => row.text().includes(name))!
      .trigger("click");
    return wrapper.get("dialog.stock-detail-dialog");
  };
  const detail = await open(hk.instrument.name);
  const summary = detail.get(".stock-dividend-summary").text();
  expect(summary).toContain("累计手续费 1,234.56 HKD");
  expect(summary).toContain("累计分红");
  expect(summary).toContain("已扣红利税 0.20 HKD");
  await detail.get(".lp-close").trigger("click");
  expect(
    (await open(cn.instrument.name)).get(".stock-dividend-summary").text(),
  ).toContain("累计手续费 0.00 CNY");
});

it("does not display a zero subtotal when none of the holdings can be valued", async () => {
  const missing: StockItem = {
    ...foreign(),
    price: null,
    quote: null,
    fx: null,
    market_value: null,
    account_market_value: null,
    weight: null,
    quote_status: "unavailable",
  };
  await start({
    ...book([missing]),
    complete: false,
    positions_value: null,
    total_assets: null,
  });
  const subtotal = wrapper.get(".stock-totals").findAll(".stock-total")[1]!;
  expect(subtotal.get("strong").text()).toBe("—");
  expect(subtotal.text()).not.toContain("0.00");
  expect(wrapper.get(".stock-total-note").text()).toContain("报价或汇率暂缺");
});

it("shows recovered weights and the last available price without historical price labels", async () => {
  const item = foreign();
  item.quote = {
    ...item.quote!,
    date: "2026-03-19",
    quoted_at: "2026-03-19T00:00:00+08:00",
  };
  item.quote_status = "prior_date";
  item.weight = "45.95";
  const local = { ...domestic(), weight: "49.55" };
  await start({
    ...book([item, local]),
    cash: "100.00",
    positions_value: "2120.00",
    total_assets: "2220.00",
  });
  expect(wrapper.get(".stock-totals").text()).toContain("2,220.00");
  expect(wrapper.text()).not.toContain("含历史参考价估值");
  const rows = wrapper.findAll(".stock-row");
  // Positions list the largest share of the account first by default.
  expect(rows[0]!.text()).toContain("49.55%");
  expect(rows[1]!.text()).toContain("45.95%");
  expect(wrapper.text()).not.toContain("参考价日期");
  await rows.find((row) => row.text().includes("合成港币股"))!.trigger("click");
  const detail = wrapper.get(".stock-detail-dialog");
  expect(detail.get(".stock-quote").attributes("aria-label")).toBe("最新价");
  expect(detail.text()).not.toContain("2026-03-19");
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("identifies a partial subtotal and explains unavailable weights without inventing percentages", async () => {
  const item = {
    ...foreign(),
    quote: null,
    price: null,
    market_value: null,
    account_market_value: null,
    weight: null,
    quote_status: "unavailable" as const,
  };
  await start({
    ...book([item, { ...domestic(), weight: null }]),
    complete: false,
    total_assets: null,
    positions_value: "1100.00",
  });
  expect(wrapper.get(".stock-totals").text()).toContain("已知持仓市值");
  expect(wrapper.get(".stock-total-note").text()).toContain("仓位暂不可算");
  for (const row of wrapper.findAll(".stock-row")) {
    expect(row.get("span:nth-child(3) > strong").text()).toBe("—");
  }
});

it("lists recorded closed rounds under 已清仓 without mixing them into current holdings", async () => {
  const item = domestic();
  const closed: ClosedItem = {
    id: "closed-1",
    version: "1",
    sequence: "1",
    instrument_id: item.instrument.id,
    kind: "closed",
    date: "2026-01-05",
    quantity: "0.000000",
    price: "0.000000",
    fee: "0.00",
    amount: "0.00",
    fx: "0.00000000",
    note: "历史补录",
    voided: false,
    closed: {
      opened_on: "2025-12-05",
      closed_on: "2026-01-05",
      quantity: "100.000000",
      income: "145280.42",
      expense: "142466.71",
      fees: "112.99",
      profit: "2813.71",
      profit_rate: "1.97",
      new_stock: true,
      currency: "HKD",
      flows: [
        {
          kind: "buy",
          date: "2025-12-05",
          price: "1424.46",
          quantity: "100",
          fee: "20.71",
          amount: "142466.71",
          fx: "1",
        },
        {
          kind: "tax",
          date: "2026-01-04",
          price: "0",
          quantity: "0",
          fee: "0",
          amount: "10.00",
          fx: "1",
        },
      ],
      source: "synthetic",
    },
    instrument: item.instrument,
    created_at: "2026-01-05T00:00:00Z",
    updated_at: "2026-01-05T00:00:00Z",
  };
  await start({ ...book([item]), closed: [closed] });
  expect(wrapper.findAll(".stock-closed-row")).toHaveLength(0);
  const closedButton = wrapper
    .findAll(".stock-filter button")
    .find((b) => b.text() === "已清仓")!;
  await closedButton.trigger("click");
  expect(wrapper.findAll(".stock-closed-row")).toHaveLength(1);
  expect(wrapper.get(".stock-closed-row").text()).toContain("+2,813.71");
  expect(wrapper.get(".stock-closed-row").text()).toContain("新股");
  await wrapper.get(".stock-closed-row").trigger("click");
  const dialog = wrapper.get("dialog");
  expect(dialog.text()).toContain("2025-12-05 ~ 2026-01-05");
  expect(dialog.text()).toContain("流水明细");
  expect(dialog.text()).toContain("HKD");
  expect(dialog.text()).toContain("红利税");
});

// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import LedgerHoldings from "./LedgerHoldings.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import {
  validateHoldings,
  holdingNumber,
  validateHoldingTransactions,
  type HoldingsView,
  type HoldingTransaction,
} from "./holdings";
import { todayShanghai } from "./ledgerView";
import type { Account, Instrument } from "./ledger";

const account: Account = {
  id: "account",
  name: "合成账户",
  currency: "CNY",
  opening_date: "2026-01-01",
  version: "1",
  opening_cash: null,
  accounting_mode: "reported",
  current_holdings_input: "manual_snapshot",
};
const instrument: Instrument = {
  id: "stock",
  name: "合成证券",
  code: "600519",
  market: "SH",
  currency: "CNY",
};
const row: HoldingTransaction = {
  id: "trade",
  kind: "buy",
  date: "2026-06-01",
  quantity: "4.000000",
  price: "30.000000",
  amount: "123.00",
  fee: "3.00",
  note: "合成买入记录",
  cycle_id: "cycle",
  source: "manual",
};
function fixture(): HoldingsView {
  return {
    account_id: account.id,
    currency: "CNY",
    source: "manual_snapshot",
    as_of: todayShanghai(),
    ledger_at: new Date().toISOString(),
    revision: "a".repeat(64),
    manual_version: "5",
    trade_date_floor: "2026-06-01",
    configured: true,
    cash: "854.00",
    complete: true,
    total_assets: "1054.00",
    items: [
      {
        instrument,
        quantity: "10.000000",
        market_value: "200.00",
        account_market_value: "200.00",
        price: "20.000000",
        holding_cost: "16.000000",
        diluted_cost: "14.600000",
        weight: "18.98",
        cost_status: "known",
        cycle_id: "cycle",
        quote_status: "current",
        fx: null,
        quote: {
          symbol: "sh600519",
          price: "20.000000",
          currency: "CNY",
          source: "Tencent",
          date: todayShanghai(),
          quoted_at: new Date().toISOString(),
          fetched_at: new Date().toISOString(),
        },
      },
    ],
  };
}
let view: HoldingsView;
let history: HoldingTransaction[];
let wrapper: VueWrapper;
let fetcher: ReturnType<typeof vi.fn>;
let workspace: ReturnType<typeof createLedgerWorkspace>;
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  view = fixture();
  history = [structuredClone(row)];
  fetcher = vi.fn(async (input: string, options: RequestInit = {}) => {
    const url = new URL(input, "http://localhost");
    if (options.method === "POST") {
      const body = JSON.parse(options.body as string);
      const transaction = {
        ...row,
        id: "new-trade",
        date: body.date,
        note: body.note,
        quantity: "2.000000",
        price: "5.000000",
        fee: null,
        amount: "10.00",
      };
      view = {
        ...view,
        manual_version: "6",
        cash: "844.00",
        total_assets: "1084.00",
        items: [
          {
            ...view.items[0]!,
            quantity: "12.000000",
            market_value: "240.00",
            account_market_value: "240.00",
            holding_cost: "14.625000",
            diluted_cost: "13.000000",
            weight: "22.14",
          },
        ],
      };
      history = [transaction, ...history];
      return response(
        {
          account_id: account.id,
          instrument_id: instrument.id,
          version: "6",
          transaction,
        },
        201,
      );
    }
    if (url.pathname.endsWith("/holdings")) return response(view);
    if (url.pathname.endsWith("/current-holdings"))
      return response({
        account_id: account.id,
        audit_id: "5",
        snapshot: {
          version: view.manual_version,
          saved_at: new Date().toISOString(),
          cash: view.cash,
          positions: view.items
            .filter((i) => i.cost_status !== "closed")
            .map((i) => ({
              instrument_id: i.instrument.id,
              quantity: i.quantity,
            })),
        },
      });
    if (url.pathname.endsWith("/transactions"))
      return response({ items: history });
    if (url.pathname.endsWith("/positions")) return response({ items: [] });
    if (url.pathname.endsWith("/operations")) return response({ items: [] });
    throw new Error(`Unexpected request ${input}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function start(a = account) {
  let refresh = 0;
  workspace = createLedgerWorkspace(() => {
    void wrapper.setProps({ refreshKey: ++refresh });
  });
  wrapper = mount(LedgerHoldings, {
    attachTo: document.body,
    props: {
      account: a,
      accounts: [a],
      instruments: [instrument],
      instrumentsError: "",
      instrumentsLoading: false,
      refreshKey: 0,
      operationId: "",
    },
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace } },
  });
  await flushPromises();
}
async function click(text: string) {
  await wrapper
    .findAll("button")
    .find((b) => b.text().trim() === text)!
    .trigger("click");
  await flushPromises();
}
it("renders both cost formulas and the server-provided market value, price and account weight", async () => {
  await start();
  const item = wrapper.get(".lp-holding-row");
  for (const text of [
    "合成证券",
    "600519",
    "200.00",
    "10 份",
    "20.000",
    "16.000",
    "14.600",
    "18.98%",
  ])
    expect(item.text()).toContain(text);
  expect(wrapper.text()).toContain("1,054.00");
  expect(
    fetcher.mock.calls.every(
      ([, options]) => !options.method || options.method === "GET",
    ),
  ).toBe(true);
  expect(wrapper.findAll("dialog")).toHaveLength(0);
});
it("opens native button rows with detail metrics and security-scoped transaction history", async () => {
  await start();
  await wrapper.get(".lp-holding-row").trigger("click");
  await flushPromises();
  const detail = wrapper.get("dialog");
  expect(detail.text()).toContain("合成证券 (600519.SH)");
  expect(detail.text()).toContain("合成买入记录");
  expect(detail.text()).toContain("净支出 CNY");
  expect(detail.text()).toContain("手续费 3.00");
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.includes("/accounts/account/holdings/stock/transactions?limit=20"),
    ),
  ).toBe(true);
});
it("shows negative diluted cost and unknown cost without substituting zero", async () => {
  view.items[0]!.diluted_cost = "-5.400000";
  await start();
  expect(wrapper.get(".lp-holding-row").text()).toContain("-5.400");
  view.items[0] = {
    ...view.items[0]!,
    holding_cost: null,
    diluted_cost: null,
    cost_status: "unknown",
  };
  await click("刷新行情");
  expect(wrapper.get(".lp-holding-row").text()).toContain("成本未知");
  await wrapper.get(".lp-holding-row").trigger("click");
  await flushPromises();
  expect(wrapper.get("dialog").text()).toContain("期初买入依据不完整");
});
it("does not calculate weights from known subtotals or hide known prices when FX is missing", async () => {
  view.complete = false;
  view.total_assets = null;
  view.items[0] = {
    ...view.items[0]!,
    account_market_value: null,
    weight: null,
    quote_status: "unavailable",
  };
  await start();
  expect(wrapper.text()).toContain("总资产与仓位暂不计算");
  expect(wrapper.get(".lp-holding-value").text()).toContain("200.00");
  expect(wrapper.get(".lp-holding-weight").text()).not.toContain("%");
  const save = wrapper
    .findAll("button")
    .find((b) => b.text().trim() === "更新并保存总资产")!;
  expect(save.attributes("disabled")).toBeDefined();
});
it("can reveal closed cycles and enter a registered never-held security", async () => {
  view.items[0] = {
    ...view.items[0]!,
    quantity: "0.000000",
    market_value: "0.00",
    account_market_value: "0.00",
    price: null,
    quote: null,
    holding_cost: null,
    diluted_cost: null,
    cost_status: "closed",
    quote_status: "closed",
    weight: "0.00",
  };
  await start();
  expect(wrapper.find(".lp-holding-row").exists()).toBe(false);
  await wrapper.get('.lp-holdings-tools input[type="checkbox"]').setValue(true);
  expect(wrapper.get(".lp-holding-row").text()).toContain("已清仓");
  view.items = [];
  await click("刷新行情");
  await click("新增买卖 / 分红");
  await wrapper.get(".lp-security-choice").trigger("click");
  await flushPromises();
  expect(wrapper.get("dialog").text()).toContain("当前未持有此证券");
});
it("appends a buy from detail and refreshes quantity, costs and history without saving assets", async () => {
  await start();
  await wrapper.get(".lp-holding-row").trigger("click");
  await flushPromises();
  await click("＋ 新增记录");
  const form = wrapper.get('[data-test="holding-trade-form"]');
  await form.get('[name="quantity"]').setValue("2");
  await form.get('[name="price"]').setValue("5");
  await form.get('[name="reason"]').setValue("真实成交补录");
  await form.trigger("submit");
  await flushPromises();
  expect(wrapper.find('[data-test="holding-trade-form"]').exists()).toBe(false);
  expect(wrapper.get(".lp-holding-row").text()).toContain("12 份");
  expect(wrapper.get("dialog").text()).toContain("14.625");
  expect(wrapper.get(".lp-security-trades").findAll("li")).toHaveLength(2);
  const writes = fetcher.mock.calls.filter(
    ([, options]) => options.method === "POST",
  );
  expect(writes).toHaveLength(1);
  expect(writes[0]![0]).toBe(
    "/api/platform/ledger/accounts/account/holdings/stock/transactions",
  );
});
it("paginates only this security and restarts after refreshing financial state", async () => {
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input: string, options: RequestInit) =>
    input.includes("/transactions")
      ? response(
          input.includes("cursor=older")
            ? {
                items: [
                  {
                    ...row,
                    id: "older",
                    date: "2026-05-01",
                    note: "更早买入",
                    cycle_id: "old-cycle",
                  },
                ],
              }
            : { items: [row], next_cursor: "older" },
        )
      : original(input, options),
  );
  await start();
  await wrapper.get(".lp-holding-row").trigger("click");
  await flushPromises();
  await click("下一页");
  expect(wrapper.get("dialog").text()).toContain("更早买入");
  expect(wrapper.get("dialog").text()).toContain("历史周期");
  await wrapper.setProps({ refreshKey: 1 });
  await flushPromises();
  expect(wrapper.get("dialog").text()).toContain("第 1 页");
});
it("retains replay management instead of adding manual trades to a replay account", async () => {
  const a: Account = {
    ...account,
    accounting_mode: "holdings",
    current_holdings_input: "transaction_replay",
  };
  view.source = "transaction_replay";
  view.manual_version = null;
  view.trade_date_floor = null;
  history = [{ ...row, source: "operation", operation_id: "trade" }];
  await start(a);
  await wrapper.get(".lp-holding-row").trigger("click");
  await flushPromises();
  expect(wrapper.get("dialog").text()).toContain("管理交易");
  expect(wrapper.get("dialog").text()).not.toContain("新增记录");
  await click("管理交易");
  expect(wrapper.find("dialog").exists()).toBe(false);
  expect(wrapper.text()).toContain("录入交易");
});
it("can declare a historical opening snapshot without pretending current shares are buys", async () => {
  await start();
  await click("调整现金与持仓");
  const form = wrapper.get('[data-test="current-holdings-form"]');
  await form.get('[name="baseline_date"]').setValue("2026-01-02");
  fetcher.mockImplementationOnce(
    async (_input: string, options: RequestInit) => {
      const body = JSON.parse(options.body as string);
      expect(body.baseline_date).toBe("2026-01-02");
      expect(body.expected_version).toBe("5");
      return response({
        account_id: "account",
        audit_id: "6",
        snapshot: {
          version: "6",
          saved_at: new Date().toISOString(),
          cash: "854.00",
          positions: [{ instrument_id: "stock", quantity: "10.000000" }],
        },
      });
    },
  );
  await form.trigger("submit");
  await flushPromises();
  expect(wrapper.find("dialog").exists()).toBe(false);
  expect(wrapper.emitted("changed")).toHaveLength(1);
});
it.each([
  (v: HoldingsView) => {
    v.account_id = "other";
  },
  (v: HoldingsView) => {
    v.complete = false;
  },
  (v: HoldingsView) => {
    v.items[0]!.cost_status = "unknown";
  },
  (v: HoldingsView) => {
    v.items[0]!.weight = "NaN";
  },
  (v: HoldingsView) => {
    v.items[0]!.quote!.currency = "USD";
  },
  (v: HoldingsView) => {
    v.trade_date_floor = "2099-01-01";
  },
])("rejects a malformed financial read rather than rendering it", (mutate) => {
  mutate(view);
  expect(() => validateHoldings(view, account)).toThrow("invalid_response");
});
it("validates zero, missing basis and chronological transaction response contracts", () => {
  expect(holdingNumber("900719925474.123456", 3)).toBe(
    "900,719,925,474.123456",
  );
  expect(holdingNumber("24.380000", 3)).toBe("24.380");
  expect(holdingNumber("8000.000000")).toBe("8,000");
  expect(validateHoldings(view, account)).toBe(view);
  expect(validateHoldingTransactions({ items: [row] }).items).toHaveLength(1);
  expect(() =>
    validateHoldingTransactions({
      items: [row, { ...row, id: "later", date: "2026-07-01" }],
    }),
  ).toThrow();
  expect(() =>
    validateHoldingTransactions({ items: [{ ...row, amount: "NaN" }] }),
  ).toThrow();
});

it("keeps the draft through version-conflict recovery and explicitly reloads the new basis", async () => {
  await start();
  await wrapper.get(".lp-holding-row").trigger("click");
  await flushPromises();
  await click("＋ 新增记录");
  const form = wrapper.get('[data-test="holding-trade-form"]');
  await form.get('[name="quantity"]').setValue("2");
  await form.get('[name="price"]').setValue("5");
  await form.get('[name="reason"]').setValue("保留我的草稿");
  fetcher.mockResolvedValueOnce(response({ code: "version_conflict" }, 409));
  await form.trigger("submit");
  await flushPromises();
  expect(workspace.locked.value).toBe(false);
  view.manual_version = "6";
  await click("刷新持仓依据（保留草稿）");
  const retained = wrapper.get('[data-test="holding-trade-form"]');
  expect(retained.element).toBe(form.element);
  expect(
    (retained.get('[name="reason"]').element as HTMLInputElement).value,
  ).toBe("保留我的草稿");
  expect(
    (retained.get('[name="quantity"]').element as HTMLInputElement).value,
  ).toBe("2");
  expect(
    (retained.get('[name="price"]').element as HTMLInputElement).matches(
      ":disabled",
    ),
  ).toBe(false);
});

it.each(["missing", "unavailable", "wrong-total"])(
  "keeps an invalid saved valuation receipt pending: %s",
  async (bad) => {
    await start();
    await click("更新并保存总资产");
    const result: any = {
      account_id: account.id,
      currency: "CNY",
      source: "manual_snapshot",
      as_of: todayShanghai(),
      history_id: "9",
      complete: true,
      cash: "854.00",
      positions_value: "200.00",
      known_positions_value: "200.00",
      total_assets: "1054.00",
      items: [
        {
          instrument_id: "stock",
          quantity: "10.000000",
          market_value: "200.00",
          status: "current",
          quote: view.items[0]!.quote,
          fx: null,
        },
      ],
    };
    if (bad === "missing") delete result.items;
    if (bad === "unavailable") result.items[0].status = "unavailable";
    if (bad === "wrong-total") result.total_assets = "1000.00";
    fetcher.mockResolvedValueOnce(response(result, 201));
    await click("确认更新并保存");
    expect(workspace.locked.value).toBe(true);
    expect(workspace.pending.value?.uncertain).toBe(true);
    expect(wrapper.get("dialog").text()).toContain("invalid_response");
  },
);

it("consumes replay operation navigation when returning to the holdings list", async () => {
  const a: Account = {
    ...account,
    accounting_mode: "holdings",
    current_holdings_input: "transaction_replay",
  };
  view.source = "transaction_replay";
  view.manual_version = null;
  view.trade_date_floor = null;
  history = [{ ...row, source: "operation", operation_id: "trade" }];
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input: string, options: RequestInit) =>
    input.endsWith("/operations/trade")
      ? response({
          operation: {
            id: "trade",
            kind: "buy",
            account_id: account.id,
            instrument_id: "stock",
            date: row.date,
            quantity: row.quantity,
            price: row.price,
            fee: row.fee,
            sequence: "1",
            amount: "0.00",
            voided: false,
          },
          version: "1",
          note: row.note,
        })
      : original(input, options),
  );
  await start(a);
  await wrapper.get(".lp-holding-row").trigger("click");
  await flushPromises();
  await click("查看 / 修改交易");
  expect(wrapper.find("dialog").exists()).toBe(true);
  await wrapper.get('button[aria-label="关闭弹窗"]').trigger("click");
  await flushPromises();
  await click("持仓一览");
  await click("持仓交易");
  expect(wrapper.find("dialog").exists()).toBe(false);
});

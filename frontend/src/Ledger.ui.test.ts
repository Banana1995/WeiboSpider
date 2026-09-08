// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Ledger from "./Ledger.vue";
import AccountRecords from "./AccountRecords.vue";
import { type Account, type LedgerRecord, type Mutation } from "./ledger";

let wrapper: VueWrapper;
let accountItems: Account[];
let current: LedgerRecord | undefined;
let writes: {
  method: string;
  path: string;
  body: any;
  key: string | undefined;
}[];
let failRead = false;
let uncertainWrite = false;
let fetcher: ReturnType<typeof vi.fn>;
const stock = {
  id: "stock",
  name: "合成证券",
  market: "TEST",
  code: "001",
  currency: "CNY",
};
const baseAccount: Account = {
  accounting_mode: "holdings",
  id: "account",
  name: "合成账户",
  currency: "CNY",
  opening_date: "2026-01-01",
  opening_cash: "1000.00",
  version: "1",
};
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
beforeEach(() => {
  accountItems = [{ ...baseAccount }];
  current = undefined;
  writes = [];
  failRead = false;
  uncertainWrite = false;
  fetcher = vi.fn(async (url: string, init: RequestInit = {}) => {
    const u = new URL(url, "http://localhost");
    const path = u.pathname.replace("/api/platform/ledger", "");
    const method = init.method ?? "GET";
    if (method !== "GET") {
      const body = JSON.parse(init.body as string);
      writes.push({
        method,
        path,
        body,
        key: (init.headers as Record<string, string>)["Idempotency-Key"],
      });
      if (uncertainWrite) {
        uncertainWrite = false;
        throw new TypeError("lost response");
      }
      if (path === "/accounts") {
        const a = { ...body, version: "1", accounting_mode: "holdings" };
        accountItems.push(a);
        return response(a, 201);
      }
      if (path === "/instruments") return response(body, 201);
      if (method === "DELETE")
        current = {
          ...current!,
          operation: { ...current!.operation, voided: true },
          version: "3",
        };
      else {
        const m = body as Mutation;
        current = {
          operation: { ...m.operation, voided: false },
          note: m.note,
          version: method === "PUT" ? "2" : "1",
          created_at: "2026-01-02T12:00:00Z",
          updated_at: "2026-01-02T12:00:00Z",
        };
      }
      return response(current, method === "POST" ? 201 : 200);
    }
    if (failRead) return response({ code: "storage_busy" }, 503);
    if (path === "/weekly-status")
      return response({
        enabled: false,
        timezone: "Asia/Shanghai",
        weekday: "Saturday",
        time: "08:00",
        next_scheduled_at: null,
        window_open: false,
        max_attempts: 3,
      });
    if (path.endsWith("/weekly-jobs")) return response({ items: [] });
    if (path === "/fx")
      return response({
        base: "USD",
        quote: "CNY",
        mode: "historical",
        requested_date: "2026-01-02",
        rate: "7.00000001",
        date: "2026-01-02",
        source: "Tencent/close/USDCNY",
        fetched_at: "2026-01-02T12:00:00Z",
      });
    if (path === "/accounts") return response({ items: accountItems });
    if (path.endsWith("/import-summary"))
      return response({ code: "not_found" }, 404);
    if (path.endsWith("/valuations")) return response({ items: [] });
    if (path.endsWith("/valuation"))
      return response({
        account_id: path.split("/")[2],
        ledger_revision: "a".repeat(64),
        currency: "CNY",
        cash: current ? "1995.00" : "1000.00",
        known_positions_value: "0.00",
        positions_value: null,
        total_assets: null,
        complete: false,
        as_of: "2026-01-02",
        ledger_at: "2026-01-02T12:00:00Z",
        calculated_at: "2026-01-02T12:00:01Z",
        items: [
          {
            instrument_id: "stock",
            quantity: current ? "100.000001" : "1.000001",
            quote: null,
            fx: null,
            market_value: null,
            status: "unavailable",
            error_code: "unsupported_instrument",
          },
        ],
      });
    if (path === "/instruments")
      return response({
        items: [
          stock,
          { ...stock, id: "usd-stock", name: "美元证券", currency: "USD" },
        ],
      });
    if (path.endsWith("/positions"))
      return response({
        items: [
          {
            instrument_id: "stock",
            cycle_id: "opening:account:stock",
            quantity: "1.000001",
            remaining_cost: null,
            moving_average: null,
            diluted_basis: "0.00",
            diluted_cost: "0.000000",
            realized_profit: null,
            dividends: "0.00",
          },
        ],
      });
    if (path.startsWith("/accounts/"))
      return response({
        ...(accountItems.find((a) => path.endsWith(a.id)) ?? baseAccount),
        cash: current ? "1995.00" : "1000.00",
      });
    if (path.endsWith("/revisions"))
      return response({
        items: current ? [{ record: current, reason: "合成审计原因" }] : [],
        next_cursor: u.searchParams.has("cursor") ? undefined : "1",
      });
    if (path === "/operations")
      return response({
        items: current ? [current] : [],
        next_cursor: u.searchParams.has("cursor") ? undefined : "2026-01-02:1",
      });
    return response(current);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
async function start() {
  wrapper = mount(Ledger);
  await flushPromises();
}
it("keeps canonical records unchanged after a delayed read-only preview without a valuation loop", async () => {
  const original = fetcher.getMockImplementation()!;
  let finish!: (response: Response) => void;
  fetcher.mockImplementation((url: string, init: RequestInit = {}) =>
    url.endsWith("/valuation")
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : original(url, init),
  );
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  const before = wrapper.getComponent(AccountRecords).props("refreshKey");
  finish(
    response({
      account_id: "account",
      currency: "CNY",
      ledger_revision: "a".repeat(64),
      cash: "1000.00",
      known_positions_value: "0.00",
      positions_value: "0.00",
      total_assets: "1000.00",
      complete: true,
      items: [],
      as_of: "2026-01-02",
      ledger_at: "2026-01-02T12:00:00Z",
      calculated_at: "2026-01-02T12:00:01Z",
    }),
  );
  await flushPromises();
  expect(wrapper.getComponent(AccountRecords).props("refreshKey")).toBe(before);
  expect(
    fetcher.mock.calls.filter(([url]) => url.endsWith("/valuation")),
  ).toHaveLength(1);
});
it("keeps schedule selection and refresh isolated from parent valuation reads and ledger writes", async () => {
  accountItems.push({ ...baseAccount, id: "second", name: "第二个合成账户" });
  await start();
  const accountButtons = wrapper.get(".ledger-account-list");
  const original = accountButtons
    .findAll("button")
    .find((b) => b.attributes("aria-pressed") === "true")
    ?.text();
  fetcher.mockClear();
  const panel = wrapper.get('[data-test="weekly"]');
  await panel.get('[name="weekly_account"]').setValue("second");
  await flushPromises();
  await panel
    .findAll("button")
    .find((b) => b.text() === "刷新调度状态")!
    .trigger("click");
  await flushPromises();
  await panel.get('[name="weekly_status"]').setValue("succeeded");
  await flushPromises();
  expect(
    accountButtons
      .findAll("button")
      .find((b) => b.attributes("aria-pressed") === "true")
      ?.text(),
  ).toBe(original);
  expect(fetcher.mock.calls.length).toBeGreaterThan(0);
  expect(
    fetcher.mock.calls.every(
      ([url, init]) =>
        (init.method ?? "GET") === "GET" &&
        (url.endsWith("/weekly-status") ||
          url.includes("/accounts/second/weekly-jobs?")),
    ),
  ).toBe(true);
  expect(writes).toEqual([]);
});
async function fillOperation(kind = "deposit_buy") {
  const form = wrapper.findAll('[data-test="operation-form"]')[0]!;
  await form.get('[name="kind"]').setValue(kind);
  await form.get('[name="account_id"]').setValue("account");
  await form.get('[name="date"]').setValue("2026-01-02");
  await form.get('[name="sequence"]').setValue("9");
  await form.get('[name="amount"]').setValue("2000.00");
  if (kind === "deposit_buy") {
    await form.get('[name="instrument_id"]').setValue("stock");
    await form.get('[name="quantity"]').setValue("100.000001");
    await form.get('[name="price"]').setValue("10.000001");
    await form.get('[name="fee"]').setValue("0.00");
  }
  await form.get('[name="reason"]').setValue("合成首次录入");
  return form;
}

it("creates multiple opening positions with distinct unknown and zero costs", async () => {
  await start();
  const form = wrapper.get('[data-test="account-form"]');
  await form.get('[name="account_name"]').setValue("新账户");
  await form.get('[name="opening_date"]').setValue("2026-01-01");
  await form.get('[name="opening_cash"]').setValue("9007199254740991.01");
  expect(form.find('[name="init_reason"]').exists()).toBe(false);
  await form.get('[data-test="add-position"]').trigger("click");
  await form.get('[data-test="add-position"]').trigger("click");
  for (const index of [0, 1]) {
    await form
      .get(`[name="opening_instrument_${index}"]`)
      .setValue(index ? "usd-stock" : "stock");
    await form.get(`[name="opening_quantity_${index}"]`).setValue("1.000001");
  }
  await form.get('[name="opening_cost_1"]').setValue("0.00");
  await form.get('[name="opening_basis_0"]').setValue("0.00");
  await form.trigger("submit");
  await flushPromises();
  expect(writes[0]!.body.opening_cash).toBe("9007199254740991.01");
  expect(writes[0]!.body.positions).toEqual([
    {
      instrument_id: "stock",
      quantity: "1.000001",
      cost: null,
      diluted_basis: "0.00",
    },
    {
      instrument_id: "usd-stock",
      quantity: "1.000001",
      cost: "0.00",
      diluted_basis: null,
    },
  ]);
  expect(writes[0]!.body).not.toHaveProperty("reason");
  expect(wrapper.text()).toContain("写入已确认成功");
  expect(wrapper.text()).toContain("未知 / 不适用");
  expect(wrapper.text()).toContain("不是总资产");
});

it("registers an instrument without an ephemeral reason but still requires operation audit reasons", async () => {
  await start();
  const instrumentForm = wrapper.get('[data-test="instrument-form"]');
  expect(instrumentForm.find('[name="instrument_reason"]').exists()).toBe(
    false,
  );
  await instrumentForm.get('[name="instrument_name"]').setValue("新合成证券");
  await instrumentForm.get('[name="market"]').setValue("TEST");
  await instrumentForm.get('[name="code"]').setValue("002");
  await instrumentForm.trigger("submit");
  await flushPromises();
  expect(writes[0]).toMatchObject({
    path: "/instruments",
    body: { name: "新合成证券", market: "TEST", code: "002", currency: "CNY" },
  });
  expect(writes[0]!.body).not.toHaveProperty("reason");
  const form = await fillOperation();
  await form.get('[name="reason"]').setValue(" ");
  await form.trigger("submit");
  await flushPromises();
  expect(writes).toHaveLength(1);
  expect(form.text()).toContain("请填写操作原因");
});

it("creates deposit-buy, reloads actual state, edits the full operation with CAS and voids with a reason", async () => {
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  const form = await fillOperation();
  await form.trigger("submit");
  await flushPromises();
  expect(writes[0]!.body.operation).toMatchObject({
    kind: "deposit_buy",
    amount: "2000.00",
    quantity: "100.000001",
    price: "10.000001",
    fee: "0.00",
    sequence: "9",
  });
  expect(writes[0]!.body).not.toHaveProperty("expected_version");
  expect(wrapper.text()).toContain("1995.00");
  expect(wrapper.get('[data-test="valuation-cash"]').text()).toContain(
    "1995.00",
  );
  expect(wrapper.get('[data-test="valuation"]').text()).toContain("100.000001");
  await wrapper.get('[data-test="edit"]').trigger("click");
  const edit = wrapper.findAll('[data-test="operation-form"]')[1]!;
  await edit.get('[name="amount"]').setValue("2100.01");
  await edit.get('[name="reason"]').setValue("纠正合成金额");
  await edit.trigger("submit");
  await flushPromises();
  expect(writes[1]!.method).toBe("PUT");
  expect(writes[1]!.body.expected_version).toBe("1");
  expect(writes[1]!.body.operation).toEqual({
    ...writes[0]!.body.operation,
    amount: "2100.01",
  });
  expect(writes[1]!.key).not.toBe(writes[0]!.key);
  const voidForm = wrapper.get('[data-test="void-form"]');
  await voidForm.get('[name="void_reason"]').setValue("合成作废原因");
  await voidForm.trigger("submit");
  await flushPromises();
  expect(writes[2]).toMatchObject({
    method: "DELETE",
    body: { expected_version: "2", reason: "合成作废原因" },
  });
  expect(wrapper.get('[data-test="operation-detail"]').text()).toContain(
    "当前版本 3",
  );
  expect(wrapper.find('[data-test="void-form"]').exists()).toBe(false);
});

it("locks all write payloads after uncertainty, retries exact bytes and warns before leaving", async () => {
  await start();
  const form = await fillOperation();
  await form.get('[name="instrument_id"]').setValue("usd-stock");
  await flushPromises();
  uncertainWrite = true;
  await form.trigger("submit");
  await flushPromises();
  expect(form.get("fieldset").attributes()).toHaveProperty("disabled");
  expect(
    wrapper.get('[data-test="account-form"] fieldset').attributes(),
  ).toHaveProperty("disabled");
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true);
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  await form.get('[data-test="fx-fetch"]').trigger("click");
  await form.get('[data-test="fx-manual"]').trigger("click");
  await wrapper.get('[data-test="retry"]').trigger("click");
  await flushPromises();
  expect(writes[1]!.body).toEqual(writes[0]!.body);
  expect(writes[1]!.key).toBe(writes[0]!.key);
  expect(writes[0]!.body.operation.fx.source).toBe("Tencent/close/USDCNY");
  expect(
    fetcher.mock.calls.filter(([url]) => url.includes("/fx?")),
  ).toHaveLength(1);
  expect(wrapper.find('[data-test="retry"]').exists()).toBe(false);
});

it("reads the current version after retrying a committed write with an older receipt", async () => {
  const normal = fetcher.getMockImplementation()!;
  let receipt: LedgerRecord | undefined;
  fetcher.mockImplementation(async (url: string, init: RequestInit = {}) => {
    if (url.endsWith("/operations") && init.method === "POST") {
      if (receipt) return response(receipt, 201);
      receipt = (await (await normal(url, init)).json()) as LedgerRecord;
      current = {
        ...receipt,
        version: "3",
        operation: { ...receipt.operation, voided: true },
      };
      throw new TypeError("committed but response lost");
    }
    return normal(url, init);
  });
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  expect(wrapper.get('[data-test="valuation-cash"]').text()).toContain(
    "1000.00",
  );
  const form = await fillOperation();
  await form.trigger("submit");
  await flushPromises();
  await wrapper.get('[data-test="retry"]').trigger("click");
  await flushPromises();
  const posts = fetcher.mock.calls.filter(([, init]) => init.method === "POST");
  expect(posts).toHaveLength(2);
  expect(posts[0]![1].body).toBe(posts[1]![1].body);
  expect(posts[0]![1].headers).toEqual(posts[1]![1].headers);
  expect(writes).toHaveLength(1);
  expect(wrapper.text()).toContain("写入已确认成功");
  expect(wrapper.get('[data-test="valuation-cash"]').text()).toContain(
    "1995.00",
  );
  expect(
    fetcher.mock.calls.filter(([url]) => url.endsWith("/valuation")),
  ).toHaveLength(2);
  expect(wrapper.get('[data-test="operation-detail"]').text()).toContain(
    "当前版本 3",
  );
  expect(wrapper.find('[data-test="edit"]').exists()).toBe(false);
  expect(wrapper.find('[data-test="void-form"]').exists()).toBe(false);
});

it("does not turn a successful mutation into a failed write when independent GETs fail", async () => {
  await start();
  const form = await fillOperation();
  failRead = true;
  await form.trigger("submit");
  await flushPromises();
  expect(wrapper.text()).toContain("写入已确认成功");
  expect(wrapper.text()).toContain("读取失败");
  expect(wrapper.find('[data-test="retry"]').exists()).toBe(false);
  expect(writes).toHaveLength(1);
  failRead = false;
  await wrapper.get('[data-test="refresh"]').trigger("click");
  await flushPromises();
  expect(writes).toHaveLength(1);
  expect(wrapper.text()).toContain("当前版本 1");
});

it("preserves filter dates/status across cursor pages and paginates revisions", async () => {
  await start();
  const form = await fillOperation();
  await form.trigger("submit");
  await flushPromises();
  const filters = wrapper.get('[data-test="filters"]');
  await filters.get('[name="from"]').setValue("2026-01-01");
  await filters.get('[name="to"]').setValue("2026-01-03");
  await filters.get('[name="status"]').setValue("voided");
  await filters.trigger("submit");
  await flushPromises();
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "下一页流水")!
    .trigger("click");
  await flushPromises();
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.includes(
        "from=2026-01-01&to=2026-01-03&status=voided&cursor=2026-01-02%3A1",
      ),
    ),
  ).toBe(true);
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "下一页修订")!
    .trigger("click");
  await flushPromises();
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.includes("/revisions?cursor=1&limit=30"),
    ),
  ).toBe(true);
});

it("rejects excess precision before writing and requires explicit FX for foreign trades", async () => {
  await start();
  const form = await fillOperation();
  await form.get('[name="amount"]').setValue("1.001");
  await form.trigger("submit");
  await flushPromises();
  expect(writes).toHaveLength(0);
  expect(form.text()).toContain("精度错误");
  await form.get('[name="amount"]').setValue("2000.00");
  await form.get('[name="instrument_id"]').setValue("usd-stock");
  await form.get('[data-test="fx-manual"]').trigger("click");
  await form.get('[name="fx_rate"]').setValue("7.00000001");
  await form.get('[name="fx_date"]').setValue("2026-01-02");
  await form.get('[name="fx_source"]').setValue("手工确认来源");
  await form.get('[name="fx_fetched_at"]').setValue("2026-01-02T12:00:00Z");
  await form.trigger("submit");
  await flushPromises();
  expect(writes[0]!.body.operation.fx).toEqual({
    rate: "7.00000001",
    date: "2026-01-02",
    source: "手工确认来源",
    fetched_at: "2026-01-02T12:00:00Z",
  });
});

it("attributes dividends to a loaded current cycle or an explicitly entered old cycle", async () => {
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  let form = await fillOperation("dividend");
  await form.get('[name="instrument_id"]').setValue("stock");
  await form.trigger("submit");
  await flushPromises();
  expect(writes[0]!.body.operation.cycle_id).toBe("opening:account:stock");
  expect(writes[0]!.body.operation).not.toHaveProperty("quantity");
  form = await fillOperation("dividend");
  await form.get('[name="instrument_id"]').setValue("stock");
  await form.get('[name="sequence"]').setValue("10");
  await form.get('[name="cycle_mode"]').setValue("explicit");
  await form.get('[name="cycle_id"]').setValue("old-purchase-id");
  await form.trigger("submit");
  await flushPromises();
  expect(writes[1]!.body.operation.cycle_id).toBe("old-purchase-id");
  const row = wrapper
    .get('[data-test="view-operation"]')
    .element.closest("tr")!;
  expect(row.textContent).toContain("归属周期 old-purchase-id");
  expect(row.textContent).not.toContain("股数");
  expect(row.textContent).not.toContain("价格");
});

it("renders both transfer legs with the same amount and preserves target identity", async () => {
  accountItems.push({ ...baseAccount, id: "target", name: "目标账户" });
  await start();
  const form = await fillOperation("transfer");
  await form.get('[name="to_account_id"]').setValue("target");
  await form.trigger("submit");
  await flushPromises();
  expect(writes[0]!.body.operation).toMatchObject({
    kind: "transfer",
    account_id: "account",
    to_account_id: "target",
    amount: "2000.00",
  });
  expect(wrapper.text()).toContain("合成账户 转出 2000.00 CNY");
  expect(wrapper.text()).toContain("目标账户 转入 2000.00 CNY");
});

it.each(["deposit", "withdrawal", "buy", "sell", "sell_withdraw"])(
  "serializes only relevant fields for %s",
  async (kind) => {
    await start();
    const form = await fillOperation();
    await form.get('[name="kind"]').setValue(kind);
    if (["buy", "sell", "sell_withdraw"].includes(kind))
      await form.get('[name="fee"]').setValue("");
    await form.trigger("submit");
    await flushPromises();
    const op = writes[0]!.body.operation;
    expect(op.kind).toBe(kind);
    if (["buy", "sell"].includes(kind)) expect(op).not.toHaveProperty("amount");
    if (["deposit", "withdrawal"].includes(kind)) {
      expect(op).not.toHaveProperty("instrument_id");
      expect(op).not.toHaveProperty("fee");
    } else expect(op.fee).toBeNull();
  },
);

it("ignores superseded GET results even when fetch ignores abort", async () => {
  let resolveOld!: (value: Response) => void;
  const normal = fetcher.getMockImplementation()!;
  fetcher.mockImplementation((url: string, init: RequestInit) =>
    url.includes("/operations?") && !url.includes("status=active")
      ? new Promise((resolve) => {
          resolveOld = resolve;
        })
      : normal(url, init),
  );
  await start();
  const filters = wrapper.get('[data-test="filters"]');
  await filters.get('[name="status"]').setValue("active");
  await filters.trigger("submit");
  await flushPromises();
  resolveOld(
    response({
      items: [
        { operation: { id: "stale", date: "1900-01-01", kind: "deposit" } },
      ],
    }),
  );
  await flushPromises();
  expect(wrapper.text()).not.toContain("1900-01-01");
});

it("marks retained valuations stale on failed refresh and recovers without writes", async () => {
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  failRead = true;
  await wrapper.get('[data-test="refresh"]').trigger("click");
  expect(wrapper.get('[data-test="valuation"]').text()).toContain("过期");
  await flushPromises();
  expect(wrapper.get('[data-test="valuation"]').text()).toContain(
    "不能视为当前资产",
  );
  expect(wrapper.get('[data-test="valuation-cash"]').text()).toContain(
    "1000.00",
  );
  failRead = false;
  await wrapper.get('[data-test="refresh"]').trigger("click");
  await flushPromises();
  expect(wrapper.get('[data-test="valuation"]').text()).not.toContain("过期");
  expect(writes).toHaveLength(0);
  expect(
    fetcher.mock.calls
      .filter(([url]) => url.split("?")[0]!.endsWith("/valuation"))
      .every(([url]) => !url.includes("?")),
  ).toBe(true);
});

it("saves only on explicit POST and refreshes records/history without automatic saves after reads or operations", async () => {
  const normal = fetcher.getMockImplementation()!;
  let saved = 0;
  let finish!: () => void;
  fetcher.mockImplementation(async (url: string, init: RequestInit) => {
    if (url.endsWith("/valuation") && init.method === "POST") {
      await new Promise<void>((resolve) => {
        finish = resolve;
      });
      saved++;
      return response({
        account_id: "account",
        ledger_revision: "a".repeat(64),
        history_id: String(saved),
        currency: "CNY",
        cash: "1000.00",
        known_positions_value: "0.00",
        positions_value: "0.00",
        total_assets: "1000.00",
        complete: true,
        as_of: "2026-01-02",
        ledger_at: "2026-01-02T12:00:00Z",
        calculated_at: "2026-01-02T12:00:01Z",
        items: [],
      });
    }
    if (url.includes("/valuations?"))
      return response({
        items: saved
          ? [
              {
                id: String(saved),
                account_id: "account",
                currency: "CNY",
                as_of: "2026-01-02",
                total_assets: "1000.00",
              },
            ]
          : [],
      });
    return normal(url, init);
  });
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  expect(wrapper.get('[data-test="valuation-history"]').text()).toContain(
    "暂无已保存历史记录",
  );
  const before = wrapper.getComponent(AccountRecords).props("refreshKey");
  await wrapper.get('[data-test="save-valuation"]').trigger("click");
  await flushPromises();
  finish();
  await flushPromises();
  expect(wrapper.get('[data-test="valuation-history"]').text()).toContain(
    "历史记录 #1",
  );
  expect(wrapper.text()).toContain("总资产已保存为记录 #1");
  expect(
    wrapper.getComponent(AccountRecords).props("refreshKey"),
  ).toBeGreaterThan(before);
  const form = await fillOperation();
  await form.trigger("submit");
  await flushPromises();
  expect(saved).toBe(1);
  await wrapper.get('[data-test="save-valuation"]').trigger("click");
  await flushPromises();
  finish();
  await flushPromises();
  expect(wrapper.get('[data-test="valuation-history"]').text()).toContain(
    "历史记录 #2",
  );
  expect(
    fetcher.mock.calls.filter(
      ([url, init]) => url.endsWith("/valuation") && init.method === "POST",
    ),
  ).toHaveLength(2);
  await wrapper
    .get('[data-test="valuation-history"]')
    .findAll("button")
    .find((button) => button.text() === "刷新历史列表")!
    .trigger("click");
  await flushPromises();
  expect(saved).toBe(2);
  expect(
    fetcher.mock.calls.filter(
      ([url, init]) => url.endsWith("/valuation") && init.method === "POST",
    ),
  ).toHaveLength(2);
});

it("locks navigation and retries the same explicit valuation save after a lost response", async () => {
  const normal = fetcher.getMockImplementation()!;
  const saves: { body: unknown; key: string | undefined }[] = [];
  const receipt = {
    account_id: "account",
    history_id: "7",
    complete: true,
    total_assets: "1000.00",
    items: [],
  };
  fetcher.mockImplementation(async (url: string, init: RequestInit) => {
    if (url.endsWith("/valuation") && init.method === "POST") {
      saves.push({
        body: init.body,
        key: (init.headers as Record<string, string>)["Idempotency-Key"],
      });
      if (saves.length === 1) throw new TypeError("synthetic lost response");
      return response(receipt);
    }
    return normal(url, init);
  });
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  expect(saves).toHaveLength(0);
  await wrapper.get('[data-test="save-valuation"]').trigger("click");
  await flushPromises();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  expect(
    wrapper.get(".ledger-account-list button").attributes("disabled"),
  ).toBeDefined();
  expect(
    wrapper.get('[data-test="save-valuation"]').attributes("disabled"),
  ).toBeDefined();
  await wrapper.get('[data-test="retry"]').trigger("click");
  await flushPromises();
  expect(saves).toHaveLength(2);
  expect(saves[0]).toEqual(saves[1]);
  expect(saves[0]!.body).toBe("{}");
  expect(saves[0]!.key).toBeTruthy();
  expect(wrapper.text()).toContain("总资产已保存为记录 #7");
  expect(wrapper.find('[data-test="retry"]').exists()).toBe(false);
  await wrapper.get('[data-test="refresh"]').trigger("click");
  await flushPromises();
  expect(saves).toHaveLength(2);
});

it("clears old valuation on account switch and ignores a late response", async () => {
  accountItems.push({ ...baseAccount, id: "target", name: "目标账户" });
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  let resolveOld!: (value: Response) => void;
  const normal = fetcher.getMockImplementation()!;
  let oldSignal: AbortSignal | null | undefined;
  fetcher.mockImplementation((url: string, init: RequestInit) => {
    if (url.endsWith("/accounts/account/valuation")) {
      oldSignal = init.signal;
      return new Promise((resolve) => {
        resolveOld = resolve;
      });
    }
    return normal(url, init);
  });
  await wrapper.get('[data-test="refresh"]').trigger("click");
  await wrapper.findAll(".ledger-account-list button")[1]!.trigger("click");
  expect(wrapper.find('[data-test="valuation-cash"]').exists()).toBe(false);
  await flushPromises();
  expect(oldSignal?.aborted).toBe(true);
  resolveOld(response({ account_id: "account", cash: "987654.32", items: [] }));
  await flushPromises();
  expect(wrapper.get('[data-test="valuation"]').text()).not.toContain(
    "987654.32",
  );
  expect(wrapper.get('[data-test="valuation"]').text()).not.toContain("过期");
});

it("rejects an absent or mismatched valuation rather than presenting account cash as assets", async () => {
  const normal = fetcher.getMockImplementation()!;
  fetcher.mockImplementation((url: string, init: RequestInit) =>
    url.endsWith("/valuation")
      ? Promise.resolve(response({ account_id: "wrong", items: [] }))
      : normal(url, init),
  );
  await start();
  await wrapper.get(".ledger-account-list button").trigger("click");
  await flushPromises();
  expect(wrapper.get('[data-test="valuation"]').text()).toContain("总资产未知");
  expect(wrapper.find('[data-test="valuation-cash"]').exists()).toBe(false);
  expect(wrapper.text()).not.toContain("市值、总资产与收益率暂不可用");
});

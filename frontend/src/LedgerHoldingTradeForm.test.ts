// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Form from "./LedgerHoldingTradeForm.vue";
import {
  type Account,
  type FXQuote,
  type Instrument,
  PendingWrite,
} from "./ledger";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";

const account: Account = {
  id: "account-1",
  name: "手工账户",
  currency: "CNY",
  opening_date: "2026-01-01",
  opening_cash: null,
  accounting_mode: "reported",
  current_holdings_input: "manual_snapshot",
  version: "1",
};
const instrument: Instrument = {
  id: "stock-1",
  market: "TEST",
  code: "TEST",
  name: "手工证券",
  currency: "CNY",
};
const now = "2026-09-11T17:00:00.000Z"; // Shanghai is already September 12.
let wrapper: VueWrapper;
let fetcher: ReturnType<typeof vi.fn>;
let workspace: ReturnType<typeof createLedgerWorkspace>;
let changed: ReturnType<typeof vi.fn>;
const response = (data: unknown, status = 201) =>
  new Response(JSON.stringify(data), { status });
function receipt(
  overrides: Record<string, unknown> = {},
  transaction: Record<string, unknown> = {},
) {
  return {
    account_id: account.id,
    instrument_id: instrument.id,
    version: "8",
    ...overrides,
    transaction: {
      id: "mt-1",
      kind: "buy",
      date: "2026-09-12",
      quantity: "2.000000",
      price: "10.100000",
      fee: null,
      amount: "20.20",
      note: "成交单",
      cycle_id: "opening:account-1:stock-1",
      source: "manual",
      ...transaction,
    },
  };
}
function quote(overrides: Partial<FXQuote> = {}): FXQuote {
  return {
    base: "USD",
    quote: "CNY",
    mode: "historical",
    requested_date: "2026-09-10",
    rate: "7.12345678",
    date: "2026-09-09",
    source: "Tencent/close/USDCNY",
    fetched_at: now,
    ...overrides,
  };
}
function start(props: Partial<InstanceType<typeof Form>["$props"]> = {}) {
  changed = vi.fn();
  workspace = createLedgerWorkspace(changed);
  wrapper = mount(Form, {
    props: {
      account: { ...account },
      instrument: { ...instrument },
      expectedVersion: "7",
      minDate: "2026-09-01",
      disabled: false,
      ...props,
    },
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace } },
  });
}
async function fill(fields: Record<string, string> = {}) {
  for (const [name, value] of Object.entries({
    quantity: "2",
    price: "10.1",
    note: "成交单",
    reason: "补录成交",
    ...fields,
  }))
    await wrapper.get(`[name="${name}"]`).setValue(value);
}
const value = (name: string) =>
  (wrapper.get(`[name="${name}"]`).element as HTMLInputElement).value;
async function submit() {
  await wrapper.get("form").trigger("submit");
  await flushPromises();
}
function body(index = 0) {
  return JSON.parse(fetcher.mock.calls[index]![1].body as string);
}
function deferred() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date(now));
  fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("manual holding trade payloads", () => {
  it.each(["buy", "sell"] as const)(
    "sends %s as strings without inferred prices, costs, FX, amount or sequence",
    async (kind) => {
      start();
      expect(wrapper.find("dialog").exists()).toBe(false);
      expect(wrapper.find('[role="dialog"]').exists()).toBe(false);
      expect(value("price")).toBe("");
      expect(value("date")).toBe("2026-09-12");
      expect(fetcher).not.toHaveBeenCalled();
      expect(wrapper.emitted("dirty")).toEqual([[false]]);
      await wrapper.get(`[name="kind"][value="${kind}"]`).trigger("click");
      await fill({ fee: "0.10" });
      fetcher.mockResolvedValue(
        response(
          receipt(
            {},
            { kind, fee: "0.1", amount: kind === "sell" ? "20.10" : "20.30" },
          ),
        ),
      );
      await submit();
      expect(fetcher.mock.calls[0]![0]).toBe(
        `/api/platform/ledger/accounts/${account.id}/holdings/${instrument.id}/transactions`,
      );
      expect(fetcher.mock.calls[0]![1].method).toBe("POST");
      expect(body()).toEqual({
        expected_version: "7",
        kind,
        date: "2026-09-12",
        quantity: "2",
        price: "10.1",
        fee: "0.10",
        note: "成交单",
        reason: "补录成交",
      });
      expect(wrapper.emitted("saved")).toHaveLength(1);
      expect(wrapper.emitted("dirty")).toEqual([[false], [true], [false]]);
      expect(changed).toHaveBeenCalledTimes(1);
      await submit();
      expect(fetcher).toHaveBeenCalledTimes(1); // Parent must refresh before another trade.
    },
  );

  it("sends only the dividend amount even after filling trade fields", async () => {
    start();
    await fill({ fee: "3" });
    await wrapper.get('[name="kind"][value="dividend"]').trigger("click");
    expect(wrapper.find('[name="quantity"]').exists()).toBe(false);
    expect(wrapper.find('[name="fee"]').exists()).toBe(false);
    await wrapper.get('[name="amount"]').setValue("1.20");
    fetcher.mockResolvedValue(
      response(
        receipt(
          {},
          {
            kind: "dividend",
            quantity: null,
            price: null,
            fee: null,
            amount: "1.2",
          },
        ),
      ),
    );
    await submit();
    expect(body()).toEqual({
      expected_version: "7",
      kind: "dividend",
      date: "2026-09-12",
      amount: "1.20",
      note: "成交单",
      reason: "补录成交",
    });
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it("keeps omitted fees distinct from explicit zero and permits zero net sell proceeds", async () => {
    start();
    await fill({ fee: "0" });
    await wrapper.get('[name="kind"][value="sell"]').trigger("click");
    fetcher.mockResolvedValue(
      response(receipt({}, { kind: "sell", fee: "0.00", amount: "0.00" })),
    );
    await submit();
    expect(body().fee).toBe("0");
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it("allows minDate and appends without a client sequence", async () => {
    start();
    await fill({ date: "2026-09-01" });
    fetcher.mockResolvedValue(response(receipt({}, { date: "2026-09-01" })));
    await submit();
    expect(body().date).toBe("2026-09-01");
    expect(body()).not.toHaveProperty("sequence");
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });
});

describe("validation and exact decimals", () => {
  it.each([
    ["quantity", "0"],
    ["quantity", "-1"],
    ["quantity", "1.0000001"],
    ["quantity", "1e3"],
    ["price", "0"],
    ["price", "1.0000001"],
    ["price", "9223372036854.775808"],
    ["fee", "-0.01"],
    ["fee", "0.001"],
    ["reason", "  "],
    ["reason", "中".repeat(171)],
    ["note", "中".repeat(1366)],
    ["date", "2026-08-31"],
    ["date", "2026-09-13"],
    ["date", ""],
  ])("rejects invalid %s without writing (%s)", async (field, invalid) => {
    start();
    await fill({ [field]: invalid });
    await submit();
    expect(fetcher).not.toHaveBeenCalled();
    expect(wrapper.find(".lp-error").exists()).toBe(true);
    expect(wrapper.emitted("saved")).toBeUndefined();
  });

  it.each(["0", "-1", "0.001", "NaN", "1e2"])(
    "rejects dividend %s",
    async (amount) => {
      start();
      await fill();
      await wrapper.get('[name="kind"][value="dividend"]').trigger("click");
      await wrapper.get('[name="amount"]').setValue(amount);
      await submit();
      expect(fetcher).not.toHaveBeenCalled();
    },
  );

  it("uses the required UI text limits", () => {
    start();
    expect(wrapper.get('[name="note"]').attributes("maxlength")).toBe("1000");
    expect(wrapper.get('[name="reason"]').attributes("maxlength")).toBe("120");
    expect(wrapper.get('[name="date"]').attributes("min")).toBe("2026-09-01");
    expect(wrapper.get('[name="date"]').attributes("max")).toBe("2026-09-12");
  });

  it.each([
    { account: { ...account, accounting_mode: "holdings" as const } },
    {
      account: {
        ...account,
        current_holdings_input: "transaction_replay" as const,
      },
    },
    { expectedVersion: "01" },
    { expectedVersion: "9223372036854775807" },
    { minDate: "2026-02-30" },
  ])("rejects an ineligible or invalid basis %j", async (props) => {
    start(props);
    await fill();
    await submit();
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("does not round large monetary strings or versions through floating point", async () => {
    start({ expectedVersion: "9007199254740992" });
    await fill({
      quantity: "9007199254.740991",
      price: "0.000001",
      fee: "90071992547409.91",
    });
    fetcher.mockResolvedValue(
      response(
        receipt(
          { version: "9007199254740993" },
          {
            quantity: "9007199254.740991",
            price: "0.000001",
            fee: "90071992547409.91",
            amount: "90071992547409.91",
          },
        ),
      ),
    );
    await submit();
    expect(body().quantity).toBe("9007199254.740991");
    expect(body().fee).toBe("90071992547409.91");
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it("detects one-cent dividend mismatch above the floating point safe range", async () => {
    start();
    await fill();
    await wrapper.get('[name="kind"][value="dividend"]').trigger("click");
    await wrapper.get('[name="amount"]').setValue("9007199254740991.01");
    fetcher.mockResolvedValue(
      response(
        receipt(
          {},
          {
            kind: "dividend",
            quantity: null,
            price: null,
            amount: "9007199254740991.02",
          },
        ),
      ),
    );
    await submit();
    expect(body().amount).toBe("9007199254740991.01");
    expect(workspace.pending.value?.uncertain).toBe(true);
    expect(wrapper.emitted("saved")).toBeUndefined();
  });
});

describe("explicit FX snapshots", () => {
  it.each(["2026-09-12", "2026-09-10"])(
    "queries the correct mode for %s and submits only the four snapshot fields",
    async (date) => {
      start({ instrument: { ...instrument, currency: "USD" } });
      await fill({ date });
      expect(fetcher).not.toHaveBeenCalled();
      await submit();
      expect(fetcher).not.toHaveBeenCalled();
      const latest = date === "2026-09-12";
      const fx = quote({
        mode: latest ? "latest" : "historical",
        requested_date: date,
        source: latest ? "Tencent/spot/USDCNY" : "Tencent/close/USDCNY",
      });
      fetcher.mockResolvedValueOnce(response(fx, 200));
      await wrapper.get('[data-test="fx-fetch"]').trigger("click");
      await flushPromises();
      const url = new URL(
        fetcher.mock.calls[0]![0] as string,
        "http://localhost",
      );
      expect(Object.fromEntries(url.searchParams)).toEqual({
        base: "USD",
        quote: "CNY",
        mode: latest ? "latest" : "historical",
        ...(latest ? {} : { date }),
      });
      expect(value("fx_rate")).toBe(fx.rate);
      expect(wrapper.text()).toContain(fx.source);
      expect(wrapper.text()).toContain(fx.date);
      fetcher.mockResolvedValueOnce(response(receipt({}, { date })));
      await submit();
      expect(body(1).fx).toEqual({
        rate: fx.rate,
        date: fx.date,
        source: fx.source,
        fetched_at: fx.fetched_at,
      });
      expect(wrapper.emitted("saved")).toHaveLength(1);
    },
  );

  it("supports inverse reference sources without inverting in the browser", async () => {
    start({ account: { ...account, currency: "USD" } });
    await fill({ date: "2026-09-10" });
    fetcher.mockResolvedValueOnce(
      response(
        quote({
          base: "CNY",
          quote: "USD",
          source: "Tencent/close/USDCNY/inverse",
          rate: "0.14000000",
        }),
        200,
      ),
    );
    await wrapper.get('[data-test="fx-fetch"]').trigger("click");
    await flushPromises();
    fetcher.mockResolvedValueOnce(
      response(receipt({}, { date: "2026-09-10" })),
    );
    await submit();
    expect(body(1).fx.rate).toBe("0.14000000");
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it.each([
    { base: "HKD" },
    { quote: "USD" },
    { mode: "latest" },
    { requested_date: "2026-09-09" },
    { date: "2026-09-11" },
    { date: "2026-02-30" },
    { source: "" },
    { source: "manual" },
    { source: "Tencent/spot/USDCNY" },
    { source: "Tencent/close/HKDCNY" },
    { rate: "0" },
    { rate: "1.123456789" },
    { rate: 7 },
    { fetched_at: "invalid" },
    { fetched_at: "2026-09-12T17:00:00Z" },
  ])("rejects mismatched FX metadata %j", async (invalid) => {
    start({ instrument: { ...instrument, currency: "USD" } });
    await fill({ date: "2026-09-10" });
    fetcher.mockResolvedValue(response({ ...quote(), ...invalid }, 200));
    await wrapper.get('[data-test="fx-fetch"]').trigger("click");
    await flushPromises();
    expect(value("fx_rate")).toBe("");
    await submit();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(wrapper.text()).toContain("不匹配");
  });

  it("manual FX uses the actual submit timestamp and forbids later FX dates", async () => {
    start({ instrument: { ...instrument, currency: "USD" } });
    await fill({ date: "2026-09-10" });
    await wrapper.get('[data-test="fx-manual"]').trigger("click");
    await wrapper.get('[name="fx_rate"]').setValue("7.1");
    await wrapper.get('[name="fx_date"]').setValue("2026-09-11");
    await submit();
    expect(fetcher).not.toHaveBeenCalled();
    await wrapper.get('[name="fx_date"]').setValue("2026-09-09");
    vi.setSystemTime(new Date("2026-09-11T17:05:00Z"));
    fetcher.mockResolvedValue(response(receipt({}, { date: "2026-09-10" })));
    await submit();
    expect(body().fx).toEqual({
      rate: "7.1",
      date: "2026-09-09",
      source: "manual",
      fetched_at: "2026-09-11T17:05:00.000Z",
    });
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it("date changes abort and clear old FX; late responses cannot overwrite manual input", async () => {
    start({ instrument: { ...instrument, currency: "USD" } });
    await fill({ date: "2026-09-10" });
    const old = deferred();
    fetcher.mockReturnValueOnce(old.promise);
    await wrapper.get('[data-test="fx-fetch"]').trigger("click");
    const signal = fetcher.mock.calls[0]![1].signal as AbortSignal;
    await submit();
    expect(fetcher).toHaveBeenCalledTimes(1);
    await wrapper.get('[name="date"]').setValue("2026-09-11");
    expect(signal.aborted).toBe(true);
    expect(value("fx_rate")).toBe("");
    expect(fetcher).toHaveBeenCalledTimes(1);
    await wrapper.get('[data-test="fx-manual"]').trigger("click");
    await wrapper.get('[name="fx_rate"]').setValue("7.2");
    await wrapper.get('[name="fx_date"]').setValue("2026-09-10");
    old.resolve(response(quote(), 200));
    await flushPromises();
    expect(value("fx_rate")).toBe("7.2");
    expect(wrapper.text()).toContain("来源：manual");
    await wrapper.get('[name="date"]').setValue("2026-09-12");
    expect(value("fx_rate")).toBe("");
    expect(value("fx_date")).toBe("");
    await submit();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("switching directly to manual mode cancels a pending query", async () => {
    start({ instrument: { ...instrument, currency: "USD" } });
    await fill({ date: "2026-09-10" });
    const old = deferred();
    fetcher.mockReturnValueOnce(old.promise);
    await wrapper.get('[data-test="fx-fetch"]').trigger("click");
    await wrapper.get('[data-test="fx-manual"]').trigger("click");
    expect(fetcher.mock.calls[0]![1].signal.aborted).toBe(true);
    await wrapper.get('[name="fx_rate"]').setValue("7.3");
    old.resolve(response(quote(), 200));
    await flushPromises();
    expect(value("fx_rate")).toBe("7.3");
  });

  it("currency changes discard a quote, and same-currency writes omit FX", async () => {
    start({ instrument: { ...instrument, currency: "USD" } });
    await fill({ date: "2026-09-10" });
    await wrapper.get('[data-test="fx-manual"]').trigger("click");
    await wrapper.get('[name="fx_rate"]').setValue("7.1");
    await wrapper.get('[name="fx_date"]').setValue("2026-09-10");
    await wrapper.setProps({ instrument: { ...instrument } });
    expect(wrapper.find('[name="fx_rate"]').exists()).toBe(false);
    fetcher.mockResolvedValue(response(receipt({}, { date: "2026-09-10" })));
    await submit();
    expect(body()).not.toHaveProperty("fx");
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it("unmount cancels an outstanding FX request", async () => {
    start({ instrument: { ...instrument, currency: "USD" } });
    const old = deferred();
    fetcher.mockReturnValueOnce(old.promise);
    await wrapper.get('[data-test="fx-fetch"]').trigger("click");
    wrapper.unmount();
    expect(fetcher.mock.calls[0]![1].signal.aborted).toBe(true);
    old.resolve(response(quote(), 200));
    await flushPromises();
  });
});

describe("workspace ownership and receipt verification", () => {
  it("keeps unknown writes locked and retries the identical body and idempotency key", async () => {
    start();
    await fill();
    fetcher.mockRejectedValueOnce(new TypeError("connection lost"));
    await submit();
    const write = workspace.pending.value;
    expect(write?.uncertain).toBe(true);
    expect(workspace.locked.value).toBe(true);
    expect(wrapper.get("fieldset").attributes()).toHaveProperty("disabled");
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty(
      "disabled",
    );
    await submit();
    await wrapper.get('button[type="button"]').trigger("click");
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(wrapper.emitted("saved")).toBeUndefined();
    expect(wrapper.emitted("dirty")?.at(-1)).toEqual([true]);
    fetcher.mockResolvedValueOnce(response(receipt()));
    await workspace.retry();
    await flushPromises();
    expect(fetcher.mock.calls[1]![1].body).toBe(fetcher.mock.calls[0]![1].body);
    expect(fetcher.mock.calls[1]![1].headers["Idempotency-Key"]).toBe(
      write!.key,
    );
    expect(workspace.pending.value).toBeUndefined();
    expect(wrapper.emitted("saved")).toHaveLength(1);
    expect(wrapper.emitted("dirty")?.at(-1)).toEqual([false]);
  });

  it("blocks duplicate submits while running and obeys external/prop locks", async () => {
    start({ disabled: true });
    await submit();
    expect(fetcher).not.toHaveBeenCalled();
    await wrapper.setProps({ disabled: false });
    await fill();
    workspace.externalLock.value = true;
    await submit();
    expect(fetcher).not.toHaveBeenCalled();
    workspace.externalLock.value = false;
    const wait = deferred();
    fetcher.mockReturnValueOnce(wait.promise);
    await submit();
    await submit();
    await workspace.retry();
    expect(fetcher).toHaveBeenCalledTimes(1);
    wait.resolve(response(receipt()));
    await flushPromises();
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it("does not take ownership of another component's pending request", async () => {
    start();
    await fill();
    fetcher.mockRejectedValueOnce(new TypeError("network"));
    const other = new PendingWrite("/other", "POST", {});
    workspace.send(other, "其他操作", () => true);
    await flushPromises();
    await submit();
    expect(workspace.pending.value).toBe(other);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(wrapper.emitted("saved")).toBeUndefined();
  });

  it("version conflict stays blocked until refreshed props supply a new version", async () => {
    start();
    await fill();
    fetcher.mockResolvedValueOnce(response({ code: "version_conflict" }, 409));
    await submit();
    expect(workspace.pending.value).toBeUndefined();
    expect(wrapper.text()).toContain("刷新持仓依据（保留草稿）");
    workspace.error.value = "";
    await wrapper.setProps({
      account: { ...account },
      expectedVersion: "7",
      disabled: false,
    });
    await submit();
    expect(fetcher).toHaveBeenCalledTimes(1);
    await wrapper.setProps({ expectedVersion: "8" });
    fetcher.mockResolvedValueOnce(response(receipt({ version: "9" })));
    await submit();
    expect(body(1).expected_version).toBe("8");
    expect(wrapper.emitted("saved")).toHaveLength(1);
  });

  it.each([
    "unsafe_trade_date",
    "manual_holdings_required",
    "insufficient_cash",
  ])("surfaces definite %s without claiming success", async (code) => {
    start();
    await fill();
    fetcher.mockResolvedValueOnce(response({ code }, 400));
    await submit();
    expect(wrapper.text()).toContain(`[${code}]`);
    expect(workspace.pending.value).toBeUndefined();
    expect(wrapper.emitted("saved")).toBeUndefined();
  });

  it.each([
    { account_id: "other" },
    { instrument_id: "other" },
    { version: "7" },
    { version: "9" },
    { version: 8 },
  ])(
    "rejects receipt identity/version mismatch %j and retains the write",
    async (invalid) => {
      start();
      await fill();
      fetcher.mockResolvedValueOnce(response(receipt(invalid)));
      await submit();
      expect(workspace.pending.value?.uncertain).toBe(true);
      expect(wrapper.emitted("saved")).toBeUndefined();
      fetcher.mockResolvedValueOnce(response(receipt()));
      await workspace.retry();
      expect(wrapper.emitted("saved")).toHaveLength(1);
    },
  );

  it.each([
    { kind: "sell" },
    { date: "2026-09-11" },
    { quantity: "2.000001" },
    { price: "10.100001" },
    { quantity: 2 },
    { fee: "0.00" },
    { note: "other" },
    { amount: "-0.01" },
    { amount: "0.001" },
    { amount: 20.2 },
    { id: "bad/id" },
    { cycle_id: "" },
    { cycle_id: "\u0000" },
    { cycle_id: "x".repeat(513) },
    { source: "import" },
  ])("rejects malformed or mismatched transaction %j", async (invalid) => {
    start();
    await fill();
    fetcher.mockResolvedValueOnce(response(receipt({}, invalid)));
    await submit();
    expect(workspace.pending.value?.uncertain).toBe(true);
    expect(wrapper.emitted("saved")).toBeUndefined();
    expect(changed).not.toHaveBeenCalled();
  });

  it("cancel emits without discarding the dirty state owned by the enclosing dialog", async () => {
    start();
    await fill();
    const cancel = wrapper
      .findAll("button")
      .find((button) => button.text() === "取消")!;
    await cancel.trigger("click");
    expect(wrapper.emitted("cancel")).toHaveLength(1);
    expect(wrapper.emitted("dirty")?.at(-1)).toEqual([true]);
    expect(fetcher).not.toHaveBeenCalled();
  });
});

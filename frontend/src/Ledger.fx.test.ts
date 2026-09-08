// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Form from "./LedgerOperationForm.vue";
import { type FXQuote, type LedgerRecord, type Mutation } from "./ledger";

let wrapper: VueWrapper;
let fetcher: ReturnType<typeof vi.fn>;
const snapshot = {
  rate: "7.10000000",
  date: "2026-09-04",
  source: "Tencent/close/USDCNY",
  fetched_at: "2026-09-05T17:00:00Z",
};
const quote = (overrides: Partial<FXQuote> = {}): FXQuote => ({
  ...snapshot,
  base: "USD",
  quote: "CNY",
  mode: "historical",
  requested_date: "2026-09-05",
  ...overrides,
});
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  // UTC is still September 5; Beijing is September 6.
  vi.setSystemTime(new Date("2026-09-05T17:00:00Z"));
  fetcher = vi.fn(async () => response(quote()));
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
function start(record?: LedgerRecord) {
  wrapper = mount(Form, {
    props: {
      accounts: ["CNY", "USD", "HKD"].map((currency) => ({
        accounting_mode: "holdings" as const,
        current_holdings_input: "transaction_replay" as const,
        id: currency,
        name: currency,
        currency: currency as "CNY",
        opening_date: "2020-01-01",
        opening_cash: "1000",
        version: "1",
      })),
      instruments: ["USD", "CNY", "HKD"].map((currency) => ({
        id: currency,
        name: currency,
        currency: currency as "USD",
        market: "TEST",
        code: currency,
      })),
      positions: [],
      positionsAccount: "",
      positionsFresh: false,
      locked: false,
      record,
    },
  });
}
async function fill(date = "2026-09-05", account = "CNY", instrument = "USD") {
  await wrapper.get('[name="kind"]').setValue("buy");
  await wrapper.get('[name="account_id"]').setValue(account);
  await wrapper.get('[name="instrument_id"]').setValue(instrument);
  await wrapper.get('[name="date"]').setValue(date);
  await wrapper.get('[name="sequence"]').setValue("1");
  await wrapper.get('[name="quantity"]').setValue("1");
  await wrapper.get('[name="price"]').setValue("10");
  await wrapper.get('[name="reason"]').setValue("test");
  await flushPromises();
}
const value = (name: string) =>
  (wrapper.get(`[name="${name}"]`).element as HTMLInputElement).value;
const saved = () =>
  (wrapper.emitted("save")?.at(-1)?.[0] as Mutation)?.operation.fx;

it.each(["2026-09-06", "2026-09-05"])(
  "fetches the exact today/historical contract for %s and writes only four fields",
  async (date) => {
    const latest = date === "2026-09-06";
    fetcher.mockResolvedValue(
      response(
        quote({
          mode: latest ? "latest" : "historical",
          requested_date: date,
          ...(latest ? { quoted_at: "2026-09-04T16:00:00+08:00" } : {}),
        }),
      ),
    );
    start();
    await fill(date);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]![0]).toBe(
      `/api/platform/ledger/fx?base=USD&quote=CNY&mode=${latest ? "latest" : "historical&date=2026-09-05"}`,
    );
    expect(value("fx_date")).toBe("2026-09-04");
    expect(wrapper.text()).toContain(latest ? "最新报价" : "历史日收盘");
    await wrapper.get("form").trigger("submit");
    expect(saved()).toEqual(snapshot);
  },
);

it("requests reverse direction rather than inverting client-side", async () => {
  fetcher.mockResolvedValue(
    response(
      quote({
        base: "CNY",
        quote: "USD",
        rate: "0.14084507",
        source: "Tencent/close/USDCNY/inverse",
      }),
    ),
  );
  start();
  await fill("2026-09-05", "USD", "CNY");
  expect(fetcher.mock.calls[0]![0]).toContain("base=CNY&quote=USD");
  await wrapper.get("form").trigger("submit");
  expect(saved()?.rate).toBe("0.14084507");
});

it("retries provider failure and clears a fetched snapshot on security currency changes", async () => {
  fetcher.mockResolvedValueOnce(response({ code: "fx_unavailable" }, 502));
  start();
  await fill();
  expect(wrapper.text()).toContain("重试获取");
  await wrapper.get('[data-test="fx-fetch"]').trigger("click");
  await flushPromises();
  expect(value("fx_rate")).toBe(snapshot.rate);
  await wrapper.get('[name="instrument_id"]').setValue("HKD");
  expect(value("fx_rate")).toBe("");
  expect(fetcher.mock.calls.at(-1)![0]).toContain("base=HKD&quote=CNY");
  await flushPromises();
  expect(wrapper.text()).toContain("不匹配");
  await wrapper.get("form").trigger("submit");
  expect(saved()).toBeUndefined();
});

it.each(["fx_unavailable", "fx_timeout", "unsupported_currency"])(
  "shows %s and permits explicit validated manual entry",
  async (code) => {
    fetcher.mockResolvedValue(
      response({ code }, code === "unsupported_currency" ? 400 : 504),
    );
    start();
    await fill();
    expect(wrapper.text()).toContain(code);
    await wrapper.get("form").trigger("submit");
    expect(saved()).toBeUndefined();
    await wrapper.get('[data-test="fx-manual"]').trigger("click");
    expect(value("fx_source")).toBe("");
    expect(value("fx_fetched_at")).toBe("2026-09-05T17:00:00.000Z");
    await wrapper.get('[name="fx_rate"]').setValue("7.2");
    await wrapper.get('[name="fx_date"]').setValue("2026-09-04");
    await wrapper.get('[name="fx_source"]').setValue("manual:broker");
    for (const [field, bad, good] of [
      ["fx_rate", "0", "7.2"],
      ["fx_date", "2026-09-06", "2026-09-04"],
      ["fx_source", " ", "manual:broker"],
      ["fx_fetched_at", "2026-02-30T00:00:00Z", "2026-09-05T17:00:00Z"],
    ]) {
      await wrapper.get(`[name="${field}"]`).setValue(bad);
      await wrapper.get("form").trigger("submit");
      expect(saved()).toBeUndefined();
      await wrapper.get(`[name="${field}"]`).setValue(good);
    }
    await wrapper.get("form").trigger("submit");
    expect(saved()?.source).toBe("manual:broker");
    expect(fetcher).toHaveBeenCalledTimes(1);
  },
);

it("aborts superseded pair/date requests and ignores responses arriving out of order", async () => {
  const pending: ((r: Response) => void)[] = [];
  fetcher.mockImplementation(
    () => new Promise((resolve) => pending.push(resolve)),
  );
  start();
  await fill();
  await wrapper.get("form").trigger("submit");
  expect(saved()).toBeUndefined();
  await wrapper.get('[name="account_id"]').setValue("HKD");
  await wrapper.get('[name="date"]').setValue("2026-09-03");
  expect(fetcher.mock.calls[0]![1].signal.aborted).toBe(true);
  expect(fetcher.mock.calls[1]![1].signal.aborted).toBe(true);
  pending[2]!(
    response(
      quote({
        quote: "HKD",
        requested_date: "2026-09-03",
        date: "2026-09-02",
        rate: "7.8",
      }),
    ),
  );
  await flushPromises();
  pending[0]!(response(quote()));
  pending[1]!(response(quote({ quote: "HKD" })));
  await flushPromises();
  expect(value("fx_rate")).toBe("7.8");
  expect(value("fx_date")).toBe("2026-09-02");
});

it("manual mode aborts pending lookup and context changes invalidate manual snapshots", async () => {
  let finish!: (r: Response) => void;
  fetcher.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  start();
  await fill();
  await wrapper.get('[data-test="fx-manual"]').trigger("click");
  expect(fetcher.mock.calls[0]![1].signal.aborted).toBe(true);
  await wrapper.get('[name="fx_rate"]').setValue("8");
  finish(response(quote()));
  await flushPromises();
  expect(value("fx_rate")).toBe("8");
  expect(value("fx_source")).toBe("");
  await wrapper.get('[name="date"]').setValue("2026-09-03");
  expect(value("fx_rate")).toBe("");
  expect(fetcher).toHaveBeenCalledTimes(2);
  wrapper.unmount();
  expect(fetcher.mock.calls[1]![1].signal.aborted).toBe(true);
  finish(response(quote()));
  await flushPromises();
});

it("does not fetch invalid/future dates or same-currency operations", async () => {
  start();
  await fill("2026-09-07");
  await wrapper.get('[name="date"]').setValue("");
  expect(fetcher).not.toHaveBeenCalled();
  await wrapper.get('[name="instrument_id"]').setValue("CNY");
  await wrapper.get('[name="date"]').setValue("2026-09-05");
  expect(fetcher).not.toHaveBeenCalled();
  await wrapper.get("form").trigger("submit");
  expect(wrapper.emitted("save")).toHaveLength(1);
  expect(saved()).toBeUndefined();
});

it("preserves original edit snapshots until explicit replacement or changed context", async () => {
  start({
    operation: {
      id: "old",
      account_id: "CNY",
      instrument_id: "USD",
      date: "2026-09-05",
      sequence: "1",
      kind: "buy",
      quantity: "1",
      price: "10",
      fx: snapshot,
      voided: false,
    },
    note: "original",
    version: "2",
    created_at: snapshot.fetched_at,
    updated_at: snapshot.fetched_at,
  });
  await wrapper.get('[name="note"]').setValue("new note");
  await wrapper.get('[name="fee"]').setValue("1");
  await wrapper.get('[name="reason"]').setValue("correction");
  await wrapper.get("form").trigger("submit");
  expect(saved()).toEqual(snapshot);
  expect(fetcher).not.toHaveBeenCalled();
  fetcher.mockImplementation(async () => response(quote({ rate: "7.3" })));
  await wrapper.get('[data-test="fx-fetch"]').trigger("click");
  await flushPromises();
  expect(value("fx_rate")).toBe("7.3");
  await wrapper.setProps({ locked: true });
  await wrapper.get('[data-test="fx-fetch"]').trigger("click");
  await wrapper.get('[data-test="fx-manual"]').trigger("click");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(value("fx_rate")).toBe("7.3");
  await wrapper.setProps({ locked: false });
  await wrapper.get('[name="date"]').setValue("2026-09-04");
  expect(value("fx_rate")).toBe("");
  await flushPromises();
  expect(wrapper.text()).toContain("不匹配");
});

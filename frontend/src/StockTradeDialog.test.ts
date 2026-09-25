// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import StockTradeDialog from "./StockTradeDialog.vue";
import LedgerSecurityDialog from "./LedgerSecurityDialog.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import { emptyStockBook, stockItem } from "./stockBook.testHelpers";
import { tradePreview, validateStockBook } from "./stockBook";
import type { Account } from "./ledger";

const account: Account = {
  id: "a",
  name: "Synthetic",
  currency: "CNY",
  opening_date: "2020-01-01",
  opening_cash: null,
  version: "1",
  current_holdings_input: "manual_snapshot",
};
let wrapper: VueWrapper;
let workspace: ReturnType<typeof createLedgerWorkspace>;
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  workspace = createLedgerWorkspace(vi.fn());
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
function start(mode: "buy" | "cash" = "buy", existing = false) {
  const item = stockItem();
  wrapper = mount(StockTradeDialog, {
    props: {
      account,
      book: {
        ...emptyStockBook(),
        version: "1",
        cash_date: existing ? "2026-01-01" : "",
        items: existing ? [item] : [],
      },
      mode,
      ...(existing ? { item } : {}),
    },
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace } },
  });
}
it("records the first purchase with date, shares, price and fee without inventing a cash debit", async () => {
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
    const p = JSON.parse(init.body as string);
    return new Response(
      JSON.stringify({
        account_id: "a",
        version: "2",
        id: p.id,
        action: p.action,
      }),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  start();
  wrapper.getComponent(LedgerSecurityDialog).vm.$emit("selected", {
    id: "i",
    market: "SH",
    code: "600036",
    name: "Synthetic",
    currency: "CNY",
  });
  await flushPromises();
  await wrapper.get('[name="date"]').setValue("2026-01-01");
  await wrapper.get('[name="quantity"]').setValue("1000");
  await wrapper.get('[name="price"]').setValue("10.123456");
  await wrapper.get('[name="fee"]').setValue("1.01");
  expect(wrapper.text()).toContain("10,124.47");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  const payload = JSON.parse(fetcher.mock.calls[0]![1].body as string);
  expect(payload).toMatchObject({
    action: "create",
    expected_version: "1",
    security: { id: "i", code: "600036" },
    entry: {
      date: "2026-01-01",
      quantity: "1000",
      price: "10.123456",
      fee: "1.01",
      fx: "1",
      amount: "0",
    },
  });
  expect(payload.cash).toBeUndefined();
  expect(wrapper.emitted("saved")).toHaveLength(1);
});
it("reuses an existing security and preserves a foreign settlement rate", async () => {
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
    const p = JSON.parse(init.body as string);
    return new Response(
      JSON.stringify({
        account_id: "a",
        version: "2",
        id: p.id,
        action: p.action,
      }),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  start("buy", true);
  await wrapper.get('[name="date"]').setValue("2026-01-01");
  await wrapper.get('[name="quantity"]').setValue("100");
  await wrapper.get('[name="price"]').setValue("400");
  await wrapper.get('[name="fx"]').setValue("0.9");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  const payload = JSON.parse(fetcher.mock.calls[0]![1].body as string);
  expect(payload.entry.fx).toBe("0.9");
  expect(payload.security).toBeUndefined();
});
it("freezes one command and key after a lost receipt and unlocks after retry", async () => {
  let calls = 0;
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
    if (++calls === 1) throw new TypeError("lost receipt");
    const p = JSON.parse(init.body as string);
    return new Response(
      JSON.stringify({
        account_id: "a",
        version: "2",
        id: p.id,
        action: p.action,
      }),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  start("cash");
  await wrapper.get('[name="cash"]').setValue("5000");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(workspace.locked.value).toBe(true);
  expect(wrapper.emitted("saved")).toBeUndefined();
  await workspace.retry();
  await flushPromises();
  expect(workspace.locked.value).toBe(false);
  expect(wrapper.emitted("saved")).toHaveLength(1);
  expect(fetcher.mock.calls[0]![1].body).toBe(fetcher.mock.calls[1]![1].body);
  expect(
    new Headers(fetcher.mock.calls[0]![1].headers).get("Idempotency-Key"),
  ).toBe(new Headers(fetcher.mock.calls[1]![1].headers).get("Idempotency-Key"));
});
it("rejects a mismatched successful receipt instead of losing the pending command", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            account_id: "a",
            version: "99",
            id: "",
            action: "cash",
          }),
        ),
    ),
  );
  start("cash");
  await wrapper.get('[name="cash"]').setValue("100");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(workspace.locked.value).toBe(true);
  expect(wrapper.emitted("saved")).toBeUndefined();
});
it("validates response account isolation and retains exact decimal amounts", () => {
  const b = { ...emptyStockBook(), items: [stockItem()] };
  expect(validateStockBook(b, account)).toBe(b);
  expect(() =>
    validateStockBook({ ...b, account_id: "other" }, account),
  ).toThrow();
  expect(tradePreview("0.000001", "5000", "0", "buy")).toBe("0.01");
  expect(tradePreview("1", "1", "2", "sell")).toBeNull();
  expect(tradePreview("9007199254.740993", "1", "0", "buy")).toBe(
    "9007199254.74",
  );
});

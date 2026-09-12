// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Panel from "./LedgerHoldings.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { Account } from "./ledger";

let wrapper: VueWrapper;
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
const account: Account = {
  id: "a",
  name: "Synthetic",
  currency: "CNY",
  opening_date: "2020-01-01",
  opening_cash: null,
  version: "1",
  current_holdings_input: "manual_snapshot",
};
it("shows a single cash display and reference total with one refresh and no quote save", async () => {
  const fetcher = vi.fn(
    async (url: string) =>
      new Response(
        JSON.stringify(
          url.endsWith("/current-holdings")
            ? {
                account_id: "a",
                audit_id: "1",
                snapshot: {
                  version: "1",
                  cash: "123.00",
                  positions: [],
                  saved_at: "2026-09-12T00:00:00Z",
                },
              }
            : {
                account_id: "a",
                currency: "CNY",
                source: "manual_snapshot",
                as_of: "2026-09-12",
                ledger_at: "2026-09-12T00:00:00Z",
                revision: "a".repeat(64),
                manual_version: "1",
                trade_date_floor: "2026-09-12",
                configured: true,
                cash: "123.00",
                complete: true,
                total_assets: "123.00",
                items: [],
              },
        ),
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  wrapper = mount(Panel, {
    props: {
      account,
      accounts: [account],
      instruments: [],
      instrumentsError: "",
      instrumentsLoading: false,
      refreshKey: 0,
    },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  await flushPromises();
  expect(wrapper.text()).toContain("当前参考估值");
  expect(wrapper.text()).toContain("123.00");
  expect(wrapper.text()).toContain("不是账本总资产");
  expect(wrapper.text().match(/当前现金/g)).toHaveLength(1);
  for (const text of ["登记证券", "更新并保存", "新增买卖", "持仓交易"])
    expect(wrapper.text()).not.toContain(text);
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "刷新行情")!
    .trigger("click");
  await flushPromises();
  expect(fetcher).toHaveBeenCalledTimes(4);
  expect(
    fetcher.mock.calls.every(
      (args) => !((args as unknown[])[1] as RequestInit)?.method,
    ),
  ).toBe(true);
});
it("offers account creation rather than security registration without an account", () => {
  wrapper = mount(Panel, {
    props: {
      accounts: [],
      instruments: [],
      instrumentsError: "",
      instrumentsLoading: false,
      refreshKey: 0,
    },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  expect(wrapper.text()).toContain("新建账户");
  expect(wrapper.text()).not.toContain("登记证券");
});

it("uses one securities table with explicit original and converted currency, and clears stale quotes on failed refresh", async () => {
  const instrument = {
    id: "hk",
    name: "Synthetic HK",
    market: "HK",
    code: "00700",
    currency: "HKD" as const,
  };
  let failed = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.endsWith("/current-holdings"))
        return new Response(
          JSON.stringify({
            account_id: "a",
            audit_id: "1",
            snapshot: {
              version: "1",
              cash: "10.00",
              saved_at: "2026-09-12T00:00:00Z",
              positions: [{ instrument_id: "hk", quantity: "10.500000" }],
            },
          }),
        );
      if (failed)
        return new Response(JSON.stringify({ code: "storage_busy" }), {
          status: 503,
        });
      return new Response(
        JSON.stringify({
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
          total_assets: "199.00",
          items: [
            {
              instrument,
              quantity: "10.500000",
              market_value: "210.00",
              account_market_value: "189.00",
              price: "20.000000",
              weight: null,
              quote_status: "prior_date",
              quote: {
                price: "20.000000",
                currency: "HKD",
                date: "2026-09-11",
                source: "synthetic",
                quoted_at: "2026-09-11T00:00:00Z",
                fetched_at: "2026-09-12T00:00:00Z",
              },
              fx: {
                base: "HKD",
                quote: "CNY",
                rate: "0.90000000",
                date: "2026-09-11",
                source: "synthetic",
              },
            },
          ],
        }),
      );
    }),
  );
  wrapper = mount(Panel, {
    props: {
      account,
      accounts: [account],
      instruments: [instrument],
      instrumentsError: "",
      instrumentsLoading: false,
      refreshKey: 0,
    },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  await flushPromises();
  expect(wrapper.findAll("table")).toHaveLength(1);
  expect(wrapper.findAll("tbody tr")).toHaveLength(1);
  expect(wrapper.text().match(/Synthetic HK/g)).toHaveLength(1);
  expect(wrapper.text().match(/当前现金/g)).toHaveLength(1);
  expect(wrapper.text()).toContain("10.5");
  expect(wrapper.text()).not.toContain("10.500000");
  expect(wrapper.text()).toContain("原币 210.00 HKD");
  expect(wrapper.text()).toContain("折合 189.00 CNY");
  expect(wrapper.text()).toContain("199.00");
  failed = true;
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "刷新行情")!
    .trigger("click");
  await flushPromises();
  expect(wrapper.text()).not.toContain("199.00");
  expect(wrapper.text()).not.toContain("210.00");
  expect(wrapper.text()).toContain("原币 — HKD");
  expect(wrapper.text()).toContain("Synthetic HK");
});

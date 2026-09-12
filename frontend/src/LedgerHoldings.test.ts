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
it("shows current reference valuation separately and never offers quote saves or registration", async () => {
  const fetcher = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
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
        }),
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
      stubs: { CurrentHoldings: true },
    },
  });
  await flushPromises();
  expect(wrapper.text()).toContain("当前参考估值");
  expect(wrapper.text()).toContain("123.00");
  expect(wrapper.text()).toContain("不是账本总资产");
  for (const text of ["登记证券", "更新并保存", "新增买卖", "持仓交易"])
    expect(wrapper.text()).not.toContain(text);
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "刷新行情")!
    .trigger("click");
  await flushPromises();
  expect(fetcher).toHaveBeenCalledTimes(2);
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

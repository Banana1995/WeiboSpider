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
let workspace: ReturnType<typeof createLedgerWorkspace>;
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
  document.body.innerHTML = "";
});
function start() {
  workspace = createLedgerWorkspace(() => {});
  wrapper = mount(Panel, {
    attachTo: document.body,
    props,
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: workspace,
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
  const detail = wrapper.get("dialog.stock-detail-dialog");
  expect(wrapper.findAll(".stock-row")).toHaveLength(1);
  expect(detail.attributes("open")).toBeDefined();
  expect(detail.text()).toContain("摊薄成本");
  expect(detail.text()).toContain("380");
  expect(detail.text()).toContain("个股累计盈亏");
  expect(detail.text()).toContain("86,720.00");
  failed = true;
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "刷新")!
    .trigger("click");
  await flushPromises();
  expect(detail.get('[role="alert"]').text()).toContain("可能已过期");
  expect(wrapper.text()).toContain("合成腾讯");
  expect(
    detail
      .findAll("button")
      .find((b) => b.text() === "＋ 加仓")!
      .attributes("disabled"),
  ).toBeDefined();
});

it("keeps the detail dialog beneath trades, isolates pending feedback and restores focus after saving", async () => {
  let book = { ...emptyStockBook(), version: "1", items: [stockItem()] };
  let rejectFirst!: (reason: unknown) => void;
  const commands: RequestInit[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, options: RequestInit = {}) => {
      if (options.method === "POST") {
        commands.push(options);
        if (commands.length === 1)
          return new Promise<Response>((_resolve, reject) => {
            rejectFirst = reject;
          });
        const payload = JSON.parse(String(options.body));
        book = {
          ...book,
          version: "2",
          items: [
            {
              ...stockItem(),
              metrics: { ...stockItem().metrics, dividends: "8.25" },
            },
          ],
        };
        return new Response(
          JSON.stringify({
            account_id: "a",
            id: payload.id,
            version: "2",
            action: payload.action,
          }),
        );
      }
      return new Response(JSON.stringify(book));
    }),
  );
  start();
  await flushPromises();
  const row = wrapper.get<HTMLButtonElement>(".stock-row");
  row.element.focus();
  await row.trigger("click");
  const detail = wrapper.get("dialog.stock-detail-dialog");
  expect(workspace.dialogs.value).toBe(1);
  const action = (text: string) =>
    detail.findAll("button").find((button) => button.text() === text)!;

  const buy = action("＋ 加仓");
  (buy.element as HTMLButtonElement).focus();
  await buy.trigger("click");
  let trade = wrapper.get("dialog:not(.stock-detail-dialog)");
  expect(workspace.dialogs.value).toBe(2);
  expect(trade.attributes("aria-labelledby")).not.toBe(
    detail.attributes("aria-labelledby"),
  );
  expect(trade.text()).toContain("买入 / 加仓");
  await trade.get(".lp-close").trigger("click");
  await flushPromises();
  expect(workspace.dialogs.value).toBe(1);
  expect(document.activeElement).toBe(buy.element);

  const dividend = action("记录分红");
  (dividend.element as HTMLButtonElement).focus();
  await dividend.trigger("click");
  trade = wrapper.get("dialog:not(.stock-detail-dialog)");
  await trade.get('[name="amount"]').setValue("8.25");
  await trade.get("form").trigger("submit");
  await flushPromises();
  expect(workspace.busy.value).toBe(true);
  expect(trade.get('button[type="submit"]').text()).toBe("保存中…");
  expect(trade.find(".lp-dialog-body.lp-reference").exists()).toBe(false);
  expect(detail.find(".lp-dialog-body.lp-reference").exists()).toBe(false);

  rejectFirst(new TypeError("synthetic lost response"));
  await flushPromises();
  expect(workspace.busy.value).toBe(false);
  expect(workspace.locked.value).toBe(true);
  expect(trade.find(".lp-dialog-body.lp-reference").exists()).toBe(true);
  expect(detail.find(".lp-dialog-body.lp-reference").exists()).toBe(false);
  expect(detail.find('[role="alert"]').exists()).toBe(false);
  await trade
    .findAll("button")
    .find((button) => button.text() === "按原请求重试确认")!
    .trigger("click");
  await flushPromises();
  expect(commands).toHaveLength(2);
  expect(commands[0]!.body).toBe(commands[1]!.body);
  expect(commands[0]!.headers).toEqual(commands[1]!.headers);
  expect(workspace.locked.value).toBe(false);
  expect(workspace.dialogs.value).toBe(1);
  expect(wrapper.find("dialog:not(.stock-detail-dialog)").exists()).toBe(false);
  expect(detail.text()).toContain("累计分红 8.25");
  expect(document.activeElement).toBe(detail.get(".lp-close").element);
  await detail.get(".lp-close").trigger("click");
  await flushPromises();
  expect(wrapper.find("dialog").exists()).toBe(false);
  expect(workspace.dialogs.value).toBe(0);
  expect(document.activeElement).toBe(row.element);
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

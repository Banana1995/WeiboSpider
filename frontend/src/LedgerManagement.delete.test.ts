// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Management from "./LedgerManagement.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { Account } from "./ledger";

const account: Account = {
  id: "a",
  name: "Synthetic account",
  currency: "CNY",
  opening_date: "2026-01-01",
  opening_cash: null,
  current_holdings_input: "manual_snapshot",
  version: "1",
};
let wrapper: VueWrapper;
let workspace: ReturnType<typeof createLedgerWorkspace>;
let fetcher: ReturnType<typeof vi.fn>;
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
beforeEach(() => {
  vi.useFakeTimers();
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  workspace = createLedgerWorkspace(() => {});
  fetcher = vi
    .fn()
    .mockResolvedValue(response({ account_id: "a", deleted: true }));
  vi.stubGlobal("fetch", fetcher);
  wrapper = mount(Management, {
    props: {
      account,
      accounts: [account],
      instruments: [],
      instrumentsError: "",
      instrumentsLoading: false,
      initialTab: "info",
      refreshKey: 0,
    },
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace } },
  });
});
afterEach(() => {
  wrapper.unmount();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const button = (text: string) =>
  wrapper.findAll("button").find((b) => b.text() === text)!;
async function click(text: string) {
  await button(text).trigger("click");
  await flushPromises();
}
it("waits the full five seconds and requires an explicit second confirmation", async () => {
  await click("删除账户");
  expect(wrapper.get("dialog").text()).toContain(account.name);
  expect(workspace.navigationLocked.value).toBe(true);
  expect(button("确认删除（5 秒）").attributes("disabled")).toBeDefined();
  await click("确认删除（5 秒）");
  await vi.advanceTimersByTimeAsync(4999);
  expect(button("确认删除（1 秒）").attributes("disabled")).toBeDefined();
  expect(fetcher).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(1);
  expect(button("确认删除").attributes("disabled")).toBeUndefined();
  expect(fetcher).not.toHaveBeenCalled();
  await click("确认删除");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]).toMatchObject([
    "/api/platform/ledger/accounts/a",
    { method: "DELETE", body: "{}" },
  ]);
  expect(wrapper.emitted("deleted")).toHaveLength(1);
  expect(wrapper.find("dialog").exists()).toBe(false);
});
it("cancels safely and starts a fresh countdown on every opening", async () => {
  await click("删除账户");
  await vi.advanceTimersByTimeAsync(4000);
  await click("取消");
  expect(vi.getTimerCount()).toBe(0);
  expect(workspace.navigationLocked.value).toBe(false);
  await click("删除账户");
  expect(button("确认删除（5 秒）").attributes("disabled")).toBeDefined();
  await wrapper.get("dialog").trigger("cancel");
  expect(wrapper.find("dialog").exists()).toBe(false);
  expect(fetcher).not.toHaveBeenCalled();
});
it("locks an uncertain deletion and retries with the original identity", async () => {
  fetcher.mockRejectedValueOnce(new TypeError("lost response"));
  await click("删除账户");
  await vi.advanceTimersByTimeAsync(5000);
  await click("确认删除");
  expect(workspace.locked.value).toBe(true);
  await click("取消");
  expect(wrapper.find("dialog").exists()).toBe(true);
  await click("按原请求重试确认");
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(fetcher.mock.calls[0]![1].headers).toEqual(
    fetcher.mock.calls[1]![1].headers,
  );
  expect(wrapper.emitted("deleted")).toHaveLength(1);
});
it("keeps the dialog and shows a definitive failure without claiming deletion", async () => {
  fetcher.mockResolvedValueOnce(response({ code: "not_found" }, 404));
  await click("删除账户");
  await vi.advanceTimersByTimeAsync(5000);
  await click("确认删除");
  expect(wrapper.find('[role="alert"]').exists()).toBe(true);
  expect(wrapper.emitted("deleted")).toBeUndefined();
  expect(workspace.locked.value).toBe(false);
  await click("取消");
  expect(wrapper.find("dialog").exists()).toBe(false);
});
it("discards the target on account changes and cleans up its timer", async () => {
  await click("删除账户");
  await wrapper.setProps({ account: { ...account, id: "b" } });
  expect(wrapper.find("dialog").exists()).toBe(false);
  expect(vi.getTimerCount()).toBe(0);
  await click("删除账户");
  wrapper.unmount();
  expect(vi.getTimerCount()).toBe(0);
  expect(fetcher).not.toHaveBeenCalled();
});

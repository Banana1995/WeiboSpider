// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import LedgerWeekly from "./LedgerWeekly.vue";
import type { Account, ValuationHistory } from "./ledger";
import {
  weeklyStates,
  type WeeklyCarryHistory,
  type WeeklyJob,
  type WeeklyStatus,
} from "./ledgerWeekly";

const accounts: Account[] = [
  {
    id: "a",
    name: "合成持仓账户",
    currency: "CNY",
    accounting_mode: "holdings",
    current_holdings_input: "transaction_replay",
    opening_date: "2020-01-01",
    opening_cash: "0.00",
    version: "1",
  },
  {
    id: "b",
    name: "合成手工账户 <b>原文</b>",
    currency: "CNY",
    accounting_mode: "reported",
    current_holdings_input: "manual_snapshot",
    opening_date: "2020-01-01",
    opening_cash: null,
    version: "1",
  },
];
const off: WeeklyStatus = {
  enabled: false,
  timezone: "Asia/Shanghai",
  weekday: "Saturday",
  time: "08:00",
  next_scheduled_at: null,
  window_open: false,
  max_attempts: 3,
};
const success: WeeklyJob = {
  id: "9007199254740993",
  account_id: "a",
  scheduled_business_date: "2026-09-05",
  source: "holdings_current",
  status: "succeeded",
  attempts: 1,
  created_at: "2026-09-05T00:00:00.000000000Z",
  started_at: "2026-09-05T00:00:01.000000000Z",
  finished_at: "2026-09-05T00:00:04.000000000Z",
  next_attempt_at: null,
  error_code: "",
  history_id: "9007199254740994",
};
const frozen: ValuationHistory = {
  id: success.history_id!,
  schema_version: 1,
  account_name: "冻结名称 <img src=x onerror=alert(1)>",
  saved_at: "2026-09-05T00:00:03Z",
  instruments: [],
  valuation: {
    source: "transaction_replay",
    account_id: "a",
    currency: "CNY",
    as_of: "2026-09-05",
    ledger_revision: "a".repeat(64),
    ledger_at: "2026-09-05T00:00:01Z",
    calculated_at: "2026-09-05T00:00:02Z",
    cash: "0.00",
    positions_value: "0.00",
    known_positions_value: "0.00",
    total_assets: "0.00",
    complete: true,
    items: [],
  },
};
const carriedJob: WeeklyJob = {
  ...success,
  id: "3",
  account_id: "b",
  source: "account_record_carry",
  history_id: "4",
};
const carried: WeeklyCarryHistory = {
  id: "4",
  saved_at: "2026-09-05T00:00:03Z",
  schema_version: 1,
  account_name: "合成手工账户 <b>原文</b>",
  record_id: "weekly-carry-3",
  account_id: "b",
  currency: "CNY",
  as_of: "2026-09-05",
  total_assets: "900.00",
  source_record: {
    id: "manual-old",
    account_id: "b",
    sequence: "1",
    version: "1",
    date: "2026-09-01",
    origin: "manual",
    total_assets: "900.00",
  },
};
const skipped: WeeklyJob = {
  ...success,
  id: "2",
  account_id: "b",
  source: "account_record_carry",
  status: "skipped",
  attempts: 1,
  error_code: "no_source",
  history_id: null,
};
const pending: WeeklyJob = {
  ...success,
  status: "pending",
  attempts: 0,
  started_at: null,
  finished_at: null,
  history_id: null,
};
const running: WeeklyJob = {
  ...success,
  status: "running",
  finished_at: null,
  history_id: null,
};
const failed: WeeklyJob = {
  ...success,
  status: "failed",
  error_code: "timeout",
  history_id: null,
  next_attempt_at: "2026-09-05T00:05:04Z",
};
let wrapper: VueWrapper;
let status: WeeklyStatus;
let rows: WeeklyJob[];
let detail: WeeklyJob;
let history: ValuationHistory;
let error: string;
let fetcher: ReturnType<typeof vi.fn>;
const response = (data: unknown, code = 200) =>
  new Response(JSON.stringify(data), { status: code });
beforeEach(() => {
  status = { ...off };
  rows = [success];
  detail = { ...success };
  history = structuredClone(frozen);
  error = "";
  fetcher = vi.fn(async (url: string) => {
    if (error && url.includes(error))
      return response({ code: "storage_busy" }, 503);
    if (url.endsWith("/weekly-status")) return response(status);
    if (url.includes("/valuations/")) return response(history);
    if (/\/weekly-jobs\/\d+$/.test(url)) return response(detail);
    if (url.includes("/accounts/b/")) return response({ items: [skipped] });
    return response({ items: rows });
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
async function setPanelOpen(open = true) {
  const panel = wrapper.get('[data-test="weekly"]');
  (panel.element as HTMLDetailsElement).open = open;
  await panel.trigger("toggle");
  await flushPromises();
}
async function start(items = accounts, open = true) {
  wrapper = mount(LedgerWeekly, { props: { accounts: items } });
  await flushPromises();
  if (open) await setPanelOpen();
}
async function click(text: string) {
  await wrapper
    .findAll("button")
    .find((b) => b.text() === text)!
    .trigger("click");
  await flushPromises();
}
it("loads status with no accounts, exposes no enable/retry action and never polls", async () => {
  vi.useFakeTimers();
  await start([], false);
  const panel = wrapper.get('[data-test="weekly"]');
  expect(panel.get("summary").text()).toBe("周六自动更新记录（只读）");
  expect((panel.element as HTMLDetailsElement).open).toBe(false);
  expect(fetcher).not.toHaveBeenCalled();
  await setPanelOpen();
  expect(wrapper.text()).toContain("查询时：全局已关闭");
  expect(wrapper.text()).toContain("暂无可选账户");
  expect(fetcher).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(180_000);
  expect(fetcher).toHaveBeenCalledTimes(1);
  await click("刷新调度状态");
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(
    fetcher.mock.calls.every(([url]) => url.endsWith("/weekly-status")),
  ).toBe(true);
  expect(wrapper.findAll("button").map((b) => b.text())).toEqual([
    "刷新调度状态",
    "任务首页",
    "任务下一页",
  ]);
});
it("cancels lazy status and list reads when the panel closes, then reloads cleanly", async () => {
  const pending: {
    url: string;
    signal?: AbortSignal | null;
    resolve: (value: Response) => void;
  }[] = [];
  fetcher.mockImplementation(
    (url: string, init: RequestInit) =>
      new Promise<Response>((resolve) =>
        pending.push({ url, signal: init.signal, resolve }),
      ),
  );
  await start(accounts, false);
  expect(fetcher).not.toHaveBeenCalled();
  await setPanelOpen();
  expect(pending).toHaveLength(2);
  await setPanelOpen(false);
  expect(pending.every((request) => request.signal?.aborted)).toBe(true);
  for (const request of pending)
    request.resolve(
      request.url.endsWith("/weekly-status")
        ? response({ ...off, enabled: true })
        : response({ items: [success] }),
    );
  await flushPromises();
  expect(wrapper.text()).not.toContain("查询时：全局已启用");
  fetcher.mockImplementation(async (url: string) =>
    url.endsWith("/weekly-status")
      ? response(off)
      : response({ items: [success] }),
  );
  await setPanelOpen();
  expect(wrapper.text()).toContain("查询时：全局已关闭");
  expect(wrapper.get("tbody").text()).toContain(success.id);
});
it.each([false, true])(
  "shows enabled snapshot, open window=%s and next fixed time without heartbeat claims",
  async (open) => {
    status = {
      ...off,
      enabled: true,
      window_open: open,
      next_scheduled_at: "2026-09-12T08:00:00+08:00",
    };
    await start();
    expect(wrapper.text()).toContain("查询时：全局已启用");
    expect(wrapper.text()).toContain(open ? "窗口开放" : "窗口未开放");
    expect(wrapper.text()).toContain("2026/09/12 08:00:00 北京时间");
    expect(wrapper.text()).toContain("不是心跳证明");
  },
);
it.each([
  pending,
  running,
  success,
  failed,
  { ...failed, attempts: 3, next_attempt_at: null },
  {
    ...pending,
    status: "skipped",
    finished_at: success.finished_at,
    error_code: "expired",
  } as WeeklyJob,
])(
  "shows state $status attempts $attempts with fixed reason and latest-attempt times",
  async (row) => {
    rows = [row];
    detail = row;
    await start();
    await click(`任务 #${row.id}`);
    const text = wrapper.get('[data-test="weekly-detail"]').text();
    expect(text).toContain(weeklyStates[row.status]);
    expect(text).toContain(`${row.attempts} / 3`);
    expect(text).toContain("最近开始时间");
    if (row.status === "failed" && row.attempts === 3)
      expect(text).toContain("已达 3 次上限");
    if (row.next_attempt_at) expect(text).toContain("全局已关闭，不会执行重试");
    if (row.status === "running")
      expect(wrapper.text()).toContain("不代表现在正在执行");
  },
);
it("reads linked zero-asset frozen history only, literal escaped names and old-record explanation", async () => {
  await start();
  await click(`任务 #${success.id}`);
  await click(`查看已保存总资产 #${success.history_id}`);
  const record = wrapper.get('[data-test="weekly-history"]');
  expect(record.get('[data-test="valuation-total"]').text()).toBe(
    "历史快照总资产 0.00 CNY",
  );
  expect(record.text()).toContain(frozen.account_name);
  expect(record.text()).toContain(
    "账本快照时间（ledger_at）2026/09/05 08:00:01 北京时间",
  );
  expect(record.text()).not.toContain("2026-09-05T00:00:01Z");
  expect(record.find("img").exists()).toBe(false);
  expect(wrapper.text()).toContain("后续交易或更正可能使旧快照过期");
  expect(wrapper.text()).toContain("不是所有历史估值都来自周六任务");
  expect(wrapper.text()).toContain("共用一条收益曲线，不设切换日期");
  expect(
    fetcher.mock.calls.every(
      ([url, init]) =>
        (init?.method ?? "GET") === "GET" && !/\/valuation(?:\?|$)/.test(url),
    ),
  ).toBe(true);
});
it("an account without any prior total stays missing rather than becoming fake zero", async () => {
  await start();
  await click(`任务 #${success.id}`);
  await click(`查看已保存总资产 #${success.history_id}`);
  await wrapper.get('[name="weekly_account"]').setValue("b");
  await flushPromises();
  expect(wrapper.find('[data-test="weekly-history"]').exists()).toBe(false);
  expect(wrapper.text()).toContain("周六会复制截至当日最近一笔有效总资产");
  expect(wrapper.text()).toContain(
    "没有可沿用的历史总资产，本周未更新（不是失败或零资产）",
  );
  expect(wrapper.text()).not.toContain("0.00");
  expect(wrapper.get('[data-test="weekly-account-context"]').text()).toContain(
    accounts[1]!.name,
  );
  expect(
    wrapper.get('[data-test="weekly-account-context"]').find("b").exists(),
  ).toBe(false);
  expect(wrapper.emitted("refresh")).toBeUndefined();
  expect(wrapper.emitted("update:accountId")).toBeUndefined();
});
it("shows carried total and original record without requesting valuation history", async () => {
  fetcher.mockImplementation(async (url: string) => {
    if (url.endsWith("/weekly-status")) return response(off);
    if (/\/weekly-jobs\/3$/.test(url))
      return response({ ...carriedJob, carry: carried });
    if (url.includes("/accounts/b/")) return response({ items: [carriedJob] });
    return response({ items: [success] });
  });
  await start();
  await wrapper.get('[name="weekly_account"]').setValue("b");
  await flushPromises();
  await click("任务 #3");
  const evidence = wrapper.get('[data-test="weekly-carry"]');
  expect(evidence.text()).toContain("900.00 CNY");
  expect(evidence.text()).toContain("2026-09-01");
  expect(evidence.text()).toContain("manual-old");
  expect(evidence.text()).toContain("手工记录");
  expect(wrapper.text()).toContain("本次没有重新采集行情");
  expect(
    wrapper
      .findAll("button")
      .some((button) => button.text().startsWith("查看已保存总资产")),
  ).toBe(false);
  expect(fetcher.mock.calls.some(([url]) => url.includes("/valuations/"))).toBe(
    false,
  );
});
it("paginates 30 with exact cursor, resets filters/pages on account switch and clears detail on filter", async () => {
  fetcher.mockImplementation(async (url: string) => {
    const u = new URL(url, "http://localhost");
    if (url.endsWith("/weekly-status")) return response(off);
    if (u.searchParams.has("status") || url.includes("/accounts/b/"))
      return response({ items: [] });
    return response(
      u.searchParams.has("cursor")
        ? { items: [{ ...success, id: "1" }] }
        : { items: [success], next_cursor: success.id },
    );
  });
  await start();
  expect(
    fetcher.mock.calls.some(([url]) => url.endsWith("weekly-jobs?limit=30")),
  ).toBe(true);
  await click("任务下一页");
  expect(fetcher.mock.lastCall![0]).toContain(`cursor=${success.id}&limit=30`);
  await wrapper.get('[name="weekly_status"]').setValue("failed");
  await flushPromises();
  expect(fetcher.mock.lastCall![0]).toContain("?status=failed&limit=30");
  expect(fetcher.mock.lastCall![0]).not.toContain("cursor=");
  expect(wrapper.text()).toContain("暂无任务记录");
  await wrapper.get('[name="weekly_account"]').setValue("b");
  await flushPromises();
  expect(fetcher.mock.lastCall![0]).toContain(
    "/accounts/b/weekly-jobs?limit=30",
  );
  expect(
    (wrapper.get('[name="weekly_status"]').element as HTMLSelectElement).value,
  ).toBe("");
});
it("uses fresh transitioned detail rather than treating list status as immutable", async () => {
  rows = [running];
  await start();
  await click(`任务 #${success.id}`);
  expect(wrapper.get("tbody").text()).toContain("执行中记录");
  expect(wrapper.get('[data-test="weekly-detail"]').text()).toContain("已保存");
  await click(`查看已保存总资产 #${success.history_id}`);
  expect(wrapper.find('[data-test="weekly-history"]').exists()).toBe(true);
});
it("rejects a different valid history ID for an already succeeded task", async () => {
  await start();
  detail = { ...success, history_id: "2" };
  await click(`任务 #${success.id}`);
  expect(wrapper.get('[data-test="weekly-detail"]').text()).toContain(
    "已保存记录与任务身份不匹配",
  );
  expect(fetcher.mock.calls.some(([url]) => url.includes("/valuations/"))).toBe(
    false,
  );
});
it.each(["id", "account_id", "source", "scheduled_business_date"])(
  "rejects changed detail identity %s and never follows history ID",
  async (field) => {
    await start();
    detail = { ...success, [field]: field === "source" ? "none" : "wrong" };
    await click(`任务 #${success.id}`);
    expect(wrapper.get('[data-test="weekly-detail"]').text()).toContain(
      "读取失败",
    );
    expect(
      wrapper
        .findAll("button")
        .some((b) => b.text().startsWith("查看已保存总资产")),
    ).toBe(false);
    expect(
      fetcher.mock.calls.some(([url]) => url.includes("/valuations/")),
    ).toBe(false);
  },
);
it("clears old status/list/detail/history on failed or malformed refresh and allows recovery", async () => {
  await start();
  await click(`任务 #${success.id}`);
  await click(`查看已保存总资产 #${success.history_id}`);
  history.valuation.account_id = "b";
  await click(`查看已保存总资产 #${success.history_id}`);
  expect(wrapper.find('[data-test="weekly-history"]').exists()).toBe(false);
  expect(wrapper.text()).toContain("响应无效");
  detail = { ...success, history_id: "../valuation" };
  await click("重新读取任务详情");
  expect(wrapper.text()).not.toContain("任务当时已保存");
  error = "weekly";
  await click("刷新调度状态");
  expect(wrapper.find("tbody").exists()).toBe(false);
  expect(wrapper.find('[data-test="weekly-detail"]').exists()).toBe(false);
  expect(wrapper.text()).not.toContain("查询时：全局已关闭");
  error = "";
  rows = [{ ...success, account_id: "b" }];
  await click("刷新调度状态");
  expect(wrapper.find("tbody").exists()).toBe(false);
  rows = [success];
  await click("刷新调度状态");
  expect(wrapper.get("tbody").text()).toContain(success.id);
});
it.each([false, true])(
  "aborts account list/detail/history and ignores late responses/errors (%s)",
  async (reject) => {
    await start();
    const old: {
      resolve: (v: Response) => void;
      reject: (e: Error) => void;
      signal?: AbortSignal | null;
    }[] = [];
    const defer = () =>
      fetcher.mockImplementationOnce(
        (_url: string, init: RequestInit) =>
          new Promise<Response>((resolve, reject) =>
            old.push({ resolve, reject, signal: init.signal }),
          ),
      );
    // A new detail cancels the old history read even when the transport ignores abort.
    await click(`任务 #${success.id}`);
    defer();
    await click(`查看已保存总资产 #${success.history_id}`);
    defer();
    await click("重新读取任务详情");
    fetcher.mockImplementation((url: string, init: RequestInit) => {
      if (url.includes("/accounts/a/"))
        return new Promise<Response>((resolve, reject) =>
          old.push({ resolve, reject, signal: init.signal }),
        );
      return Promise.resolve(
        response(url.endsWith("weekly-status") ? off : { items: [skipped] }),
      );
    });
    await click("刷新调度状态");
    await wrapper.get('[name="weekly_account"]').setValue("b");
    await flushPromises();
    expect(old).toHaveLength(3);
    for (const p of old) {
      expect(p.signal?.aborted).toBe(true);
      if (reject) p.reject(new Error("old private error"));
      else p.resolve(response(frozen));
    }
    await flushPromises();
    expect(wrapper.text()).not.toContain("读取失败");
    expect(wrapper.text()).not.toContain(frozen.account_name);
    expect(wrapper.get("tbody").text()).toContain("未更新");
  },
);
it("shows loading, ignores late global refresh, clears removed accounts, and respects parent write lock", async () => {
  let resolve!: (v: Response) => void;
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((r) => {
        resolve = r;
      }),
  );
  await start();
  expect(wrapper.text()).toContain("正在读取全局调度状态");
  await click("刷新调度状态");
  resolve(
    response({
      ...off,
      enabled: true,
      next_scheduled_at: "2026-09-12T08:00:00+08:00",
    }),
  );
  await flushPromises();
  expect(wrapper.text()).toContain("查询时：全局已关闭");
  await wrapper.setProps({ disabled: true });
  expect(
    wrapper
      .findAll("button, select")
      .every((b) => b.attributes("disabled") !== undefined),
  ).toBe(true);
  const count = fetcher.mock.calls.length;
  await click("刷新调度状态");
  expect(fetcher).toHaveBeenCalledTimes(count);
  await wrapper.setProps({ accounts: [] });
  await flushPromises();
  expect(wrapper.find("tbody").exists()).toBe(false);
  expect(fetcher.mock.calls.some(([url]) => url.includes("/accounts//"))).toBe(
    false,
  );
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import LedgerValuationHistory from "./LedgerValuationHistory.vue";
import type { ValuationHistory, ValuationSummary } from "./ledger";

const row: ValuationSummary = {
  id: "9007199254740993",
  account_id: "a",
  currency: "CNY",
  as_of: "2026-09-01",
  ledger_at: "2026-09-01T10:00:00Z",
  calculated_at: "2026-09-01T10:00:01Z",
  saved_at: "2026-09-01T10:00:02Z",
  ledger_revision: "a".repeat(64),
  cash: "90071992547409.01",
  positions_value: "70.01",
  total_assets: "90071992547479.02",
};
const snapshot: ValuationHistory = {
  id: row.id,
  saved_at: row.saved_at,
  schema_version: 1,
  account_name: "保存时账户名称",
  valuation: {
    source: "transaction_replay",
    ...row,
    complete: true,
    known_positions_value: row.positions_value,
    items: [
      {
        instrument_id: "stock",
        quantity: "1.000001",
        market_value: "70.01",
        status: "prior_date",
        quote: {
          symbol: "sh900001",
          price: "10.000001",
          currency: "USD",
          source: "Tencent",
          date: "2026-08-31",
          quoted_at: "2026-08-31T15:00:00+08:00",
          fetched_at: row.ledger_at,
        },
        fx: {
          base: "USD",
          quote: "CNY",
          rate: "7.00000001",
          mode: "latest",
          requested_date: "",
          source: "Tencent/USDCNY",
          date: "2026-08-31",
          fetched_at: row.ledger_at,
        },
      },
    ],
  },
  instruments: [
    {
      id: "stock",
      name: "冻结证券旧名称",
      market: "SH",
      code: "900001",
      currency: "USD",
    },
  ],
};
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
let wrapper: VueWrapper;
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
async function click(text: string) {
  await wrapper
    .findAll("button")
    .find((button) => button.text() === text)!
    .trigger("click");
  await flushPromises();
}

it("reads bounded pages and inclusive as_of filters, and shows frozen full provenance without querying current quotes", async () => {
  const fetcher = vi.fn(async (url: string) => {
    const u = new URL(url, "http://localhost");
    if (u.pathname.endsWith(`/${row.id}`)) return response(snapshot);
    return response({
      items: [row],
      next_cursor: u.searchParams.has("cursor") ? undefined : row.id,
    });
  });
  vi.stubGlobal("fetch", fetcher);
  wrapper = mount(LedgerValuationHistory, { props: { accountId: "a" } });
  await flushPromises();
  expect(fetcher.mock.calls[0]![0]).toContain("limit=30");
  expect(wrapper.text()).toContain(row.total_assets);
  await click("历史下一页");
  expect(fetcher.mock.lastCall![0]).toContain(`cursor=${row.id}`);
  expect(
    wrapper
      .findAll("button")
      .find((b) => b.text() === "历史下一页")!
      .attributes("disabled"),
  ).toBeDefined();
  await click("历史首页");
  expect(fetcher.mock.lastCall![0]).not.toContain("cursor=");
  await wrapper.get('[name="history_from"]').setValue("2026-09-01");
  await wrapper.get('[name="history_to"]').setValue("2026-09-01");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(fetcher.mock.lastCall![0]).toContain("from=2026-09-01&to=2026-09-01");
  await click(`历史记录 #${row.id}`);
  const text = wrapper.get('[data-test="history-detail"]').text();
  for (const value of [
    "历史估值快照（只读）",
    "历史快照总资产",
    "保存时账户名称",
    "冻结证券旧名称",
    "SH / 900001 / USD",
    "保存时参考汇率",
    "Tencent/USDCNY",
    "1.000001",
    "10.000001",
    "7.00000001",
    row.total_assets,
    row.ledger_at,
    row.saved_at,
  ])
    expect(text).toContain(value);
  expect(text).not.toContain("当前参考估值");
  expect(text).not.toContain("最新参考汇率");
  expect(text).not.toContain("无法确认此快照已保存");
  expect(fetcher.mock.calls.every(([url]) => url.includes("/valuations"))).toBe(
    true,
  );
});

it("labels failed retained reads stale and clears detail when another record fails", async () => {
  let fail = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      fail
        ? response({ code: "storage_busy" }, 503)
        : response(
            url.includes("/valuations?")
              ? { items: [row, { ...row, id: "2" }] }
              : snapshot,
          ),
    ),
  );
  wrapper = mount(LedgerValuationHistory, { props: { accountId: "a" } });
  await flushPromises();
  await click(`历史记录 #${row.id}`);
  fail = true;
  await click("重新读取历史详情");
  expect(wrapper.get('[data-test="history-detail"]').text()).toContain(
    "读取失败，保留的数据可能已过期",
  );
  await click("历史记录 #2");
  expect(wrapper.get('[data-test="history-detail"]').text()).not.toContain(
    "冻结证券旧名称",
  );
  await click("刷新历史列表");
  expect(wrapper.text()).toContain("读取失败，保留的数据可能已过期");
  expect(wrapper.text()).toContain(row.total_assets);
});

it.each([false, true])(
  "aborts old-account list/detail and ignores late results or errors (%s)",
  async (rejectOld) => {
    let pending = false;
    const old: {
      resolve: (r: Response) => void;
      reject: (e: Error) => void;
      signal?: AbortSignal | null;
    }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string, init: RequestInit) => {
        if (pending && url.includes("/accounts/a/"))
          return new Promise<Response>((resolve, reject) =>
            old.push({ resolve, reject, signal: init.signal }),
          );
        return Promise.resolve(
          response(
            url.includes("/accounts/b/")
              ? { items: [] }
              : url.includes("/valuations?")
                ? { items: [row] }
                : snapshot,
          ),
        );
      }),
    );
    wrapper = mount(LedgerValuationHistory, { props: { accountId: "a" } });
    await flushPromises();
    await click(`历史记录 #${row.id}`);
    pending = true;
    await click("刷新历史列表");
    await click("重新读取历史详情");
    await wrapper.setProps({ accountId: "b" });
    await flushPromises();
    expect(old).toHaveLength(2);
    for (const item of old) {
      expect(item.signal?.aborted).toBe(true);
      if (rejectOld) item.reject(new Error("old account failure"));
      else item.resolve(response(snapshot));
    }
    await flushPromises();
    expect(wrapper.find('[data-test="history-detail"]').exists()).toBe(false);
    expect(wrapper.text()).not.toContain(row.total_assets);
    expect(wrapper.text()).not.toContain("读取失败");
    expect(wrapper.text()).toContain("暂无已保存历史记录");
  },
);

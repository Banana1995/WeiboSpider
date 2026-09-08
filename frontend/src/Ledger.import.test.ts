// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Ledger from "./Ledger.vue";
import ImportAccount from "./ImportAccount.vue";
import ImportedRecords from "./ImportedRecords.vue";
import { LedgerError, request, type Account } from "./ledger";
import type { ImportPreview } from "./ledgerImport";

const holdings: Account = {
  id: "holdings",
  name: "合成持仓账户",
  currency: "CNY",
  accounting_mode: "holdings",
  opening_cash: "10.00",
  opening_date: "2026-01-01",
  version: "1",
};
const reported: Account = {
  ...holdings,
  id: "reported",
  name: "合成总资产账户",
  accounting_mode: "reported",
  opening_cash: null,
};
const preview: ImportPreview = {
  digest: "a".repeat(64),
  metadata: {
    name: "合成来源账户",
    goal: "合成目标",
    expected_return: "合成原文 4%",
    investment_horizon: "合成期限",
    currency: "CNY",
    money_bucket: "合成分类",
  },
  rows: Array.from({ length: 65 }, (_, i) => ({
    source_row: i + 5,
    kind: i === 0 ? "asset" : "cash_flow",
    date: "2026-01-02",
    flow: i === 0 ? "-1.01" : "2.02",
    total_assets: i === 0 ? "90071992547409.01" : null,
    note: "合成日志\n第二行",
    detail: "合成明细\n第二行",
    source_created_at: "2026-01-03T00:00:00+08:00",
    date_raw: "2026/1/2",
    created_raw: "2026/1/3 00:00:00",
  })),
  summary: {
    row_count: 65,
    asset_count: 1,
    flow_count: 65,
    from: "2026-01-02",
    to: "2026-01-02",
    total_in: "129.28",
    total_out: "1.01",
    latest_assets: "90071992547409.01",
    latest_asset_date: "2026-01-02",
  },
  warnings: [
    "reported_history_only",
    "incremental_import_not_supported",
    "creation_time_precision_nanoseconds_truncated",
  ],
};
const importedSummary = {
  batch_id: "1",
  metadata: preview.metadata,
  summary: preview.summary,
  imported_at: "2026-01-04T00:00:00Z",
};
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
function formEntries(form: FormData) {
  const entries: [string, FormDataEntryValue][] = [];
  form.forEach((value, key) => entries.push([key, value]));
  return entries;
}
let wrapper: VueWrapper;
let accounts: Account[];
let fetcher: ReturnType<typeof vi.fn>;
let readFailure: boolean;
let uncertain: boolean;
let summary404: boolean;
let committed: boolean;
let commits: { path: string; body: FormData; key: string | null }[];
beforeEach(() => {
  accounts = [holdings, reported];
  readFailure = false;
  uncertain = false;
  summary404 = false;
  committed = false;
  commits = [];
  fetcher = vi.fn(async (url: string, init: RequestInit = {}) => {
    const u = new URL(url, "http://localhost");
    const path = u.pathname.replace("/api/platform/ledger", "");
    if (path === "/imports/youzhiyouxing/preview") return response(preview);
    if (path.endsWith("/imports/youzhiyouxing")) {
      const body = init.body as FormData;
      const id = path.split("/")[2]!;
      commits.push({
        path,
        body,
        key: new Headers(init.headers).get("Idempotency-Key"),
      });
      if (
        body.get("create_account") === "true" &&
        !accounts.some((a) => a.id === id)
      )
        accounts.push({ ...reported, id, name: preview.metadata.name });
      const duplicate = committed;
      committed = true;
      if (uncertain) {
        uncertain = false;
        throw new TypeError("synthetic network failure");
      }
      return response({
        account_id: id,
        batch_id: "1",
        imported_count: 65,
        duplicate,
      });
    }
    if (readFailure) return response({ code: "storage_busy" }, 503);
    if (path === "/accounts") return response({ items: accounts });
    if (path.endsWith("/import-summary"))
      return summary404
        ? response({ code: "not_found" }, 404)
        : response(importedSummary);
    if (path.endsWith("/imported-records"))
      return response({
        items: preview.rows
          .slice(0, 30)
          .map((r) => ({ ...r, id: String(r.source_row) })),
        next_cursor: u.searchParams.has("cursor") ? undefined : "2026-01-02:5",
      });
    if (
      path.endsWith("/positions") ||
      path.endsWith("/valuations") ||
      path === "/operations" ||
      path === "/instruments"
    )
      return response({ items: [] });
    if (path.endsWith("/valuation"))
      return response({
        account_id: path.split("/")[2],
        currency: "CNY",
        cash: "10.00",
        known_positions_value: "0.00",
        positions_value: "0.00",
        total_assets: "10.00",
        complete: true,
        items: [],
        as_of: "2026-01-02",
        ledger_at: "2026-01-02T00:00:00Z",
        calculated_at: "2026-01-02T00:00:00Z",
        ledger_revision: "a".repeat(64),
      });
    const account = accounts.find((a) => path === `/accounts/${a.id}`);
    if (account)
      return response({
        ...account,
        cash: account.accounting_mode === "holdings" ? "10.00" : null,
      });
    return response({ code: "not_found" }, 404);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
async function upload(file = new File(["synthetic"], "synthetic.xlsx")) {
  const input = wrapper.get('[data-test="import-file"]');
  Object.defineProperty(input.element, "files", {
    configurable: true,
    value: [file],
  });
  await input.trigger("change");
  return file;
}
async function showPreview() {
  const file = await upload();
  await wrapper.get('[data-test="import-preview"]').trigger("click");
  await flushPromises();
  return file;
}
async function start(
  component: typeof ImportAccount | typeof Ledger = ImportAccount,
) {
  wrapper =
    component === Ledger
      ? mount(Ledger)
      : mount(ImportAccount, { props: { accounts, disabled: false } });
  await flushPromises();
}

it("requires explicit preview and confirmation, shows exact signed/combined/missing amounts with bounded paging", async () => {
  await start();
  await upload();
  expect(fetcher).not.toHaveBeenCalled();
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
  await wrapper.get('[data-test="import-preview"]').trigger("click");
  await flushPromises();
  expect(commits).toHaveLength(0);
  const init = fetcher.mock.calls[0]![1] as RequestInit;
  expect(formEntries(init.body as FormData).map(([key]) => key)).toEqual([
    "file",
  ]);
  expect(new Headers(init.headers).has("Content-Type")).toBe(false);
  expect(wrapper.findAll("tbody tr")).toHaveLength(30);
  expect(wrapper.findAll("tbody tr")[0]!.text()).toContain("-1.01");
  expect(wrapper.findAll("tbody tr")[0]!.text()).toContain("90071992547409.01");
  expect(wrapper.findAll("tbody tr")[1]!.text()).toContain("未提供");
  expect(wrapper.get("pre").text()).toBe("合成日志\n第二行");
  for (const text of [
    "合成原文 4%",
    "累计流出（正数）",
    "实际记录日期 2026-01-02",
    "仅导入账户级历史记录，总资产不是现金，也不关联持仓。",
    "每个账户只支持首个导入批次，不支持增量合并。",
    "来源创建时间精度已截断，请以预览时间为准。",
    "不同文件会被拒绝",
  ])
    expect(wrapper.text()).toContain(text);
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "下一页预览")!
    .trigger("click");
  expect(wrapper.findAll("tbody tr")[0]!.text()).toContain("#35");
  await wrapper.get('[data-test="import-confirm"]').trigger("click");
  await flushPromises();
  expect(commits).toHaveLength(1);
  expect(commits[0]!.body.get("create_account")).toBe("true");
  expect(commits[0]!.body.get("preview_digest")).toBe(preview.digest);
  expect(commits[0]!.key).toBeTruthy();
  expect(wrapper.text()).toContain("导入已确认成功");
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
});

it("translates known warning codes without displaying the raw codes", async () => {
  await start();
  await showPreview();
  for (const code of preview.warnings)
    expect(wrapper.text()).not.toContain(code);
  expect(wrapper.text()).toContain("新建总资产账户（使用来源原名）");
  expect(wrapper.text()).toContain("导入的总资产不是现金");
  expect(wrapper.text()).not.toContain("申报");
});

it.each([
  ["synthetic_future_warning", "synthetic_future_warning"],
  ["synthetic cell value: 123.45", "unknown_warning"],
])("shows a generic warning with a safe code for %s", async (warning, code) => {
  fetcher.mockResolvedValueOnce(response({ ...preview, warnings: [warning] }));
  await start();
  await showPreview();
  expect(wrapper.text()).toContain(
    `导入存在其他注意事项，请核对预览（警告代码：${code}）`,
  );
  if (warning !== code) expect(wrapper.text()).not.toContain(warning);
});

it("imports into existing holdings without rewriting its name or creating an account, and blocks currency mismatch", async () => {
  accounts.push({ ...holdings, id: "usd", currency: "USD" });
  await start();
  await showPreview();
  await wrapper.get('[data-test="import-target"]').setValue("usd");
  expect(
    wrapper.get('[data-test="import-confirm"]').attributes(),
  ).toHaveProperty("disabled");
  await wrapper.get('[data-test="import-target"]').setValue("holdings");
  expect(wrapper.text()).toContain("来源账户原名合成来源账户");
  expect(wrapper.text()).toContain("确认目标：合成持仓账户");
  await wrapper.get('[data-test="import-confirm"]').trigger("click");
  await flushPromises();
  expect(commits[0]!.path).toBe("/accounts/holdings/imports/youzhiyouxing");
  expect(commits[0]!.body.get("create_account")).toBe("false");
  expect(accounts[0]).toEqual(holdings);
});

it("locks parent navigation and all writes after uncertainty, retries the same File/digest/key/ID and accepts a duplicate receipt", async () => {
  await start(Ledger);
  const file = await showPreview();
  uncertain = true;
  await wrapper.get('[data-test="import-confirm"]').trigger("click");
  await flushPromises();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true);
  expect(
    wrapper.get('[data-test="import-account"] fieldset').attributes(),
  ).toHaveProperty("disabled");
  expect(
    wrapper.get('[data-test="account-form"] fieldset').attributes(),
  ).toHaveProperty("disabled");
  expect(
    wrapper.get(".ledger-account-list button").attributes(),
  ).toHaveProperty("disabled");
  expect(
    wrapper.get('[data-test="import-retry"]').attributes("disabled"),
  ).toBeUndefined();
  await wrapper.get('[data-test="import-retry"]').trigger("click");
  await flushPromises();
  expect(commits).toHaveLength(2);
  expect(commits[1]!.key).toBe(commits[0]!.key);
  expect(commits[1]!.path).toBe(commits[0]!.path);
  expect(formEntries(commits[1]!.body)).toEqual(formEntries(commits[0]!.body));
  expect(commits[1]!.body.get("file")).toBe(file);
  expect(wrapper.text()).toContain("相同内容已存在，未重复导入");
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([false]);
  expect(wrapper.get('[data-test="imported-records"]').text()).toContain(
    "合成来源账户",
  );
  const id = commits[0]!.path.split("/")[2]!;
  expect(
    fetcher.mock.calls.some(([url]) =>
      new RegExp(`/accounts/${id}/(positions|valuation)`).test(url),
    ),
  ).toBe(false);
});

it("keeps success final when account and import reads fail, and never retries the commit on refresh", async () => {
  await start(Ledger);
  await showPreview();
  readFailure = true;
  await wrapper.get('[data-test="import-confirm"]').trigger("click");
  await flushPromises();
  expect(wrapper.text()).toContain("导入已确认成功");
  expect(wrapper.text()).toContain("读取失败");
  expect(wrapper.find('[data-test="import-retry"]').exists()).toBe(false);
  expect(wrapper.find('[data-test="valuation"]').exists()).toBe(false);
  readFailure = false;
  await wrapper.get('[data-test="refresh"]').trigger("click");
  await flushPromises();
  expect(commits).toHaveLength(1);
  expect(wrapper.get('[data-test="imported-records"]').text()).toContain(
    "合成来源账户",
  );
});

it("reported accounts never request positions/current or historical valuations and cannot be trade or transfer inputs", async () => {
  await start(Ledger);
  await wrapper.findAll(".ledger-account-list button")[1]!.trigger("click");
  await flushPromises();
  expect(wrapper.find('[data-test="valuation"]').exists()).toBe(false);
  expect(wrapper.text()).toContain("总资产账户（无期初现金）");
  expect(wrapper.text()).toContain("账户级导入记录");
  expect(wrapper.text()).not.toContain("申报");
  expect(wrapper.get('[name="account_id"]').text()).not.toContain(
    reported.name,
  );
  await wrapper.get('[name="kind"]').setValue("transfer");
  expect(wrapper.get('[name="to_account_id"]').text()).not.toContain(
    reported.name,
  );
  expect(
    fetcher.mock.calls.some(([url]) =>
      /\/accounts\/reported\/(positions|valuation)/.test(url),
    ),
  ).toBe(false);
  await wrapper.findAll(".ledger-account-list button")[0]!.trigger("click");
  await flushPromises();
  expect(wrapper.get('[data-test="valuation"]').text()).toContain("10.00");
  expect(wrapper.get('[data-test="imported-records"]').text()).toContain(
    "90071992547409.01",
  );
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.includes("/accounts/holdings/positions?"),
    ),
  ).toBe(true);
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.includes("/accounts/holdings/valuations?"),
    ),
  ).toBe(true);
});

it("treats an unimported summary 404 as normal and does not request rows", async () => {
  summary404 = true;
  wrapper = mount(ImportedRecords, {
    props: { accountId: "holdings", refreshKey: 0 },
  });
  await flushPromises();
  expect(wrapper.text()).toContain("此账户尚未导入");
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(
    fetcher.mock.calls.some(([url]) => url.includes("/imported-records")),
  ).toBe(false);
});

it("preserves inclusive date filters and limit 30 across first/next imported record pages", async () => {
  wrapper = mount(ImportedRecords, {
    props: { accountId: "reported", refreshKey: 0 },
  });
  await flushPromises();
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.endsWith("/imported-records?limit=30"),
    ),
  ).toBe(true);
  await wrapper.get('[name="import_from"]').setValue("2026-01-01");
  await wrapper.get('[name="import_to"]').setValue("2026-01-03");
  await wrapper.get('[data-test="import-filters"]').trigger("submit");
  await flushPromises();
  await wrapper.get('[name="import_from"]').setValue("2026-01-02");
  await wrapper.get('[data-test="import-next"]').trigger("click");
  await flushPromises();
  const url = new URL(fetcher.mock.calls.at(-1)![0], "http://localhost");
  expect(Object.fromEntries(url.searchParams)).toEqual({
    from: "2026-01-01",
    to: "2026-01-03",
    limit: "30",
    cursor: "2026-01-02:5",
  });
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "导入记录首页")!
    .trigger("click");
  await flushPromises();
  expect(fetcher.mock.calls.at(-1)![0]).toContain(
    "from=2026-01-01&to=2026-01-03&limit=30",
  );
});

it("cancels obsolete previews when the File changes, ignores late results and requires a new preview", async () => {
  let finish!: (value: Response) => void;
  fetcher.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await start();
  await upload();
  await wrapper.get('[data-test="import-preview"]').trigger("click");
  const signal = fetcher.mock.calls[0]![1].signal as AbortSignal;
  await upload(new File(["synthetic replacement"], "synthetic.xlsx"));
  expect(signal.aborted).toBe(true);
  finish(response(preview));
  await flushPromises();
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
  expect(wrapper.text()).not.toContain("合成来源账户");
  expect(commits).toHaveLength(0);
  await wrapper.get('[data-test="import-preview"]').trigger("click");
  await flushPromises();
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(true);
  await upload();
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
});

it("rejects oversized and empty files locally without uploads", async () => {
  await start();
  const large = new File(["synthetic"], "synthetic.xlsx");
  Object.defineProperty(large, "size", { value: 8 * 1024 * 1024 + 1 });
  await upload(large);
  expect(wrapper.text()).toContain("8 MiB 限制");
  await upload(new File([], "synthetic.xlsx"));
  expect(wrapper.text()).toContain("文件为空");
  expect(fetcher).not.toHaveBeenCalled();
});

it("shows Chinese malformed-file errors with coordinates but never echoes cell values, retaining typed detail_code", async () => {
  const error = {
    code: "invalid_import",
    detail_code: "invalid_money",
    row: 8,
    column: "B",
    message: "synthetic-cell-content",
  };
  fetcher.mockResolvedValue(response(error, 400));
  await start();
  await showPreview();
  expect(wrapper.text()).toContain("导入文件格式或内容不合法");
  expect(wrapper.text()).toContain("第 8 行；列 B");
  expect(wrapper.text()).not.toContain("synthetic-cell-content");
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
  fetcher.mockResolvedValue(response(error, 400));
  await expect(request("/imports/youzhiyouxing/preview")).rejects.toMatchObject(
    { detail_code: "invalid_money", row: 8, column: "B" },
  );
});

it.each([
  "preview_mismatch",
  "currency_mismatch",
  "import_already_exists",
  "idempotency_conflict",
  "upload_too_large",
])(
  "handles definite %s rejection without treating it as success",
  async (code) => {
    await start();
    await showPreview();
    fetcher.mockResolvedValueOnce(
      response(
        { code },
        code === "upload_too_large"
          ? 413
          : code === "preview_mismatch"
            ? 400
            : 409,
      ),
    );
    await wrapper.get('[data-test="import-confirm"]').trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain(new LedgerError(code).message);
    expect(wrapper.text()).not.toContain("导入已确认成功");
    expect(wrapper.find('[data-test="import-retry"]').exists()).toBe(false);
  },
);

it("storage_busy freezes payload and retry stays available even if account reads become unavailable", async () => {
  await start();
  const file = await showPreview();
  fetcher.mockResolvedValueOnce(response({ code: "storage_busy" }, 503));
  await wrapper.get('[data-test="import-confirm"]').trigger("click");
  await flushPromises();
  const first = fetcher.mock.calls.at(-1)![1] as RequestInit;
  await wrapper.setProps({ disabled: true });
  await wrapper.get('[data-test="import-retry"]').trigger("click");
  await flushPromises();
  expect(commits[0]!.key).toBe(
    new Headers(first.headers).get("Idempotency-Key"),
  );
  expect(commits[0]!.body.get("file")).toBe(file);
});

it("ignores old account metadata and imported rows after switching to reported", async () => {
  let finishAccount!: (value: Response) => void;
  let finishRows!: (value: Response) => void;
  const normal = fetcher.getMockImplementation()!;
  fetcher.mockImplementation((url: string, init: RequestInit) => {
    if (url.endsWith("/accounts/holdings"))
      return new Promise((resolve) => {
        finishAccount = resolve;
      });
    if (url.includes("/accounts/holdings/imported-records"))
      return new Promise((resolve) => {
        finishRows = resolve;
      });
    return normal(url, init);
  });
  await start(Ledger);
  await wrapper.findAll(".ledger-account-list button")[0]!.trigger("click");
  await flushPromises();
  await wrapper.findAll(".ledger-account-list button")[1]!.trigger("click");
  await flushPromises();
  finishAccount(response({ ...holdings, cash: "999.99" }));
  finishRows(
    response({
      items: [{ ...preview.rows[0], id: "old", note: "合成过时内容" }],
    }),
  );
  await flushPromises();
  expect(wrapper.text()).not.toContain("合成过时内容");
  expect(wrapper.find('[data-test="valuation"]').exists()).toBe(false);
  expect(
    fetcher.mock.calls.some(([url]) =>
      /\/accounts\/reported\/(positions|valuation)/.test(url),
    ),
  ).toBe(false);
});

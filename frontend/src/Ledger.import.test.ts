// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Ledger from "./Ledger.vue";
import ImportAccount from "./ImportAccount.vue";
import ImportedRecords from "./ImportedRecords.vue";
import { errorText, LedgerError, request, type Account } from "./ledger";
import type { ImportPreview } from "./ledgerImport";

const holdings: Account = {
  current_holdings_input: "manual_snapshot",
  id: "holdings",
  name: "合成持仓账户",
  currency: "CNY",
  opening_cash: null,
  opening_date: "2026-01-01",
  version: "1",
};
const reported: Account = {
  ...holdings,
  id: "reported",
  name: "合成总资产账户",
  current_holdings_input: "manual_snapshot",
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
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
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
    if (path.endsWith("/current-holdings"))
      return response({
        account_id: path.split("/")[2],
        audit_id: "",
        snapshot: null,
      });
    if (path.endsWith("/holdings"))
      return response({
        account_id: path.split("/")[2],
        currency: "CNY",
        source: "manual_snapshot",
        as_of: "2026-09-06",
        ledger_at: "2026-09-06T00:00:00Z",
        revision: "a".repeat(64),
        manual_version: "0",
        configured: false,
        cash: null,
        complete: false,
        total_assets: null,
        items: [],
      });
    if (path.endsWith("/records")) return response({ items: [] });
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
        cash: null,
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
async function importFile() {
  const file = await upload();
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  return file;
}
async function start(
  component: typeof ImportAccount | typeof Ledger = ImportAccount,
  openImport = true,
) {
  wrapper =
    component === Ledger
      ? mount(Ledger)
      : mount(ImportAccount, { props: { accounts, disabled: false } });
  await flushPromises();
  if (component === Ledger && openImport) {
    await click("管理账户");
    await click("导入 Excel 账本");
  }
}
async function click(text: string) {
  const button = wrapper.findAll("button").find((b) => b.text() === text);
  expect(button, text).toBeTruthy();
  await button!.trigger("click");
  await flushPromises();
}

it("imports with one click after selecting a file, validates internally and reports the count", async () => {
  await start();
  await upload();
  expect(fetcher).not.toHaveBeenCalled();
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
  expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(false);
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  expect(commits).toHaveLength(1);
  const init = fetcher.mock.calls[0]![1] as RequestInit;
  expect(formEntries(init.body as FormData).map(([key]) => key)).toEqual([
    "file",
  ]);
  expect(new Headers(init.headers).has("Content-Type")).toBe(false);
  expect(wrapper.findAll(".lp-import-preview li")).toHaveLength(0);
  expect(commits).toHaveLength(1);
  expect(commits[0]!.body.get("create_account")).toBe("true");
  expect(commits[0]!.body.get("preview_digest")).toBe(preview.digest);
  expect(commits[0]!.key).toBeTruthy();
  expect(wrapper.text()).toContain("已导入 65 笔记录");
  expect(
    wrapper.get('[data-test="import-submit"]').attributes("disabled"),
  ).toBeDefined();
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
});

it("explains import scope without displaying implementation warnings", async () => {
  await start();
  await importFile();
  for (const code of preview.warnings)
    expect(wrapper.text()).not.toContain(code);
  expect(wrapper.text()).toContain("新建账户（使用文件中的名称）");
  expect(wrapper.text()).toContain("不改变当前持仓");
  expect(wrapper.text()).not.toContain("申报");
});

it.each([
  ["synthetic_future_warning", "synthetic_future_warning"],
  ["synthetic cell value: 123.45", "unknown_warning"],
])("never echoes internal warning or cell content for %s", async (warning) => {
  fetcher.mockResolvedValueOnce(response({ ...preview, warnings: [warning] }));
  await start();
  await importFile();
  expect(wrapper.text()).not.toContain(warning);
  expect(commits).toHaveLength(1);
});

it("imports into existing holdings without rewriting its name or creating an account, and blocks currency mismatch", async () => {
  accounts.push({ ...holdings, id: "usd", currency: "USD" });
  await start();
  await upload();
  await wrapper.get('[data-test="import-target"]').setValue("usd");
  await click("导入");
  expect(wrapper.text()).toContain("文件币种与目标账户不一致");
  expect(commits).toHaveLength(0);
  await wrapper.get('[data-test="import-target"]').setValue("holdings");
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  expect(commits[0]!.path).toBe("/accounts/holdings/imports/youzhiyouxing");
  expect(commits[0]!.body.get("create_account")).toBe("false");
  expect(accounts[0]).toEqual(holdings);
});

it("locks parent navigation and all writes after uncertainty, retries the same File/digest/key/ID and accepts a duplicate receipt", async () => {
  await start(Ledger);
  const file = await upload();
  uncertain = true;
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true);
  expect(
    wrapper.get('[data-test="import-account"] fieldset').attributes(),
  ).toHaveProperty("disabled");
  expect(
    wrapper
      .findAll('[role="tab"]')
      .every((b) => b.attributes("disabled") !== undefined),
  ).toBe(true);
  expect(
    wrapper
      .findAll("nav a")
      .every((a) => a.attributes("aria-disabled") === "true"),
  ).toBe(true);
  expect(
    wrapper.get('[data-test="import-submit"]').attributes("disabled"),
  ).toBeUndefined();
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  expect(commits).toHaveLength(2);
  expect(commits[1]!.key).toBe(commits[0]!.key);
  expect(commits[1]!.path).toBe(commits[0]!.path);
  expect(formEntries(commits[1]!.body)).toEqual(formEntries(commits[0]!.body));
  expect(commits[1]!.body.get("file")).toBe(file);
  expect(wrapper.text()).toContain("未重复添加");
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([false]);
  expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toBe(
    "合成来源账户",
  );
  expect(
    wrapper.get('[data-test="account-records"]').attributes("open"),
  ).toBeDefined();
  expect(wrapper.find('[aria-label="账户管理"]').exists()).toBe(false);
  const id = commits[0]!.path.split("/")[2]!;
  expect(
    fetcher.mock.calls.some(([url]) =>
      new RegExp(`/accounts/${id}/(positions|valuation)`).test(url),
    ),
  ).toBe(false);
});

it("keeps success final when account and import reads fail, and never retries the commit on refresh", async () => {
  await start(Ledger);
  await upload();
  readFailure = true;
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  expect(wrapper.text()).toContain("已导入 65 笔记录");
  expect(wrapper.text()).toContain("读取失败");
  expect(wrapper.find('[data-test="import-retry"]').exists()).toBe(false);
  expect(wrapper.find('[data-test="valuation"]').exists()).toBe(false);
  readFailure = false;
  await click("重新读取账户");
  await flushPromises();
  expect(commits).toHaveLength(1);
  expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toBe(
    "合成来源账户",
  );
  expect(
    wrapper.get('[data-test="account-records"]').attributes("open"),
  ).toBeDefined();
});

it("accounts without a configured current source do not request valuations or accept transaction inputs", async () => {
  await start(Ledger, false);
  await wrapper.findAll('[role="tab"]')[1]!.trigger("click");
  await flushPromises();
  expect(wrapper.get('[data-test="current-holdings"]').text()).toContain(
    "尚未设置当前持仓",
  );
  expect(wrapper.find('[data-test="save-valuation"]').exists()).toBe(false);
  expect(wrapper.find('[name="to_account_id"]').exists()).toBe(false);
  expect(
    wrapper
      .findAll("button")
      .find((b) => b.text() === "添加持仓")!
      .attributes("disabled"),
  ).toBeUndefined();
  await wrapper.findAll('[role="tab"]')[0]!.trigger("click");
  await flushPromises();
  expect(wrapper.get('[data-test="current-holdings"]').text()).toContain(
    "尚未设置当前持仓",
  );
  expect(
    fetcher.mock.calls.some(([url]) =>
      /\/(positions|operations|valuation)(?:\?|$)/.test(url),
    ),
  ).toBe(false);
  expect(commits).toHaveLength(0);
});

it("edits current holdings on the imported account, coordinates pending locks and only explicitly quotes", async () => {
  const original = fetcher.getMockImplementation()!;
  let snapshot: unknown = null;
  let attempts = 0;
  fetcher.mockImplementation(async (url: string, init: RequestInit = {}) => {
    if (url.endsWith("/accounts/reported/current-holdings")) {
      if (init.method === "PUT") {
        const input = JSON.parse(init.body as string);
        snapshot = {
          version: "1",
          saved_at: "2026-09-08T00:00:00Z",
          cash: input.cash,
          positions: input.positions,
        };
        if (++attempts === 1) throw new TypeError("synthetic lost receipt");
      }
      return response({
        account_id: "reported",
        audit_id: snapshot ? "5" : "",
        snapshot,
      });
    }
    if (url.endsWith("/accounts/reported/holdings") && snapshot)
      return response({
        account_id: "reported",
        currency: "CNY",
        source: "manual_snapshot",
        as_of: "2026-09-08",
        ledger_at: "2026-09-08T00:00:00Z",
        revision: "a".repeat(64),
        manual_version: "1",
        configured: true,
        cash: "0.00",
        complete: true,
        total_assets: "0.00",
        items: [],
      });
    return original(url, init);
  });
  await start(Ledger, false);
  await wrapper.findAll('[role="tab"]')[1]!.trigger("click");
  await flushPromises();
  const panel = wrapper.get('[data-test="current-holdings"]');
  expect(panel.text()).toContain("尚未设置");
  await click("设置现金");
  await panel.get('[name="current_cash"]').setValue("0.00");
  await panel.get("form").trigger("submit");
  await flushPromises();
  expect(
    wrapper
      .findAll("button")
      .find((b) => b.text() === "刷新当前数据")!
      .attributes("disabled"),
  ).toBeDefined();
  expect(
    wrapper
      .findAll('[role="tab"]')
      .every((b) => b.attributes("disabled") !== undefined),
  ).toBe(true);
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  await panel.get('[data-test="current-retry"]').trigger("click");
  await flushPromises();
  expect(wrapper.find('[data-test="save-valuation"]').exists()).toBe(false);
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.endsWith("/accounts/reported/valuation"),
    ),
  ).toBe(false);
  const writes = fetcher.mock.calls.filter(
    ([url, init]) => url.endsWith("/current-holdings") && init.method === "PUT",
  );
  expect(writes).toHaveLength(2);
  expect(writes[0]![1].body).toBe(writes[1]![1].body);
  expect(new Headers(writes[0]![1].headers).get("Idempotency-Key")).toBe(
    new Headers(writes[1]![1].headers).get("Idempotency-Key"),
  );
  await click("刷新行情");
  await flushPromises();
  expect(wrapper.get(".lp-holdings-totals").text()).toContain("0.00");
  expect(wrapper.get('[data-test="current-holdings"]').text()).toContain(
    "历史记录不变",
  );
  expect(
    fetcher.mock.calls.filter(([, init]) => init.method === "POST"),
  ).toHaveLength(0);
  expect(
    fetcher.mock.calls.some(([url]) =>
      /\/accounts\/reported\/(positions|operations)/.test(url),
    ),
  ).toBe(false);
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

it("locks file and target during internal validation and ignores replacement file events", async () => {
  let finish!: (value: Response) => void;
  fetcher.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await start();
  const file = await upload();
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  const signal = fetcher.mock.calls[0]![1].signal as AbortSignal;
  expect(wrapper.get("fieldset").attributes("disabled")).toBeDefined();
  await upload(new File(["synthetic replacement"], "synthetic.xlsx"));
  expect(signal.aborted).toBe(false);
  finish(response(preview));
  await flushPromises();
  expect(wrapper.find('[data-test="import-confirm"]').exists()).toBe(false);
  expect(commits).toHaveLength(1);
  expect(commits[0]!.body.get("file")).toBe(file);
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
  await importFile();
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
    await upload();
    fetcher.mockResolvedValueOnce(response(preview));
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
    await wrapper.get('[data-test="import-submit"]').trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain(errorText(new LedgerError(code).message));
    expect(wrapper.text()).not.toContain("导入已确认成功");
    const first = fetcher.mock.calls.at(-1)![1] as RequestInit;
    await click("重试导入");
    expect(commits[0]!.key).toBe(
      new Headers(first.headers).get("Idempotency-Key"),
    );
  },
);

it("storage_busy freezes payload and retry stays available even if account reads become unavailable", async () => {
  await start();
  const file = await upload();
  fetcher.mockResolvedValueOnce(response(preview));
  fetcher.mockResolvedValueOnce(response({ code: "storage_busy" }, 503));
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  const first = fetcher.mock.calls.at(-1)![1] as RequestInit;
  await wrapper.setProps({ disabled: true });
  await wrapper.get('[data-test="import-submit"]').trigger("click");
  await flushPromises();
  expect(commits[0]!.key).toBe(
    new Headers(first.headers).get("Idempotency-Key"),
  );
  expect(commits[0]!.body.get("file")).toBe(file);
});

it("ignores old account metadata and imported rows after switching to reported", async () => {
  let finishRows!: (value: Response) => void;
  let signal: AbortSignal | undefined;
  const normal = fetcher.getMockImplementation()!;
  fetcher.mockImplementation((url: string, init: RequestInit) => {
    if (url.includes("/accounts/holdings/imported-records"))
      return new Promise((resolve) => {
        signal = init.signal as AbortSignal;
        finishRows = resolve;
      });
    return normal(url, init);
  });
  wrapper = mount(ImportedRecords, {
    props: { accountId: "holdings", refreshKey: 0 },
  });
  await flushPromises();
  await wrapper.setProps({ accountId: "reported" });
  await flushPromises();
  expect(signal?.aborted).toBe(true);
  finishRows(
    response({
      items: [{ ...preview.rows[0], id: "old", note: "合成过时内容" }],
    }),
  );
  await flushPromises();
  expect(wrapper.text()).not.toContain("合成过时内容");
  expect(wrapper.get('[data-test="imported-records"]').text()).toContain(
    "合成来源账户",
  );
  expect(
    fetcher.mock.calls.some(([url]) =>
      /\/accounts\/reported\/(positions|valuation)/.test(url),
    ),
  ).toBe(false);
});

it("locks the import dialog during validation, but allows a selected file to be discarded before importing", async () => {
  await start(Ledger);
  await upload();
  await wrapper.get('[aria-label="关闭弹窗"]').trigger("click");
  await flushPromises();
  expect(wrapper.text()).toContain("放弃尚未保存的修改");
  expect(commits).toHaveLength(0);
  await click("继续编辑");
  expect(
    wrapper.get('[data-test="import-submit"]').attributes("disabled"),
  ).toBeUndefined();
});

it.each([
  null,
  { digest: "bad" },
  { ...preview, rows: [{ ...preview.rows[0], total_assets: "not money" }] },
])(
  "never writes after an invalid internal validation response",
  async (invalid) => {
    await start();
    fetcher.mockResolvedValueOnce(response(invalid));
    await importFile();
    expect(commits).toHaveLength(0);
    expect(wrapper.text()).toContain("无法确认服务响应");
    expect(
      wrapper.get('[data-test="import-submit"]').attributes("disabled"),
    ).toBeUndefined();
  },
);

it("retains the original write after an invalid receipt and safely retries it", async () => {
  await start();
  await upload();
  fetcher.mockResolvedValueOnce(response(preview));
  fetcher.mockResolvedValueOnce(
    response({
      account_id: "wrong",
      batch_id: "1",
      imported_count: "3",
      duplicate: false,
    }),
  );
  await click("导入");
  const first = fetcher.mock.calls.at(-1)![1] as RequestInit;
  expect(wrapper.text()).toContain("导入结果暂未确认");
  await click("重试导入");
  expect(commits[0]!.key).toBe(
    new Headers(first.headers).get("Idempotency-Key"),
  );
  expect(commits[0]!.body.get("file")).toBe(
    (first.body as FormData).get("file"),
  );
  expect(wrapper.text()).toContain("已导入 65 笔记录");
});

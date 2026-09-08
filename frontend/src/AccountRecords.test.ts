// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import AccountRecords from "./AccountRecords.vue";
import type { AccountRecord } from "./accountRecords";

let wrapper: VueWrapper;
let records: AccountRecord[];
let calls: { path: string; method: string; body: string; key: string }[];
let loseWrite: boolean;
let readFailure: boolean;
let conflict: boolean;
let intercept:
  ((path: string, method: string) => Promise<Response> | undefined) | undefined;
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
const sample = (id = "import-1", account = "a"): AccountRecord => ({
  id,
  account_id: account,
  kind: "asset",
  date: "2020-01-01",
  flow: null,
  total_assets: "90071992547409.01",
  note: "Synthetic original",
  version: "1",
  origin: "import",
  original: {
    source_row: 5,
    kind: "asset",
    date: "2020-01-01",
    flow: null,
    total_assets: "90071992547409.01",
    note: "Synthetic original",
    source_created_at: "",
    detail: "Synthetic detail",
    date_raw: "2020/1/1",
    created_raw: "",
  },
  voided: false,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
});
beforeEach(() => {
  records = [sample()];
  calls = [];
  loseWrite = false;
  readFailure = false;
  conflict = false;
  intercept = undefined;
  const receipts = new Map<string, unknown>();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, options: RequestInit = {}) => {
      const path = String(url).replace("/api/platform/ledger", "");
      const method = options.method ?? "GET";
      const body = String(options.body ?? "");
      const key = new Headers(options.headers).get("Idempotency-Key") ?? "";
      calls.push({ path, method, body, key });
      const intercepted = intercept?.(path, method);
      if (intercepted) return intercepted;
      if (method !== "GET") {
        if (conflict) return response({ code: "version_conflict" }, 409);
        if (receipts.has(key)) return response(receipts.get(key));
        const input = JSON.parse(body);
        let result;
        if (path === "/reported-accounts")
          result = {
            ...input,
            accounting_mode: "reported",
            opening_cash: null,
            version: "1",
          };
        else {
          const id = input.id ?? path.split("/").at(-1);
          const previous = records.find((record) => record.id === id);
          result = {
            ...(previous ?? sample(id)),
            ...input.entry,
            id,
            version: String(Number(previous?.version ?? 0) + 1),
            origin: previous?.origin ?? "manual",
            original: previous?.original ?? null,
            voided: method === "DELETE",
          };
          records = [result, ...records.filter((record) => record.id !== id)];
        }
        receipts.set(key, result);
        if (loseWrite) {
          loseWrite = false;
          throw new TypeError("Synthetic lost receipt");
        }
        return response(result);
      }
      if (readFailure) return response({ code: "storage_busy" }, 503);
      if (path.includes("effective-summary"))
        return response({
          row_count: records.filter((r) => !r.voided).length,
          asset_count: 1,
          flow_count: 0,
          log_count: 0,
          voided_count: records.filter((r) => r.voided).length,
          from: "2020-01-01",
          to: "2020-01-01",
          total_in: "0.00",
          total_out: "0.00",
          latest_assets: records[0]?.total_assets,
          latest_asset_date: "2020-01-01",
          latest_asset_count: 1,
        });
      if (path.includes("revisions"))
        return response({ items: [{ record: sample(), reason: "" }] });
      if (path.includes("/records")) return response({ items: records });
      throw new Error(`Unexpected ${path}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
async function start(accountId = "a") {
  wrapper = mount(AccountRecords, {
    props: { accountId, refreshKey: 0, disabled: false },
  });
  await flushPromises();
}
async function draft(kind = "asset", assets = "0") {
  await wrapper.get('[name="entry_kind"]').setValue(kind);
  await wrapper.get('[name="entry_date"]').setValue("2099-01-01");
  if (kind !== "log")
    await wrapper.get('[name="entry_assets"]').setValue(assets);
}
const writes = () => calls.filter((c) => c.method !== "GET");

it("creates a reported account without cash, positions or Excel", async () => {
  await start("");
  await wrapper.get('[name="reported_name"]').setValue("Synthetic");
  await wrapper.get('[name="reported_opening_date"]').setValue("2020-01-01");
  await wrapper.get('[data-test="reported-create"]').trigger("submit");
  await flushPromises();
  expect(writes()).toHaveLength(1);
  expect(writes()[0]!.path).toBe("/reported-accounts");
  expect(JSON.parse(writes()[0]!.body)).toEqual({
    id: expect.any(String),
    name: "Synthetic",
    currency: "CNY",
    opening_date: "2020-01-01",
  });
  expect(writes()[0]!.key).toBeTruthy();
  expect(wrapper.emitted("created")?.[0]).toEqual([
    JSON.parse(writes()[0]!.body).id,
  ]);
});
it("renders imported exact money and immutable source revisions", async () => {
  await start();
  expect(wrapper.text()).toContain("90071992547409.01");
  await wrapper.get('[data-test="effective-records"] button').trigger("click");
  await flushPromises();
  expect(wrapper.text()).toContain("Synthetic detail");
  expect(wrapper.text()).toContain("版本 1");
  expect(writes()).toHaveLength(0);
});
it("records zero assets without turning them into cash", async () => {
  await start();
  await draft();
  await wrapper.get('[data-test="account-entry"]').trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[0]!.body).entry).toEqual({
    kind: "asset",
    date: "2099-01-01",
    flow: null,
    total_assets: "0",
    note: "",
  });
  expect(calls.some((c) => /positions|valuation|operations/.test(c.path))).toBe(
    false,
  );
});
it.each(["", "0", "90071992547409.01"])(
  "preserves flow optional assets %s and exact negative amounts",
  async (assets) => {
    await start();
    await draft("cash_flow", assets);
    await wrapper.get('[name="entry_flow"]').setValue("-90071992547409.01");
    await wrapper.get('[data-test="account-entry"]').trigger("submit");
    await flushPromises();
    expect(JSON.parse(writes()[0]!.body).entry).toMatchObject({
      flow: "-90071992547409.01",
      total_assets: assets || null,
    });
  },
);
it("records logs without leaking hidden asset or flow fields", async () => {
  await start();
  await draft("cash_flow", "100");
  await wrapper.get('[name="entry_flow"]').setValue("20");
  await wrapper.get('[name="entry_kind"]').setValue("log");
  await wrapper.get('[name="entry_note"]').setValue("Synthetic log\nnext line");
  await wrapper.get('[data-test="account-entry"]').trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[0]!.body).entry).toMatchObject({
    kind: "log",
    flow: null,
    total_assets: null,
    note: "Synthetic log\nnext line",
  });
});
it("replaces and voids with selected versions and reasons", async () => {
  await start();
  await wrapper.get('[data-test="effective-records"] button').trigger("click");
  await flushPromises();
  await wrapper.get('[name="entry_assets"]').setValue("0");
  await wrapper.get('[name="entry_reason"]').setValue("Synthetic correction");
  await wrapper.get('[data-test="account-entry"]').trigger("submit");
  await flushPromises();
  expect(writes()[0]).toMatchObject({
    method: "PUT",
    path: "/accounts/a/records/import-1",
  });
  expect(JSON.parse(writes()[0]!.body)).toMatchObject({
    expected_version: "1",
    reason: "Synthetic correction",
  });
  await wrapper.get('[data-test="effective-records"] button').trigger("click");
  await flushPromises();
  await wrapper.get('[name="entry_reason"]').setValue("Synthetic void");
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "作废此记录")!
    .trigger("click");
  await flushPromises();
  expect(JSON.parse(writes()[1]!.body)).toEqual({
    expected_version: "2",
    reason: "Synthetic void",
  });
  expect(wrapper.text()).toContain("已作废");
});
it("locks ambiguous writes and retries exact bytes even after a later revision", async () => {
  await start();
  await draft();
  loseWrite = true;
  await wrapper.get('[data-test="account-entry"]').trigger("submit");
  await flushPromises();
  expect(
    wrapper.get('[data-test="account-entry"] fieldset').attributes("disabled"),
  ).toBeDefined();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  records[0] = { ...records[0]!, version: "2", total_assets: "23.00" };
  await wrapper.get('[data-test="record-retry"]').trigger("click");
  await flushPromises();
  expect(writes()).toHaveLength(2);
  expect(writes()[0]).toEqual(writes()[1]);
  expect(wrapper.get('[data-test="effective-records"]').text()).toContain(
    "23.00",
  );
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([false]);
});
it("keeps confirmed writes successful when independent reads fail", async () => {
  await start();
  await draft();
  readFailure = true;
  await wrapper.get('[data-test="account-entry"]').trigger("submit");
  await flushPromises();
  expect(wrapper.text()).toContain("写入已确认成功");
  expect(wrapper.text()).toContain("读取失败");
  expect(wrapper.find('[data-test="record-retry"]').exists()).toBe(false);
});
it("does not automatically overwrite a CAS conflict", async () => {
  await start();
  await wrapper.get('[data-test="effective-records"] button').trigger("click");
  await flushPromises();
  await wrapper.get('[name="entry_reason"]').setValue("Synthetic");
  conflict = true;
  await wrapper.get('[data-test="account-entry"]').trigger("submit");
  await flushPromises();
  expect(wrapper.text()).toContain("version_conflict");
  expect(writes()).toHaveLength(1);
  expect(wrapper.find('[data-test="record-retry"]').exists()).toBe(false);
});
it("ignores late reads on account switch even when fetch ignores abort", async () => {
  let release!: (response: Response) => void;
  intercept = (path, method) =>
    method === "GET" && path.startsWith("/accounts/a/records")
      ? new Promise((resolve) => {
          release = resolve;
        })
      : undefined;
  await start();
  records = [sample("manual-b", "b")];
  await wrapper.setProps({ accountId: "b" });
  await flushPromises();
  release(response({ items: [sample()] }));
  await flushPromises();
  expect(wrapper.get('[data-test="effective-records"]').text()).not.toContain(
    "import-1",
  );
  await wrapper.get('[data-test="effective-records"] button').trigger("click");
  expect(wrapper.text()).toContain("manual-b");
});
it("rejects excess precision before writing and rejects malformed read responses", async () => {
  await start();
  await draft("asset", "1.001");
  await wrapper.get('[data-test="account-entry"]').trigger("submit");
  await flushPromises();
  expect(writes()).toHaveLength(0);
  intercept = (path, method) =>
    method === "GET" && path.includes("/records")
      ? Promise.resolve(response({ wrong: true }))
      : undefined;
  await wrapper.setProps({ refreshKey: 1 });
  await flushPromises();
  expect(wrapper.text()).toContain("invalid_response");
});

it("keeps applied date filters across list pages and paginates revisions", async () => {
  intercept = (path, method) => {
    if (method !== "GET" || !path.includes("/records")) return;
    const url = new URL(path, "http://localhost");
    if (path.includes("revisions"))
      return Promise.resolve(
        response({
          items: [{ record: sample(), reason: "" }],
          next_cursor: url.searchParams.has("cursor") ? undefined : "1",
        }),
      );
    return Promise.resolve(
      response({
        items: [sample()],
        next_cursor: url.searchParams.has("cursor")
          ? undefined
          : "2020-01-01:import-1",
      }),
    );
  };
  await start();
  await wrapper.get('[name="record_from"]').setValue("2020-01-01");
  await wrapper.get('[name="record_to"]').setValue("2020-12-31");
  await wrapper.get('[data-test="record-filters"]').trigger("submit");
  await flushPromises();
  await wrapper.get('[name="record_from"]').setValue("2021-01-01");
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "下一页记录")!
    .trigger("click");
  await flushPromises();
  const url = new URL(calls.at(-1)!.path, "http://localhost");
  expect(url.searchParams.get("from")).toBe("2020-01-01");
  expect(url.searchParams.get("cursor")).toBe("2020-01-01:import-1");
  await wrapper.get('[data-test="effective-records"] button').trigger("click");
  await flushPromises();
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "下一页修订")!
    .trigger("click");
  await flushPromises();
  expect(calls.at(-1)!.path).toContain("/revisions?limit=30&cursor=1");
});

it("retries uncertain account creation using its durable receipt rather than adopting a GET", async () => {
  await start("");
  await wrapper.get('[name="reported_name"]').setValue("Synthetic");
  await wrapper.get('[name="reported_opening_date"]').setValue("2020-01-01");
  loseWrite = true;
  await wrapper.get('[data-test="reported-create"]').trigger("submit");
  await flushPromises();
  expect(
    wrapper
      .get('[data-test="reported-create"] fieldset')
      .attributes("disabled"),
  ).toBeDefined();
  await wrapper.get('[data-test="record-retry"]').trigger("click");
  await flushPromises();
  expect(writes()[0]).toEqual(writes()[1]);
  expect(calls.every((c) => c.method === "POST")).toBe(true);
  expect(wrapper.emitted("created")).toHaveLength(1);
});

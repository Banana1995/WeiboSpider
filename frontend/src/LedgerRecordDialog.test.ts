// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Dialog from "./LedgerRecordDialog.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { Account } from "./ledger";
import type { AccountRecord } from "./accountRecords";

const account: Account = {
  id: "a",
  name: "Synthetic account",
  currency: "CNY",
  opening_date: "2026-01-01",
  opening_cash: null,
  current_holdings_input: "manual_snapshot",
  version: "1",
};
const base: AccountRecord = {
  id: "r",
  account_id: "a",
  kind: "cash_flow",
  date: "2026-01-02",
  flow: "10.00",
  total_assets: null,
  note: "Synthetic note",
  sequence: "1",
  version: "1",
  origin: "manual",
  original: null,
  voided: false,
  created_at: "2026-01-02T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
};
let wrapper: VueWrapper;
let fetcher: ReturnType<typeof vi.fn>;
let workspace: ReturnType<typeof createLedgerWorkspace>;
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  workspace = createLedgerWorkspace(() => {});
  fetcher = vi.fn(async (url: string, init: RequestInit = {}) => {
    if (url.includes("/channels?")) return response(channelContext(url));
    if (url.endsWith("/revisions?limit=30"))
      return response({
        items: [{ record: base, reason: "Synthetic revision" }],
      });
    if (!init.method) return response(base);
    const data = JSON.parse(init.body as string);
    return response({
      ...base,
      id: data.id || base.id,
      ...data.entry,
      version: "2",
      voided: init.method === "DELETE",
    });
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function start(mode: "create" | "detail" | "edit" = "detail") {
  wrapper = mount(Dialog, {
    props: {
      account,
      initialMode: mode,
      recordId: mode === "create" ? undefined : "r",
    },
    global: { provide: { [ledgerWorkspaceKey as symbol]: workspace } },
  });
  await flushPromises();
}
async function click(text: string) {
  const button = wrapper.findAll("button").find((b) => b.text() === text);
  expect(button, text).toBeTruthy();
  await button!.trigger("click");
  await flushPromises();
}
const writes = () => fetcher.mock.calls.filter(([, init]) => init.method);

function channelContext(
  url: string,
  items = [] as { name: string; amount: string }[],
) {
  return {
    account_id: account.id,
    as_of: new URL(url, "http://localhost").searchParams.get("to"),
    source_record_id: items.length ? "import-1" : "",
    source_date: items.length ? "2020-01-01" : "",
    items,
  };
}
function withChannels() {
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (url: string, init: RequestInit = {}) =>
    url.includes("/channels?")
      ? response(
          channelContext(url, [
            { name: "平台甲", amount: "120.10" },
            { name: "平台乙", amount: "80.20" },
            { name: "钱包", amount: "0.00" },
          ]),
        )
      : original(url, init),
  );
}

it("hides the channel selector without channels and offers one total input for assets", async () => {
  await start("create");
  expect(wrapper.find('select[aria-label="资金渠道"]').exists()).toBe(false);
  await click("更新总资产");
  expect(wrapper.find(".lp-channel-editor").exists()).toBe(false);
  expect(wrapper.findAll('input[inputmode="decimal"]')).toHaveLength(1);
  await wrapper.get('input[inputmode="decimal"]').setValue("123.45");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  const entry = JSON.parse(writes()[0]![1].body).entry;
  expect(entry).toMatchObject({ total_assets: "123.45", flow: null });
  expect(entry.channel_assets).toBeUndefined();
  expect(entry.flow_channel).toBeUndefined();
});

it("defaults inflows to the first existing channel and lets outflows choose another", async () => {
  withChannels();
  await start("create");
  const select = wrapper.get('select[aria-label="资金渠道"]');
  expect((select.element as HTMLSelectElement).value).toBe("平台甲");
  expect(select.findAll("option").map((option) => option.text())).toEqual([
    "平台甲",
    "平台乙",
    "钱包",
  ]);
  await wrapper.get('input[inputmode="decimal"]').setValue("10.25");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[0]![1].body).entry).toMatchObject({
    flow_channel: "平台甲",
    flow: "10.25",
    total_assets: null,
  });
  await click("转出");
  await select.setValue("平台乙");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[1]![1].body).entry).toMatchObject({
    flow_channel: "平台乙",
    flow: "-10.25",
    total_assets: null,
  });
});

it("lists every channel with its last amount, preserves zero and sums a new asset snapshot", async () => {
  withChannels();
  await start("create");
  await click("更新总资产");
  expect(wrapper.findAll(".lp-channel-row")).toHaveLength(3);
  expect(wrapper.text()).toContain("上次：120.10");
  expect(wrapper.text()).toContain("上次：80.20");
  expect(wrapper.text()).toContain("上次：0.00");
  expect(
    (
      wrapper.get('input[aria-label="平台甲资产金额"]')
        .element as HTMLInputElement
    ).value,
  ).toBe("");
  await wrapper.get("form").trigger("submit");
  expect(writes()).toHaveLength(0);
  await click("填入上次金额");
  await wrapper.get('input[aria-label="平台甲资产金额"]').setValue("120.11");
  expect(wrapper.get("output").text()).toBe("200.31");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  const entry = JSON.parse(writes()[0]![1].body).entry;
  expect(entry).toMatchObject({
    kind: "asset",
    flow: null,
    total_assets: "200.31",
    channel_assets: [
      { name: "平台甲", amount: "120.11" },
      { name: "平台乙", amount: "80.20" },
      { name: "钱包", amount: "0.00" },
    ],
  });
  expect(entry.flow_channel).toBeUndefined();
});

it("keeps imported channel snapshots when editing a mixed flow and asset record", async () => {
  const imported = {
    ...base,
    origin: "import",
    total_assets: "200.30",
    channel_assets: [
      { name: "平台甲", amount: "120.10" },
      { name: "平台乙", amount: "80.20" },
    ],
  };
  fetcher.mockResolvedValueOnce(response(imported));
  withChannels();
  await start("edit");
  expect(wrapper.findAll(".lp-channel-row")).toHaveLength(2);
  await wrapper.get('input[maxlength="160"]').setValue("补充渠道");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[0]![1].body).entry).toMatchObject({
    total_assets: "200.30",
    channel_assets: imported.channel_assets,
  });
});

it("does not silently record without a default channel when the channel read fails", async () => {
  fetcher.mockResolvedValueOnce(response({ code: "storage_busy" }, 503));
  await start("create");
  await wrapper.get('input[inputmode="decimal"]').setValue("5.00");
  await wrapper.get("form").trigger("submit");
  expect(writes()).toHaveLength(0);
  await click("重新读取渠道");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(writes()).toHaveLength(1);
});

it("requires an asset amount when explicitly adding an asset update to a cash flow", async () => {
  await start("create");
  await wrapper.get('input[inputmode="decimal"]').setValue("5.00");
  await wrapper.get('input[type="checkbox"]').setValue(true);
  await wrapper.get("form").trigger("submit");
  expect(writes()).toHaveLength(0);
  expect(wrapper.text()).toContain("总资产请填写非负金额");
});

it("adds a first channel from the total-only editor without creating an extra cash flow", async () => {
  await start("create");
  await click("更新总资产");
  await wrapper.get('input[inputmode="decimal"]').setValue("123.45");
  await click("＋ 添加渠道");
  await wrapper.get('input[aria-label="渠道名称 1"]').setValue("新平台");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[0]![1].body).entry).toMatchObject({
    kind: "asset",
    flow: null,
    total_assets: "123.45",
    channel_assets: [{ name: "新平台", amount: "123.45" }],
  });
});

it("creates an outflow with a negative transport amount and preserves missing assets", async () => {
  await start("create");
  await click("转出");
  await wrapper.get('input[inputmode="decimal"]').setValue("25.50");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[0]![1].body)).toMatchObject({
    entry: { kind: "cash_flow", flow: "-25.50", total_assets: null },
  });
  expect(wrapper.emitted("close")).toHaveLength(1);
});
it.each(["0", "-1", "1.001"])(
  "rejects invalid cash flow %s without writing",
  async (value) => {
    await start("create");
    await wrapper.get('input[inputmode="decimal"]').setValue(value);
    await wrapper.get("form").trigger("submit");
    expect(wrapper.text()).toContain("转入、转出金额请填写大于零");
    expect(writes()).toHaveLength(0);
  },
);
it("protects drafts on cancel and does not lock the editor merely because a dialog is open", async () => {
  await start("create");
  expect(workspace.navigationLocked.value).toBe(true);
  expect(workspace.locked.value).toBe(false);
  await wrapper.get("textarea").setValue("Unsaved");
  await click("取消");
  expect(wrapper.text()).toContain("放弃尚未保存的修改");
  await click("继续编辑");
  expect((wrapper.get("textarea").element as HTMLTextAreaElement).value).toBe(
    "Unsaved",
  );
  expect(wrapper.emitted("close")).toBeUndefined();
});
it("requires a reason for edits and retains the version and exact large amounts", async () => {
  await start("edit");
  await wrapper.get('input[inputmode="decimal"]').setValue("90071992547409.01");
  await wrapper.get("form").trigger("submit");
  expect(writes()).toHaveLength(0);
  await wrapper.get('input[maxlength="160"]').setValue("Synthetic correction");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(JSON.parse(writes()[0]![1].body)).toMatchObject({
    expected_version: "1",
    entry: { flow: "90071992547409.01" },
    reason: "Synthetic correction",
  });
});
it("confirms a void in the same dialog with a reason and makes only one DELETE", async () => {
  await start();
  await click("作废记录");
  expect(wrapper.findAll("dialog")).toHaveLength(1);
  expect(writes()).toHaveLength(0);
  await wrapper.get('input[maxlength="160"]').setValue("Synthetic duplicate");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(writes()).toHaveLength(1);
  expect(writes()[0]![1].method).toBe("DELETE");
  expect(wrapper.emitted("close")).toHaveLength(1);
});
it("does not offer edits or voids for a voided record", async () => {
  fetcher.mockResolvedValueOnce(response({ ...base, voided: true }));
  await start();
  expect(wrapper.text()).toContain("已作废");
  expect(
    wrapper
      .findAll("button")
      .some((b) => ["编辑记录", "作废记录"].includes(b.text())),
  ).toBe(false);
});
it("retries a lost write using the same body and key while protecting the draft", async () => {
  await start("edit");
  await wrapper.get('input[maxlength="160"]').setValue("Synthetic correction");
  fetcher.mockRejectedValueOnce(new TypeError("Synthetic lost receipt"));
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(workspace.locked.value).toBe(true);
  await click("按原请求重试确认");
  expect(writes()).toHaveLength(2);
  expect(writes()[0]![1].body).toBe(writes()[1]![1].body);
  expect(writes()[0]![1].headers).toEqual(writes()[1]![1].headers);
  expect(wrapper.emitted("close")).toHaveLength(1);
});
it("offers a direct retry for a failed record read and failed revision read", async () => {
  fetcher.mockResolvedValueOnce(response({ code: "storage_busy" }, 503));
  await start();
  await click("重新读取记录");
  expect(wrapper.text()).toContain("Synthetic note");
  fetcher.mockResolvedValueOnce(response({ code: "storage_busy" }, 503));
  const details = wrapper.get("details");
  (details.element as HTMLDetailsElement).open = true;
  await details.trigger("toggle");
  await flushPromises();
  await click("重试读取修改历史");
  expect(wrapper.text()).toContain("Synthetic revision");
  expect(wrapper.text()).not.toContain("[storage_busy]");
});

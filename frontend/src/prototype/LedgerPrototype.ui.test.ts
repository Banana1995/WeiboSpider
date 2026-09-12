// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { defineComponent } from "vue";
import LedgerPrototype from "./LedgerPrototype.vue";

let wrapper: VueWrapper;
const fetchSpy = vi.fn(() => {
  throw new Error("Prototype must never use the network");
});
const appLoaded = vi.fn();
const ledgerLoaded = vi.fn();
vi.mock("../App.vue", () => ({
  default: defineComponent({
    setup() {
      appLoaded();
    },
    template: "<div data-live-app />",
  }),
}));
vi.mock("../Ledger.vue", () => ({
  default: defineComponent({
    setup() {
      ledgerLoaded();
    },
    template: "<div data-live-ledger />",
  }),
}));

beforeEach(() => {
  fetchSpy.mockClear();
  appLoaded.mockClear();
  ledgerLoaded.mockClear();
  vi.stubGlobal("fetch", fetchSpy);
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value() {
      this.open = true;
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value() {
      this.open = false;
    },
  });
});
afterEach(() => {
  expect(fetchSpy).not.toHaveBeenCalled();
  wrapper?.unmount();
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});
function start() {
  wrapper = mount(LedgerPrototype, { attachTo: document.body });
}
function button(text: string, scope = wrapper) {
  const result = scope.findAll("button").find((b) => b.text() === text);
  if (!result) throw new Error(`Missing button: ${text}`);
  return result;
}
const click = async (text: string) => {
  await button(text).trigger("click");
  await flushPromises();
};
const dialog = () => wrapper.get("dialog");
async function fillRecord(amount: string, assets = "", date = "2026-09-10") {
  await dialog().get('[name="date"]').setValue(date);
  await dialog().get('[name="amount"]').setValue(amount);
  await dialog().get('[name="assets"]').setValue(assets);
}
async function submit() {
  await dialog().get("form").trigger("submit");
  await flushPromises();
}

it.each(["/ledger-prototype", "/ledger-prototype/"])(
  "lazily mounts only the prototype on %s",
  async (path) => {
    window.history.replaceState({}, "", path);
    vi.resetModules();
    const Root = (await import("../Root.vue")).default;
    wrapper = mount(Root, { attachTo: document.body });
    await vi.waitFor(() =>
      expect(wrapper.find(".ledger-prototype").exists()).toBe(true),
    );
    expect(document.title).toBe("账本交互原型 · 观价");
    expect(wrapper.find(".root-nav").exists()).toBe(false);
    expect(appLoaded).not.toHaveBeenCalled();
    expect(ledgerLoaded).not.toHaveBeenCalled();
    expect(wrapper.get("#records-title").text()).toBe("账户记录");
  },
);

it("switches ranges, view and curve metric while keeping unified records visible", async () => {
  start();
  const initialProfit = wrapper.get('[data-testid="period-profit"]').text();
  await click("今年");
  expect(wrapper.get('[data-testid="period-profit"]').text()).not.toBe(
    initialProfit,
  );
  const initialRate = wrapper.get('[data-testid="period-rate"]').text();
  await click("基金经理视角");
  expect(wrapper.get('[data-testid="period-rate"]').text()).not.toBe(
    initialRate,
  );
  const path = wrapper.get(".lp-curve").attributes("d");
  await click("收益金额");
  expect(wrapper.get(".lp-curve").attributes("d")).not.toBe(path);
  await click("自定义");
  const dates = wrapper.findAll(".lp-date-range input");
  await dates[0]!.setValue("2026-03-10");
  await dates[1]!.setValue("2026-05-10");
  expect(wrapper.get('[data-testid="period-profit"]').text()).toBe("6,700.00");
  await dates[0]!.setValue("2026-06-10");
  expect(wrapper.get('[role="alert"]').text()).toContain("开始日期不能晚于");
  expect(wrapper.get("#records-title").text()).toBe("账户记录");
  await wrapper.get("#demo-account").setValue(2);
  expect(wrapper.get('[data-testid="period-profit"]').text()).toBe("-800.00");
  expect(wrapper.get('[data-testid="period-profit"]').classes()).toContain(
    "lp-negative",
  );
  await wrapper.get("#demo-account").setValue(4);
  expect(wrapper.get('[data-testid="latest-assets"]').text()).toBe("—");
  expect(wrapper.text()).toContain("还没有账户记录");
  expect(wrapper.find(".lp-pagination").exists()).toBe(false);
});

it("locates a real event row across pages, clearing only record filters", async () => {
  start();
  await wrapper.get('[aria-label="记录类型"]').setValue("note");
  await wrapper.get(".lp-event").trigger("click");
  await flushPromises();
  const row = wrapper.get(".lp-highlighted");
  expect(row.text()).toContain("2025-09-10");
  expect(row.text()).toContain("100,000.00");
  expect(document.activeElement).toBe(row.element);
  expect(wrapper.get('[aria-label="记录类型"]').element).toHaveProperty(
    "value",
    "all",
  );
  expect(wrapper.get(".lp-pagination").text()).toContain("13–15 / 15");
  expect(wrapper.get('[role="status"]').text()).toContain("已定位");
});

it("adds, edits and voids a flow, updating profits and retaining change history", async () => {
  start();
  await click("＋ 记一笔");
  await fillRecord("1000", "170984.00");
  await dialog().get('[name="note"]').setValue("本地体验");
  await submit();
  expect(dialog().attributes("open")).toBeUndefined();
  expect(wrapper.get('[data-testid="latest-assets"]').text()).toBe(
    "170,984.00",
  );
  expect(wrapper.get('[data-testid="period-profit"]').text()).toBe("32,984.00");
  const first = wrapper.get("tbody tr");
  expect(first.text()).toContain("1,000.00");
  expect(first.text()).toContain("170,984.00");
  await first.findAll("button")[0]!.trigger("click");
  await flushPromises();
  expect(dialog().get('[name="amount"]').element).toHaveProperty(
    "value",
    "1000.00",
  );
  await dialog().get('[name="amount"]').setValue("2000");
  await submit();
  expect(wrapper.get('[data-testid="period-profit"]').text()).toBe("31,984.00");
  await wrapper.get("tbody tr").findAll("button")[1]!.trigger("click");
  await flushPromises();
  expect(dialog().text()).toContain("修改后");
  await click("作废记录");
  await submit();
  expect(dialog().text()).toContain("请填写作废原因");
  await dialog().get('[name="reason"]').setValue("重复体验");
  await submit();
  expect(wrapper.get('[data-testid="period-profit"]').text()).toBe("32,840.00");
  expect(wrapper.get("tbody tr").text()).not.toContain("本地体验");
  await wrapper.get('.lp-filter-menu input[type="checkbox"]').setValue(true);
  expect(wrapper.get("tbody tr").text()).toContain("已作废");
  await wrapper.get("tbody tr button").trigger("click");
  await flushPromises();
  expect(dialog().text()).toContain("已作废：重复体验");
});

it("validates amounts, protects unsaved edits on close and Escape, and restores focus", async () => {
  start();
  const trigger = button("＋ 记一笔");
  (trigger.element as HTMLElement).focus();
  await click("＋ 记一笔");
  await fillRecord("0");
  await submit();
  expect(dialog().text()).toContain("请输入大于 0");
  await fillRecord("10.001");
  await submit();
  expect(dialog().text()).toContain("最多两位小数");
  await fillRecord("100");
  await dialog().trigger("cancel");
  expect(dialog().text()).toContain("放弃尚未保存的修改");
  await click("继续编辑");
  expect(dialog().get('[name="amount"]').element).toHaveProperty(
    "value",
    "100",
  );
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true);
  await click("取消");
  await click("放弃修改");
  expect(document.activeElement).toBe(trigger.element);
  expect(wrapper.findAll("tbody tr")).toHaveLength(6);
  await click("＋ 记一笔");
  await click("更新总资产");
  await dialog().get('[name="assets"]').setValue("0");
  await submit();
  expect(wrapper.get('[data-testid="latest-assets"]').text()).toBe("0.00");
});

it("keeps notes and missing assets blank, differentiating empty results from empty accounts", async () => {
  start();
  await wrapper.get('[aria-label="记录类型"]').setValue("note");
  await wrapper.get("tbody tr button").trigger("click");
  await flushPromises();
  await dialog().get('[name="note"]').setValue("");
  await submit();
  expect(dialog().text()).toContain("请填写备注内容");
  await dialog().get('[name="note"]').setValue("仅备注不改金额");
  await submit();
  await wrapper.get('[aria-label="记录类型"]').setValue("note");
  expect(wrapper.get("tbody tr").text()).toContain("仅备注不改金额");
  expect(wrapper.get("tbody tr .lp-record-assets").text()).toBe("—");
  expect(wrapper.get('[data-testid="latest-assets"]').text()).toBe(
    "169,840.00",
  );
  await wrapper.get("#demo-account").setValue(3);
  expect(wrapper.get(".lp-reference").text()).toContain("未记录总资产");
  await click("基金经理视角");
  expect(wrapper.get('[data-testid="period-rate"]').text()).toBe("—");
  await wrapper.get('[aria-label="记录类型"]').setValue("out");
  expect(wrapper.text()).toContain("没有符合筛选条件的记录");
  await wrapper.get("#demo-account").setValue(1);
  expect(wrapper.get('[aria-label="记录类型"]').element).toHaveProperty(
    "value",
    "all",
  );
  await wrapper.get('[aria-label="记录类型"]').setValue("note");
  expect(wrapper.get("tbody tr").text()).toContain("仅备注不改金额");
});

it("edits account information without silently relabelling populated currencies", async () => {
  start();
  await click("管理账户");
  await click("编辑资料");
  expect(
    dialog().get('[name="currency"]').attributes("disabled"),
  ).toBeDefined();
  await dialog().get('[name="accountName"]').setValue("长期安排（示例）");
  await submit();
  expect(wrapper.text()).toContain("长期安排（示例）");
  await click("新建账户");
  await dialog().get('[name="accountName"]').setValue("独立美元计划");
  await dialog().get('[name="currency"]').setValue("USD");
  await submit();
  expect(wrapper.text()).toContain("还没有账户记录");
  expect(wrapper.text()).toContain("USD");
  await wrapper.get("#demo-account").setValue(1);
  expect(wrapper.get('[data-testid="latest-assets"]').text()).toBe(
    "169,840.00",
  );
});

it("previews and saves editable holdings without generating asset or flow records", async () => {
  start();
  await click("管理账户");
  await click("当前持仓");
  await click("编辑持仓");
  await dialog().get('[name="cash"]').setValue("1000");
  await dialog().get('[aria-label="第 1 笔数量"]').setValue("100");
  await click("添加持仓");
  await dialog().get('[aria-label="第 2 笔证券"]').setValue(2);
  await dialog().get('[aria-label="第 2 笔数量"]').setValue("10");
  await submit();
  expect(dialog().text()).toContain("确认持仓快照");
  expect(dialog().text()).toContain("3,270.00");
  await submit();
  expect(wrapper.get(".lp-holding-summary").text()).toContain("3,270.00");
  await click("编辑持仓");
  await dialog().get('[aria-label="移除第 2 笔持仓"]').trigger("click");
  await click("找不到证券？添加示例证券");
  await dialog().get('[name="securityName"]').setValue("成长组合（示例）");
  await dialog().get('[name="securityCode"]').setValue("DEMO03");
  await dialog().get('[name="securityPrice"]').setValue("20.00");
  await click("添加证券");
  await submit();
  await submit();
  expect(wrapper.get(".lp-holdings-list").text()).toContain("成长组合（示例）");
  await click("返回账户");
  expect(wrapper.get('[data-testid="latest-assets"]').text()).toBe(
    "169,840.00",
  );
  expect(wrapper.get(".lp-title-count").text()).toContain("15 笔");
  await wrapper.get("#demo-account").setValue(2);
  await click("管理账户");
  await click("当前持仓");
  expect(wrapper.get(".lp-holdings-list").text()).not.toContain("成长组合");
});

it("imports exactly the built-in preview to a new account without reading files", async () => {
  start();
  await click("管理账户");
  await click("导入示例账本");
  expect(dialog().text()).toContain("不读取 / 上传你的文件");
  await click("使用示例文件");
  const preview = dialog().findAll(".lp-import-preview li");
  expect(preview).toHaveLength(3);
  expect(preview[2]!.text()).toContain("26,800.00");
  await dialog().get('[name="importName"]').setValue("导入体验（示例）");
  await submit();
  expect(wrapper.get(".lp-title-count").text()).toContain("3 笔");
  expect(wrapper.get('[data-testid="latest-assets"]').text()).toBe("26,800.00");
  expect(wrapper.get('[data-testid="period-profit"]').text()).toBe("1,800.00");
  await wrapper.get("#demo-account").setValue(1);
  expect(wrapper.get(".lp-title-count").text()).toContain("15 笔");
});

it("treats file selection as metadata only and allows recovery to the built-in example", async () => {
  start();
  await click("管理账户");
  await click("导入示例账本");
  const fileInput = dialog().get('input[type="file"]');
  const file = new File(["must never be parsed"], "local.xlsx");
  const read = vi.fn(() => {
    throw new Error("Do not read personal files");
  });
  Object.defineProperty(file, "text", { value: read });
  Object.defineProperty(file, "arrayBuffer", { value: read });
  const reader = vi.fn(() => {
    throw new Error("Do not instantiate FileReader");
  });
  vi.stubGlobal("FileReader", reader);
  Object.defineProperty(fileInput.element, "files", {
    configurable: true,
    value: [new File([], "invalid.csv")],
  });
  await fileInput.trigger("change");
  expect(dialog().text()).toContain("请选择 .xlsx 文件");
  await click("使用示例文件");
  expect(dialog().find('[role="alert"]').exists()).toBe(false);
  await click("上一步");
  const nextInput = dialog().get('input[type="file"]');
  Object.defineProperty(nextInput.element, "files", {
    configurable: true,
    value: [file],
  });
  await nextInput.trigger("change");
  expect(dialog().findAll(".lp-import-preview li")).toHaveLength(3);
  expect(read).not.toHaveBeenCalled();
  expect(reader).not.toHaveBeenCalled();
});

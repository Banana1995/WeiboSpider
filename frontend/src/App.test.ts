// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import App from "./App.vue";
import { request, type History } from "./market";

vi.mock("./market", async (original) => ({
  ...(await original<object>()),
  request: vi.fn(),
}));
vi.mock("./TrendChart.vue", () => ({
  __esModule: true,
  default: {
    props: ["name"],
    template: '<div class="test-chart">{{ name }}</div>',
  },
}));
const quote = {
  price_date: "2026-09-05",
  price_cents: 10000,
  change_cents: -200,
  fetched_at: "",
};
const products = [1, 2].map((id) => ({
  ...quote,
  id,
  name: `酒品${id}`,
  specifications: "53/500ml",
  unit: "元/瓶",
  sort: id,
}));
const history = (id: number): History => ({
  source: "sina_jiujia",
  price_basis: "terminal_retail_weighted_mean",
  product: products[id - 1]!,
  items: [quote],
});
let wrapper: VueWrapper;
beforeEach(() => {
  vi.mocked(request).mockReset();
  vi.mocked(request).mockImplementation(async (path) => {
    if (path === "latest")
      return { items: products, price_date: quote.price_date };
    if (path === "sync") return { state: "succeeded" };
    return history(path.includes("/2/") ? 2 : 1);
  });
});
afterEach(() => {
  wrapper?.unmount();
  vi.useRealTimers();
});

it("updates the stale quotation marker after Shanghai midnight on refresh and resume", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-09-05T15:59:00Z"));
  wrapper = mount(App);
  await flushPromises();
  expect(wrapper.find(".stale").exists()).toBe(false);
  vi.setSystemTime(new Date("2026-09-05T16:01:00Z"));
  await wrapper.get(".refresh").trigger("click");
  await flushPromises();
  expect(wrapper.find(".stale").exists()).toBe(true);
  vi.setSystemTime(new Date("2026-09-05T15:59:00Z"));
  document.dispatchEvent(new Event("visibilitychange"));
  await flushPromises();
  expect(wrapper.find(".stale").exists()).toBe(false);
});

it("loads real API paths, filters products and changes inclusive history windows", async () => {
  wrapper = mount(App);
  await flushPromises();
  expect(wrapper.text()).toContain("100.00");
  expect(
    vi
      .mocked(request)
      .mock.calls.some(
        ([path]) =>
          path === "products/1/history?from=2026-08-07&to=2026-09-05&limit=366",
      ),
  ).toBe(true);
  await wrapper.get("input").setValue("酒品2");
  expect(wrapper.findAll(".product-row")).toHaveLength(1);
  await wrapper.get(".product-row").trigger("click");
  await flushPromises();
  expect(wrapper.get(".quote-heading h2").text()).toBe("酒品2");
  await wrapper.findAll(".ranges button")[0]!.trigger("click");
  await flushPromises();
  expect(vi.mocked(request).mock.calls.at(-1)![0]).toBe(
    "products/2/history?from=2026-08-30&to=2026-09-05&limit=366",
  );
});

it("ignores an obsolete history response even if transport ignores abort", async () => {
  let resolveOld!: (value: History) => void;
  const normal = vi.mocked(request).getMockImplementation()!;
  vi.mocked(request).mockImplementation((path, signal) =>
    path.startsWith("products/1/")
      ? new Promise((resolve) => {
          resolveOld = resolve;
        })
      : normal(path, signal),
  );
  wrapper = mount(App);
  await flushPromises();
  const oldSignal = vi
    .mocked(request)
    .mock.calls.find(([path]) => path.startsWith("products/1/"))![1]!;
  await wrapper.findAll(".product-row")[1]!.trigger("click");
  await flushPromises();
  expect(oldSignal.aborted).toBe(true);
  resolveOld({
    ...history(1),
    items: [{ ...quote, price_date: "2020-01-01" }],
  });
  await flushPromises();
  expect(wrapper.text()).not.toContain("2020-01-01");
  expect(wrapper.get(".quote-heading h2").text()).toBe("酒品2");
});

it("distinguishes an empty database from a failed history request", async () => {
  vi.mocked(request).mockResolvedValue({
    items: [],
    price_date: "",
    state: "idle",
  });
  wrapper = mount(App);
  await flushPromises();
  expect(wrapper.text()).toContain("还没有行情数据");
  expect(wrapper.find(".detail").exists()).toBe(false);
});

it("retains latest quotes on history failure without drawing false zeroes", async () => {
  const normal = vi.mocked(request).getMockImplementation()!;
  vi.mocked(request).mockImplementation((path, signal) =>
    path.startsWith("products/")
      ? Promise.reject(new Error("测试历史服务失败"))
      : normal(path, signal),
  );
  wrapper = mount(App);
  await flushPromises();
  expect(wrapper.get('[role="alert"]').text()).toContain("测试历史服务失败");
  expect(wrapper.text()).toContain("100.00");
  expect(wrapper.find(".test-chart").exists()).toBe(false);
});

it("refreshes stored data without a sync POST, and retains last-good quotes on refresh failure", async () => {
  wrapper = mount(App);
  await flushPromises();
  vi.mocked(request).mockRejectedValue(new Error("测试连接失败"));
  await wrapper.get(".refresh").trigger("click");
  await flushPromises();
  expect(wrapper.text()).toContain("当前保留上次读取的数据");
  expect(wrapper.findAll(".product-row")).toHaveLength(2);
});

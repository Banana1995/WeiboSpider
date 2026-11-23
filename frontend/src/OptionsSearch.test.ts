// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Options from "./Options.vue";
import {
  emptyFilter,
  optionsRequest,
  type Settings,
  type Status,
  type Watch,
} from "./options";

vi.mock("./options", async (original) => ({
  ...(await original<object>()),
  optionsRequest: vi.fn(),
}));

const watch: Watch = {
  code: "US.AAPL",
  name: "苹果",
  english_name: "",
  aliases: "",
  kind: "STOCK",
  currency: "USD",
  enabled: true,
  version: 1,
  filter: emptyFilter(),
  call_filter: emptyFilter(),
};
const settings: Settings = {
  version: 1,
  mode: "full",
  fees: null,
  filter: emptyFilter(),
  call_filter: emptyFilter(),
  horizon_days: 365,
};
const status: Status = {
  provider: {
    connected: true,
    version: "",
    market_state: "",
    us_options: "verified",
    checked_at: "",
  },
  run: null,
  favorite_count: 0,
  manual_only: true,
};
const emptyPage = {
  items: [],
  total: 0,
  available: 0,
  excluded: {},
  snapshots: [],
  view: "",
};

let wrapper: VueWrapper;
beforeEach(() => {
  vi.mocked(optionsRequest).mockReset();
  HTMLDialogElement.prototype.showModal = vi.fn();
  HTMLDialogElement.prototype.close = vi.fn();
});
afterEach(() => {
  wrapper?.unmount();
});

async function open(search: () => Promise<unknown>) {
  vi.mocked(optionsRequest).mockImplementation(async (path: string) => {
    if (path.startsWith("/watchlist")) return { items: [watch] };
    if (path === "/settings") return settings;
    if (path === "/status") return status;
    if (path.startsWith("/candidates")) return emptyPage;
    if (path.startsWith("/instruments/search")) return search();
    throw new Error(`unexpected request ${path}`);
  });
  wrapper = mount(Options);
  await flushPromises();
  await wrapper.get(".opt-add").trigger("click");
  await flushPromises();
}

it("reports an unavailable search instead of claiming there are no matches", async () => {
  await open(async () => {
    throw new Error("行情请求队列繁忙");
  });
  expect(document.body.textContent).toContain("搜索暂不可用，请稍后重试");
  expect(document.body.textContent).not.toContain("没有找到匹配股票");
});

it("keeps the no-match hint after a successful empty search", async () => {
  await open(async () => ({ items: [], limit: 40 }));
  expect(document.body.textContent).toContain("0 个结果");
  expect(document.body.textContent).toContain("没有找到匹配股票");
});

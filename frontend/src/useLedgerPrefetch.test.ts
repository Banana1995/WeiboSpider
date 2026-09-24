// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { defineComponent, ref } from "vue";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { useLedgerPrefetch } from "./useLedgerPrefetch";
import { prefetchAccount } from "./ledgerCachedRequests";
import { LedgerReadCache } from "./ledgerReadCache";
import type { Account } from "./ledger";

vi.mock("./ledgerCachedRequests", () => ({ prefetchAccount: vi.fn() }));
let wrapper: VueWrapper;
const state = ref({
  accounts: [] as Account[],
  selected: "a",
  ready: "",
  paused: false,
  epoch: 0,
});
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  vi.stubGlobal("requestIdleCallback", undefined);
  vi.mocked(prefetchAccount).mockReset().mockResolvedValue();
  state.value = {
    accounts: ["a", "b", "c", "d"].map(
      (id) => ({ id, currency: "CNY" }) as Account,
    ),
    selected: "a",
    ready: "",
    paused: false,
    epoch: 0,
  };
  wrapper = mount(
    defineComponent({
      setup() {
        useLedgerPrefetch(
          () => ({ ...state.value }),
          new LedgerReadCache(),
          new LedgerReadCache(),
        );
        return () => null;
      },
    }),
  );
});
afterEach(() => {
  wrapper.unmount();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it("waits for foreground completion, idles, then prefetches only two accounts sequentially", async () => {
  await vi.advanceTimersByTimeAsync(3000);
  expect(prefetchAccount).not.toHaveBeenCalled();
  let finish!: () => void;
  vi.mocked(prefetchAccount).mockImplementationOnce(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
  );
  state.value.ready = "a";
  await vi.advanceTimersByTimeAsync(1499);
  expect(prefetchAccount).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(1);
  expect(prefetchAccount).toHaveBeenCalledTimes(1);
  expect(vi.mocked(prefetchAccount).mock.calls[0]![0].id).toBe("b");
  finish();
  await flushPromises();
  expect(
    vi.mocked(prefetchAccount).mock.calls.map(([account]) => account.id),
  ).toEqual(["b", "c"]);
});

it("cancels speculative work on navigation, writes, hiding and unmount", async () => {
  vi.mocked(prefetchAccount).mockImplementation(
    (_a, _b, _c, signal) =>
      new Promise<void>((resolve) => {
        signal.addEventListener("abort", () => resolve(), { once: true });
      }),
  );
  state.value.ready = "a";
  await vi.advanceTimersByTimeAsync(1500);
  const signal = vi.mocked(prefetchAccount).mock.calls[0]![3];
  state.value.selected = "b";
  expect(signal.aborted).toBe(true);
  await vi.advanceTimersByTimeAsync(2000);
  expect(prefetchAccount).toHaveBeenCalledTimes(1);
  state.value.ready = "b";
  state.value.paused = true;
  await vi.advanceTimersByTimeAsync(2000);
  expect(prefetchAccount).toHaveBeenCalledTimes(1);
  state.value.paused = false;
  const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
  document.dispatchEvent(new Event("visibilitychange"));
  await vi.advanceTimersByTimeAsync(2000);
  expect(prefetchAccount).toHaveBeenCalledTimes(1);
  hidden.mockReturnValue(false);
  document.dispatchEvent(new Event("visibilitychange"));
  await vi.advanceTimersByTimeAsync(1500);
  expect(prefetchAccount).toHaveBeenCalledTimes(2);
  const last = vi.mocked(prefetchAccount).mock.calls[1]![3];
  wrapper.unmount();
  expect(last.aborted).toBe(true);
});

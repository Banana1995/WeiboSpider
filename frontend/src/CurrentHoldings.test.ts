// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Panel from "./CurrentHoldings.vue";
import {
  validateCurrentHoldings,
  type CurrentHoldings,
} from "./currentHoldings";

const empty: CurrentHoldings = {
  account_id: "a",
  audit_id: "",
  snapshot: null,
};
const saved: CurrentHoldings = {
  account_id: "a",
  audit_id: "3",
  snapshot: {
    version: "1",
    saved_at: "2026-09-08T00:00:00Z",
    cash: "0.00",
    positions: [],
  },
};
const response = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), { status });
let wrapper: VueWrapper;
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
async function start() {
  wrapper = mount(Panel, {
    props: {
      accountId: "a",
      currency: "CNY",
      instruments: [
        {
          id: "i",
          name: "Synthetic",
          market: "SH",
          code: "600000",
          currency: "CNY",
        },
      ],
      disabled: false,
      refreshKey: 0,
    },
  });
  await flushPromises();
}
async function submit() {
  await wrapper.get("form").trigger("submit");
  await flushPromises();
}
it("distinguishes absence from configured zero and rejects invalid read contracts", () => {
  expect(validateCurrentHoldings(empty, "a").snapshot).toBeNull();
  expect(validateCurrentHoldings(saved, "a").snapshot?.cash).toBe("0.00");
  for (const v of [
    { ...empty, account_id: "b" },
    { ...empty, audit_id: "1" },
    { ...saved, snapshot: { ...saved.snapshot, cash: 0 } },
    {
      ...saved,
      snapshot: {
        ...saved.snapshot,
        positions: [{ instrument_id: "i", quantity: "0" }],
      },
    },
    {
      ...saved,
      snapshot: {
        ...saved.snapshot,
        positions: [
          { instrument_id: "i", quantity: "1" },
          { instrument_id: "i", quantity: "2" },
        ],
      },
    },
  ])
    expect(() => validateCurrentHoldings(v as CurrentHoldings, "a")).toThrow();
});
it("loads only GET, adds/removes exact quantities, saves complete snapshots without quotes", async () => {
  let current = empty;
  const fetcher = vi.fn(async (url: string, init: RequestInit = {}) => {
    expect(url).toMatch(/\/current-holdings$/);
    if (init.method === "PUT") {
      const input = JSON.parse(init.body as string);
      current = {
        ...saved,
        snapshot: {
          ...saved.snapshot!,
          version: String(BigInt(input.expected_version) + 1n),
          cash: input.cash,
          positions: input.positions,
        },
      };
    }
    return response(current);
  });
  vi.stubGlobal("fetch", fetcher);
  await start();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(wrapper.emitted("configured")?.at(-1)).toEqual([false, ""]);
  await wrapper.get('[data-test="current-add"]').trigger("click");
  await wrapper.get('[name="current_instrument_0"]').setValue("i");
  await wrapper
    .get('[name="current_quantity_0"]')
    .setValue("9007199254.740993");
  await wrapper.get('[name="current_cash"]').setValue("90071992547409.01");
  await submit();
  expect(
    JSON.parse(
      fetcher.mock.calls.find(([, init]) => init?.method === "PUT")![1]!
        .body as string,
    ),
  ).toEqual({
    expected_version: "0",
    cash: "90071992547409.01",
    positions: [{ instrument_id: "i", quantity: "9007199254.740993" }],
  });
  expect(wrapper.emitted("configured")?.at(-1)).toEqual([true, "3"]);
  const source = wrapper.get('[data-test="current-holdings-source"]');
  expect((source.element as HTMLDetailsElement).open).toBe(false);
  expect(source.get("summary").text()).toBe("查看保存版本与审计信息");
  (source.element as HTMLDetailsElement).open = true;
  await source.trigger("toggle");
  expect(source.text()).toContain("版本 1 · 审计 #3");
  await wrapper
    .findAll("button")
    .find((b) => b.text() === "移除此证券")!
    .trigger("click");
  await wrapper.get('[name="current_cash"]').setValue("0.00");
  await submit();
  expect(current.snapshot).toMatchObject({
    version: "2",
    cash: "0.00",
    positions: [],
  });
  expect(wrapper.emitted("saved")).toHaveLength(2);
});
it("rejects duplicate securities before PUT", async () => {
  const fetcher = vi.fn(async () => response(empty));
  vi.stubGlobal("fetch", fetcher);
  await start();
  for (let n = 0; n < 2; n++) {
    await wrapper.get('[data-test="current-add"]').trigger("click");
    await wrapper.get(`[name="current_instrument_${n}"]`).setValue("i");
    await wrapper.get(`[name="current_quantity_${n}"]`).setValue("1");
  }
  await submit();
  expect(wrapper.get('[role="alert"]').text()).toContain("证券不得重复");
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it("locks the original request and key through uncertain retry, then reads latest state", async () => {
  let attempts = 0;
  const fetcher = vi.fn(async (_url: string, init: RequestInit = {}) => {
    if (init.method === "PUT") {
      if (++attempts === 1) throw new TypeError("synthetic lost response");
      return response(saved);
    }
    return response(
      attempts
        ? {
            ...saved,
            snapshot: { ...saved.snapshot!, version: "2", cash: "9.00" },
          }
        : empty,
    );
  });
  vi.stubGlobal("fetch", fetcher);
  await start();
  await submit();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  expect(wrapper.get("fieldset").attributes("disabled")).toBeDefined();
  await wrapper.setProps({ disabled: true, refreshKey: 1 });
  await flushPromises();
  expect(fetcher).toHaveBeenCalledTimes(2);
  await wrapper.get('[data-test="current-retry"]').trigger("click");
  await flushPromises();
  const writes = fetcher.mock.calls.filter(
    ([, init]) => init?.method === "PUT",
  );
  expect(writes[0]![1]!.body).toBe(writes[1]![1]!.body);
  expect(new Headers(writes[0]![1]!.headers).get("Idempotency-Key")).toBe(
    new Headers(writes[1]![1]!.headers).get("Idempotency-Key"),
  );
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([false]);
  expect(
    (wrapper.get('[name="current_cash"]').element as HTMLInputElement).value,
  ).toBe("9.00");
});
it("shows CAS conflict, retains draft and unlocks for explicit reload", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init: RequestInit = {}) =>
      init.method === "PUT"
        ? response({ code: "version_conflict" }, 409)
        : response(empty),
    ),
  );
  await start();
  await wrapper.get('[name="current_cash"]').setValue("7.00");
  await submit();
  expect(wrapper.get('[role="alert"]').text()).toContain("记录已被修改");
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([false]);
  expect(
    (wrapper.get('[name="current_cash"]').element as HTMLInputElement).value,
  ).toBe("7.00");
});
it("keeps a malformed success receipt uncertain instead of allowing another write", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init: RequestInit = {}) =>
      response(
        init.method === "PUT" ? { ...saved, account_id: "wrong" } : empty,
      ),
    ),
  );
  await start();
  await submit();
  expect(wrapper.emitted("saved")).toBeUndefined();
  expect(wrapper.emitted("locked")?.at(-1)).toEqual([true]);
  expect(wrapper.find('[data-test="current-retry"]').exists()).toBe(true);
});
it("aborts old-account reads and ignores late results", async () => {
  let finish!: (r: Response) => void;
  let signal: AbortSignal | null | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init: RequestInit = {}) =>
      url.includes("/a/")
        ? new Promise<Response>((resolve) => {
            finish = resolve;
            signal = init.signal;
          })
        : Promise.resolve(response({ ...empty, account_id: "b" })),
    ),
  );
  await start();
  await wrapper.setProps({ accountId: "b" });
  await flushPromises();
  expect(signal?.aborted).toBe(true);
  finish(
    response({ ...saved, snapshot: { ...saved.snapshot!, cash: "999.00" } }),
  );
  await flushPromises();
  expect(
    (wrapper.get('[name="current_cash"]').element as HTMLInputElement).value,
  ).toBe("0.00");
  expect(wrapper.text()).not.toContain("999.00");
});

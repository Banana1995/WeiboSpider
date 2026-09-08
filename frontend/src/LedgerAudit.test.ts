// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import LedgerAudit from "./LedgerAudit.vue";

const entry = {
  id: "9007199254740993",
  account_id: "a",
  entity_type: "account_record",
  entity_id: "manual-a",
  action: "replace",
  version: "2",
  recorded_at: "2026-09-07T00:00:00Z",
  source: "human",
};
const response = (data: unknown) =>
  new Response(JSON.stringify(data), {
    headers: { "Content-Type": "application/json" },
  });
afterEach(() => vi.unstubAllGlobals());

it("reads only on demand, filters and paginates 30, and renders frozen JSON as escaped exact text", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue(response({ items: [entry], next_cursor: entry.id }));
  vi.stubGlobal("fetch", fetcher);
  const w = mount(LedgerAudit, { props: { accountId: "a" } });
  expect(fetcher).not.toHaveBeenCalled();
  await w.get('input[name="audit_action"]').setValue("replace");
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(fetcher.mock.calls[0][0]).toContain("account_id=a");
  expect(fetcher.mock.calls[0][0]).toContain("limit=30");
  expect(fetcher.mock.calls[0][0]).toContain("action=replace");
  const raw =
    '{"version":9007199254740993,"note":"<img src=x onerror=alert(1)>"}';
  fetcher.mockResolvedValue(
    response({
      ...entry,
      before_json: "{}",
      after_json: raw,
      metadata_json: "{}",
    }),
  );
  await w.get("li button").trigger("click");
  await flushPromises();
  expect(w.text()).toContain(raw);
  expect(w.find("img").exists()).toBe(false);
  fetcher.mockResolvedValue(response({ items: [] }));
  await w
    .findAll("button")
    .find((b) => b.text() === "下一页审计")!
    .trigger("click");
  await flushPromises();
  expect(fetcher.mock.calls[2][0]).toContain(`cursor=${entry.id}`);
  expect(w.text()).not.toContain(raw);
  expect(
    fetcher.mock.calls.every(
      ([, opts]) => !opts.method || opts.method === "GET",
    ),
  ).toBe(true);
  w.unmount();
});

it("clears context on account switch and rejects late or cross-account detail", async () => {
  let finish!: (r: Response) => void;
  const fetcher = vi.fn().mockResolvedValue(response({ items: [entry] }));
  vi.stubGlobal("fetch", fetcher);
  const w = mount(LedgerAudit, { props: { accountId: "a" } });
  await w.get("form").trigger("submit");
  await flushPromises();
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  await w.get("li button").trigger("click");
  await w.setProps({ accountId: "b" });
  finish(response({ ...entry, after_json: "old-context" }));
  await flushPromises();
  expect(w.text()).not.toContain("old-context");
  fetcher.mockResolvedValue(response({ items: [entry] }));
  await w.get("form").trigger("submit");
  await flushPromises();
  expect(w.find('[role="alert"]').exists()).toBe(true);
  expect(w.find("li").exists()).toBe(false);
  w.unmount();
});

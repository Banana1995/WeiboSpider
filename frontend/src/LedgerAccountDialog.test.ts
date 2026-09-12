// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import Dialog from "./LedgerAccountDialog.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";

let wrapper: VueWrapper;
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
});
it("creates one account kind without opening holdings or security registration", async () => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  const fetcher = vi.fn(
    async (_url: string, init: RequestInit) =>
      new Response(
        JSON.stringify({ ...JSON.parse(init.body as string), version: "1" }),
      ),
  );
  vi.stubGlobal("fetch", fetcher);
  wrapper = mount(Dialog, {
    props: { instruments: [] },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  expect(wrapper.text()).not.toContain("记录方式");
  expect(wrapper.text()).not.toContain("登记证券");
  await wrapper.get('input[maxlength="160"]').setValue("Synthetic");
  await wrapper.get("form").trigger("submit");
  await flushPromises();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]![0]).toBe("/api/platform/ledger/accounts");
  const body = JSON.parse(fetcher.mock.calls[0]![1].body as string);
  expect(body).toMatchObject({ name: "Synthetic", currency: "CNY" });
  expect(body).not.toHaveProperty("positions");
  expect(body).not.toHaveProperty("accounting_mode");
  expect(
    new Headers(fetcher.mock.calls[0]![1].headers).get("Idempotency-Key"),
  ).toBeTruthy();
  expect(wrapper.emitted("created")?.[0]).toEqual([body.id]);
});

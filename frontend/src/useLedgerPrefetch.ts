import { onBeforeUnmount, watch } from "vue";
import type { Account } from "./ledger";
import { prefetchAccount } from "./ledgerCachedRequests";
import type { LedgerReadCache } from "./ledgerReadCache";

export function useLedgerPrefetch(
  state: () => {
    accounts: Account[];
    selected: string;
    ready: string;
    paused: boolean;
    epoch: number;
  },
  accounts: LedgerReadCache,
  benchmarks: LedgerReadCache,
) {
  let cancel = () => {};
  function schedule() {
    cancel();
    const current = state();
    if (current.paused || current.ready !== current.selected || document.hidden)
      return;
    const index = current.accounts.findIndex(
      (account) => account.id === current.selected,
    );
    if (index < 0) return;
    // At most two adjacent accounts per idle round. Large workspaces do not
    // turn initial load into an unbounded burst of financial computations.
    const candidates = [
      ...current.accounts.slice(index + 1),
      ...current.accounts.slice(0, index),
    ].slice(0, 2);
    const controller = new AbortController();
    let idle: number | undefined;
    const run = async () => {
      for (const account of candidates) {
        if (controller.signal.aborted || document.hidden) break;
        try {
          await prefetchAccount(
            account,
            accounts,
            benchmarks,
            controller.signal,
          );
        } catch {
          // Never spin/retry or surface speculative errors in the active view.
          if (controller.signal.aborted) break;
        }
      }
    };
    const timer = setTimeout(() => {
      if (typeof window.requestIdleCallback === "function")
        idle = window.requestIdleCallback(() => void run());
      else void run();
    }, 1500);
    cancel = () => {
      clearTimeout(timer);
      if (idle !== undefined) window.cancelIdleCallback(idle);
      controller.abort();
    };
  }
  watch(state, schedule, { flush: "sync" });
  document.addEventListener("visibilitychange", schedule);
  onBeforeUnmount(() => {
    cancel();
    document.removeEventListener("visibilitychange", schedule);
  });
}

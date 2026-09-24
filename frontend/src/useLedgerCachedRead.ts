import { LedgerError } from "./ledger";
import type { LedgerReadCache } from "./ledgerReadCache";
import { useLedgerRead } from "./useLedgerRead";

export interface CachedLedgerRequest<T> {
  key: string;
  read: (signal: AbortSignal) => Promise<T>;
}

export function useLedgerCachedRead<T>(cache: LedgerReadCache) {
  const state = useLedgerRead<T>();
  function load(request: CachedLedgerRequest<T>, revalidate = true) {
    state.clear();
    state.data.value = cache.peek<T>(request.key);
    return state.load(async (signal) => {
      try {
        return await cache.fetch(request.key, request.read, signal, revalidate);
      } catch (error) {
        // Fail closed on deletion, identity/validation errors and rejected reads.
        // Transient failures can retain a clearly labelled last-known snapshot.
        if (
          !signal.aborted &&
          error instanceof LedgerError &&
          (!error.uncertain || error.code === "invalid_response")
        )
          state.data.value = undefined;
        throw error;
      }
    });
  }
  return { ...state, load };
}

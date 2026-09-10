import { computed, inject, ref, shallowRef, type InjectionKey } from "vue";
import { failure, LedgerError, type PendingWrite } from "./ledger";

export function createLedgerWorkspace(changed: () => void) {
  const pending = shallowRef<PendingWrite<unknown>>();
  const externalLock = ref(false);
  const busy = ref(false);
  const error = ref("");
  const notice = ref("");
  const label = ref("");
  const locked = computed(() => !!pending.value || externalLock.value);
  let validate: ((result: unknown) => boolean) | undefined;
  let completed: (() => void) | undefined;
  async function retry() {
    if (!pending.value || busy.value) return;
    const write = pending.value;
    busy.value = true;
    error.value = "";
    try {
      const result = await write.run();
      if (!validate?.(result)) {
        write.uncertain = true;
        throw new LedgerError("invalid_response");
      }
    } catch (e) {
      error.value = failure(e);
      if (!write.uncertain) pending.value = undefined;
      return;
    } finally {
      busy.value = false;
    }
    pending.value = undefined;
    notice.value = `${label.value}已确认成功。正在读取最新数据，读取失败不影响保存，请勿重复录入。`;
    // A replayed receipt is not current financial state. Only fresh GETs update it.
    const done = completed;
    completed = undefined;
    validate = undefined;
    done?.();
    changed();
  }
  function send<T>(write: PendingWrite<T>, operation: string, check: (result: T) => boolean, done?: () => void) {
    if (locked.value) return;
    pending.value = write;
    label.value = operation;
    notice.value = "";
    validate = (result) => {
      try { return check(result as T); } catch { return false; }
    };
    completed = done;
    void retry();
  }
  return { pending, busy, error, notice, label, locked, externalLock, send, retry };
}
export const ledgerWorkspaceKey: InjectionKey<ReturnType<typeof createLedgerWorkspace>> = Symbol("ledger-workspace");
export function useLedgerWorkspace() {
  const workspace = inject(ledgerWorkspaceKey);
  if (!workspace) throw new Error("账本写入上下文缺失");
  return workspace;
}

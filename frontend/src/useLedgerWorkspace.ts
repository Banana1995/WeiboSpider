import { computed, inject, ref, shallowRef, type InjectionKey } from "vue";
import { failure, LedgerError, type PendingWrite } from "./ledger";

export function createLedgerWorkspace(changed: () => void) {
  const pending = shallowRef<PendingWrite<unknown>>();
  const externalLock = ref(false);
  const draftLock = ref(false);
  const dialogs = ref(0);
  const busy = ref(false);
  const error = ref("");
  const notice = ref("");
  const label = ref("");
  const locked = computed(() => !!pending.value || externalLock.value);
  const navigationLocked = computed(
    () => locked.value || draftLock.value || dialogs.value > 0,
  );
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
    notice.value = `${label.value}成功。`;
    // A replayed receipt is not current financial state. Only fresh GETs update it.
    const done = completed;
    completed = undefined;
    validate = undefined;
    done?.();
    changed();
  }
  function send<T>(
    write: PendingWrite<T>,
    operation: string,
    check: (result: T) => boolean,
    done?: () => void,
  ) {
    if (locked.value) return;
    pending.value = write;
    label.value = operation;
    notice.value = "";
    validate = (result) => {
      try {
        return check(result as T);
      } catch {
        return false;
      }
    };
    completed = done;
    void retry();
  }
  return {
    pending,
    busy,
    error,
    notice,
    label,
    locked,
    navigationLocked,
    draftLock,
    dialogs,
    externalLock,
    send,
    retry,
  };
}
export const ledgerWorkspaceKey: InjectionKey<
  ReturnType<typeof createLedgerWorkspace>
> = Symbol("ledger-workspace");
export function useLedgerWorkspace() {
  const workspace = inject(ledgerWorkspaceKey);
  if (!workspace) throw new Error("账本写入上下文缺失");
  return workspace;
}

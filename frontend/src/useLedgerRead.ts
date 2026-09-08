import { onBeforeUnmount, ref, shallowRef } from "vue";
import { failure } from "./ledger";

export function useLedgerRead<T>() {
  const data = shallowRef<T>();
  const error = ref("");
  const loading = ref(false);
  let generation = 0;
  let controller: AbortController | undefined;
  function clear() {
    generation++;
    controller?.abort();
    data.value = undefined;
    error.value = "";
    loading.value = false;
  }
  async function load(read: (signal: AbortSignal) => Promise<T>) {
    controller?.abort();
    controller = new AbortController();
    const current = ++generation;
    loading.value = true;
    error.value = "";
    try {
      const result = await read(controller.signal);
      if (current === generation) data.value = result;
    } catch (e) {
      if (current === generation)
        error.value = `${failure(e)}；读取失败，保留的数据可能已过期，请刷新。`;
    } finally {
      if (current === generation) loading.value = false;
    }
  }
  onBeforeUnmount(clear);
  return { data, error, loading, clear, load };
}

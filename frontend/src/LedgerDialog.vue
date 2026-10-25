<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from "vue";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import { errorText } from "./ledger";
const props = withDefaults(
  defineProps<{
    title: string;
    caption?: string;
    dirty?: boolean;
    showWriteStatus?: boolean;
  }>(),
  { showWriteStatus: true },
);
const emit = defineEmits<{ close: [] }>();
const { locked, pending, busy, error, retry, label, dialogs } =
  useLedgerWorkspace();
const dialog = ref<HTMLDialogElement>();
const titleId = useId();
let opener: HTMLElement | null = null;
function requestClose() {
  if (locked.value) return;
  emit("close");
}
watch(error, (value) => {
  if (value && props.showWriteStatus !== false)
    void nextTick(() =>
      dialog.value?.querySelector<HTMLElement>(".lp-error")?.focus(),
    );
});
function backdrop(event: MouseEvent) {
  const element = dialog.value;
  if (!element || event.target !== element) return;
  const r = element.getBoundingClientRect();
  if (
    event.clientX < r.left ||
    event.clientX > r.right ||
    event.clientY < r.top ||
    event.clientY > r.bottom
  )
    requestClose();
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (locked.value || props.dirty) {
    event.preventDefault();
    event.returnValue = "";
  }
}
onMounted(() => {
  dialogs.value++;
  opener =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
  dialog.value?.showModal();
  window.addEventListener("beforeunload", beforeUnload);
});
onBeforeUnmount(() => {
  dialogs.value--;
  window.removeEventListener("beforeunload", beforeUnload);
  dialog.value?.close();
  void nextTick(() => {
    const remainingDialog = Array.from(
      document.querySelectorAll<HTMLDialogElement>("dialog[open]"),
    ).at(-1);
    // Saving can refresh and temporarily disable the action that opened a
    // nested editor. Keep keyboard focus in the remaining detail dialog.
    const target =
      opener?.isConnected &&
      !opener.matches(":disabled") &&
      (!remainingDialog || remainingDialog.contains(opener))
        ? opener
        : (remainingDialog?.querySelector<HTMLElement>(
            ".lp-close:not(:disabled)",
          ) ??
          remainingDialog ??
          document.querySelector<HTMLElement>(
            ".ledger-page [data-ledger-focus]",
          ) ??
          document.querySelector<HTMLElement>(
            '.lp-account-tabs [aria-selected="true"]',
          ));
    target?.focus({ preventScroll: !!remainingDialog });
  });
});
</script>

<template>
  <dialog
    ref="dialog"
    class="lp-dialog"
    :aria-labelledby="titleId"
    :aria-busy="showWriteStatus !== false && busy && !!pending"
    @cancel.prevent="requestClose"
    @click="backdrop"
  >
    <header class="lp-dialog-header">
      <div>
        <span class="lp-caption">{{ caption }}</span>
        <h2 :id="titleId">{{ title }}</h2>
      </div>
      <button
        type="button"
        class="lp-close"
        aria-label="关闭弹窗"
        :disabled="locked"
        @click="requestClose"
      >
        ×
      </button>
    </header>
    <span
      v-if="showWriteStatus !== false && pending && busy && !pending.uncertain"
      class="sr-only"
      role="status"
      >正在保存{{ label }}…</span
    >
    <p
      v-if="showWriteStatus !== false && error"
      class="lp-error"
      role="alert"
      tabindex="-1"
    >
      {{ errorText(error) }}
    </p>
    <div
      v-if="
        showWriteStatus !== false && pending && (!busy || pending.uncertain)
      "
      class="lp-dialog-body lp-reference"
      role="status"
    >
      <strong>{{ busy ? `正在确认${label}` : `${label}结果待确认` }}</strong>
      <p>请保留此页。账户、内容和请求已锁定，不要重复填写或刷新。</p>
      <button type="button" :disabled="busy" @click="retry">
        按原请求重试确认
      </button>
    </div>
    <slot :request-close="requestClose" />
  </dialog>
</template>

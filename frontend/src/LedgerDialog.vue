<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import { errorText } from "./ledger";
const props = defineProps<{
  title: string;
  caption?: string;
  dirty?: boolean;
}>();
const emit = defineEmits<{ close: [] }>();
const { locked, pending, busy, error, retry, label, dialogs } =
  useLedgerWorkspace();
const dialog = ref<HTMLDialogElement>();
const discard = ref(false);
let opener: HTMLElement | null = null;
let resumeTarget: HTMLElement | null = null;
function requestClose(dirty: boolean) {
  if (locked.value) return;
  if (dirty) {
    resumeTarget =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    discard.value = true;
    void nextTick(() =>
      dialog.value
        ?.querySelector<HTMLButtonElement>(".lp-discard button")
        ?.focus(),
    );
  } else emit("close");
}
function resume() {
  discard.value = false;
  void nextTick(() => resumeTarget?.isConnected && resumeTarget.focus());
}
watch(error, (value) => {
  if (value)
    void nextTick(() =>
      dialog.value?.querySelector<HTMLElement>(".lp-error")?.focus(),
    );
});
function backdrop(event: MouseEvent, dirty: boolean) {
  const element = dialog.value;
  if (!element || event.target !== element) return;
  const r = element.getBoundingClientRect();
  if (
    event.clientX < r.left ||
    event.clientX > r.right ||
    event.clientY < r.top ||
    event.clientY > r.bottom
  )
    requestClose(dirty);
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
    const target = opener?.isConnected
      ? opener
      : (document.querySelector<HTMLElement>(
          ".ledger-page [data-ledger-focus]",
        ) ??
        document.querySelector<HTMLElement>(
          '.lp-account-tabs [aria-selected="true"]',
        ));
    target?.focus();
  });
});
</script>

<template>
  <dialog
    ref="dialog"
    class="lp-dialog"
    aria-labelledby="ledger-dialog-title"
    @cancel.prevent="requestClose(!!dirty)"
    @click="backdrop($event, !!dirty)"
  >
    <header class="lp-dialog-header">
      <div>
        <span class="lp-caption">{{ caption }}</span>
        <h2 id="ledger-dialog-title">{{ title }}</h2>
      </div>
      <button
        type="button"
        class="lp-close"
        aria-label="关闭弹窗"
        :disabled="locked"
        @click="requestClose(!!dirty)"
      >
        ×
      </button>
    </header>
    <p v-if="error" class="lp-error" role="alert" tabindex="-1">
      {{ errorText(error) }}
    </p>
    <div v-if="pending" class="lp-dialog-body lp-reference" role="status">
      <strong>{{ busy ? `正在确认${label}` : `${label}结果待确认` }}</strong>
      <p>请保留此页。账户、内容和请求已锁定，不要重复填写或刷新。</p>
      <button type="button" :disabled="busy" @click="retry">
        按原请求重试确认
      </button>
    </div>
    <div v-if="discard" class="lp-discard" role="alert">
      <strong>放弃尚未保存的修改？</strong>
      <p>关闭后，本次填写的内容不会保留。</p>
      <div class="lp-actions">
        <button type="button" @click="resume">继续编辑</button>
        <button
          type="button"
          class="lp-danger-button"
          :disabled="locked"
          @click="emit('close')"
        >
          放弃修改
        </button>
      </div>
    </div>
    <slot :request-close="() => requestClose(!!dirty)" />
  </dialog>
</template>

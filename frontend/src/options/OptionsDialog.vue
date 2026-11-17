<script setup lang="ts">
import { onMounted, ref } from "vue";
defineProps<{ title: string; subtitle?: string; wide?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>();
onMounted(() => dialog.value?.showModal());
</script>

<template>
  <Teleport to="body">
    <dialog
      ref="dialog"
      class="opt-modal"
      :class="{ 'opt-wide': wide }"
      aria-labelledby="opt-dialog-title"
      @close="emit('close')"
      @click="
        (e) => {
          if (e.target === dialog) dialog?.close();
        }
      "
    >
      <header class="opt-dialog-head">
        <div>
          <h2 id="opt-dialog-title">{{ title }}</h2>
          <p v-if="subtitle">{{ subtitle }}</p>
        </div>
        <button
          class="opt-close"
          type="button"
          aria-label="关闭弹窗"
          @click="dialog?.close()"
        >
          ×
        </button>
      </header>
      <div class="opt-dialog-body"><slot /></div>
    </dialog>
  </Teleport>
</template>

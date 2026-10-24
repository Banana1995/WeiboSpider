<script setup lang="ts">
import { onBeforeUnmount, ref } from "vue";
const props = defineProps<{ text: string; label?: string }>();
const open = ref(false);
const style = ref<Record<string, string>>({});
const trigger = ref<HTMLElement>();

function place() {
  const element = trigger.value;
  if (!element) return;
  const rect = element.getBoundingClientRect();
  const width = Math.min(340, window.innerWidth - 24);
  const half = width / 2;
  const left = Math.min(
    Math.max(rect.left + rect.width / 2, half + 12),
    window.innerWidth - half - 12,
  );
  if (rect.top < 150) {
    style.value = {
      left: `${left}px`,
      top: `${rect.bottom + 10}px`,
      maxWidth: `${width}px`,
      transform: "translate(-50%, 0)",
    };
  } else {
    style.value = {
      left: `${left}px`,
      top: `${rect.top - 10}px`,
      maxWidth: `${width}px`,
      transform: "translate(-50%, -100%)",
    };
  }
}
function show() {
  place();
  open.value = true;
  window.addEventListener("scroll", hide, true);
  window.addEventListener("resize", hide);
}
function hide() {
  open.value = false;
  window.removeEventListener("scroll", hide, true);
  window.removeEventListener("resize", hide);
}
onBeforeUnmount(hide);
</script>

<template>
  <span
    ref="trigger"
    class="tax-tip-trigger"
    tabindex="0"
    :aria-label="props.label ?? '查看说明'"
    @mouseenter="show"
    @mouseleave="hide"
    @focus="show"
    @blur="hide"
    @keydown.esc="hide"
    >i</span
  >
  <Teleport to="body">
    <span v-if="open" class="tax-tooltip" role="tooltip" :style="style">{{
      props.text
    }}</span>
  </Teleport>
</template>

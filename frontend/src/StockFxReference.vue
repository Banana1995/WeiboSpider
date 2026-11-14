<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from "vue";
import type { FXQuote } from "./ledger";
import { holdingNumber } from "./holdings";

const props = defineProps<{ fx: FXQuote }>();
const id = useId();
const open = ref(false);
const trigger = ref<HTMLButtonElement>();
const tip = ref<HTMLElement>();
const position = ref<Record<string, string>>({});

async function show() {
  open.value = true;
  await nextTick();
  if (!open.value || !trigger.value || !tip.value) return;
  const anchor = trigger.value.getBoundingClientRect();
  const bounds = tip.value.getBoundingClientRect();
  const left = Math.max(
    12,
    Math.min(
      anchor.right - bounds.width,
      window.innerWidth - bounds.width - 12,
    ),
  );
  const top =
    anchor.bottom + bounds.height + 20 <= window.innerHeight
      ? anchor.bottom + 8
      : Math.max(12, anchor.top - bounds.height - 8);
  position.value = { left: `${left}px`, top: `${top}px` };
  window.addEventListener("scroll", hide, true);
  window.addEventListener("resize", hide);
}
function hide() {
  open.value = false;
  window.removeEventListener("scroll", hide, true);
  window.removeEventListener("resize", hide);
}
watch(() => props.fx, hide);
onBeforeUnmount(hide);
</script>

<template>
  <span class="stock-fx-reference" @mouseleave="hide">
    <button
      ref="trigger"
      type="button"
      class="stock-fx-trigger"
      :aria-label="`查看 ${fx.base}/${fx.quote} 参考汇率日期与来源`"
      :aria-expanded="open"
      :aria-describedby="open ? id : undefined"
      @mouseenter="show"
      @focus="show"
      @click="show"
      @blur="hide"
      @keydown.esc.prevent.stop="hide"
    >
      参考汇率 1 {{ fx.base }} = {{ holdingNumber(fx.rate, 4) }} {{ fx.quote }}
      <span class="stock-fx-info" aria-hidden="true">i</span>
    </button>
    <span
      v-if="open"
      :id="id"
      ref="tip"
      class="stock-fx-tip"
      role="tooltip"
      :style="position"
    >
      <strong
        >1 {{ fx.base }} = {{ holdingNumber(fx.rate, 4) }}
        {{ fx.quote }}</strong
      >
      <span>汇率日期 {{ fx.date }}</span>
      <span
        >来源：{{
          fx.source.startsWith("Tencent/spot/") ? "腾讯即期汇率" : fx.source
        }}</span
      >
      <small>用于当前市值估算，非历史成交结算汇率。</small>
    </span>
  </span>
</template>

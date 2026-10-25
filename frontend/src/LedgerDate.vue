<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import "./datefield.css";

const props = defineProps<{
  modelValue: string;
  name?: string;
  min?: string;
  max?: string;
  required?: boolean;
  disabled?: boolean;
  ariaLabel?: string;
}>();
const emit = defineEmits<{
  "update:modelValue": [value: string];
  change: [value: string];
}>();
const pad = (value: number) => String(value).padStart(2, "0");
const iso = (year: number, month: number, day: number) =>
  `${year}-${pad(month + 1)}-${pad(day)}`;
const valid = (value: string) =>
  /^\d{4}-\d{2}-\d{2}$/.test(value) &&
  iso(
    Number(value.slice(0, 4)),
    Number(value.slice(5, 7)) - 1,
    Number(value.slice(8)),
  ) === value;
const todayValue = () => {
  const now = new Date();
  return iso(now.getFullYear(), now.getMonth(), now.getDate());
};
const text = ref(props.modelValue);
const open = ref(false);
const panel = ref<HTMLElement>();
const field = ref<HTMLElement>();
const input = ref<HTMLInputElement>();
const now = new Date();
const view = ref({ year: now.getFullYear(), month: now.getMonth() });
const weeks = ["一", "二", "三", "四", "五", "六", "日"];

watch(
  () => props.modelValue,
  (value) => {
    text.value = value;
    if (valid(value)) {
      view.value = {
        year: Number(value.slice(0, 4)),
        month: Number(value.slice(5, 7)) - 1,
      };
    }
  },
  { immediate: true },
);
const monthTitle = computed(
  () => `${view.value.year}年${view.value.month + 1}月`,
);
const cells = computed(() => {
  const first = new Date(Date.UTC(view.value.year, view.value.month, 1));
  const offset = (first.getUTCDay() + 6) % 7;
  const start = new Date(
    Date.UTC(view.value.year, view.value.month, 1 - offset),
  );
  return Array.from({ length: 42 }, (_, index) => {
    const current = new Date(start.getTime() + index * 86400000);
    const value = iso(
      current.getUTCFullYear(),
      current.getUTCMonth(),
      current.getUTCDate(),
    );
    return {
      value,
      day: current.getUTCDate(),
      current: current.getUTCMonth() === view.value.month,
      disabled:
        (!!props.min && value < props.min) ||
        (!!props.max && value > props.max),
    };
  });
});
const canClear = computed(
  () => !!props.modelValue && !props.disabled && !props.required,
);

function shift(months: number) {
  const next = new Date(
    Date.UTC(view.value.year, view.value.month + months, 1),
  );
  view.value = { year: next.getUTCFullYear(), month: next.getUTCMonth() };
}
function pick(value: string) {
  if (props.disabled) return;
  if ((!!props.min && value < props.min) || (!!props.max && value > props.max))
    return;
  text.value = value;
  emit("update:modelValue", value);
  emit("change", value);
  close();
  void nextTick(() => input.value?.focus());
}
function clear() {
  text.value = "";
  emit("update:modelValue", "");
  emit("change", "");
  close();
}
function goToday() {
  const value = todayValue();
  if ((!!props.min && value < props.min) || (!!props.max && value > props.max))
    return;
  pick(value);
}
function typed(event: Event) {
  const value = (event.target as HTMLInputElement).value
    .trim()
    .replace(/\//g, "-");
  text.value = value;
  if (!valid(value)) return;
  if ((!!props.min && value < props.min) || (!!props.max && value > props.max))
    return;
  emit("update:modelValue", value);
  emit("change", value);
}
function settled() {
  text.value = props.modelValue;
}
function place() {
  const el = panel.value;
  const anchor = field.value;
  if (!el || !anchor) return;
  const rect = anchor.getBoundingClientRect();
  const width = 318;
  const height = 372;
  const left = Math.min(
    Math.max(8, rect.left),
    Math.max(8, window.innerWidth - width - 8),
  );
  const above =
    rect.bottom + height + 12 > window.innerHeight && rect.top > height;
  el.style.width = `${width}px`;
  el.style.left = `${left}px`;
  el.style.top = `${above ? rect.top - height - 6 : rect.bottom + 6}px`;
}
function show() {
  if (props.disabled || open.value) return;
  if (valid(props.modelValue)) {
    view.value = {
      year: Number(props.modelValue.slice(0, 4)),
      month: Number(props.modelValue.slice(5, 7)) - 1,
    };
  } else {
    const now = new Date();
    view.value = { year: now.getFullYear(), month: now.getMonth() };
  }
  open.value = true;
  void nextTick(() => {
    const el = panel.value;
    place();
    try {
      (
        el as (HTMLElement & { showPopover?: () => void }) | undefined
      )?.showPopover?.();
    } catch {
      /* already open */
    }
    const active =
      el?.querySelector<HTMLButtonElement>('[data-selected="true"]') ??
      el?.querySelector<HTMLButtonElement>('[data-today="true"]') ??
      el?.querySelector<HTMLButtonElement>('[data-current="true"]');
    active?.focus();
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("keydown", escape);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
  });
}
function close() {
  if (!open.value) return;
  open.value = false;
  try {
    (
      panel.value as (HTMLElement & { hidePopover?: () => void }) | undefined
    )?.hidePopover?.();
  } catch {
    /* already closed */
  }
  document.removeEventListener("pointerdown", outside, true);
  document.removeEventListener("keydown", escape);
  window.removeEventListener("resize", close);
  window.removeEventListener("scroll", close, true);
}
function outside(event: PointerEvent) {
  const target = event.target as Node;
  if (field.value?.contains(target) || panel.value?.contains(target)) return;
  close();
}
function escape(event: KeyboardEvent) {
  if (event.key !== "Escape") return;
  event.stopPropagation();
  close();
  input.value?.focus();
}
function toggle() {
  if (open.value) close();
  else show();
}
onBeforeUnmount(close);
</script>

<template>
  <div
    ref="field"
    class="ld-field"
    :class="{ 'ld-open': open, 'ld-off': disabled }"
  >
    <input
      ref="input"
      class="ld-input"
      type="text"
      inputmode="numeric"
      autocomplete="off"
      spellcheck="false"
      :name="name"
      :value="text"
      :required="required"
      :disabled="disabled"
      :aria-label="ariaLabel"
      placeholder="YYYY-MM-DD"
      @input="typed"
      @blur="settled"
      @click="show"
    />
    <button
      type="button"
      class="ld-icon"
      :disabled="disabled"
      aria-label="打开日期选择"
      @click="toggle"
    >
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path
          d="M4.5 2v2m7-2v2M2.5 6h11M3.5 3.5h9A1.5 1.5 0 0 1 14 5v7.5a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 12.5V5a1.5 1.5 0 0 1 1.5-1.5Z"
        />
      </svg>
    </button>
    <Teleport to="body">
      <div
        ref="panel"
        popover="manual"
        class="ld-popover"
        @pointerdown.stop
        @mousedown.prevent
      >
        <header class="ld-head">
          <button
            type="button"
            class="ld-nav"
            aria-label="上个月"
            @click="shift(-1)"
          >
            ‹
          </button>
          <strong>{{ monthTitle }}</strong>
          <button
            type="button"
            class="ld-nav"
            aria-label="下个月"
            @click="shift(1)"
          >
            ›
          </button>
        </header>
        <div class="ld-week" aria-hidden="true">
          <span v-for="week in weeks" :key="week">{{ week }}</span>
        </div>
        <div class="ld-grid">
          <button
            v-for="cell in cells"
            :key="cell.value"
            type="button"
            class="ld-day"
            :class="{ 'ld-out': !cell.current }"
            :disabled="cell.disabled"
            :data-selected="cell.value === modelValue ? 'true' : undefined"
            :data-today="cell.value === todayValue() ? 'true' : undefined"
            :data-current="cell.current ? 'true' : undefined"
            :aria-label="cell.value"
            :aria-pressed="cell.value === modelValue"
            @click="pick(cell.value)"
          >
            {{ cell.day }}
          </button>
        </div>
        <footer class="ld-foot">
          <button v-if="canClear" type="button" @click="clear">清除</button>
          <span v-else />
          <button type="button" class="ld-today" @click="goToday">今天</button>
        </footer>
      </div>
    </Teleport>
  </div>
</template>

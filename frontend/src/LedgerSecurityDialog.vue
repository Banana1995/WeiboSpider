<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import {
  errorText,
  LedgerError,
  newID,
  request,
  type Instrument,
} from "./ledger";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

type SearchItem = Omit<Instrument, "id">;
const emit = defineEmits<{
  close: [];
  selected: [Instrument];
  dirty: [boolean];
}>();
const { locked } = useLedgerWorkspace();
const input = ref("");
const search = reactive(useLedgerRead<SearchItem[]>());
const open = ref(false);
const chosen = ref(false);
const dirty = computed(() => !chosen.value && input.value.trim().length > 0);
watch(dirty, (value) => emit("dirty", value), { flush: "sync" });

function validItems(items: unknown): items is SearchItem[] {
  return (
    Array.isArray(items) &&
    items.length <= 8 &&
    items.every(
      (i: SearchItem) =>
        !!i &&
        typeof i.name === "string" &&
        !!i.name.trim() &&
        i.name.length <= 160 &&
        ["SH", "SZ", "HK"].includes(i.market) &&
        (i.market === "HK" ? /^\d{5}$/ : /^\d{6}$/).test(i.code) &&
        ["CNY", "HKD", "USD"].includes(i.currency),
    ) &&
    new Set((items as SearchItem[]).map((i) => `${i.market}/${i.code}`))
      .size === items.length
  );
}

let timer: ReturnType<typeof setTimeout> | undefined;
let generation = 0;
function schedule() {
  clearTimeout(timer);
  generation++;
  search.clear();
  open.value = false;
  const text = input.value.trim();
  if (chosen.value || !text) return;
  timer = setTimeout(() => void lookup(text), 220);
}
watch(input, schedule);

async function lookup(text: string) {
  if (locked.value || chosen.value) return;
  const current = ++generation;
  search.clear();
  await search.load(async (signal) => {
    const result = await request<{ items: SearchItem[] }>(
      `/instruments/search?q=${encodeURIComponent(text)}`,
      { signal },
    );
    if (!result || !validItems(result.items))
      throw new LedgerError("invalid_response");
    return result.items;
  });
  if (current === generation) open.value = true;
}
function lookupNow() {
  clearTimeout(timer);
  const text = input.value.trim();
  if (text && !chosen.value) void lookup(text);
}
function choose(item: SearchItem) {
  if (locked.value || chosen.value) return;
  chosen.value = true;
  clearTimeout(timer);
  generation++;
  search.clear();
  open.value = false;
  emit("selected", { ...item, id: newID() });
}
function cancel() {
  if (!locked.value) emit("close");
}
function onBlur() {
  setTimeout(() => {
    open.value = false;
  }, 150);
}
onBeforeUnmount(() => {
  clearTimeout(timer);
  generation++;
});
</script>

<template>
  <div class="lp-security-picker" data-test="instrument-form">
    <label class="lp-security-picker-field" for="security-search"
      >输入证券代码或名称
      <input
        id="security-search"
        v-model="input"
        name="security_search"
        placeholder="如 600519、茅台"
        maxlength="32"
        autocomplete="off"
        :disabled="locked"
        role="combobox"
        aria-controls="security-search-results"
        :aria-expanded="open && !!search.data?.length"
        @focus="open = !!search.data?.length"
        @blur="onBlur"
        @keydown.esc="open = false"
        @keydown.enter.prevent="lookupNow"
      />
    </label>
    <p v-if="search.loading" class="lp-field-hint" role="status">
      正在查询证券身份…
    </p>
    <p v-else-if="search.error" class="lp-error" role="alert">
      {{ errorText(search.error) }}
    </p>
    <p v-else class="lp-field-hint">
      支持沪深港股票代码或名称，点选结果即可加入持仓。
    </p>
    <ul
      v-if="open && search.data?.length"
      id="security-search-results"
      class="lp-search-results"
      role="listbox"
    >
      <li v-for="item in search.data" :key="`${item.market}/${item.code}`">
        <button
          type="button"
          role="option"
          :aria-selected="false"
          @mousedown.prevent
          @click="choose(item)"
        >
          <strong>{{ item.name }}</strong>
          <span>{{ item.market }} / {{ item.code }} · {{ item.currency }}</span>
        </button>
      </li>
    </ul>
    <p
      v-else-if="open && !search.loading && !search.error && input.trim()"
      class="lp-field-hint"
    >
      未找到支持的股票，请检查代码或名称。
    </p>
    <div class="lp-actions">
      <button type="button" :disabled="locked" @click="cancel">取消</button>
    </div>
  </div>
</template>

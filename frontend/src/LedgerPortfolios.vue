<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import { all, errorText, LedgerError, type Account } from "./ledger";
import { validPortfolio, type Portfolio } from "./ledgerPortfolios";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import LedgerOverview from "./LedgerOverview.vue";
import LedgerPortfolioDialog from "./LedgerPortfolioDialog.vue";
import "./ledgerPortfolios.css";

const props = defineProps<{ accounts: Account[]; refreshKey: number }>();
const emit = defineEmits<{
  account: [event: { id: string; portfolio: string }];
  locate: [
    event: { id: string; date: string; accountId: string; portfolio: string },
  ];
}>();
const { navigationLocked, locked } = useLedgerWorkspace();
const list = reactive(useLedgerRead<Portfolio[]>());
const selected = ref("");
const current = computed(() => list.data?.find((p) => p.id === selected.value));
const modal = ref<"" | "create" | "edit" | "delete">("");
const actions = ref<HTMLDetailsElement>();
let generation = 0;

async function load(preferred = selected.value) {
  const currentGeneration = ++generation;
  selected.value = preferred;
  await list.load(async (signal) => {
    const items = await all<Portfolio>("/portfolios", signal);
    if (
      items.some((p) => !validPortfolio(p)) ||
      new Set(items.map((p) => p.id)).size !== items.length
    )
      throw new LedgerError("invalid_response");
    return items;
  });
  if (currentGeneration !== generation || !list.data || list.error) return;
  selected.value = list.data.some((p) => p.id === preferred)
    ? preferred
    : (list.data[0]?.id ?? "");
}

function select(id: string) {
  if (navigationLocked.value) return;
  selected.value = id;
  if (actions.value) actions.value.open = false;
  void nextTick(() =>
    document
      .getElementById(`portfolio-tab-${id}`)
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" }),
  );
}
function tabsKey(event: KeyboardEvent) {
  if (
    navigationLocked.value ||
    !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)
  )
    return;
  const tabs = Array.from(
    (event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>(
      '[role="tab"]',
    ),
  );
  const index = tabs.indexOf(event.target as HTMLButtonElement);
  if (index < 0 || !tabs.length) return;
  event.preventDefault();
  const next =
    event.key === "Home"
      ? 0
      : event.key === "End"
        ? tabs.length - 1
        : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
          tabs.length;
  tabs[next]!.click();
  tabs[next]!.focus();
}
function open(mode: "create" | "edit" | "delete") {
  if (navigationLocked.value) return;
  if (actions.value) actions.value.open = false;
  modal.value = mode;
}
function closeActions(event: Event) {
  const menu = actions.value;
  if (menu?.open && !menu.contains(event.target as Node)) menu.open = false;
}
function actionsEscape(event: KeyboardEvent) {
  const menu = actions.value;
  if (event.key !== "Escape" || !menu?.open) return;
  event.stopPropagation();
  menu.open = false;
  menu.querySelector("summary")?.focus();
}
onMounted(() => {
  document.addEventListener("click", closeActions, true);
  document.addEventListener("keydown", actionsEscape);
});
onBeforeUnmount(() => {
  document.removeEventListener("click", closeActions, true);
  document.removeEventListener("keydown", actionsEscape);
});
function reload() {
  modal.value = "";
  void load();
}
const memberName = (id: string) =>
  props.accounts.find((a) => a.id === id)?.name ?? "已删除的账户";
watch(
  () => props.refreshKey,
  () => void load(),
  { immediate: true },
);
</script>

<template>
  <section aria-label="账户组合" class="lp-portfolios">
    <div v-if="list.data?.length" class="lp-portfolio-navigation">
      <div
        class="lp-account-tabs"
        role="tablist"
        aria-label="选择组合"
        @keydown="tabsKey"
      >
        <button
          v-for="p in list.data"
          :id="`portfolio-tab-${p.id}`"
          :key="p.id"
          role="tab"
          :aria-selected="selected === p.id"
          :aria-controls="`portfolio-panel-${p.id}`"
          :tabindex="selected === p.id ? 0 : -1"
          :title="p.name"
          :disabled="navigationLocked"
          @click="select(p.id)"
        >
          {{ p.name }}
        </button>
      </div>
      <button
        class="lp-text-button lp-new-portfolio"
        :disabled="navigationLocked"
        @click="open('create')"
      >
        ＋ 新建组合
      </button>
    </div>
    <p v-if="list.loading && !list.data" class="lp-empty" role="status">
      正在读取账户组合…
    </p>
    <div v-if="list.error" class="lp-empty">
      <p class="lp-error" role="alert">{{ errorText(list.error) }}</p>
      <button :disabled="locked || list.loading" @click="load()">
        重新读取组合
      </button>
    </div>
    <div
      v-else-if="current"
      :id="`portfolio-panel-${current.id}`"
      role="tabpanel"
      :aria-labelledby="`portfolio-tab-${current.id}`"
    >
      <div class="lp-portfolio-membership">
        <div class="lp-member-names">
          <span class="lp-muted"
            >{{ current.currency }} ·
            {{ current.account_ids.length }} 个成员</span
          ><span
            v-for="id in current.account_ids"
            :key="id"
            class="lp-member-chip"
            >{{ memberName(id) }}</span
          >
        </div>
        <div class="lp-actions">
          <button
            class="lp-text-button"
            :disabled="navigationLocked"
            @click="open('edit')"
          >
            调整成员
          </button>
          <details ref="actions" class="lp-portfolio-menu">
            <summary aria-label="组合操作">···</summary>
            <div>
              <button :disabled="navigationLocked" @click="open('edit')">
                重命名组合</button
              ><button :disabled="navigationLocked" @click="open('delete')">
                删除组合
              </button>
            </div>
          </details>
        </div>
      </div>
      <LedgerOverview
        :key="current.id"
        :account="current"
        :portfolio="current"
        :refresh-key="refreshKey"
        @account="emit('account', { id: $event, portfolio: current.name })"
        @locate="emit('locate', { ...$event, portfolio: current.name })"
      />
    </div>
    <section v-else-if="list.data && !list.loading" class="lp-income lp-empty">
      <h2>把分散的账户放在一起看</h2>
      <p>选择成员、给组合起名，就能查看合并收益和各账户贡献。</p>
      <button
        class="lp-primary"
        :disabled="navigationLocked || !accounts.length"
        @click="open('create')"
      >
        新建账户组合
      </button>
      <p v-if="!accounts.length" class="lp-muted">
        先在单账户中创建或导入投资记录。
      </p>
    </section>
    <template v-for="p in list.data" :key="p.id"
      ><div
        v-if="p.id !== selected"
        :id="`portfolio-panel-${p.id}`"
        role="tabpanel"
        :aria-labelledby="`portfolio-tab-${p.id}`"
        hidden
    /></template>
    <LedgerPortfolioDialog
      v-if="modal"
      :accounts="accounts"
      :portfolio="modal === 'create' ? undefined : current"
      :remove="modal === 'delete'"
      @close="modal = ''"
      @saved="load($event)"
      @deleted="load('')"
      @reload="reload"
    />
  </section>
</template>

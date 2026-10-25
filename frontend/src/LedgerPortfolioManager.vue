<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import PortfolioForm from "./PortfolioForm.vue";
import { all, errorText, LedgerError, type Account } from "./ledger";
import { validPortfolio, type Portfolio } from "./ledgerPortfolios";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{ accounts: Account[]; refreshKey: number }>();
const emit = defineEmits<{ changed: [] }>();
const { locked } = useLedgerWorkspace();
const list = reactive(useLedgerRead<Portfolio[]>());
const modal = ref<"" | "create" | "edit" | "delete">("");
const targetId = ref("");
const current = computed(() =>
  list.data?.find((portfolio) => portfolio.id === targetId.value),
);
const editing = computed(() =>
  modal.value === "create" ? undefined : current.value,
);

function load() {
  void list.load(async (signal) => {
    const items = await all<Portfolio>("/portfolios", signal);
    if (
      items.some((portfolio) => !validPortfolio(portfolio)) ||
      new Set(items.map((portfolio) => portfolio.id)).size !== items.length
    )
      throw new LedgerError("invalid_response");
    return items;
  });
}
watch(() => props.refreshKey, load, { immediate: true });

const memberName = (id: string) =>
  props.accounts.find((account) => account.id === id)?.name ?? "已删除的账户";
function openCreate() {
  if (locked.value) return;
  targetId.value = "";
  modal.value = "create";
}
function openEdit(portfolio: Portfolio) {
  if (locked.value) return;
  targetId.value = portfolio.id;
  modal.value = "edit";
}
function openDelete(portfolio: Portfolio) {
  if (locked.value) return;
  targetId.value = portfolio.id;
  modal.value = "delete";
}
function close() {
  modal.value = "";
  targetId.value = "";
}
function saved(id: string) {
  targetId.value = id;
  modal.value = "";
  load();
  emit("changed");
}
function deleted() {
  close();
  load();
  emit("changed");
}
function reload() {
  close();
  load();
}
</script>

<template>
  <section aria-label="账户组合管理">
    <header class="lp-manager-heading">
      <div>
        <h2>账户组合</h2>
        <p class="lp-muted">
          共 {{ list.data?.length ?? 0 }} 个组合，调整成员后按新成员集合重算收益
        </p>
      </div>
      <div class="lp-actions">
        <button
          class="lp-primary"
          :disabled="locked || list.loading || !!modal"
          @click="openCreate"
        >
          ＋ 新建组合
        </button>
      </div>
    </header>
    <div v-if="modal" class="lp-management lp-inline-editor">
      <PortfolioForm
        inline
        :accounts="accounts"
        :portfolio="editing"
        :remove="modal === 'delete'"
        @close="close"
        @saved="saved"
        @deleted="deleted"
        @reload="reload"
      />
    </div>
    <p v-else-if="list.loading && !list.data" class="lp-empty" role="status">
      正在读取账户组合…
    </p>
    <div v-else-if="list.error" class="lp-empty">
      <p class="lp-error" role="alert">{{ errorText(list.error) }}</p>
      <button :disabled="locked || list.loading" @click="load">
        重新读取组合
      </button>
    </div>
    <div v-else-if="list.data?.length" class="lp-management">
      <ul class="lp-portfolio-list">
        <li v-for="portfolio in list.data" :key="portfolio.id">
          <div class="lp-portfolio-entry">
            <div class="lp-portfolio-entry-main">
              <strong>{{ portfolio.name }}</strong>
              <span class="lp-muted"
                >{{ portfolio.currency }} ·
                {{ portfolio.account_ids.length }} 个成员</span
              >
              <div class="lp-portfolio-entry-members">
                <span
                  v-for="id in portfolio.account_ids"
                  :key="id"
                  class="lp-pm-chip"
                  >{{ memberName(id) }}</span
                >
              </div>
            </div>
            <div class="lp-actions">
              <button
                :disabled="locked"
                :aria-label="`调整${portfolio.name}的成员`"
                @click="openEdit(portfolio)"
              >
                调整成员
              </button>
              <button
                class="lp-text-button"
                :disabled="locked"
                :aria-label="`删除${portfolio.name}`"
                @click="openDelete(portfolio)"
              >
                删除
              </button>
            </div>
          </div>
        </li>
      </ul>
    </div>
    <div v-else class="lp-management lp-empty">
      <h3>还没有账户组合</h3>
      <p>新建组合并选择成员，就能合并查看多个账户的收益。</p>
    </div>
  </section>
</template>

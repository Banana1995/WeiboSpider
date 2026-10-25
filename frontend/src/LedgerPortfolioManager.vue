<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import PortfolioForm from "./PortfolioForm.vue";
import {
  all,
  errorText,
  failure,
  LedgerError,
  newID,
  request,
  type Account,
} from "./ledger";
import { validPortfolio, type Portfolio } from "./ledgerPortfolios";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{ accounts: Account[]; refreshKey: number }>();
const emit = defineEmits<{ changed: [] }>();
const { locked } = useLedgerWorkspace();
const list = reactive(useLedgerRead<Portfolio[]>());
const modal = ref<"" | "create" | "delete">("");
const targetId = ref("");
const expandedId = ref("");
const current = computed(() =>
  list.data?.find((portfolio) => portfolio.id === targetId.value),
);
const editing = computed(() =>
  modal.value === "delete" ? current.value : undefined,
);
const savingId = ref("");
const membershipError = ref("");
const draft = ref<string[]>([]);
const draftError = ref("");

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
function toggle(portfolio: Portfolio) {
  if (locked.value) return;
  membershipError.value = "";
  draftError.value = "";
  if (expandedId.value === portfolio.id) {
    expandedId.value = "";
    draft.value = [];
    return;
  }
  expandedId.value = portfolio.id;
  draft.value = [...portfolio.account_ids];
}
const dirtyFor = (portfolio: Portfolio) =>
  expandedId.value === portfolio.id &&
  [...draft.value].sort().join(",") !==
    [...portfolio.account_ids].sort().join(",");
function toggleMember(accountId: string, event: Event) {
  const input = event.target as HTMLInputElement;
  if (locked.value || savingId.value) return;
  draftError.value = "";
  if (input.checked) {
    if (!draft.value.includes(accountId))
      draft.value = [...draft.value, accountId].sort();
    return;
  }
  if (draft.value.length <= 1) {
    input.checked = true;
    draftError.value =
      "组合至少需要保留一个账户；如需清空，请直接删除整个组合。";
    return;
  }
  draft.value = draft.value.filter((id) => id !== accountId);
}
async function saveMembers(portfolio: Portfolio) {
  if (locked.value || savingId.value) return;
  const accountIds = draft.value
    .filter((id) => props.accounts.some((account) => account.id === id))
    .sort();
  if (!accountIds.length) {
    draftError.value = "组合至少需要保留一个账户。";
    return;
  }
  savingId.value = portfolio.id;
  draftError.value = "";
  membershipError.value = "";
  try {
    const updated = await request<Portfolio>(
      `/portfolios/${encodeURIComponent(portfolio.id)}`,
      {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": newID(),
        },
        body: JSON.stringify({
          name: portfolio.name,
          currency: portfolio.currency,
          account_ids: accountIds,
          expected_version: portfolio.version,
        }),
      },
    );
    if (!validPortfolio(updated) || updated.id !== portfolio.id)
      throw new LedgerError("invalid_response");
    if (list.data)
      list.data = list.data.map((item) =>
        item.id === portfolio.id ? updated : item,
      );
    draft.value = [...updated.account_ids];
    emit("changed");
  } catch (e) {
    membershipError.value = errorText(failure(e));
  } finally {
    savingId.value = "";
  }
}

function openDelete(portfolio: Portfolio) {
  if (locked.value) return;
  targetId.value = portfolio.id;
  expandedId.value = portfolio.id;
  modal.value = "delete";
}
function close() {
  modal.value = "";
  targetId.value = "";
}
function saved(id: string) {
  targetId.value = id;
  expandedId.value = id;
  modal.value = "";
  load();
  emit("changed");
}
function deleted() {
  close();
  expandedId.value = "";
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
    <div v-if="modal" class="lp-management lp-portfolio-inline">
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
          <button
            class="lp-portfolio-entry"
            :disabled="locked"
            :aria-expanded="expandedId === portfolio.id"
            :aria-label="`${expandedId === portfolio.id ? '收起' : '管理'}${portfolio.name}组合`"
            @click="toggle(portfolio)"
          >
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
            <span class="lp-portfolio-entry-action"
              >{{ expandedId === portfolio.id ? "收起" : "管理" }} ›</span
            >
          </button>
          <div
            v-if="expandedId === portfolio.id"
            class="lp-portfolio-entry-panel"
          >
            <p class="lp-field-hint">勾选要汇总的账户，点击保存后生效。</p>
            <div
              class="lp-portfolio-members"
              role="group"
              :aria-label="`${portfolio.name}的成员账户`"
            >
              <label v-for="item in accounts" :key="item.id">
                <input
                  type="checkbox"
                  :checked="draft.includes(item.id)"
                  :disabled="locked || savingId === portfolio.id"
                  @change="toggleMember(item.id, $event)"
                />
                <span
                  ><strong>{{ item.name }}</strong
                  ><small
                    >{{ item.currency }} · 开始于 {{ item.opening_date }}</small
                  ></span
                >
              </label>
            </div>
            <p v-if="draftError" class="lp-error" role="alert">
              {{ draftError }}
            </p>
            <p v-if="membershipError" class="lp-error" role="alert">
              {{ membershipError }}
            </p>
            <div class="lp-portfolio-panel-footer">
              <span class="lp-muted" role="status">{{
                savingId === portfolio.id
                  ? "正在保存…"
                  : dirtyFor(portfolio)
                    ? "有未保存的修改"
                    : "尚未修改"
              }}</span>
              <div class="lp-actions">
                <button
                  class="lp-danger-solid"
                  :disabled="locked || savingId === portfolio.id"
                  :aria-label="`删除${portfolio.name}`"
                  @click="openDelete(portfolio)"
                >
                  删除组合
                </button>
                <button
                  class="lp-primary"
                  :disabled="
                    locked || savingId === portfolio.id || !dirtyFor(portfolio)
                  "
                  :aria-label="`保存${portfolio.name}的成员`"
                  @click="saveMembers(portfolio)"
                >
                  保存
                </button>
              </div>
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

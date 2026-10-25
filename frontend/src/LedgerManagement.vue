<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import ImportAccount from "./ImportAccount.vue";
import LedgerPortfolioManager from "./LedgerPortfolioManager.vue";
import {
  all,
  errorText,
  failure,
  LedgerError,
  newID,
  PendingWrite,
  request,
  type Account,
  type Instrument,
} from "./ledger";
import { validPortfolio, type Portfolio } from "./ledgerPortfolios";
import type { ImportResult } from "./ledgerImport";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{
  account?: Account;
  accounts: Account[];
  instruments: Instrument[];
  instrumentsError: string;
  instrumentsLoading: boolean;
  initialTab: string;
  refreshKey: number;
}>();
const emit = defineEmits<{
  locked: [boolean];
  create: [];
  imported: [ImportResult];
  instruments: [];
  changed: [];
  deleted: [];
  updated: [];
}>();
const { locked, error, send } = useLedgerWorkspace();
function loadPortfolios() {
  void all<Portfolio>("/portfolios", new AbortController().signal)
    .then((items) => {
      if (items.every((item) => validPortfolio(item))) portfolios.value = items;
    })
    .catch(() => {
      portfolios.value = [];
    });
}
watch(() => props.refreshKey, loadPortfolios, { immediate: true });
const portfolioNames = (accountId: string) =>
  portfolios.value
    .filter((portfolio) => portfolio.account_ids.includes(accountId))
    .map((portfolio) => portfolio.name);
const section = ref<"accounts" | "portfolios">("accounts");
const detailId = ref(props.account?.id ?? "");
const detailAccount = computed(
  () =>
    props.accounts.find((account) => account.id === detailId.value) ??
    (props.account?.id === detailId.value ? props.account : undefined),
);
const importOpen = ref(props.initialTab === "import");
const importDirty = ref(false);
const editId = ref("");
const nameDraft = ref("");
const nameError = ref("");
const savingName = ref(false);
const portfolios = ref<Portfolio[]>([]);
const deleteTarget = ref<Account>();
const remaining = ref(5);
let deleteDeadline = 0;
let deleteTimer: ReturnType<typeof setInterval> | undefined;
function closeDelete() {
  if (locked.value) return;
  clearInterval(deleteTimer);
  deleteTarget.value = undefined;
}
function openDetail(account: Account) {
  if (locked.value) return;
  detailId.value = detailId.value === account.id ? "" : account.id;
}
function closeDetail() {
  if (locked.value) return;
  closeDelete();
  detailId.value = "";
  editId.value = "";
  nameError.value = "";
}
function startEdit(account: Account) {
  if (locked.value) return;
  editId.value = account.id;
  nameDraft.value = account.name;
  nameError.value = "";
}
function cancelEdit() {
  editId.value = "";
  nameError.value = "";
}
async function saveName(account: Account) {
  if (locked.value || savingName.value) return;
  const name = nameDraft.value.trim();
  if (!name) {
    nameError.value = "账户名称不能为空。";
    return;
  }
  if (name === account.name) {
    cancelEdit();
    return;
  }
  savingName.value = true;
  nameError.value = "";
  try {
    const updated = await request<Account>(
      `/accounts/${encodeURIComponent(account.id)}`,
      {
        method: "PATCH",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": newID(),
        },
        body: JSON.stringify({
          name,
          expected_version: account.version,
        }),
      },
    );
    if (
      !updated ||
      updated.id !== account.id ||
      updated.name !== name ||
      updated.version !== (BigInt(account.version) + 1n).toString()
    )
      throw new LedgerError("invalid_response");
    editId.value = "";
    emit("updated");
  } catch (e) {
    nameError.value = errorText(failure(e));
  } finally {
    savingName.value = false;
  }
}
function openImport() {
  if (locked.value) return;
  importDirty.value = false;
  error.value = "";
  importOpen.value = true;
}
function openDelete() {
  if (locked.value || !detailAccount.value) return;
  error.value = "";
  deleteTarget.value = { ...detailAccount.value };
  remaining.value = 5;
  deleteDeadline = performance.now() + 5000;
  clearInterval(deleteTimer);
  deleteTimer = setInterval(() => {
    remaining.value = Math.max(
      0,
      Math.ceil((deleteDeadline - performance.now()) / 1000),
    );
    if (!remaining.value) clearInterval(deleteTimer);
  }, 100);
}
function deleteAccount() {
  const target = deleteTarget.value;
  if (
    !target ||
    locked.value ||
    remaining.value > 0 ||
    performance.now() < deleteDeadline
  )
    return;
  send(
    new PendingWrite<{ account_id: string; deleted: boolean }>(
      `/accounts/${encodeURIComponent(target.id)}`,
      "DELETE",
      {},
    ),
    "删除账户",
    (result) => result?.account_id === target.id && result.deleted === true,
    () => {
      closeDelete();
      detailId.value = "";
      emit("deleted");
    },
  );
}
watch(
  () => props.account?.id,
  (id) => {
    closeDelete();
    detailId.value = id ?? "";
  },
);
watch(detailAccount, (account) => {
  if (!account) closeDetail();
});
onBeforeUnmount(() => clearInterval(deleteTimer));
watch(
  () => props.initialTab,
  (value) => {
    if (value === "import") {
      section.value = "accounts";
      importOpen.value = true;
    }
  },
);
</script>

<template>
  <section class="lp-account-manager" aria-label="管理账户">
    <div class="lp-management-tabs" aria-label="管理范围">
      <button
        :aria-pressed="section === 'accounts'"
        :disabled="locked"
        @click="section = 'accounts'"
      >
        账户
      </button>
      <button
        :aria-pressed="section === 'portfolios'"
        :disabled="locked"
        @click="section = 'portfolios'"
      >
        账户组合
      </button>
    </div>
    <template v-if="section === 'accounts'">
      <header class="lp-manager-heading">
        <div>
          <h2>账户</h2>
          <p class="lp-muted">
            共 {{ accounts.length }} 个账户，点击查看详细资料
          </p>
        </div>
        <div class="lp-actions">
          <button :disabled="locked" @click="openImport">
            导入 Excel 账本
          </button>
          <button
            class="lp-primary"
            data-ledger-focus
            :disabled="locked"
            @click="emit('create')"
          >
            新建账户
          </button>
        </div>
      </header>
      <div v-if="accounts.length" class="lp-management lp-account-directory">
        <div class="lp-account-directory-heading" aria-hidden="true">
          <span>账户名称</span><span>记账币种</span><span>开始记录</span
          ><span />
        </div>
        <ul>
          <li v-for="item in accounts" :key="item.id">
            <button
              class="lp-account-entry"
              :disabled="locked"
              :aria-label="`${detailId === item.id ? '收起' : '查看'}${item.name}账户资料`"
              :aria-expanded="detailId === item.id"
              @click="openDetail(item)"
            >
              <strong class="lp-account-entry-name">{{ item.name }}</strong>
              <span class="lp-account-entry-currency"
                ><small>币种</small>{{ item.currency }}</span
              >
              <span class="lp-account-entry-date"
                ><small>开始记录</small>{{ item.opening_date }}</span
              >
              <span class="lp-account-entry-action" aria-hidden="true"
                >{{ detailId === item.id ? "收起资料" : "查看资料" }} ›</span
              >
            </button>
            <div
              v-if="detailAccount?.id === item.id"
              class="lp-account-detail-panel"
            >
              <dl class="lp-info">
                <div>
                  <dt>账户名称</dt>
                  <dd class="lp-account-name">
                    <template v-if="editId === item.id">
                      <input
                        v-model="nameDraft"
                        maxlength="160"
                        :disabled="savingName"
                        @keyup.enter="saveName(item)"
                        @keyup.esc="cancelEdit"
                      />
                      <button
                        class="lp-primary"
                        :disabled="savingName || locked"
                        @click="saveName(item)"
                      >
                        {{ savingName ? "保存中…" : "保存" }}
                      </button>
                      <button
                        class="lp-text-button"
                        :disabled="savingName"
                        @click="cancelEdit"
                      >
                        取消
                      </button>
                    </template>
                    <template v-else>
                      <span>{{ item.name }}</span>
                      <button
                        class="lp-text-button"
                        :disabled="locked"
                        @click="startEdit(item)"
                      >
                        编辑
                      </button>
                    </template>
                    <small
                      v-if="nameError && editId === item.id"
                      class="lp-error"
                      role="alert"
                      >{{ nameError }}</small
                    >
                  </dd>
                </div>
                <div>
                  <dt>记账币种</dt>
                  <dd>{{ item.currency }}</dd>
                </div>
                <div>
                  <dt>开始记录</dt>
                  <dd>{{ item.opening_date }}</dd>
                </div>
                <div>
                  <dt>所属组合</dt>
                  <dd class="lp-account-portfolios">
                    <template v-if="portfolioNames(item.id).length">
                      <span
                        v-for="name in portfolioNames(item.id)"
                        :key="name"
                        class="lp-pm-chip"
                        >{{ name }}</span
                      >
                    </template>
                    <span v-else class="lp-muted">未加入组合</span>
                  </dd>
                </div>
              </dl>
              <div class="lp-management-secondary">
                <div>
                  <h3>删除账户</h3>
                  <p>删除此账户及其账本数据，不影响其他账户。</p>
                </div>
                <button
                  class="lp-danger-button"
                  :disabled="locked"
                  @click="openDelete"
                >
                  删除账户
                </button>
              </div>
            </div>
          </li>
        </ul>
      </div>
      <div v-else class="lp-management lp-empty">
        <h3>还没有账户</h3>
        <p>在上方新建账户，或导入 Excel 账本开始记录。</p>
      </div>
    </template>
    <LedgerPortfolioManager
      v-else
      :accounts="accounts"
      :refresh-key="refreshKey"
      @changed="emit('changed')"
    />
    <LedgerDialog v-if="deleteTarget" title="确认删除账户" @close="closeDelete">
      <div class="lp-dialog-body">
        <p>确定删除账户「{{ deleteTarget.name }}」吗？</p>
        <p>
          该账户的历史记录、当前现金与持仓、估值历史和自动更新记录将被删除，无法恢复。其他账户和共享证券资料不受影响。
        </p>
        <p>组合中会同步移除此账户；没有剩余成员的组合也会删除。</p>
        <p class="lp-muted">
          为保留操作审计及重复请求保护，系统仍保留审计历史（含历史财务快照、导入原始记录）和请求回执。
        </p>
        <p role="status" aria-live="polite">
          {{
            remaining > 0
              ? `请仔细确认，${remaining} 秒后可删除。`
              : "等待已结束，请确认是否删除。"
          }}
        </p>
        <div class="lp-dialog-footer">
          <button :disabled="locked" @click="closeDelete">取消</button>
          <button
            class="lp-danger-button"
            :disabled="locked || remaining > 0"
            @click="deleteAccount"
          >
            {{ remaining > 0 ? `确认删除（${remaining} 秒）` : "确认删除" }}
          </button>
        </div>
      </div>
    </LedgerDialog>
    <LedgerDialog
      v-if="importOpen"
      title="导入 Excel 账本"
      :dirty="importDirty"
      @close="importOpen = false"
    >
      <div
        class="lp-dialog-body"
        @input="importDirty = true"
        @change="importDirty = true"
      >
        <ImportAccount
          :accounts="accounts"
          :disabled="locked"
          @locked="emit('locked', $event)"
          @imported="
            importOpen = false;
            emit('imported', $event);
          "
        />
      </div>
    </LedgerDialog>
  </section>
</template>

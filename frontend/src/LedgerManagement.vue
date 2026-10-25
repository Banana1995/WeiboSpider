<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import ImportAccount from "./ImportAccount.vue";
import LedgerPortfolioManager from "./LedgerPortfolioManager.vue";
import { PendingWrite, type Account, type Instrument } from "./ledger";
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
}>();
const { locked, navigationLocked, error, send } = useLedgerWorkspace();
const section = ref<"accounts" | "portfolios">("accounts");
const detailId = ref(props.account?.id ?? "");
const detailAccount = computed(
  () =>
    props.accounts.find((account) => account.id === detailId.value) ??
    (props.account?.id === detailId.value ? props.account : undefined),
);
const importOpen = ref(props.initialTab === "import");
const importDirty = ref(false);
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
  if (navigationLocked.value) return;
  detailId.value = account.id;
}
function closeDetail() {
  if (locked.value) return;
  closeDelete();
  detailId.value = "";
}
function openImport() {
  if (navigationLocked.value) return;
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
        :disabled="navigationLocked"
        @click="section = 'accounts'"
      >
        账户
      </button>
      <button
        :aria-pressed="section === 'portfolios'"
        :disabled="navigationLocked"
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
          <button :disabled="navigationLocked" @click="openImport">
            导入 Excel 账本
          </button>
          <button
            class="lp-primary"
            data-ledger-focus
            :disabled="navigationLocked"
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
              :disabled="navigationLocked"
              :aria-label="`查看${item.name}账户资料`"
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
                >查看资料 ›</span
              >
            </button>
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
    <LedgerDialog
      v-if="detailAccount"
      :key="detailAccount.id"
      :title="detailAccount.name"
      caption="账户资料"
      class="lp-account-details-dialog"
      :show-write-status="false"
      @close="closeDetail"
    >
      <div class="lp-management-body">
        <dl class="lp-info">
          <div>
            <dt>账户名称</dt>
            <dd>{{ detailAccount.name }}</dd>
          </div>
          <div>
            <dt>记账币种</dt>
            <dd>{{ detailAccount.currency }}</dd>
          </div>
          <div>
            <dt>开始记录</dt>
            <dd>{{ detailAccount.opening_date }}</dd>
          </div>
          <div>
            <dt>持仓来源</dt>
            <dd>独立维护当前现金与持仓</dd>
          </div>
        </dl>
        <p class="lp-muted">
          账户名称、币种和开始日期创建后不可修改。历史记录可在账户页单独编辑。
        </p>
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
    </LedgerDialog>
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

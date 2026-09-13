<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import ImportAccount from "./ImportAccount.vue";
import LedgerAutoUpdates from "./LedgerAutoUpdates.vue";
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
const tab = ref(props.initialTab === "import" ? "info" : props.initialTab);
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
function openDelete() {
  if (navigationLocked.value || !props.account) return;
  error.value = "";
  deleteTarget.value = { ...props.account };
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
      emit("deleted");
    },
  );
}
watch(() => props.account?.id, closeDelete);
onBeforeUnmount(() => clearInterval(deleteTimer));
watch(
  () => props.initialTab,
  (value) => {
    tab.value = value === "import" ? "info" : value;
    if (value === "import") importOpen.value = true;
  },
);
</script>

<template>
  <section class="lp-management" aria-label="账户管理">
    <div class="lp-management-tabs" aria-label="管理栏目">
      <button
        v-for="item in [
          ['info', '账户资料'],
          ['weekly', '自动更新记录'],
        ]"
        :key="item[0]"
        :aria-pressed="tab === item[0]"
        :disabled="navigationLocked"
        @click="tab = item[0]!"
      >
        {{ item[1] }}
      </button>
    </div>
    <div v-if="tab === 'info'" class="lp-management-body">
      <h2>账户资料</h2>
      <dl v-if="account" class="lp-info">
        <div>
          <dt>账户名称</dt>
          <dd>{{ account.name }}</dd>
        </div>
        <div>
          <dt>记账币种</dt>
          <dd>{{ account.currency }}</dd>
        </div>
        <div>
          <dt>开始记录</dt>
          <dd>{{ account.opening_date }}</dd>
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
          <h3>开启另一份计划</h3>
          <p>账户之间独立记录，不合并资金和持仓。</p>
        </div>
        <button :disabled="locked" @click="emit('create')">新建账户</button>
      </div>
      <div class="lp-management-secondary">
        <div>
          <h3>从文件开始</h3>
          <p>导入 Excel 历史记录，可新建账户或导入到空账户。</p>
        </div>
        <button
          :disabled="locked"
          @click="
            importOpen = true;
            importDirty = false;
            error = '';
          "
        >
          导入 Excel 账本
        </button>
      </div>
      <div v-if="account" class="lp-management-secondary">
        <div>
          <h3>删除账户</h3>
          <p>删除当前账户及其账本数据，此操作不可恢复，不影响其他账户。</p>
        </div>
        <button
          class="lp-danger-button"
          :disabled="navigationLocked"
          @click="openDelete"
        >
          删除账户
        </button>
      </div>
    </div>
    <div v-else class="lp-management-body">
      <LedgerAutoUpdates :account="account" />
    </div>
    <LedgerDialog v-if="deleteTarget" title="确认删除账户" @close="closeDelete">
      <div class="lp-dialog-body">
        <p>确定删除账户「{{ deleteTarget.name }}」吗？</p>
        <p>
          该账户的历史记录、当前现金与持仓、估值历史和自动更新记录将被删除，无法恢复。其他账户和共享证券资料不受影响。
        </p>
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

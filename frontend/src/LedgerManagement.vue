<script setup lang="ts">
import { ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import LedgerHoldings from "./LedgerHoldings.vue";
import ImportAccount from "./ImportAccount.vue";
import LedgerAutoUpdates from "./LedgerAutoUpdates.vue";
import type { Account, Instrument } from "./ledger";
import type { ImportResult } from "./ledgerImport";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
const props = defineProps<{
  account?: Account;
  accounts: Account[];
  instruments: Instrument[];
  instrumentsError: string;
  instrumentsLoading: boolean;
  initialTab: string;
  operationId: string;
  refreshKey: number;
}>();
const emit = defineEmits<{
  locked: [boolean];
  create: [];
  imported: [ImportResult];
  instruments: [];
  changed: [];
}>();
const { locked, error } = useLedgerWorkspace();
const tab = ref(props.initialTab === "import" ? "info" : props.initialTab);
const importOpen = ref(props.initialTab === "import");
const importDirty = ref(false);
watch(
  () => props.initialTab,
  (value) => {
    tab.value = value === "import" ? "info" : value;
    if (value === "import") importOpen.value = true;
  },
);
watch(
  () => props.operationId,
  (id) => {
    if (id) tab.value = "holdings";
  },
);
</script>

<template>
  <section class="lp-management" aria-label="账户管理">
    <div class="lp-management-tabs" aria-label="管理栏目">
      <button
        v-for="item in [
          ['info', '账户资料'],
          ['holdings', '当前持仓'],
          ['weekly', '自动更新记录'],
        ]"
        :key="item[0]"
        :aria-pressed="tab === item[0]"
        :disabled="locked || importOpen"
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
          <dd>
            {{
              account.current_holdings_input === "manual_snapshot"
                ? "独立维护当前持仓"
                : "由交易记录计算"
            }}
          </dd>
        </div>
      </dl>
      <p class="lp-muted">
        账户名称、币种和期初创建后不可修改，当前服务不提供资料编辑。记录可以单独更正。
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
          <p>导入真实 Excel 账本，先预览再确认，仅用于空账户初始化。</p>
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
    </div>
    <LedgerHoldings
      v-else-if="tab === 'holdings'"
      :key="account?.id"
      :account="account"
      :accounts="accounts"
      :instruments="instruments"
      :instruments-error="instrumentsError"
      :instruments-loading="instrumentsLoading"
      :operation-id="operationId"
      :refresh-key="refreshKey"
      @locked="emit('locked', $event)"
      @create="emit('create')"
      @instruments="emit('instruments')"
      @changed="emit('changed')"
    />
    <div v-else class="lp-management-body">
      <LedgerAutoUpdates :account="account" />
    </div>
    <LedgerDialog
      v-if="importOpen"
      title="导入 Excel 账本"
      :caption="account?.name"
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

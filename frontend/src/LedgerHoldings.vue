<script setup lang="ts">
import StockHoldings from "./StockHoldings.vue";
import { type Account, type Instrument } from "./ledger";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

defineProps<{
  account?: Account;
  accounts: Account[];
  instruments: Instrument[];
  instrumentsError: string;
  instrumentsLoading: boolean;
  refreshKey: number;
}>();
const emit = defineEmits<{
  locked: [boolean];
  create: [];
  instruments: [];
  changed: [];
}>();
const { locked } = useLedgerWorkspace();
</script>

<template>
  <section
    class="lp-holdings lp-management-body"
    aria-label="账户持仓"
    data-test="account-holdings"
  >
    <template v-if="account">
      <StockHoldings
        :key="account.id"
        :account="account"
        :refresh-key="refreshKey"
        @changed="
          emit('changed');
          emit('instruments');
        "
      />
    </template>
    <div v-else class="lp-empty">
      <p>先创建账户，再添加持仓。</p>
      <button :disabled="locked" @click="emit('create')">新建账户</button>
    </div>
  </section>
</template>

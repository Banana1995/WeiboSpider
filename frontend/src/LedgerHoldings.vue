<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import CurrentHoldings from "./CurrentHoldings.vue";
import { errorText, request, type Account, type Instrument } from "./ledger";
import { validateHoldings, type HoldingsView } from "./holdings";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{
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
const view = reactive(useLedgerRead<HoldingsView>());
const current = ref<InstanceType<typeof CurrentHoldings>>();
function load() {
  const account = props.account;
  if (!account) return;
  view.clear();
  void view.load(async (signal) =>
    validateHoldings(
      await request<HoldingsView>(
        `/accounts/${encodeURIComponent(account.id)}/holdings`,
        { signal },
      ),
      account,
    ),
  );
}
watch(
  () => [props.account?.id, props.refreshKey],
  () => {
    view.clear();
    load();
  },
  { immediate: true },
);
</script>

<template>
  <section
    class="lp-holdings lp-management-body"
    aria-label="账户持仓"
    data-test="account-holdings"
  >
    <template v-if="account">
      <CurrentHoldings
        ref="current"
        :account-id="account.id"
        :currency="account.currency"
        :instruments="instruments"
        :disabled="locked || instrumentsLoading || !!instrumentsError"
        :refresh-key="refreshKey"
        :valuation="view.data"
        :valuation-loading="view.loading"
        :valuation-error="view.error"
        @refresh="
          current?.load();
          load();
        "
        @locked="emit('locked', $event)"
        @saved="
          load();
          emit('instruments');
        "
      />
      <p v-if="instrumentsError" role="alert">
        {{ errorText(instrumentsError) }}
        <button :disabled="locked" @click="emit('instruments')">
          重新读取证券
        </button>
      </p>
    </template>
    <div v-else class="lp-empty">
      <p>先创建账户，再添加持仓。</p>
      <button :disabled="locked" @click="emit('create')">新建账户</button>
    </div>
  </section>
</template>

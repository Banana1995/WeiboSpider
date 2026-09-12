<script setup lang="ts">
import { reactive, watch } from "vue";
import CurrentHoldings from "./CurrentHoldings.vue";
import { request, type Account, type Instrument } from "./ledger";
import { holdingNumber, validateHoldings, type HoldingsView } from "./holdings";
import { money } from "./ledgerView";
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
function load() {
  const account = props.account;
  if (!account) return;
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
        :account-id="account.id"
        :currency="account.currency"
        :instruments="instruments"
        :disabled="locked || instrumentsLoading || !!instrumentsError"
        :refresh-key="refreshKey"
        @locked="emit('locked', $event)"
        @saved="
          emit('instruments');
          emit('changed');
        "
      />
      <p v-if="instrumentsError" role="alert">
        {{ instrumentsError }}
        <button :disabled="locked" @click="emit('instruments')">
          重新读取证券
        </button>
      </p>
      <div class="lp-section-title">
        <h2>当前参考估值</h2>
        <button :disabled="locked || view.loading" @click="load">
          {{ view.loading ? "刷新中…" : "刷新行情" }}
        </button>
      </div>
      <p class="lp-muted">
        仅供参考，不改变账本总资产或历史收益。总资产记录由每周任务或人工资产记录维护。
      </p>
      <p v-if="view.loading" role="status">正在读取持仓与参考行情…</p>
      <p v-if="view.error" role="alert">{{ view.error }}</p>
      <template v-if="view.data?.configured">
        <div class="lp-holdings-totals">
          <div>
            <span
              >参考总资产 <small>{{ account.currency }}</small></span
            ><strong>{{ money(view.data.total_assets) }}</strong
            ><small>只读参考值，不是账本总资产</small>
          </div>
          <div>
            <span>当前现金</span><strong>{{ money(view.data.cash) }}</strong>
          </div>
        </div>
        <p v-if="!view.data.complete" class="lp-reference" role="status">
          部分行情或汇率不可用，无法计算完整参考估值。
        </p>
        <ul class="lp-business-list">
          <li v-for="item in view.data.items" :key="item.instrument.id">
            <div>
              <strong>{{ item.instrument.name }}</strong
              ><small
                >{{ item.instrument.market }} / {{ item.instrument.code }} ·
                {{ item.instrument.currency }}</small
              >
            </div>
            <div>
              <strong>{{ money(item.market_value) }}</strong
              ><small
                >{{ holdingNumber(item.quantity) }} 份 · 现价
                {{ holdingNumber(item.price, 3) }}</small
              ><small v-if="item.quote_status === 'prior_date'"
                >较早交易日行情</small
              >
            </div>
          </li>
        </ul>
      </template>
    </template>
    <div v-else class="lp-empty">
      <p>先创建账户，再添加持仓。</p>
      <button :disabled="locked" @click="emit('create')">新建账户</button>
    </div>
  </section>
</template>

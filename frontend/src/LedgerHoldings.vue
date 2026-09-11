<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import CurrentHoldings from "./CurrentHoldings.vue";
import LedgerTransactions from "./LedgerTransactions.vue";
import LedgerInstrumentDialog from "./LedgerInstrumentDialog.vue";
import {
  all,
  decimal,
  LedgerError,
  PendingWrite,
  request,
  type Account,
  type AccountDetail,
  type Instrument,
  type Position,
  type Valuation,
} from "./ledger";
import { money, validDay } from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
const props = defineProps<{
  account?: Account;
  accounts: Account[];
  instruments: Instrument[];
  instrumentsError: string;
  instrumentsLoading: boolean;
  operationId: string;
  refreshKey: number;
}>();
const emit = defineEmits<{
  locked: [boolean];
  create: [];
  instruments: [];
  changed: [];
}>();
const { locked, send } = useLedgerWorkspace();
const subtab = ref(props.operationId ? "transactions" : "positions");
const registerOpen = ref(false);
const accountRead = reactive(useLedgerRead<AccountDetail>());
const positions = reactive(useLedgerRead<Position[]>());
const valuation = reactive(useLedgerRead<Valuation>());
const configured = ref(false);
const currentAudit = ref("");
const localRefresh = ref(0);
const manual = computed(
  () => props.account?.current_holdings_input === "manual_snapshot",
);
const replay = computed(
  () => props.account?.current_holdings_input === "transaction_replay",
);
const canValue = computed(
  () => replay.value || (manual.value && configured.value),
);
const instrumentName = (id: string) =>
  props.instruments.find((i) => i.id === id)?.name ?? "未知证券";
const instrumentCurrency = (id: string) =>
  props.instruments.find((i) => i.id === id)?.currency ?? "原币";
function loadHoldings() {
  valuation.clear();
  accountRead.clear();
  positions.clear();
  const a = props.account;
  if (!a || a.current_holdings_input !== "transaction_replay") return;
  void accountRead.load(async (signal) => {
    const result = await request<AccountDetail>(
      `/accounts/${encodeURIComponent(a.id)}`,
      { signal },
    );
    if (
      !result ||
      result.id !== a.id ||
      result.currency !== a.currency ||
      typeof result.cash !== "string" ||
      !decimal(result.cash, 2)
    )
      throw new LedgerError("invalid_response");
    return result;
  });
  void positions.load((signal) =>
    all<Position>(`/accounts/${encodeURIComponent(a.id)}/positions`, signal),
  );
}
function configuredHoldings(value: boolean, audit: string) {
  configured.value = value;
  if (audit !== currentAudit.value || !value) valuation.clear();
  currentAudit.value = audit;
}
function currentSaved() {
  valuation.clear();
  emit("changed");
}
function validValuation(v: Valuation, a: Account) {
  return !!(
    v &&
    v.account_id === a.id &&
    v.currency === a.currency &&
    validDay(v.as_of) &&
    (v.source === "manual_snapshot" || v.source === "transaction_replay") &&
    v.source === a.current_holdings_input &&
    typeof v.complete === "boolean" &&
    typeof v.cash === "string" &&
    decimal(v.cash, 2) &&
    typeof v.known_positions_value === "string" &&
    decimal(v.known_positions_value, 2) &&
    (v.positions_value === null ||
      (typeof v.positions_value === "string" &&
        decimal(v.positions_value, 2))) &&
    (v.total_assets === null ||
      (typeof v.total_assets === "string" && decimal(v.total_assets, 2))) &&
    (!v.complete || v.total_assets !== null) &&
    Array.isArray(v.items) &&
    v.items.length <= 200 &&
    v.items.every(
      (p) =>
        p &&
        typeof p.instrument_id === "string" &&
        typeof p.quantity === "string" &&
        decimal(p.quantity, 6) &&
        ["current", "prior_date", "unavailable", "closed"].includes(p.status) &&
        (p.market_value === null ||
          (typeof p.market_value === "string" && decimal(p.market_value, 2))),
    )
  );
}
function preview() {
  if (locked.value || !props.account || !canValue.value) return;
  const a = props.account;
  valuation.clear();
  void valuation.load(async (signal) => {
    const v = await request<Valuation>(
      `/accounts/${encodeURIComponent(a.id)}/valuation`,
      { signal },
    );
    if (!validValuation(v, a) || v.history_id)
      throw new LedgerError("invalid_response");
    return v;
  });
}
function saveValuation() {
  const a = props.account;
  if (
    !a ||
    locked.value ||
    !canValue.value ||
    valuation.loading ||
    valuation.error ||
    !valuation.data?.complete
  )
    return;
  send(
    new PendingWrite<Valuation>(
      `/accounts/${encodeURIComponent(a.id)}/valuation`,
      "POST",
      {},
    ),
    "更新总资产",
    (v) =>
      validValuation(v, a) &&
      v.complete &&
      /^[1-9]\d*$/.test(v.history_id ?? ""),
    () => valuation.clear(),
  );
}
watch(
  () => props.account?.id,
  () => {
    configured.value = false;
    currentAudit.value = "";
    loadHoldings();
  },
  { immediate: true },
);
watch(
  () => props.operationId,
  (id) => {
    if (id) subtab.value = "transactions";
  },
);
watch(
  () => props.refreshKey,
  () => {
    localRefresh.value++;
    loadHoldings();
  },
);
</script>

<template>
  <section
    class="lp-holdings lp-management-body"
    aria-label="账户持仓"
    data-test="account-holdings"
  >
    <div v-if="!manual" class="lp-section-title">
      <div v-if="replay" class="lp-segment" aria-label="持仓管理">
        <button
          :aria-pressed="subtab === 'positions'"
          :disabled="locked"
          @click="subtab = 'positions'"
        >
          当前持仓</button
        ><button
          :aria-pressed="subtab === 'transactions'"
          :disabled="locked"
          @click="subtab = 'transactions'"
        >
          持仓交易
        </button>
      </div>
      <span v-else />
      <button
        :disabled="locked || instrumentsLoading || !!instrumentsError"
        @click="registerOpen = true"
      >
        登记证券
      </button>
    </div>
    <p v-if="instrumentsLoading" role="status">正在读取证券…</p>
    <div v-if="instrumentsError">
      <p class="lp-error" role="alert">{{ instrumentsError }}</p>
      <button :disabled="locked" @click="emit('instruments')">
        重新读取证券
      </button>
    </div>
    <template v-if="account">
      <LedgerTransactions
        v-if="replay && subtab === 'transactions'"
        :account="account"
        :accounts="accounts"
        :instruments="instruments"
        :positions="positions.data ?? []"
        :positions-fresh="
          !!positions.data && !positions.loading && !positions.error
        "
        :refresh-key="refreshKey"
        :operation-id="operationId"
      />
      <template v-else>
        <CurrentHoldings
          v-if="manual"
          :account-id="account.id"
          :currency="account.currency"
          :instruments="instruments"
          :disabled="locked || instrumentsLoading || !!instrumentsError"
          :refresh-key="localRefresh"
          @locked="emit('locked', $event)"
          @configured="configuredHoldings"
          @saved="currentSaved"
        >
          <template #actions>
            <button
              :disabled="locked || instrumentsLoading || !!instrumentsError"
              @click="registerOpen = true"
            >
              登记证券
            </button>
          </template>
        </CurrentHoldings>
        <template v-else-if="replay"
          ><h2>当前持仓</h2>
          <p class="lp-muted">由期初与有效交易计算，不允许用手工持仓覆盖。</p>
          <p v-if="accountRead.loading || positions.loading" role="status">
            正在读取当前持仓…
          </p>
          <p
            v-if="accountRead.error || positions.error"
            class="lp-error"
            role="alert"
          >
            {{ accountRead.error || positions.error }}
          </p>
          <div class="lp-holding-summary">
            <span
              >当前现金 <small>{{ account.currency }}</small></span
            ><strong>{{ money(accountRead.data?.cash) }}</strong
            ><small>现金不是总资产</small>
          </div>
          <ul class="lp-business-list">
            <li v-for="p in positions.data" :key="p.instrument_id">
              <div>
                <strong>{{ instrumentName(p.instrument_id) }}</strong
                ><small>数量 {{ money(p.quantity) }}</small>
                <details>
                  <summary>成本与损益</summary>
                  <p>
                    剩余成本 {{ money(p.remaining_cost) }} · 移动平均成本
                    {{ money(p.moving_average) }}
                  </p>
                  <p>
                    摊薄基数 {{ money(p.diluted_basis) }} · 摊薄单位成本
                    {{ money(p.diluted_cost) }}
                  </p>
                  <p>
                    已实现价差 {{ money(p.realized_profit) }} · 分红
                    {{ money(p.dividends) }} {{ account.currency }}
                  </p>
                  <small>分红归属周期：{{ p.cycle_id }}</small>
                </details>
              </div>
            </li>
          </ul>
          <p v-if="positions.data?.length === 0" class="lp-muted">
            暂无证券持仓。
          </p></template
        >
        <div class="lp-management-secondary">
          <div>
            <h3>更新总资产</h3>
            <p>
              先预览参考估值，再明确保存为一笔总资产记录。保存时服务会重新取价。
            </p>
          </div>
          <button
            :disabled="locked || !canValue || valuation.loading"
            @click="preview"
          >
            预览当前估值
          </button>
        </div>
        <p v-if="!canValue" class="lp-muted">
          先保存当前现金与证券数量，才能预览估值；零现金、无证券也可明确保存。
        </p>
        <p v-if="valuation.loading" role="status">
          正在获取参考报价，仅预览，不保存…
        </p>
        <p v-if="valuation.error" class="lp-error" role="alert">
          {{ valuation.error }}
        </p>
        <div v-if="valuation.data" class="lp-valuation-preview">
          <div class="lp-holding-summary">
            <span
              >参考总资产 <small>{{ account.currency }}</small></span
            ><strong>{{ money(valuation.data.total_assets) }}</strong
            ><small
              >{{ valuation.data.as_of }} ·
              {{
                valuation.data.complete
                  ? "估值完整，尚未保存"
                  : "报价不完整，不能保存总额"
              }}</small
            >
          </div>
          <p>
            现金 {{ money(valuation.data.cash) }} · 已知证券市值
            {{ money(valuation.data.known_positions_value) }}
            {{ account.currency }}
          </p>
          <ul class="lp-business-list">
            <li v-for="item in valuation.data.items" :key="item.instrument_id">
              <div>
                <strong>{{ instrumentName(item.instrument_id) }}</strong
                ><small
                  >数量 {{ money(item.quantity) }} ·
                  {{
                    item.status === "unavailable"
                      ? "报价不可用"
                      : item.status === "prior_date"
                        ? "较早日期参考价"
                        : item.status === "closed"
                          ? "已清仓"
                          : "参考报价"
                  }}</small
                >
                <p v-if="item.quote">
                  价格 {{ money(item.quote.price) }}
                  {{ instrumentCurrency(item.instrument_id) }} ·
                  {{ item.quote.date }}
                </p>
                <small v-if="item.fx"
                  >参考汇率 {{ item.fx.rate }} · {{ item.fx.date }}</small
                >
              </div>
              <strong>{{ money(item.market_value) }}</strong>
            </li>
          </ul>
          <p class="lp-field-hint">
            参考报价不代表券商结算价。缺少任一必要报价时，已知市值小计不等于总资产。
          </p>
          <button
            class="lp-primary"
            :disabled="locked || !valuation.data.complete || valuation.loading"
            @click="saveValuation"
          >
            更新并保存总资产
          </button>
        </div>
      </template>
    </template>
    <div v-else class="lp-empty">
      <p>先创建账户；证券可以提前登记。</p>
      <button :disabled="locked" @click="emit('create')">新建账户</button>
    </div>
    <LedgerInstrumentDialog
      v-if="registerOpen"
      :instruments="instruments"
      @close="registerOpen = false"
      @registered="
        registerOpen = false;
        emit('instruments');
      "
    />
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import CurrentHoldings from "./CurrentHoldings.vue";
import LedgerDialog from "./LedgerDialog.vue";
import LedgerHoldingDetail from "./LedgerHoldingDetail.vue";
import LedgerInstrumentDialog from "./LedgerInstrumentDialog.vue";
import LedgerTransactions from "./LedgerTransactions.vue";
import {
  all,
  decimal,
  PendingWrite,
  request,
  type Account,
  type Instrument,
  type Position,
  type Valuation,
} from "./ledger";
import { holdingNumber, validateHoldings, type HoldingsView } from "./holdings";
import { money, validDay, versionString } from "./ledgerView";
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
const { locked, error, send } = useLedgerWorkspace();
const view = reactive(useLedgerRead<HoldingsView>());
const positions = reactive(useLedgerRead<Position[]>());
const subtab = ref(props.operationId ? "transactions" : "positions");
const focusedOperation = ref(props.operationId);
const registerOpen = ref(false);
const selectOpen = ref(false);
const saveOpen = ref(false);
const selectedInstrument = ref<Instrument>();
const showClosed = ref(false);
const replay = computed(
  () => props.account?.current_holdings_input === "transaction_replay",
);
const items = computed(
  () =>
    view.data?.items.filter(
      (item) => showClosed.value || item.cost_status !== "closed",
    ) ?? [],
);
const selectedItem = computed(() =>
  view.data?.items.find(
    (item) => item.instrument.id === selectedInstrument.value?.id,
  ),
);
const fresh = computed(() => !!view.data && !view.loading && !view.error);
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
function loadPositions() {
  positions.clear();
  if (!props.account || !replay.value || subtab.value !== "transactions")
    return;
  void positions.load((signal) =>
    all<Position>(
      `/accounts/${encodeURIComponent(props.account!.id)}/positions`,
      signal,
    ),
  );
}
function inspect(instrument: Instrument) {
  if (locked.value) return;
  selectedInstrument.value = instrument;
  selectOpen.value = false;
  error.value = "";
}
function operation(id = "") {
  selectedInstrument.value = undefined;
  focusedOperation.value = id;
  subtab.value = "transactions";
}
function saveAssets() {
  const a = props.account;
  if (locked.value || !a || !fresh.value || !view.data?.complete) return;
  send(
    new PendingWrite<Valuation>(
      `/accounts/${encodeURIComponent(a.id)}/valuation`,
      "POST",
      {},
    ),
    "更新总资产",
    (result) => {
      const amount = (v: unknown): v is string =>
        typeof v === "string" && decimal(v, 2) && !v.startsWith("-");
      const cents = (v: string) => {
        const [w, f = ""] = v.split(".");
        return BigInt(w! + f.padEnd(2, "0"));
      };
      if (!(
        !!result &&
        result.account_id === a.id &&
        result.currency === a.currency &&
        result.source === a.current_holdings_input &&
        result.complete === true &&
        amount(result.total_assets) &&
        amount(result.cash) &&
        amount(result.positions_value) &&
        amount(result.known_positions_value) &&
        validDay(result.as_of) &&
        versionString(result.history_id) &&
        Array.isArray(result.items) &&
        result.items.length <= 200 &&
        result.items.every(
          (item) =>
            item &&
            typeof item.instrument_id === "string" &&
            typeof item.quantity === "string" &&
            decimal(item.quantity, 6) &&
            !item.quantity.startsWith("-") &&
            ["current", "prior_date", "closed"].includes(item.status) &&
            amount(item.market_value) &&
            (item.status === "closed"
              ? !/[1-9]/.test(item.quantity)
              : !!item.quote &&
                typeof item.quote.price === "string" &&
                decimal(item.quote.price, 6) &&
                !item.quote.price.startsWith("-") &&
                /[1-9]/.test(item.quote.price) &&
                validDay(item.quote.date)),
        )
      ))
        return false;
      return (
        cents(result.cash) + cents(result.positions_value!) ===
          cents(result.total_assets!) &&
        cents(result.known_positions_value) ===
          cents(result.positions_value!) &&
        result.items.reduce(
          (sum, item) => sum + cents(item.market_value!),
          0n,
        ) === cents(result.positions_value!)
      );
    },
    () => {
      saveOpen.value = false;
    },
  );
}
watch(
  () => props.account?.id,
  () => {
    selectedInstrument.value = undefined;
    view.clear();
    load();
    loadPositions();
  },
  { immediate: true },
);
watch(
  () => props.refreshKey,
  () => {
    load();
    loadPositions();
  },
);
watch(subtab, (value) => {
  if (value === "positions") focusedOperation.value = "";
  loadPositions();
});
watch(
  () => props.operationId,
  (id) => {
    if (id) {
      focusedOperation.value = id;
      subtab.value = "transactions";
    }
  },
);
</script>

<template>
  <section
    class="lp-holdings lp-management-body"
    aria-label="账户持仓"
    data-test="account-holdings"
  >
    <div class="lp-section-title lp-holdings-title">
      <h2>当前持仓</h2>
      <div class="lp-actions">
        <button
          :disabled="locked || instrumentsLoading || !!instrumentsError"
          @click="registerOpen = true"
        >
          登记证券
        </button>
        <CurrentHoldings
          v-if="account && !replay"
          editor-only
          :account-id="account.id"
          :currency="account.currency"
          :opening-date="account.opening_date"
          :instruments="instruments"
          :disabled="locked || instrumentsLoading || !!instrumentsError"
          :refresh-key="refreshKey"
          @locked="emit('locked', $event)"
          @saved="emit('changed')"
        />
        <button :disabled="locked || view.loading || !account" @click="load">
          {{ view.loading ? "刷新中…" : "刷新行情" }}
        </button>
      </div>
    </div>
    <div v-if="replay" class="lp-segment" aria-label="持仓管理">
      <button
        :aria-pressed="subtab === 'positions'"
        :disabled="locked"
        @click="subtab = 'positions'"
      >
        持仓一览</button
      ><button
        :aria-pressed="subtab === 'transactions'"
        :disabled="locked"
        @click="subtab = 'transactions'"
      >
        持仓交易
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
        :operation-id="focusedOperation"
      />
      <template v-else>
        <p v-if="view.loading" class="lp-muted" role="status">
          正在读取持仓与参考行情…
        </p>
        <p v-if="view.error" class="lp-error" role="alert">{{ view.error }}</p>
        <template v-if="view.data">
          <div v-if="view.data.configured" class="lp-holdings-totals">
            <div>
              <span
                >参考总资产 <small>{{ account.currency }}</small></span
              ><strong>{{ money(view.data.total_assets) }}</strong
              ><small>仅展示，尚未保存为账户记录</small>
            </div>
            <div>
              <span
                >当前现金 <small>{{ account.currency }}</small></span
              ><strong>{{ money(view.data.cash) }}</strong
              ><small>仓位按含现金总资产计算</small>
            </div>
          </div>
          <div v-else class="lp-empty">
            <h3>尚未设置当前持仓</h3>
            <p>
              先用“调整现金与持仓”录入真实期初现金与数量，再新增买卖记录。也可设置纯现金期初。
            </p>
          </div>
          <p
            v-if="view.data.configured && !view.data.complete"
            class="lp-reference"
            role="status"
          >
            部分行情或汇率不可用，总资产与仓位暂不计算；已知市值不能充当账户总资产。
          </p>
          <div v-if="view.data.configured" class="lp-holdings-tools">
            <label class="lp-check"
              ><input v-model="showClosed" type="checkbox" />显示已清仓</label
            ><button
              v-if="!replay"
              :disabled="
                locked || !fresh || instrumentsLoading || !!instrumentsError
              "
              @click="selectOpen = true"
            >
              新增买卖 / 分红
            </button>
          </div>
          <div
            v-if="items.length"
            class="lp-holding-columns"
            aria-hidden="true"
          >
            <span>证券</span><span>市值 / 数量</span><span>现价 / 持仓成本</span
            ><span>摊薄成本</span><span>仓位</span>
          </div>
          <ul class="lp-holding-list">
            <li v-for="item in items" :key="item.instrument.id">
              <button
                class="lp-holding-row"
                :disabled="locked"
                :title="`查看${item.instrument.name}持仓详情`"
                @click="inspect(item.instrument)"
              >
                <span class="lp-holding-security"
                  ><strong>{{ item.instrument.name }}</strong
                  ><small
                    ><b class="lp-market-tag">{{ item.instrument.market }}</b
                    >{{ item.instrument.code }} ·
                    {{ item.instrument.currency }}</small
                  ><small v-if="item.cost_status === 'closed'"
                    >已清仓</small
                  ></span
                >
                <span class="lp-holding-value"
                  ><span class="lp-mobile-label">市值 / 数量</span
                  ><strong>{{ money(item.market_value) }}</strong
                  ><small>{{ holdingNumber(item.quantity) }} 份</small></span
                >
                <span class="lp-holding-prices"
                  ><span class="lp-mobile-label">现价 / 持仓成本</span
                  ><strong>{{ holdingNumber(item.price, 3) }}</strong
                  ><small>{{
                    item.holding_cost === null
                      ? item.cost_status === "closed"
                        ? "—"
                        : "成本未知"
                      : holdingNumber(item.holding_cost, 3)
                  }}</small></span
                >
                <span class="lp-holding-diluted"
                  ><span class="lp-mobile-label">摊薄成本</span
                  ><strong>{{ holdingNumber(item.diluted_cost, 3) }}</strong
                  ><small>{{
                    item.quote_status === "prior_date"
                      ? "较早日期行情"
                      : item.quote_status === "unavailable"
                        ? "行情不完整"
                        : item.cost_status === "unknown"
                          ? "期初依据不完整"
                          : "含手续费、分红"
                  }}</small></span
                >
                <span class="lp-holding-weight"
                  ><span class="lp-mobile-label">仓位</span
                  ><strong>{{
                    item.weight === null ? "—" : `${item.weight}%`
                  }}</strong
                  ><small>查看详情</small></span
                >
              </button>
            </li>
          </ul>
          <p v-if="view.data.configured && !items.length" class="lp-muted">
            暂无{{
              showClosed ? "" : "在持"
            }}证券。可选择已登记证券新增买入，或查看已清仓持仓。
          </p>
          <div class="lp-holdings-footnote">
            <p>
              市值、现价和两种成本按证券原币展示；仓位先折算到账户币种。点击持仓可查看买卖、分红和成本口径。
            </p>
            <p>
              行情为请求时参考值，可能为较早交易日，不是券商实时结算价。成本未知不代表零成本。
            </p>
          </div>
          <div v-if="view.data.configured" class="lp-management-secondary">
            <div>
              <h3>保存总资产</h3>
              <p>只有明确保存才新增账户总资产记录；保存时会重新获取行情。</p>
            </div>
            <button
              :disabled="locked || !fresh || !view.data.complete"
              @click="
                saveOpen = true;
                error = '';
              "
            >
              更新并保存总资产
            </button>
          </div>
        </template>
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
    <LedgerDialog
      v-if="selectOpen"
      title="选择证券"
      caption="新增买卖或分红"
      @close="selectOpen = false"
    >
      <div class="lp-dialog-body">
        <p class="lp-field-hint">
          买入尚未持有的证券也从这里开始；新证券请先登记。
        </p>
        <ul class="lp-business-list">
          <li v-for="instrument in instruments" :key="instrument.id">
            <button
              class="lp-security-choice"
              :disabled="locked"
              @click="inspect(instrument)"
            >
              <strong>{{ instrument.name }}</strong
              ><small
                >{{ instrument.market }} / {{ instrument.code }} ·
                {{ instrument.currency }}</small
              >
            </button>
          </li>
        </ul>
        <p v-if="!instruments.length">尚未登记证券，请先关闭此窗并登记证券。</p>
      </div>
    </LedgerDialog>
    <LedgerHoldingDetail
      v-if="account && selectedInstrument"
      :key="`${account.id}/${selectedInstrument.id}`"
      :account="account"
      :instrument="selectedInstrument"
      :item="selectedItem"
      :view="view.data"
      :refresh-key="refreshKey"
      :loading="view.loading"
      :read-error="view.error"
      @close="selectedInstrument = undefined"
      @refresh="load"
      @operation="operation"
    />
    <LedgerDialog
      v-if="saveOpen"
      title="保存总资产记录"
      :caption="account?.name"
      @close="saveOpen = false"
    >
      <div class="lp-dialog-body">
        <p>
          将根据当前现金与持仓重新获取报价，完整估值成功后新增一笔总资产记录。不覆盖旧记录。
        </p>
        <p v-if="view.data">
          本次参考总资产：{{ money(view.data.total_assets) }}
          {{ account?.currency }}。最终以保存时的完整估值为准。
        </p>
        <button
          class="lp-primary"
          :disabled="locked || !fresh || !view.data?.complete"
          @click="saveAssets"
        >
          确认更新并保存
        </button>
      </div>
    </LedgerDialog>
  </section>
</template>

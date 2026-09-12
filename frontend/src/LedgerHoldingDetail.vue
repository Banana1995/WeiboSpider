<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import LedgerHoldingTradeForm from "./LedgerHoldingTradeForm.vue";
import { request, type Account, type Instrument, type Page } from "./ledger";
import {
  tradeLabels,
  holdingNumber,
  validateHoldingTransactions,
  type HoldingItem,
  type HoldingsView,
  type HoldingTransaction,
} from "./holdings";
import { money } from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
const props = defineProps<{
  account: Account;
  instrument: Instrument;
  item?: HoldingItem;
  view?: HoldingsView;
  refreshKey: number;
  loading: boolean;
  readError: string;
}>();
const emit = defineEmits<{ close: []; refresh: []; operation: [string] }>();
const { locked, error } = useLedgerWorkspace();
const history = reactive(useLedgerRead<Page<HoldingTransaction>>());
const page = ref(0);
const cursors = ref([""]);
const adding = ref(false);
const dirty = ref(false);
const fresh = computed(
  () => !!props.view && !props.loading && !props.readError,
);
const manual = computed(
  () => props.account.current_holdings_input === "manual_snapshot",
);
function load(index = 0) {
  if (locked.value) return;
  history.clear();
  page.value = index;
  const path = `/accounts/${encodeURIComponent(props.account.id)}/holdings/${encodeURIComponent(props.instrument.id)}/transactions?limit=20${cursors.value[index] ? `&cursor=${encodeURIComponent(cursors.value[index]!)}` : ""}`;
  void history.load(async (signal) =>
    validateHoldingTransactions(
      await request<Page<HoldingTransaction>>(path, { signal }),
    ),
  );
}
function next() {
  const cursor = history.data?.next_cursor;
  if (locked.value || !cursor || history.loading) return;
  cursors.value.splice(page.value + 1, Infinity, cursor);
  load(page.value + 1);
}
function cancelAdd() {
  if (
    locked.value ||
    (dirty.value && !window.confirm("放弃本次尚未保存的买卖 / 分红记录？"))
  )
    return;
  adding.value = false;
  dirty.value = false;
}
function saved() {
  adding.value = false;
  dirty.value = false;
}
function refresh() {
  if (locked.value || props.loading) return;
  emit("refresh");
  cursors.value = [""];
  load();
}
watch(
  () => props.refreshKey,
  () => {
    cursors.value = [""];
    load();
  },
  { immediate: true },
);
</script>

<template>
  <LedgerDialog
    :title="`${instrument.name} (${instrument.code}.${instrument.market})`"
    :caption="`${account.name} · ${instrument.currency}`"
    :dirty="dirty"
    @close="emit('close')"
  >
    <div class="lp-position-detail lp-dialog-body">
      <div class="lp-position-quote">
        <div>
          <span
            >最新参考价 <small>{{ instrument.currency }}</small></span
          ><strong>{{ holdingNumber(item?.price, 3) }}</strong>
        </div>
        <button
          class="lp-text-button"
          :disabled="locked || loading"
          @click="refresh"
        >
          {{ adding ? "刷新持仓依据（保留草稿）" : "刷新行情" }}
        </button>
      </div>
      <p v-if="loading" role="status">正在刷新持仓，暂不能新增交易…</p>
      <p v-if="readError" class="lp-error" role="alert">{{ readError }}</p>
      <p v-if="item?.quote" class="lp-field-hint">
        {{ item.quote.source }} · {{ item.quote.quoted_at
        }}{{ item.quote_status === "prior_date" ? "（较早日期参考价）" : "" }}
      </p>
      <dl v-if="item" class="lp-position-metrics">
        <div>
          <dt>持仓市值</dt>
          <dd>{{ money(item.market_value) }}</dd>
          <small>{{ instrument.currency }}</small>
        </div>
        <div>
          <dt>持仓数量</dt>
          <dd>{{ holdingNumber(item.quantity) }}</dd>
          <small>{{ item.cost_status === "closed" ? "已清仓" : "份" }}</small>
        </div>
        <div>
          <dt>仓位</dt>
          <dd>{{ item.weight === null ? "—" : `${item.weight}%` }}</dd>
          <small>含现金总资产</small>
        </div>
        <div>
          <dt>持仓成本</dt>
          <dd>{{ holdingNumber(item.holding_cost, 3) }}</dd>
          <small>{{ instrument.currency }} / 份</small>
        </div>
        <div>
          <dt>摊薄成本</dt>
          <dd>{{ holdingNumber(item.diluted_cost, 3) }}</dd>
          <small>{{ instrument.currency }} / 份</small>
        </div>
        <div>
          <dt>折算市值</dt>
          <dd>{{ money(item.account_market_value) }}</dd>
          <small>{{ account.currency }}</small>
        </div>
      </dl>
      <p v-else-if="fresh" class="lp-muted">
        当前未持有此证券，可查看历史记录或新增买入。
      </p>
      <p v-if="item?.cost_status === 'unknown'" class="lp-reference">
        期初买入依据不完整，两种成本暂为未知。仅补入后续交易不会自动补全期初成本；清仓后重新建仓开启新的完整周期。
      </p>
      <p v-if="view?.configured && !view.complete" class="lp-field-hint">
        账户行情或汇率不完整，仓位暂不计算。未知价格、成本不显示为零。
      </p>
      <p v-if="item?.fx" class="lp-field-hint">
        折算汇率 {{ instrument.currency }}/{{ account.currency }}：{{
          item.fx.rate
        }}，{{ item.fx.date }} · {{ item.fx.source }}
      </p>
      <details class="lp-cost-definition">
        <summary>成本与仓位怎么算？</summary>
        <p>持仓成本 = 持有期内累计买入金额 / 累计买入数量。</p>
        <p>
          摊薄成本 =（累计买入金额 − 累计卖出金额 − 累计分红）/ 当前持仓数量。
        </p>
        <p>
          买入金额含手续费，卖出金额扣除手续费；均按证券原币计算。部分卖出不减少累计买入数量，清仓后重新买入开启新周期，摊薄成本可为负。
        </p>
        <p>
          仓位 = 本证券折算市值 /（现金 +
          全部证券折算市值）。所有必要行情和汇率完整时才计算。
        </p>
      </details>
      <div class="lp-section-title lp-trade-heading">
        <h3>买卖与分红记录</h3>
        <button
          v-if="manual"
          :disabled="locked || !fresh || !view?.configured || adding"
          @click="
            adding = true;
            error = '';
          "
        >
          ＋ 新增记录</button
        ><button v-else :disabled="locked" @click="emit('operation', '')">
          管理交易
        </button>
      </div>
      <template v-if="adding && view?.manual_version && view.trade_date_floor">
        <p class="lp-field-hint">
          新增记录会增减当前现金与数量，不改写历史总资产。最早可录入
          {{
            view.trade_date_floor
          }}，请按账户内交易日期顺序提交，同日按录入顺序计算。
        </p>
        <p class="lp-field-hint">
          分红归属最新持有周期；已重新建仓后的旧周期迟到分红暂不能在这里录入。本轮明细只支持新增，请确认无误后保存。
        </p>
        <LedgerHoldingTradeForm
          :account="account"
          :instrument="instrument"
          :expected-version="view.manual_version"
          :min-date="view.trade_date_floor"
          :disabled="!fresh"
          @saved="saved"
          @dirty="dirty = $event"
          @cancel="cancelAdd"
        />
      </template>
      <p v-if="history.loading" role="status">正在读取买卖记录…</p>
      <div v-if="history.error">
        <p class="lp-error" role="alert">{{ history.error }}</p>
        <button
          :disabled="locked"
          @click="
            cursors = [''];
            load();
          "
        >
          重新读取记录
        </button>
      </div>
      <p v-if="history.data?.items.length === 0" class="lp-muted">
        暂无买卖或分红记录。手工持仓数量不是买入明细，不会自动生成历史记录。
      </p>
      <ol class="lp-security-trades">
        <li v-for="row in history.data?.items" :key="row.id">
          <div class="lp-trade-main">
            <div>
              <strong>{{ tradeLabels[row.kind] }}</strong
              ><small>{{
                row.cycle_id === item?.cycle_id ? "当前周期" : "历史周期"
              }}</small>
            </div>
            <div v-if="row.kind !== 'dividend'">
              <strong>{{ holdingNumber(row.quantity) }} 份</strong
              ><small>成交价 {{ holdingNumber(row.price, 3) }}</small>
            </div>
            <div>
              <strong>{{ money(row.amount) }}</strong
              ><small
                >{{
                  row.kind === "buy"
                    ? "净支出"
                    : row.kind === "sell"
                      ? "净收入"
                      : "分红总额"
                }}
                {{ instrument.currency }}</small
              >
            </div>
          </div>
          <div class="lp-trade-meta">
            <time :datetime="row.date">{{ row.date }}</time
            ><span v-if="row.kind !== 'dividend'"
              >手续费 {{ row.fee === null ? "未填写" : money(row.fee) }}</span
            ><button
              v-if="row.operation_id"
              class="lp-text-button"
              :disabled="locked"
              @click="emit('operation', row.operation_id!)"
            >
              查看 / 修改交易
            </button>
          </div>
          <p v-if="row.note" class="lp-note-text">{{ row.note }}</p>
        </li>
      </ol>
      <div v-if="history.data" class="lp-trade-pagination">
        <span>第 {{ page + 1 }} 页 · 包含历史持有期</span>
        <div class="lp-actions">
          <button
            :disabled="locked || history.loading || page === 0"
            @click="load(page - 1)"
          >
            上一页</button
          ><button
            :disabled="locked || history.loading || !history.data.next_cursor"
            @click="next"
          >
            下一页
          </button>
        </div>
      </div>
    </div>
  </LedgerDialog>
</template>

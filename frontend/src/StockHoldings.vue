<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import LedgerDisclosure from "./LedgerDisclosure.vue";
import StockTradeDialog from "./StockTradeDialog.vue";
import StockSyncStatus from "./StockSyncStatus.vue";
import StockFxReference from "./StockFxReference.vue";
import StockClosedDetail from "./StockClosedDetail.vue";
import { errorText, request, type Account } from "./ledger";
import { holdingMoney, holdingNumber } from "./holdings";
import {
  stockLabels,
  stockTone,
  validateStockBook,
  type ClosedItem,
  type ClosedRecord,
  type StockBook,
  type StockEntry,
  type StockItem,
} from "./stockBook";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import "./stockBook.css";

const props = defineProps<{ account: Account; refreshKey: number }>();
const emit = defineEmits<{ changed: [] }>();
const { locked } = useLedgerWorkspace();
const read = reactive(useLedgerRead<StockBook>());
const expanded = ref(false);
const selected = ref("");
const selectedClosed = ref("");
type StockFilter = "open" | "all" | "closed";
const filter = ref<StockFilter>("open");
const filterOptions: { key: StockFilter; label: string }[] = [
  { key: "open", label: "当前持仓" },
  { key: "all", label: "全部" },
  { key: "closed", label: "已清仓" },
];
const limit = ref(30);
const showVoided = ref(false);
const detail = computed(() =>
  read.data?.items.find((row) => row.instrument.id === selected.value),
);
type SortKey = "value" | "weight" | "profit";
const sortOptions: { key: SortKey; label: string }[] = [
  { key: "value", label: "市值" },
  { key: "weight", label: "持仓比例" },
  { key: "profit", label: "持仓盈亏" },
];
// Positions open sorted by their share of the account, largest first.
const sortKey = ref<SortKey | "">("weight");
const sortDir = ref<1 | -1>(-1);
const accountCurrencyName = computed(
  () => ({ CNY: "人民币", HKD: "港币", USD: "美元" })[props.account.currency],
);
const rows = computed(() => {
  const list =
    read.data?.items.filter(
      (row) =>
        filter.value === "all" ||
        (filter.value === "open" && /[1-9]/.test(row.quantity)),
    ) ?? [];
  const key = sortKey.value;
  if (!key) return list;
  const amount = (row: StockItem) =>
    magnitude(
      key === "value"
        ? row.account_market_value
        : key === "weight"
          ? row.weight
          : row.metrics.profit,
    );
  return [...list].sort((a, b) => {
    const left = amount(a);
    const right = amount(b);
    if (left === null && right === null) return 0;
    if (left === null) return 1;
    if (right === null) return -1;
    if (left === right) return 0;
    return (left < right ? -1 : 1) * sortDir.value;
  });
});
type ClosedWithRecord = ClosedItem & { closed: ClosedRecord };
const closedRows = computed<ClosedWithRecord[]>(() =>
  (read.data?.closed ?? []).filter(
    (row): row is ClosedWithRecord => !!row.closed,
  ),
);
const closedDetail = computed(() =>
  closedRows.value.find((row) => row.id === selectedClosed.value),
);
const showPositions = computed(() => filter.value !== "closed");
const showClosed = computed(() => filter.value !== "open");
const referenceRates = computed(() => {
  const seen = new Set<string>();
  return rows.value.flatMap((row) => {
    const fx = row.fx;
    if (
      !fx ||
      row.instrument.currency === props.account.currency ||
      fx.base !== row.instrument.currency ||
      fx.quote !== props.account.currency
    )
      return [];
    const key = [fx.base, fx.quote, fx.rate, fx.date, fx.source].join("|");
    if (seen.has(key)) return [];
    seen.add(key);
    return [{ key, fx }];
  });
});
// Scale every numeric column to the same six decimals so BigInt ordering is exact.
function magnitude(value: string | null): bigint | null {
  if (value == null) return null;
  const negative = value.startsWith("-");
  const [whole, fraction = ""] = value.replace("-", "").split(".");
  const scaled = BigInt(whole! + fraction.padEnd(6, "0"));
  return negative ? -scaled : scaled;
}
function sort(key: SortKey) {
  if (sortKey.value === key) sortDir.value = sortDir.value === -1 ? 1 : -1;
  else {
    sortKey.value = key;
    sortDir.value = -1;
  }
}
const sortMark = (key: SortKey) =>
  sortKey.value === key ? (sortDir.value === -1 ? " ↓" : " ↑") : "";
type TradeEntry = StockEntry & {
  kind: "buy" | "sell" | "dividend" | "tax";
};
const entries = computed<TradeEntry[]>(() =>
  [...(read.data?.entries ?? [])]
    .filter(
      (e): e is TradeEntry =>
        e.instrument_id === selected.value &&
        e.kind !== "closed" &&
        (showVoided.value || !e.voided),
    )
    .sort(
      (a, b) =>
        b.date.localeCompare(a.date) ||
        (BigInt(a.sequence) > BigInt(b.sequence) ? -1 : 1),
    ),
);
type Mode =
  | "buy"
  | "sell"
  | "dividend"
  | "tax"
  | "cash"
  | "void"
  | "opening"
  | "clear_opening";
const editor = ref<{
  mode: Mode;
  item?: StockItem;
  entry?: StockEntry;
  book: StockBook;
}>();
let refreshTimer: ReturnType<typeof setTimeout> | undefined;
let secondTimer: ReturnType<typeof setTimeout> | undefined;
const cacheReadInterval = 20 * 60_000;
let cacheTimer: ReturnType<typeof setInterval> | undefined;
let lastRead = 0;
const manual = ref(false);
const priceNotice = ref("");
function load() {
  return loadBook(false);
}
async function loadBook(refresh: boolean) {
  manual.value = refresh;
  priceNotice.value = "";
  lastRead = Date.now();
  await read.load(async (signal) =>
    validateStockBook(
      await request<StockBook>(
        `/accounts/${props.account.id}/stock-book${refresh ? "/refresh-prices" : ""}`,
        {
          ...(refresh
            ? {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: "{}",
              }
            : {}),
          signal,
        },
      ),
      props.account,
    ),
  );
  if (refresh && manual.value && !read.loading) {
    manual.value = false;
    if (!read.error)
      priceNotice.value = read.data?.refresh_misses
        ? `${read.data.refresh_misses} 只持仓未取得报价，保留已有价格`
        : "已按行情源最新报价刷新";
  }
}
// Open pages reread the stock book. The server handles scheduled price
// updates and fetches any missing marks on first use.
function readIfIdle() {
  if (
    document.visibilityState !== "hidden" &&
    !read.loading &&
    !locked.value &&
    !editor.value
  )
    load();
}
function onVisibilityChange() {
  if (Date.now() - lastRead >= cacheReadInterval) readIfIdle();
}
onMounted(() => {
  cacheTimer = setInterval(readIfIdle, cacheReadInterval);
  document.addEventListener("visibilitychange", onVisibilityChange);
});
watch(() => [props.account.id, props.refreshKey], load, { immediate: true });
watch(selected, () => {
  limit.value = 30;
  showVoided.value = false;
});
function open(mode: Mode, item?: StockItem, entry?: StockEntry) {
  if (!read.data || locked.value || read.loading || read.error) return;
  editor.value = { mode, item, entry, book: read.data };
}
function saved() {
  load();
  emit("changed");
  clearTimeout(refreshTimer);
  clearTimeout(secondTimer);
  refreshTimer = setTimeout(load, 3500);
  secondTimer = setTimeout(load, 12000);
}
function view(id: string) {
  selected.value = id;
}
const signed = (s: string | null) =>
  s == null
    ? "—"
    : `${stockTone(s) === "stock-gain" ? "+" : ""}${holdingMoney(s)}`;
const percent = (s: string | null) => (s == null ? "—" : `${s}%`);
const settlement = (e: StockEntry) =>
  !e.event
    ? ""
    : e.event.pay_date > (read.data?.as_of ?? "")
      ? "待到账"
      : !/[1-9]/.test(e.fx) &&
          !!read.data?.cash_date &&
          e.event.pay_date > read.data.cash_date
        ? "待补汇率"
        : "已到派息日";
onBeforeUnmount(() => {
  clearInterval(cacheTimer);
  document.removeEventListener("visibilitychange", onVisibilityChange);
  clearTimeout(refreshTimer);
  clearTimeout(secondTimer);
});
defineExpose({ load });
</script>

<template>
  <section
    class="stock-book"
    :class="{ 'stock-book-open': expanded }"
    aria-label="股票持仓"
    data-test="stock-book"
  >
    <header class="stock-heading">
      <h2 class="stock-title">
        <button
          type="button"
          class="stock-toggle"
          data-test="stock-toggle"
          :aria-expanded="expanded"
          aria-controls="stock-book-body"
          @click="expanded = !expanded"
        >
          <LedgerDisclosure :open="expanded" />
          我的持仓
        </button>
      </h2>
      <span class="stock-totals">
        <span class="stock-total">
          <small>总资金</small>
          <strong>{{ holdingNumber(read.data?.total_assets, 2) }}</strong>
          <small v-if="read.data?.total_assets != null">{{
            account.currency
          }}</small>
        </span>
        <span class="stock-total">
          <small>{{
            read.data?.complete === false && read.data.positions_value !== null
              ? "已知持仓市值"
              : "持仓市值"
          }}</small>
          <strong>{{ holdingNumber(read.data?.positions_value, 2) }}</strong>
          <small v-if="read.data?.positions_value != null">{{
            account.currency
          }}</small>
        </span>
        <button
          type="button"
          class="stock-cash"
          :disabled="locked || read.loading || !!read.error || !read.data"
          :aria-label="`修改现金余额，当前 ${holdingNumber(read.data?.cash ?? '0', 2)} ${account.currency}`"
          title="修改现金余额"
          @click="open('cash')"
        >
          <small>现金</small>
          <strong>{{ holdingNumber(read.data?.cash, 2) }}</strong>
          <small>{{ account.currency }}</small>
          <span aria-hidden="true">✎</span>
        </button>
        <small
          v-if="read.data && !read.data.complete"
          class="stock-total-note"
          >{{
            read.data.positions_value === null
              ? "持仓已载入，报价或汇率暂缺"
              : "部分估值暂缺，总资金及仓位暂不可算"
          }}</small
        >
      </span>
      <span
        v-if="read.loading"
        class="stock-heading-status lp-spinner lp-record-spinner"
        role="status"
        aria-label="正在读取持仓"
      />
      <span v-else-if="read.error" class="stock-heading-status" role="status">{{
        read.data ? "刷新失败，显示上次数据" : "持仓读取失败"
      }}</span>
      <div class="stock-actions">
        <small v-if="priceNotice" role="status">{{ priceNotice }}</small>
        <button
          type="button"
          :disabled="
            locked ||
            read.loading ||
            !read.data?.items.some((row) => /[1-9]/.test(row.quantity))
          "
          @click="loadBook(true)"
        >
          {{ manual ? "正在刷新…" : "刷新价格" }}
        </button>
        <button
          type="button"
          class="lp-primary"
          :disabled="locked || read.loading || !!read.error || !read.data"
          @click="open('buy')"
        >
          ＋ 添加持仓
        </button>
      </div>
    </header>
    <div v-show="expanded" id="stock-book-body" class="stock-book-body">
      <p v-if="read.error && !selected" class="lp-error" role="alert">
        {{ errorText(read.error) }}
      </p>
      <p v-if="!read.data && read.loading" role="status">正在读取持仓…</p>
      <div v-if="read.data" class="stock-list-content">
        <div
          v-if="read.data.items.length || closedRows.length"
          class="stock-list-toolbar"
        >
          <div class="stock-filter" role="group" aria-label="持仓筛选">
            <button
              v-for="option in filterOptions"
              :key="option.key"
              type="button"
              :aria-pressed="filter === option.key"
              @click="filter = option.key"
            >
              {{ option.label }}
            </button>
          </div>
          <div class="stock-sort-mobile" role="group" aria-label="持仓排序">
            <span>排序</span>
            <button
              v-for="option in sortOptions"
              :key="option.key"
              type="button"
              :aria-pressed="sortKey === option.key"
              @click="sort(option.key)"
            >
              {{ option.label }}{{ sortMark(option.key) }}
            </button>
          </div>
          <div v-if="referenceRates.length" class="stock-reference-rates">
            <StockFxReference
              v-for="reference in referenceRates"
              :key="reference.key"
              :fx="reference.fx"
            />
          </div>
        </div>
        <div v-if="showPositions && rows.length" class="stock-list">
          <div class="stock-list-labels">
            <span>股票</span>
            <button
              type="button"
              class="stock-sort"
              :aria-pressed="sortKey === 'value'"
              @click="sort('value')"
            >
              市值 / 数量{{ sortMark("value") }}
            </button>
            <button
              type="button"
              class="stock-sort"
              :aria-pressed="sortKey === 'weight'"
              @click="sort('weight')"
            >
              持仓比例{{ sortMark("weight") }}
            </button>
            <span>现价 / 成本</span>
            <button
              type="button"
              class="stock-sort"
              :aria-pressed="sortKey === 'profit'"
              @click="sort('profit')"
            >
              持仓盈亏 / 盈亏率{{ sortMark("profit") }}
            </button>
            <span />
          </div>
          <button
            v-for="row in rows"
            :key="row.instrument.id"
            class="stock-row"
            :disabled="locked"
            @click="view(row.instrument.id)"
            :aria-label="`查看${row.instrument.name}持仓详情`"
          >
            <span class="stock-identity"
              ><strong>{{ row.instrument.name }}</strong
              ><small
                ><span class="stock-market">{{ row.instrument.market }}</span>
                {{ row.instrument.code }}
                <span v-if="!/[1-9]/.test(row.quantity)">已清仓</span></small
              ></span
            >
            <span class="stock-market-value"
              ><strong
                >{{ holdingMoney(row.market_value) }}
                <small class="stock-value-currency">{{
                  row.instrument.currency
                }}</small></strong
              ><small
                v-if="row.instrument.currency !== account.currency"
                class="stock-value-converted"
                :class="{
                  'stock-value-unavailable': row.account_market_value === null,
                }"
                ><template v-if="row.account_market_value !== null"
                  >≈ {{ holdingMoney(row.account_market_value) }}
                  {{ account.currency }}</template
                ><template v-else
                  >{{ accountCurrencyName }}估值暂缺</template
                ></small
              ><small>{{ holdingNumber(row.quantity) }} 股</small></span
            >
            <span
              ><strong>{{
                row.weight === null ? "—" : `${row.weight}%`
              }}</strong
              ><small>仓位</small></span
            >
            <span
              ><strong>{{ holdingNumber(row.price) }}</strong
              ><small>{{ holdingMoney(row.metrics.cost) }}</small></span
            >
            <span :class="stockTone(row.metrics.profit)"
              ><strong>{{ signed(row.metrics.profit) }}</strong
              ><small :class="stockTone(row.metrics.profit_rate)">{{
                percent(row.metrics.profit_rate)
              }}</small></span
            ><span class="stock-chevron" aria-hidden="true">›</span>
          </button>
        </div>
        <p v-if="showPositions && !rows.length" class="lp-empty">
          {{
            read.data.items.length
              ? filter === "open"
                ? "当前没有未清仓的股票，可切换查看全部或已清仓。"
                : "还没有持仓记录。"
              : "添加第一只股票，记录买入日期、数量和价格。"
          }}
        </p>
        <section
          v-if="showClosed"
          class="stock-closed-list"
          aria-label="已清仓记录"
        >
          <h3>已清仓</h3>
          <p v-if="!closedRows.length" class="lp-empty">
            还没有录入历史已清仓记录。
          </p>
          <button
            v-for="row in closedRows"
            :key="row.id"
            type="button"
            class="stock-closed-row"
            :aria-label="`查看${row.instrument.name}已清仓记录`"
            @click="selectedClosed = row.id"
          >
            <span class="stock-identity"
              ><strong>{{ row.instrument.name }}</strong
              ><small
                ><span class="stock-market">{{ row.instrument.market }}</span>
                {{ row.instrument.code }}
                <span class="stock-closed-badge">已清仓</span></small
              ></span
            >
            <span
              ><strong>{{ holdingNumber(row.closed.quantity) }} 股</strong
              ><small
                >{{ row.closed.opened_on }} ~ {{ row.closed.closed_on }}</small
              ></span
            >
            <span
              ><strong>{{ row.channel || "未分渠道" }}</strong
              ><small>渠道</small></span
            >
            <span :class="stockTone(row.closed.profit)"
              ><strong>{{ signed(row.closed.profit) }}</strong
              ><small :class="stockTone(row.closed.profit)"
                >{{ row.closed.profit_rate }}%</small
              ></span
            >
            <span class="stock-chevron" aria-hidden="true">›</span>
          </button>
        </section>
      </div>
      <StockSyncStatus v-if="read.data" :book="read.data" />
    </div>
    <template v-if="read.data">
      <LedgerDialog
        v-if="selected"
        class="stock-detail-dialog"
        :title="detail?.instrument.name ?? '持仓详情'"
        :caption="
          detail
            ? `${account.name} · ${detail.instrument.code}.${detail.instrument.market}`
            : account.name
        "
        :show-write-status="false"
        @close="selected = ''"
      >
        <template #title-extra>
          <template v-if="detail">
            <span class="stock-quote" role="group" aria-label="最新价">
              <span class="stock-quote-label">最新价</span>
              <strong>{{ holdingNumber(detail.price) }}</strong>
              <span>{{ detail.instrument.currency }}</span>
            </span>
            <small v-if="!detail.quote" class="stock-quote-status">{{
              detail.quantity === "0.000000" ? "已清仓" : "行情暂不可用"
            }}</small>
          </template>
        </template>
        <div class="lp-dialog-body">
          <p v-if="read.error" class="lp-error" role="alert">
            {{ errorText(read.error) }}
          </p>
          <template v-if="detail">
            <section class="stock-overview" aria-label="持仓概览">
              <div class="stock-metrics">
                <div class="stock-market-value">
                  <span class="stock-metric-label">持仓市值</span>
                  <strong
                    >{{ holdingMoney(detail.market_value) }}
                    <small class="stock-value-currency">{{
                      detail.instrument.currency
                    }}</small></strong
                  >
                  <small
                    v-if="detail.instrument.currency !== account.currency"
                    class="stock-value-converted"
                    :class="{
                      'stock-value-unavailable':
                        detail.account_market_value === null,
                    }"
                    ><template v-if="detail.account_market_value !== null"
                      >≈ {{ holdingMoney(detail.account_market_value) }}
                      {{ account.currency }}</template
                    ><template v-else
                      >{{ accountCurrencyName }}估值暂缺</template
                    ></small
                  >
                  <span v-if="detail.weight !== null" class="stock-metric-note"
                    >仓位 {{ detail.weight }}%</span
                  >
                </div>
                <div>
                  <span class="stock-metric-label"
                    >持仓盈亏
                    <small>{{ detail.instrument.currency }}</small></span
                  >
                  <strong :class="stockTone(detail.metrics.profit)">{{
                    signed(detail.metrics.profit)
                  }}</strong>
                  <span class="stock-metric-note"
                    >持仓盈亏率
                    <b :class="stockTone(detail.metrics.profit_rate)">{{
                      percent(detail.metrics.profit_rate)
                    }}</b></span
                  >
                </div>
                <div class="stock-lifetime">
                  <span class="stock-metric-label"
                    >个股累计盈亏
                    <small>{{ detail.instrument.currency }}</small></span
                  >
                  <strong :class="stockTone(detail.metrics.total_profit)">{{
                    signed(detail.metrics.total_profit)
                  }}</strong>
                  <span class="stock-metric-note"
                    >累计收益率
                    <b :class="stockTone(detail.metrics.total_rate)">{{
                      percent(detail.metrics.total_rate)
                    }}</b></span
                  >
                </div>
              </div>
              <p
                v-if="
                  detail.instrument.currency !== account.currency && detail.fx
                "
                class="stock-detail-fx"
              >
                参考汇率 1 {{ detail.fx.base }} =
                {{ holdingNumber(detail.fx.rate, 4) }} {{ detail.fx.quote }}
                <span>汇率日期 {{ detail.fx.date }}</span>
                <span
                  >来源：{{
                    detail.fx.source.startsWith("Tencent/spot/")
                      ? "腾讯即期汇率"
                      : detail.fx.source
                  }}</span
                >
              </p>
              <div class="stock-position-facts">
                <div>
                  <span>持仓数量</span>
                  <strong>{{ holdingNumber(detail.quantity) }}</strong>
                  <small>股</small>
                </div>
                <div>
                  <span>持仓成本</span>
                  <strong>{{ holdingMoney(detail.metrics.cost) }}</strong>
                  <small>{{ detail.instrument.currency }}/股</small>
                </div>
                <div>
                  <span>摊薄成本</span>
                  <strong>{{
                    holdingMoney(detail.metrics.diluted_cost)
                  }}</strong>
                  <small>{{ detail.instrument.currency }}/股</small>
                </div>
              </div>
              <div v-if="detail.channels.length" class="stock-channels">
                <span class="stock-channels-label">持仓渠道</span>
                <ul class="stock-channel-list">
                  <li
                    v-for="row in detail.channels"
                    :key="row.channel"
                    class="stock-channel-chip"
                  >
                    <span>{{ row.channel || "未标注渠道" }}</span>
                    <strong>{{ holdingNumber(row.quantity) }} 股</strong>
                  </li>
                </ul>
              </div>
              <div class="stock-dividend-summary">
                <span class="stock-dividend-earned"
                  >累计分红 {{ holdingNumber(detail.metrics.dividends, 2) }}
                  {{ detail.instrument.currency }}</span
                ><span v-if="/[1-9]/.test(detail.metrics.tax)"
                  >已扣红利税 {{ holdingNumber(detail.metrics.tax, 2) }}
                  {{ detail.instrument.currency }}</span
                ><span v-if="/[1-9]/.test(detail.metrics.pending_dividend)"
                  >其中待到账 / 待补汇率
                  {{ holdingNumber(detail.metrics.pending_dividend, 2) }}
                  {{ detail.instrument.currency }}</span
                ><span
                  >累计手续费 {{ holdingNumber(detail.metrics.fees, 2) }}
                  {{ detail.instrument.currency }}</span
                >
              </div>
            </section>
            <p v-if="detail.opening" class="lp-reference stock-opening">
              这条旧持仓只有股数，补上买入日期和价格后即可计算成本。<button
                :disabled="locked || read.loading || !!read.error"
                @click="open('opening', detail)"
              >
                补录初始买入</button
              ><button
                class="lp-text-button"
                :disabled="locked || read.loading || !!read.error"
                @click="open('clear_opening', detail)"
              >
                移除旧持仓
              </button>
            </p>
            <div class="stock-trades-heading">
              <h3>买卖与分红记录</h3>
              <div class="stock-actions">
                <button
                  :disabled="locked || read.loading || !!read.error"
                  @click="open('buy', detail)"
                >
                  ＋ 加仓</button
                ><button
                  :disabled="
                    locked ||
                    read.loading ||
                    !!read.error ||
                    !/[1-9]/.test(detail.quantity)
                  "
                  @click="open('sell', detail)"
                >
                  － 减仓</button
                ><button
                  :disabled="locked || read.loading || !!read.error"
                  @click="open('dividend', detail)"
                >
                  记录分红
                </button>
              </div>
            </div>
            <p v-if="!entries.length" class="lp-empty">尚无交易记录。</p>
            <div v-else class="stock-trades">
              <article
                v-for="e in entries.slice(0, limit)"
                :key="e.id"
                class="stock-trade-row"
                :class="[`stock-trade-${e.kind}`, { 'stock-voided': e.voided }]"
              >
                <div class="stock-trade-type">
                  <strong class="stock-trade-badge">{{
                    stockLabels[e.kind]
                  }}</strong>
                  <small>{{ e.date }}</small
                  ><small v-if="e.channel" class="stock-channel">{{
                    e.channel
                  }}</small
                  ><small v-if="e.voided">已删除</small>
                </div>
                <div class="stock-trade-values">
                  <template v-if="e.kind !== 'dividend' && e.kind !== 'tax'"
                    ><strong
                      >{{ holdingNumber(e.quantity) }} 股 ×
                      {{ holdingNumber(e.price) }}</strong
                    ><small
                      >手续费 {{ holdingNumber(e.fee, 2) }}</small
                    ></template
                  ><template v-else
                    ><strong>{{
                      e.kind === "tax" ? "股息红利税扣款" : "现金分红"
                    }}</strong
                    ><small v-if="e.event"
                      >{{ holdingNumber(e.quantity) }} 股 ×
                      {{ holdingNumber(e.event.per_share) }} ·
                      {{ e.override ? "已手工更正" : "公告金额" }}</small
                    ><small v-if="e.event"
                      >{{ e.event.pay_date }} 派息 · {{ settlement(e) }}</small
                    ></template
                  ><small v-if="e.note && !e.event">{{ e.note }}</small
                  ><small
                    v-if="
                      detail.instrument.currency !== account.currency &&
                      /[1-9]/.test(e.fx)
                    "
                    >结算汇率 {{ holdingNumber(e.fx)
                    }}<template v-if="e.settlement_fx">
                      · {{ e.settlement_fx.date }}</template
                    ></small
                  >
                </div>
                <div class="stock-trade-total">
                  <strong class="stock-trade-amount"
                    >{{ e.kind === "tax" ? "−" : ""
                    }}{{ holdingNumber(e.amount, 2) }}
                    <small>{{ detail.instrument.currency }}</small></strong
                  >
                  <small v-if="e.kind === 'buy'">买入支出</small>
                  <small v-else-if="e.kind === 'sell'">卖出净回款</small>
                  <div v-if="!e.voided" class="stock-row-actions">
                    <button
                      class="lp-text-button"
                      :disabled="locked || read.loading || !!read.error"
                      @click="open(e.kind, detail, e)"
                      :aria-label="`修改${e.date}${stockLabels[e.kind]}记录`"
                    >
                      修改</button
                    ><button
                      class="lp-text-button"
                      :disabled="locked || read.loading || !!read.error"
                      @click="open('void', detail, e)"
                      :aria-label="`删除${e.date}${stockLabels[e.kind]}记录`"
                    >
                      删除
                    </button>
                  </div>
                </div>
              </article>
              <button
                v-if="entries.length > limit"
                class="stock-load-more"
                @click="limit += 30"
              >
                查看更多记录（{{ entries.length - limit }}）
              </button>
            </div>
            <label class="stock-show-voided"
              ><input
                type="checkbox"
                v-model="showVoided"
              />显示已删除记录</label
            >
            <details class="stock-method">
              <summary>成本与收益如何计算</summary>
              <p>
                本轮指最近一次清仓后的持有周期。持仓成本 = 本轮累计买入支出 ÷
                本轮累计买入股数；摊薄成本 =（本轮买入支出 − 卖出净回款 −
                应得分红）÷ 当前股数。
              </p>
              <p>
                持仓盈亏 =（现价 − 持仓成本）×
                当前股数；持仓盈亏率以当前持仓的成本金额为分母。累计盈亏 = 市值
                + 历史卖出净回款 + 历史应得分红 −
                历史买入支出；累计收益率以全部买入支出为分母。金额包含已录入手续费。
              </p>
              <p>
                自动现金分红按取得资格时的股数计算，除息日记录应得金额，派息日增加现金。默认使用公告金额，可手工更正。以上均为单股原币口径。
              </p>
            </details>
          </template>
          <p v-else class="lp-empty">
            该持仓已移除。<button @click="selected = ''">返回列表</button>
          </p>
          <StockSyncStatus :book="read.data" />
        </div>
      </LedgerDialog>
      <StockClosedDetail
        v-if="closedDetail"
        :item="closedDetail"
        :account-currency="account.currency"
        @close="selectedClosed = ''"
      />
    </template>
    <StockTradeDialog
      v-if="editor"
      :account="account"
      :book="editor.book"
      :mode="editor.mode"
      :item="editor.item"
      :entry="editor.entry"
      @close="editor = undefined"
      @saved="saved"
    />
  </section>
</template>

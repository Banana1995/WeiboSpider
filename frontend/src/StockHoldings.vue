<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import StockTradeDialog from "./StockTradeDialog.vue";
import { errorText, request, type Account } from "./ledger";
import { holdingNumber } from "./holdings";
import {
  stockLabels,
  stockTone,
  validateStockBook,
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
const selected = ref("");
const filter = ref("open");
const limit = ref(30);
const showVoided = ref(false);
const detail = computed(() =>
  read.data?.items.find((row) => row.instrument.id === selected.value),
);
const rows = computed(
  () =>
    read.data?.items.filter(
      (row) => filter.value === "all" || /[1-9]/.test(row.quantity),
    ) ?? [],
);
const entries = computed(() =>
  [...(read.data?.entries ?? [])]
    .filter(
      (e) =>
        e.instrument_id === selected.value && (showVoided.value || !e.voided),
    )
    .sort(
      (a, b) =>
        b.date.localeCompare(a.date) ||
        (BigInt(a.sequence) > BigInt(b.sequence) ? -1 : 1),
    ),
);
type Mode =
  "buy" | "sell" | "dividend" | "cash" | "void" | "opening" | "clear_opening";
const editor = ref<{
  mode: Mode;
  item?: StockItem;
  entry?: StockEntry;
  book: StockBook;
}>();
const section = ref<HTMLElement>();
let refreshTimer: ReturnType<typeof setTimeout> | undefined;
let secondTimer: ReturnType<typeof setTimeout> | undefined;
function load() {
  void read.load(async (signal) =>
    validateStockBook(
      await request<StockBook>(`/accounts/${props.account.id}/stock-book`, {
        signal,
      }),
      props.account,
    ),
  );
}
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
  section.value?.scrollIntoView?.({ block: "start", behavior: "auto" });
}
const signed = (s: string | null) =>
  s == null
    ? "—"
    : `${stockTone(s) === "stock-gain" ? "+" : ""}${holdingNumber(s, 2)}`;
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
  clearTimeout(refreshTimer);
  clearTimeout(secondTimer);
});
defineExpose({ load });
</script>

<template>
  <section
    ref="section"
    class="stock-book"
    aria-label="股票持仓"
    data-test="stock-book"
  >
    <header class="stock-heading">
      <div class="stock-heading-title">
        <button
          v-if="selected"
          class="lp-text-button"
          :disabled="locked"
          @click="selected = ''"
          aria-label="返回持仓列表"
        >
          ‹ 返回持仓
        </button>
        <h2 v-else>我的持仓</h2>
        <template v-if="!selected"
          ><button
            class="stock-cash"
            :disabled="locked || read.loading || !!read.error || !read.data"
            @click="open('cash')"
          >
            现金 <strong>{{ holdingNumber(read.data?.cash ?? "0", 2) }}</strong>
            <small>{{ account.currency }}</small
            ><span aria-hidden="true"> ✎</span>
          </button></template
        >
      </div>
      <div class="stock-actions">
        <button :disabled="locked || read.loading" @click="load">
          {{ read.loading ? "刷新中…" : "刷新" }}</button
        ><button
          v-if="!selected"
          class="lp-primary"
          :disabled="locked || read.loading || !!read.error || !read.data"
          @click="open('buy')"
        >
          ＋ 添加持仓
        </button>
      </div>
    </header>
    <p v-if="read.error" class="lp-error" role="alert">
      {{ errorText(read.error) }}
    </p>
    <p v-if="!read.data && read.loading" role="status">正在读取持仓…</p>
    <template v-if="read.data">
      <template v-if="!selected">
        <div v-if="read.data.items.length" class="stock-list-toolbar">
          <label
            >显示<select v-model="filter">
              <option value="open">当前持仓</option>
              <option value="all">全部（含已清仓）</option>
            </select></label
          ><span>点击股票查看买卖与分红</span>
        </div>
        <div v-if="rows.length" class="stock-list">
          <div class="stock-list-labels" aria-hidden="true">
            <span>股票</span><span>市值 / 数量</span><span>现价 / 成本</span
            ><span>持仓盈亏 / 盈亏率</span><span />
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
            <span
              ><strong>{{ holdingNumber(row.market_value, 2) }}</strong
              ><small
                >{{ holdingNumber(row.quantity) }} 股 ·
                {{ row.instrument.currency }}</small
              ></span
            >
            <span
              ><strong>{{ holdingNumber(row.price) }}</strong
              ><small>{{ holdingNumber(row.metrics.cost) }}</small></span
            >
            <span :class="stockTone(row.metrics.profit)"
              ><strong>{{ signed(row.metrics.profit) }}</strong
              ><small :class="stockTone(row.metrics.profit_rate)">{{
                percent(row.metrics.profit_rate)
              }}</small></span
            ><span class="stock-chevron" aria-hidden="true">›</span>
          </button>
        </div>
        <div v-else class="lp-empty">
          <p>
            {{
              read.data.items.length
                ? "当前没有未清仓的股票，可切换查看全部。"
                : "添加第一只股票，记录买入日期、数量和价格。"
            }}
          </p>
        </div>
      </template>
      <template v-else-if="detail">
        <header class="stock-detail-heading">
          <div>
            <h2>
              {{ detail.instrument.name }}
              <small
                >{{ detail.instrument.code }}.{{
                  detail.instrument.market
                }}</small
              >
            </h2>
            <p class="stock-quote">
              最新价 <strong>{{ holdingNumber(detail.price) }}</strong>
              {{ detail.instrument.currency
              }}<small v-if="detail.quote"
                >{{ detail.quote.date }} · {{ detail.quote.source }}</small
              ><small v-else>{{
                detail.quantity === "0.000000" ? "已清仓" : "行情暂不可用"
              }}</small>
            </p>
          </div>
          <button
            class="stock-cash"
            :disabled="locked || read.loading || !!read.error"
            @click="open('cash')"
          >
            现金 {{ holdingNumber(read.data.cash, 2) }} {{ account.currency }} ✎
          </button>
        </header>
        <div class="stock-metrics">
          <div>
            <span>持仓市值</span
            ><strong>{{ holdingNumber(detail.market_value, 2) }}</strong>
          </div>
          <div>
            <span>持仓盈亏</span
            ><strong :class="stockTone(detail.metrics.profit)">{{
              signed(detail.metrics.profit)
            }}</strong>
          </div>
          <div>
            <span>持仓盈亏率</span
            ><strong :class="stockTone(detail.metrics.profit_rate)">{{
              percent(detail.metrics.profit_rate)
            }}</strong>
          </div>
          <div>
            <span>持仓数量</span
            ><strong>{{ holdingNumber(detail.quantity) }}</strong>
          </div>
          <div>
            <span>持仓成本</span
            ><strong>{{ holdingNumber(detail.metrics.cost) }}</strong>
          </div>
          <div>
            <span>摊薄成本</span
            ><strong>{{ holdingNumber(detail.metrics.diluted_cost) }}</strong>
          </div>
        </div>
        <div class="stock-lifetime">
          <span>个股累计盈亏</span
          ><strong :class="stockTone(detail.metrics.total_profit)">{{
            signed(detail.metrics.total_profit)
          }}</strong
          ><span :class="stockTone(detail.metrics.total_rate)">{{
            percent(detail.metrics.total_rate)
          }}</span
          ><small>{{ detail.instrument.currency }}</small>
        </div>
        <div class="stock-dividend-summary">
          <span
            >累计分红 {{ holdingNumber(detail.metrics.dividends, 2) }}
            {{ detail.instrument.currency }}</span
          ><span v-if="/[1-9]/.test(detail.metrics.pending_dividend)"
            >其中待到账 / 待补汇率
            {{ holdingNumber(detail.metrics.pending_dividend, 2) }}</span
          ><span v-if="detail.weight !== null">仓位 {{ detail.weight }}%</span>
        </div>
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
            :class="{ 'stock-voided': e.voided }"
          >
            <div class="stock-trade-type">
              <strong>{{ stockLabels[e.kind] }}</strong
              ><small>{{ e.date }}</small
              ><small v-if="e.voided">已删除</small>
            </div>
            <div class="stock-trade-values">
              <template v-if="e.kind !== 'dividend'"
                ><strong
                  >{{ holdingNumber(e.quantity) }} 股 ×
                  {{ holdingNumber(e.price) }}</strong
                ><small>手续费 {{ holdingNumber(e.fee, 2) }}</small></template
              ><template v-else
                ><strong
                  >{{ holdingNumber(e.amount, 2) }}
                  {{ detail.instrument.currency }}</strong
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
              <strong v-if="e.kind !== 'dividend'">{{
                holdingNumber(e.amount, 2)
              }}</strong>
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
          ><input type="checkbox" v-model="showVoided" />显示已删除记录</label
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
            当前股数；持仓盈亏率以当前持仓的成本金额为分母。累计盈亏 = 市值 +
            历史卖出净回款 + 历史应得分红 −
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
      <details class="stock-sync">
        <summary>
          自动分红{{
            read.data.sync_checked_at
              ? ` · 上次检查 ${read.data.sync_checked_at.slice(0, 16).replace("T", " ")} UTC`
              : " · 等待后台检查"
          }}
        </summary>
        <p>
          {{
            read.data.sync_message ||
            "添加买入记录后后台自动检查分红，稍后刷新即可查看。分红会按历史股数计算。"
          }}
        </p>
        <p>
          数据覆盖来源可提供的历史范围。未提供完整日期、含送转或拆合股的记录需要核对；已有手工记录会保留。
        </p>
      </details>
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

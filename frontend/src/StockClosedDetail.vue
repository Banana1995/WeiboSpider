<script setup lang="ts">
import { computed } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import { holdingMoney, holdingNumber } from "./holdings";
import { stockTone, type ClosedItem } from "./stockBook";

const props = defineProps<{ item: ClosedItem; accountCurrency: string }>();
defineEmits<{ close: [] }>();

const record = computed(() => props.item.closed!);
const currency = computed(() => record.value.currency ?? props.accountCurrency);
const flowLabels: Record<string, string> = {
  buy: "买入",
  sell: "卖出",
  dividend: "股息入账",
  tax: "红利税",
};
const signed = (s: string | null) =>
  s == null
    ? "—"
    : `${stockTone(s) === "stock-gain" ? "+" : ""}${holdingMoney(s)}`;
</script>

<template>
  <LedgerDialog
    :title="props.item.instrument.name"
    :caption="`已清仓${record.new_stock ? ' · 新股' : ''} · ${props.item.instrument.code}.${props.item.instrument.market}${props.item.channel ? ` · ${props.item.channel}` : ''}`"
    :show-write-status="false"
    @close="$emit('close')"
  >
    <div class="stock-closed-summary">
      <div>
        <span>持有区间</span>
        <strong>{{ record.opened_on }} ~ {{ record.closed_on }}</strong>
      </div>
      <div>
        <span>数量</span>
        <strong>{{ holdingNumber(record.quantity) }} 股</strong>
      </div>
      <div>
        <span>累计收入</span>
        <strong>{{ holdingMoney(record.income) }} {{ currency }}</strong>
      </div>
      <div>
        <span>累计支出</span>
        <strong>{{ holdingMoney(record.expense) }} {{ currency }}</strong>
      </div>
      <div>
        <span>交易费用</span>
        <strong>{{ holdingMoney(record.fees) }} {{ currency }}</strong>
      </div>
      <div>
        <span>总盈亏</span>
        <strong :class="stockTone(record.profit)"
          >{{ signed(record.profit) }} {{ currency }}</strong
        >
      </div>
      <div>
        <span>收益率</span>
        <strong :class="stockTone(record.profit)"
          >{{ record.profit_rate }}%</strong
        >
      </div>
    </div>
    <p v-if="record.source" class="lp-reference">{{ record.source }}</p>
    <h3>流水明细</h3>
    <p v-if="!record.flows.length" class="lp-empty">
      这条记录只保存了汇总，没有逐笔流水。
    </p>
    <div v-else class="stock-closed-flows">
      <article
        v-for="(flow, index) in record.flows"
        :key="index"
        class="stock-closed-flow"
      >
        <span class="stock-trade-badge" :class="`stock-trade-${flow.kind}`">{{
          flowLabels[flow.kind]
        }}</span>
        <span>{{ flow.date }}</span>
        <span v-if="flow.kind === 'buy' || flow.kind === 'sell'"
          >{{ holdingNumber(flow.quantity) }} 股 ×
          {{ holdingNumber(flow.price) }}</span
        ><span v-else-if="flow.kind === 'dividend'"
          >每股 {{ holdingNumber(flow.price) }}</span
        ><span v-else>现金扣除</span>
        <span>费用 {{ holdingNumber(flow.fee, 2) }}</span>
        <strong
          >{{ flow.kind === "tax" ? "−" : "" }}{{ holdingMoney(flow.amount) }}
          {{ currency }}</strong
        >
      </article>
    </div>
    <p class="lp-reference">
      金额按 {{ currency }}（记录币种）；价格为{{
        props.item.instrument.currency
      }}计价。汇总字段按券商页面原值展示，累计收入/支出与总盈亏不一定是简单相减关系，以券商总盈亏为准。这条记录来自券商历史已清仓页面，只做历史归因，不进入当前持仓、当前轮成本或账户资金流。
    </p>
  </LedgerDialog>
</template>

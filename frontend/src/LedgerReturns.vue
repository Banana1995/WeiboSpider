<script setup lang="ts">
import { defineAsyncComponent, ref, watch } from "vue";
import { pointLabels, type BasisPoint } from "./ledgerChart";
import {
  returnPercent,
  returnReasons,
  returnWarnings,
  type LedgerReturns,
} from "./ledgerReturns";

const props = defineProps<{
  result: LedgerReturns;
  currency: string;
  points: BasisPoint[];
}>();
const LedgerReturnTrend = defineAsyncComponent(
  () => import("./LedgerReturnTrend.vue"),
);
const manager = ref(false);
const page = ref(0);
const investorPage = ref(0);
watch(
  () => props.result,
  () => {
    page.value = 0;
    investorPage.value = 0;
  },
);
const states = {
  available: "可计算",
  reference: "仅供参考",
  unavailable: "不可用",
};
</script>

<template>
  <section data-test="ledger-returns" aria-label="收益分析">
    <h3>收益分析 · {{ manager ? "基金经理视角" : "个人视角" }}</h3>
    <label
      >分析视角
      <select v-model="manager" name="return_view">
        <option :value="false">个人（资金加权）</option>
        <option :value="true">基金经理（时间加权）</option>
      </select></label
    >
    <p>
      请求：{{ result.requested_from || "首个明确日终资产作为基准" }} 至
      {{ result.requested_to || "北京时间今天" }}；实际计算边界：
      {{ result.effective_from || "未知" }} 日终至
      {{ result.effective_to || "未知" }} 日终（{{
        result.days > 0 ? `${result.days} 自然日` : "未形成有效计算区间"
      }}）。 没有新资产或沿用记录时，不把截止日拉长到今天。
    </p>
    <dl class="returns-cards">
      <div
        v-for="card in [
          { name: '累计收益（所选区间）', metric: result.profit, amount: true },
          {
            name: manager ? 'TWR 区间收益率' : 'Modified Dietz 区间收益率',
            metric: manager ? result.twr : result.modified_dietz,
            amount: false,
          },
          {
            name: manager ? 'TWR 复合年化收益率' : 'XIRR 年化收益率',
            metric: manager ? result.twr_annualized : result.xirr,
            amount: false,
          },
        ]"
        :key="card.name"
        :data-state="card.metric.status"
      >
        <dt>{{ card.name }}</dt>
        <dd>
          {{
            card.amount
              ? card.metric.value === null
                ? "不可用"
                : `${card.metric.value} ${currency}`
              : returnPercent(card.metric.value, card.metric.percentage)
          }}
        </dd>
        <dd>{{ states[card.metric.status] }}</dd>
        <dd v-if="card.metric.reason">
          {{ returnReasons[card.metric.reason] }}
        </dd>
      </div>
    </dl>
    <p v-for="warning in result.warnings" :key="warning" role="status">
      {{ returnWarnings[warning] }}
    </p>
    <details>
      <summary>计算口径、端点与现金流明细</summary>
      <p>
        收益 = 期末资产 - 期初资产 -
        净流入。转入为正、转出为负；账户内买卖和留存分红不是外部投入。同日选定总资产已含当日资金流，不重复加款。
      </p>
      <p>
        未指定开始日期时，以首个已知日终资产为基准，排除该日全部资金流；明确开始日期时，取开始日前最后资产，计算边界为开始日前一天日终，包含开始日和截止日资金流。不推断初始资产之前的收益。
      </p>
      <p>
        Modified Dietz = 收益 /（期初资产 + Σ 资金流 × 剩余天数 /
        区间天数）。北京时间自然日，资金流视作日终发生，期末日权重为零；分母不大于零时不可用。它不是
        TWR。
      </p>
      <p>
        TWR 按日终资金流假设，在外部资金事件日链接（当日总资产 - 当日净流入）/
        上一资金边界资产；同日非零转入转出即使净额为零仍需边界资产。缺少真实边界不可用，沿用原额只能参考，不以
        Dietz 替代。复合年化 = (1 + TWR)^(365 / 自然日数) - 1。
      </p>
      <p>
        XIRR
        使用投资者符号：期初资产及转入为负，转出及期末资产为正，同日精确净额合并，以实际天数
        / 365 独立求解，不是 Dietz
        复利年化。多次变号仅标记可能多解；有界求解失败不补零。利率显示两位百分比、半远离零舍入，不回写计算原值。
      </p>
      <p>
        计算净流入：{{ result.net_flow }} {{ currency }}；Dietz
        精确分母（本位币有理数）：{{ result.denominator ?? "不可用" }}。
      </p>
      <p
        v-for="endpoint in [
          { label: '期初', point: result.opening },
          { label: '期末', point: result.closing },
        ]"
        :key="endpoint.label"
        class="ledger-note"
      >
        {{ endpoint.label }}资产：{{ endpoint.point?.assets ?? "未知" }}
        {{ currency }}； 声明记录日期 {{ endpoint.point?.date || "未知" }}；
        资产来源日期 {{ endpoint.point?.source_date || "未知" }}； 来源
        {{ endpoint.point?.source_id || "无" }} / 版本
        {{ endpoint.point?.source_version || "无" }}；
        {{ pointLabels[endpoint.point?.status || "unavailable"] }}。
        <span v-if="endpoint.point?.valuation">
          估值计算时间 {{ endpoint.point.valuation.calculated_at }}；保存时间
          {{
            endpoint.point.valuation.saved_at
          }}。这是采样时间，不是业务日收盘证明。
        </span>
      </p>
      <h4>账户外部资金流（{{ result.flows.length }} 笔）</h4>
      <p v-if="!result.flows.length">
        没有纳入的资金流；不可用时不代表已完整核算。
      </p>
      <ul>
        <li
          v-for="(flow, index) in result.flows.slice(
            page * 30,
            (page + 1) * 30,
          )"
          :key="index"
          class="ledger-note"
        >
          {{ flow.date }} · {{ flow.flow }} {{ currency }} · 权重
          {{ flow.weight_days }}/{{ flow.period_days }} · {{ flow.record_id }} /
          版本 {{ flow.version }}
        </li>
      </ul>
      <div v-if="result.flows.length > 30" class="ledger-actions">
        <button :disabled="page === 0" @click="page--">上一页资金流</button>
        <span>{{ page + 1 }} / {{ Math.ceil(result.flows.length / 30) }}</span>
        <button
          :disabled="(page + 1) * 30 >= result.flows.length"
          @click="page++"
        >
          下一页资金流
        </button>
      </div>
      <h4>XIRR 投资者现金流（同日净额，含两端资产）</h4>
      <ul>
        <li
          v-for="flow in result.investor_flows.slice(
            investorPage * 30,
            (investorPage + 1) * 30,
          )"
          :key="flow.date"
        >
          {{ flow.date }} · {{ flow.amount }} {{ currency }}
        </li>
      </ul>
      <div v-if="result.investor_flows.length > 30" class="ledger-actions">
        <button :disabled="investorPage === 0" @click="investorPage--">
          上一页净额
        </button>
        <span
          >{{ investorPage + 1 }} /
          {{ Math.ceil(result.investor_flows.length / 30) }}</span
        >
        <button
          :disabled="(investorPage + 1) * 30 >= result.investor_flows.length"
          @click="investorPage++"
        >
          下一页净额
        </button>
      </div>
      <p class="ledger-note">
        收益、资产图与明细共用快照版本
        {{
          result.revision
        }}；更正后刷新重新计算，不改写原始估值。不可用结果的明细可能不完整，不作为已完成的对账。
      </p>
    </details>
    <LedgerReturnTrend
      :result="result"
      :currency="currency"
      :manager="manager"
      :points="points"
    />
  </section>
</template>

<style scoped>
.returns-cards {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin: 20px 0;
}
.returns-cards > div {
  padding: 18px;
  border: 1px solid #dde3db;
  border-left: 3px solid #285b46;
  border-radius: 4px;
  background: #f3f6f1;
  overflow-wrap: anywhere;
}
.returns-cards > div[data-state="reference"],
.returns-cards > div[data-state="unavailable"] {
  border-left-color: #a47827;
  background: #fff6df;
}
.returns-cards dt {
  font-size: 13px;
  color: #526657;
}
.returns-cards dd {
  margin: 8px 0 0;
  font-size: 13px;
}
.returns-cards dd:first-of-type {
  font-size: 26px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
li {
  overflow-wrap: anywhere;
  font-size: 14px;
  line-height: 1.7;
}
@media (max-width: 700px) {
  .returns-cards {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

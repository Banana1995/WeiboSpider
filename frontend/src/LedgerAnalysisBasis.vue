<script setup lang="ts">
import { defineAsyncComponent, reactive, ref, watch } from "vue";
import { decimal, LedgerError, query, request } from "./ledger";
import { useLedgerRead } from "./useLedgerRead";
import { pointLabels, type BasisPoint } from "./ledgerChart";
import LedgerReturns from "./LedgerReturns.vue";
import { validReturns, type LedgerReturns as Returns } from "./ledgerReturns";
const LedgerAssetChart = defineAsyncComponent(
  () => import("./LedgerAssetChart.vue"),
);

interface Point {
  date: string;
  sequence: string;
  record_id: string;
  assets: string | null;
  status: string;
  source_id: string;
  source_version: string;
  source_date: string;
}
interface Basis {
  returns: Returns;
  account_id: string;
  from: string;
  to: string;
  revision: string;
  change_revision: string;
  status: string;
  net_flow: string;
  opening: Point | null;
  closing: Point | null;
  points: BasisPoint[];
  currency: string;
  previous_basis_affected: boolean;
  changes: {
    revision: string;
    from: string;
    source_id: string;
    reason: string;
  }[];
}
const props = defineProps<{
  accountId: string;
  holdings?: boolean;
  refreshKey: number;
}>();
const basis = reactive(useLedgerRead<Basis>());
const opened = ref(false);
const from = ref("");
const to = ref("");
const labels: Record<string, string> = {
  current: "依据已按当前有效记录生成；收益是否可用见各指标状态",
  unavailable: "缺少资产依据，不补零",
  pending_recalculation: "存在受持仓修改影响的旧估值，需要核实或修正",
  untracked_history: "历史估值未追踪版本，不能确认有效性",
  reported: "明确记录",
  carried: "最近明确资产加后续净转入，仅供参考",
  observed: "已追踪估值，未发现后续相关修改",
  stale: "旧估值受修改影响，未重算",
  untracked: "旧估值未追踪",
};
function load() {
  opened.value = true;
  const id = props.accountId;
  const requestedFrom = from.value;
  const requestedTo = to.value;
  const previous = basis.data;
  basis.clear();
  void basis.load(async (signal) => {
    const result = await request<Basis>(
      `/accounts/${id}/analysis-basis${query({
        from: requestedFrom,
        to: requestedTo,
        since_revision:
          previous?.account_id === id ? previous.change_revision : "",
      })}`,
      { signal },
    );
    if (
      !result ||
      result.account_id !== id ||
      (requestedFrom && result.from !== requestedFrom) ||
      (requestedTo && result.to !== requestedTo) ||
      !Array.isArray(result.points) ||
      result.points.length > 10000 ||
      !["CNY", "HKD", "USD"].includes(result.currency) ||
      typeof result.revision !== "string" ||
      !result.revision ||
      result.points.some(
        (p) =>
          !p ||
          typeof p.date !== "string" ||
          !/^\d{4}-\d{2}-\d{2}$/.test(p.date) ||
          !Number.isFinite(Date.parse(`${p.date}T00:00:00Z`)) ||
          p.date < result.from ||
          p.date > result.to ||
          !Object.hasOwn(pointLabels, p.status) ||
          typeof p.selected !== "boolean" ||
          (p.assets !== null &&
            (typeof p.assets !== "string" || !decimal(p.assets, 2))) ||
          (p.flow !== null &&
            (typeof p.flow !== "string" || !decimal(p.flow, 2))) ||
          (p.record &&
            (p.record.account_id !== id ||
              p.record.id !== p.record_id ||
              p.record.date !== p.date)) ||
          (p.valuation &&
            (p.valuation.account_id !== id ||
              p.valuation.id !== p.record_id ||
              p.valuation.as_of !== p.date)),
      ) ||
      !Array.isArray(result.changes) ||
      typeof result.net_flow !== "string" ||
      !validReturns(result.returns, result.revision, requestedFrom, requestedTo)
    )
      throw new LedgerError("invalid_response");
    const selected = new Map(
      result.points.filter((p) => p.selected).map((p) => [p.date, p.record_id]),
    );
    if (
      result.returns.curve.some((p) =>
        p.baseline
          ? p.record_id !== result.returns.opening?.record_id
          : selected.get(p.date) !== p.record_id,
      )
    )
      throw new LedgerError("invalid_response");
    return result;
  });
}
watch(
  () => props.accountId,
  () => {
    basis.clear();
    opened.value = false;
    from.value = "";
    to.value = "";
  },
);
watch([from, to], () => basis.clear());
watch(
  () => props.refreshKey,
  () => {
    if (opened.value) load();
  },
);
</script>

<template>
  <section data-test="analysis-basis">
    <h3>资产走势与收益分析</h3>
    <form class="ledger-grid" @submit.prevent="load">
      <label
        >开始日期<input v-model="from" name="basis_from" type="date"
      /></label>
      <label>截止日期<input v-model="to" name="basis_to" type="date" /></label>
      <button :disabled="basis.loading || (!!from && !!to && from > to)">
        查看 / 刷新分析依据
      </button>
    </form>
    <p>
      选择日期后查看账户资产走势和收益；未指定开始日期时，以首个明确日终资产为正常计算基准。
    </p>
    <p v-if="basis.loading" role="status">正在读取分析依据…</p>
    <p v-if="basis.error" role="alert">{{ basis.error }}</p>
    <details v-if="!basis.data" data-test="analysis-source">
      <summary>查看数据来源与计算依据</summary>
      <p>
        导入、手工记录和自动估值会合并为同一账户曲线；明确开始日期时，期初取开始日前最后一笔有效资产。未来记录不进入分析。
      </p>
      <p>
        单次最多读取截至截止日 10,000 条原始记录/估值、10,000 条变更及区间内
        10,000 条合并明细。超限时不截断，请缩短截止日期后重试。
      </p>
    </details>
    <template v-if="basis.data">
      <LedgerReturns
        :points="basis.data.points"
        :result="basis.data.returns"
        :currency="basis.data.currency"
      />
      <dl class="ledger-stats" data-test="analysis-summary">
        <div>
          <dt>分析期间</dt>
          <dd>
            <template
              v-if="
                basis.data.returns.effective_from &&
                basis.data.returns.effective_to
              "
            >
              {{ basis.data.returns.effective_from }} 至
              {{ basis.data.returns.effective_to }}
            </template>
            <template v-else>尚未形成有效区间</template>
          </dd>
        </div>
        <div>
          <dt>期初资产</dt>
          <dd>
            {{
              basis.data.opening?.assets ??
              (basis.data.returns.start_mode === "baseline"
                ? "基准模式：以首个明确日终资产起算"
                : "尚无开始日前资产记录")
            }}
          </dd>
        </div>
        <div>
          <dt>期末资产</dt>
          <dd>{{ basis.data.closing?.assets ?? "尚无截止资产记录" }}</dd>
        </div>
        <div>
          <dt>期间净流入</dt>
          <dd>{{ basis.data.net_flow }} {{ basis.data.currency }}</dd>
        </div>
      </dl>
      <LedgerAssetChart
        :points="basis.data.points"
        :currency="basis.data.currency"
        :from="basis.data.from"
        :to="basis.data.to"
        :revision="basis.data.revision"
      />
      <details data-test="analysis-source">
        <summary>查看数据来源与计算依据</summary>
        <p role="status">
          {{ labels[basis.data.status] ?? basis.data.status }}
        </p>
        <p v-if="basis.data.previous_basis_affected">
          上次读取依据已受变更影响；本次重新投影账户记录，历史估值没有重算。
        </p>
        <p>
          分析截止
          {{
            basis.data.to
          }}（北京时间）。导入初始化、后续人工记录和自动估值属于同一账户、同一条曲线。未来记录不进入分析。明确开始日期时，期初取开始日之前最后有效资产，期间资金流含首尾日，同行资产已含资金进出。资产图不是收益率曲线。
        </p>
        <p v-if="basis.data.closing">
          {{ labels[basis.data.closing.status] ?? basis.data.closing.status }}；
          最后记录 {{ basis.data.closing.date }} / 同日顺序
          {{ basis.data.closing.sequence }}；资产来源日期
          {{ basis.data.closing.source_date || "无" }} / 来源记录
          {{ basis.data.closing.source_id || "无" }} / 来源版本
          {{ basis.data.closing.source_version || "无" }}。
        </p>
        <p>
          同日最后有效资产/资金记录胜出，日志不抹除资产。账户级沿用金额不加资金流，不是真实新增估值；持仓估值按日期、保存顺序取最后观察，未追踪或过期观察不能作为已核实结果。
        </p>
        <p>
          单次最多读取截至截止日 10,000 条原始记录/估值、10,000 条变更及区间内
          10,000 条合并明细。超限时不截断，请缩短截止日期后重试。
        </p>
        <h4>版本与变更影响（{{ basis.data.changes.length }} 项）</h4>
        <p class="ledger-note">
          依据版本 {{ basis.data.revision }}；变更水位
          {{ basis.data.change_revision }}
        </p>
        <p
          v-for="change in basis.data.changes"
          :key="change.revision"
          class="ledger-note"
        >
          {{ change.source_id }} · {{ change.reason }} · 自
          {{ change.from }}
          起的下游依赖可能失效。首次读取仅列出变更，不表示此前已有过期分析。
        </p>
      </details>
    </template>
  </section>
</template>

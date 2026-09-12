<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { errorText, LedgerError, query, request, type Account } from "./ledger";
import type { EffectiveSummary } from "./accountRecords";
import {
  returnPercent,
  returnReasons,
  returnSourceNotes,
  returnWarnings,
  type ReturnMetric,
} from "./ledgerReturns";
import {
  money,
  todayShanghai,
  validDay,
  validSummary,
  validateBasis,
  type AnalysisBasis,
} from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import LedgerReturnChart from "./LedgerReturnChart.vue";
import { validBenchmark, type Benchmark } from "./ledgerBenchmark";

const props = defineProps<{ account: Account; refreshKey: number }>();
const emit = defineEmits<{
  locate: [event: { id: string; date: string; accountId: string }];
}>();
const { locked } = useLedgerWorkspace();
const summary = reactive(useLedgerRead<EffectiveSummary>());
const basis = reactive(useLedgerRead<AnalysisBasis>());
const benchmark = reactive(useLedgerRead<Benchmark>());
const range = ref("all");
const today = ref(todayShanghai());
const customFrom = ref("");
const customTo = ref(today.value);
const view = ref("personal");
const chartMode = ref<"rate" | "profit">("rate");
const benchmarkOn = ref(false);
const bounds = computed(() => {
  const year = today.value.slice(0, 4);
  const start = new Date(`${today.value}T00:00:00Z`);
  const year1 = new Date(start);
  year1.setUTCFullYear(year1.getUTCFullYear() - 1);
  return range.value === "year"
    ? { from: `${year}-01-01`, to: today.value }
    : range.value === "year1"
      ? { from: year1.toISOString().slice(0, 10), to: today.value }
      : range.value === "custom"
        ? { from: customFrom.value, to: customTo.value }
        : { from: "", to: today.value };
});
const rangeError = computed(() => {
  const { from, to } = bounds.value;
  return (!from && range.value === "custom") ||
    (from && !validDay(from)) ||
    !validDay(to) ||
    from > to ||
    to > today.value
    ? "请选择有效的收益起止日期，结束日期不能晚于北京时间今天。"
    : "";
});
const result = computed(() => basis.data?.returns);
const rate = computed(() =>
  view.value === "personal" ? result.value?.modified_dietz : result.value?.twr,
);
const annual = computed(() =>
  view.value === "personal" ? result.value?.xirr : result.value?.twr_annualized,
);
const metricKey = computed(() =>
  chartMode.value === "profit"
    ? "profit"
    : view.value === "personal"
      ? "modified_dietz"
      : "twr",
);
const metricText = (m?: ReturnMetric, amount = false) =>
  !m || m.value === null
    ? "—"
    : amount
      ? money(m.value)
      : returnPercent(m.value, m.percentage);
const statusText = (m?: ReturnMetric) =>
  !m
    ? "等待数据"
    : m.status === "reference"
      ? "仅供参考"
      : m.status === "unavailable"
        ? `不可计算：${returnReasons[m.reason] ?? "暂不可计算"}`
        : "可计算";
const effectiveRange = computed(() =>
  result.value?.effective_from &&
  result.value?.effective_to &&
  result.value.effective_from <= result.value.effective_to
    ? `${result.value.effective_from} 至 ${result.value.effective_to}`
    : "尚无可计算区间",
);
const benchmarkAvailable = computed(() => props.account.currency === "CNY");
const warningLabels: Record<string, string> = {
  carried_assets_unchanged:
    "部分资产按最近明确总资产加后续净转入推算，仅供参考。",
  twr_estimated_assets: returnWarnings.twr_estimated_assets!,
  sampled_valuation_not_daily_close:
    "自动估值是保存时的参考报价，不保证为当天收盘价。",
  short_period_extrapolation: "不足一年的年化会放大短期波动，不代表未来收益。",
};
const warnings = computed(() => [
  ...new Set([
    ...[result.value?.profit, rate.value, annual.value]
      .filter((m) => m?.status === "unavailable")
      .map((m) => returnReasons[m!.reason] ?? "当前指标暂不可计算"),
    ...(result.value?.warnings ?? [])
      .filter((w) => w !== "twr_estimated_assets" || view.value === "manager")
      .map((w) => warningLabels[w] ?? "当前结果仅供参考"),
  ]),
]);
const samples = computed(() => {
  const notes = result.value
    ? returnSourceNotes(
        result.value,
        basis.data?.points ?? [],
        metricKey.value,
        props.account.currency,
      )
    : new Map<string, string>();
  return (result.value?.curve ?? []).map((p) => ({
    date: p.date,
    value:
      p[metricKey.value].value === null
        ? null
        : Number(p[metricKey.value].value),
    text: metricText(p[metricKey.value], chartMode.value === "profit"),
    note: notes.get(p.record_id) ?? "",
  }));
});
const plottedSamples = computed(() =>
  samples.value.filter((p) => p.value !== null),
);
const flows = computed(() => {
  const rows: {
    id: string;
    date: string;
    amount: string;
    direction: "in" | "out";
  }[] = [];
  const from = result.value?.effective_from ?? "";
  const to = result.value?.effective_to ?? "";
  for (const p of basis.data?.points ?? []) {
    if (
      p.flow === null ||
      !/[1-9]/.test(p.flow) ||
      !p.record ||
      p.date < from ||
      p.date > to
    )
      continue;
    rows.push({
      id: p.record.id,
      date: p.date,
      amount: money(p.flow.replace(/^-/, "")),
      direction: p.flow.startsWith("-") ? "out" : "in",
    });
  }
  return rows;
});
function locate(event: { id: string; date: string }) {
  emit("locate", { ...event, accountId: props.account.id });
}
function loadBenchmark() {
  benchmark.clear();
  const r = result.value;
  if (
    !benchmarkOn.value ||
    !benchmarkAvailable.value ||
    !r?.effective_from ||
    !r.effective_to
  )
    return;
  const code = "H00300";
  const from = r.effective_from;
  const to = r.effective_to;
  void benchmark.load(async (signal) => {
    const data = await request<unknown>(
      `/benchmark${query({ code, from, to })}`,
      { signal },
    );
    if (!validBenchmark(data, code, from, to))
      throw new LedgerError("invalid_response");
    return data;
  });
}
function loadAnalysis() {
  basis.clear();
  if (rangeError.value) return;
  const a = props.account,
    { from, to } = bounds.value;
  void basis.load(async (signal) =>
    validateBasis(
      await request<AnalysisBasis>(
        `/accounts/${encodeURIComponent(a.id)}/analysis-basis${query({ from, to })}`,
        { signal },
      ),
      a,
      from,
      to,
    ),
  );
}
function loadSummary() {
  summary.clear();
  const id = props.account.id;
  void summary.load(async (signal) => {
    const s = await request<EffectiveSummary>(
      `/accounts/${encodeURIComponent(id)}/effective-summary`,
      { signal },
    );
    if (!validSummary(s)) throw new LedgerError("invalid_response");
    return s;
  });
}
watch(
  () => props.account.id,
  () => {
    range.value = "all";
    today.value = todayShanghai();
    customFrom.value = "";
    customTo.value = today.value;
    benchmarkOn.value = false;
    benchmark.clear();
    loadSummary();
    loadAnalysis();
  },
  { immediate: true },
);
watch(bounds, loadAnalysis);
watch(
  [
    benchmarkOn,
    () => result.value?.effective_from,
    () => result.value?.effective_to,
    benchmarkAvailable,
  ],
  loadBenchmark,
);
watch(
  () => props.refreshKey,
  () => {
    today.value = todayShanghai();
    loadSummary();
    loadAnalysis();
  },
);
</script>

<template>
  <section class="lp-income" aria-labelledby="income-title">
    <div class="lp-section-title">
      <h2 id="income-title">收益曲线</h2>
      <div class="lp-segment" aria-label="收益视角">
        <button :aria-pressed="view === 'personal'" @click="view = 'personal'">
          个人视角
        </button>
        <button :aria-pressed="view === 'manager'" @click="view = 'manager'">
          基金经理视角
        </button>
      </div>
    </div>
    <div class="lp-overview">
      <div class="lp-assets">
        <span
          >账本总资产 <small>{{ account.currency }}</small></span
        >
        <strong>{{ money(summary.data?.latest_assets) }}</strong>
        <small>{{
          summary.loading
            ? "正在读取"
            : summary.data?.latest_asset_date
              ? `以 ${summary.data.latest_asset_date} 明确资产加后续净转入计算`
              : summary.error
                ? "读取失败"
                : "尚未记录总资产"
        }}</small>
      </div>
      <div>
        <span
          >区间收益 <small>{{ account.currency }}</small></span
        >
        <strong
          :class="{ 'lp-negative': result?.profit.value?.startsWith('-') }"
          >{{ metricText(result?.profit, true) }}</strong
        ><small>{{ effectiveRange }}</small>
        <small v-if="result && result.period_days > 0" data-test="period-days"
          >统计时长 {{ result.period_days }} 天</small
        >
      </div>
      <div>
        <span>{{
          view === "personal" ? "资金加权收益率" : "时间加权收益率"
        }}</span>
        <strong :class="{ 'lp-negative': rate?.value?.startsWith('-') }">{{
          metricText(rate)
        }}</strong>
        <small
          >{{ view === "personal" ? "Modified Dietz" : "TWR"
          }}{{ rate?.status === "reference" ? " · 仅供参考" : "" }}</small
        >
      </div>
      <div data-test="annual-return">
        <span>{{ view === "personal" ? "XIRR 年化参考" : "TWR 年化参考" }}</span
        ><strong :class="{ 'lp-negative': annual?.value?.startsWith('-') }">{{
          metricText(annual)
        }}</strong>
        <small>{{
          view === "personal" ? "XIRR · 个人年化" : "TWR · 复合年化"
        }}</small>
        <small
          v-if="annual && annual.status !== 'available'"
          class="lp-metric-status"
          >{{ statusText(annual) }}</small
        >
      </div>
    </div>
    <p v-if="summary.error" class="lp-error" role="alert">
      {{ errorText(summary.error) }}
      <button :disabled="locked || summary.loading" @click="loadSummary">
        重试读取资产
      </button>
    </p>
    <div class="lp-chart-toolbar">
      <div class="lp-range" aria-label="收益区间">
        <button
          v-for="item in [
            ['all', '成立以来'],
            ['year1', '近1年'],
            ['year', '今年'],
            ['custom', '自定义'],
          ]"
          :key="item[0]"
          :disabled="locked"
          :aria-pressed="range === item[0]"
          @click="range = item[0]!"
        >
          {{ item[1] }}
        </button>
      </div>
      <div class="lp-segment" aria-label="曲线指标">
        <button
          :aria-pressed="chartMode === 'rate'"
          @click="chartMode = 'rate'"
        >
          收益率曲线
        </button>
        <button
          :aria-pressed="chartMode === 'profit'"
          @click="chartMode = 'profit'"
        >
          累计收益曲线
        </button>
      </div>
    </div>
    <div v-if="range === 'custom'" class="lp-date-range">
      <label
        >收益开始日期<input
          v-model="customFrom"
          type="date"
          :max="today"
          :disabled="locked" /></label
      ><span>至</span>
      <label
        >收益结束日期<input
          v-model="customTo"
          type="date"
          :max="today"
          :disabled="locked"
      /></label>
    </div>
    <p v-if="rangeError || basis.error" class="lp-error" role="alert">
      {{ errorText(rangeError || basis.error) }}
      <button
        v-if="basis.error && !rangeError"
        :disabled="locked || basis.loading"
        @click="loadAnalysis"
      >
        重试读取收益
      </button>
    </p>
    <div
      v-else-if="basis.loading"
      class="lp-empty lp-chart-empty"
      role="status"
    >
      正在读取收益记录…
    </div>
    <div
      v-else-if="result && result.days > 0 && plottedSamples.length"
      class="lp-chart"
    >
      <LedgerReturnChart
        :mode="chartMode"
        :samples="samples"
        :flows="flows"
        :benchmark="benchmark.data ?? null"
        :currency="account.currency"
        @locate="locate"
      />
      <div class="lp-chart-footer">
        <div class="lp-legend" aria-label="图例">
          <template v-if="chartMode === 'profit'">
            <span><i class="lp-in" />转入</span
            ><span><i class="lp-out" />转出</span>
          </template>
          <button
            v-if="chartMode === 'rate'"
            type="button"
            class="lp-benchmark"
            :aria-pressed="benchmarkOn"
            :disabled="locked || !benchmarkAvailable"
            :title="
              benchmarkAvailable
                ? '叠加沪深300全收益对比'
                : '账户非人民币计价，暂不支持对比'
            "
            @click="benchmarkOn = !benchmarkOn"
          >
            沪深300全收益
            <small v-if="benchmark.loading">读取中…</small>
          </button>
        </div>
        <p>
          曲线只连接已有数值，跳过无数值日期，不补零；转入、转出同日总资产为资金变动后金额。
        </p>
      </div>
      <p v-if="benchmark.error" class="lp-error" role="alert">
        {{ errorText(benchmark.error) }}
        <button :disabled="locked || benchmark.loading" @click="loadBenchmark">
          重试读取指数
        </button>
      </p>
    </div>
    <div
      v-else-if="!rangeError && !basis.error"
      class="lp-empty lp-chart-empty"
    >
      <h3>这个区间还画不出曲线</h3>
      <p>
        {{
          result?.profit.reason === "missing_opening"
            ? range === "all"
              ? "先用“记一笔”记录总资产；两个不同日期的资产记录可形成收益区间。"
              : "缺少期初总资产，请补充开始日期之前的总资产，或选择成立以来。"
            : result?.days === 0
              ? "需要两个不同日期的有效总资产记录，才能形成收益区间。"
              : "请调整收益区间或补充总资产记录。缺失金额不会按零计算。"
        }}
      </p>
    </div>
    <p v-if="warnings.length" class="lp-reference">{{ warnings.join(" ") }}</p>
  </section>
</template>

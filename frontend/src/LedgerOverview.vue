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
import {
  accountEncoding,
  benchmarkDash,
  benchmarkDefinitions,
  benchmarkEncodings,
  validBenchmark,
  type Benchmark,
  type BenchmarkCode,
} from "./ledgerBenchmark";

const props = defineProps<{ account: Account; refreshKey: number }>();
const emit = defineEmits<{
  locate: [event: { id: string; date: string; accountId: string }];
}>();

type BenchmarkRead = {
  data?: Benchmark;
  error: string;
  loading: boolean;
  clear: () => void;
  load: (read: (signal: AbortSignal) => Promise<Benchmark>) => Promise<void>;
};
const { locked } = useLedgerWorkspace();
const summary = reactive(useLedgerRead<EffectiveSummary>());
const basis = reactive(useLedgerRead<AnalysisBasis>());
const benchmarkReads = Object.fromEntries(
  benchmarkDefinitions.map((definition) => [
    definition.code,
    reactive(useLedgerRead<Benchmark>()),
  ]),
) as unknown as Record<BenchmarkCode, BenchmarkRead>;
const selectedBenchmarks = ref<BenchmarkCode[]>([]);
const range = ref("all");
const today = ref(todayShanghai());
const customFrom = ref("");
const customTo = ref(today.value);
const view = ref("personal");
const chartMode = ref<"rate" | "profit">("rate");
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
const selectedDefinitions = computed(() =>
  benchmarkDefinitions.filter((definition) =>
    selectedBenchmarks.value.includes(definition.code),
  ),
);
const activeBenchmarks = computed(() =>
  selectedDefinitions.value
    .map((definition) => benchmarkReads[definition.code].data)
    .filter((benchmark): benchmark is Benchmark => !!benchmark),
);
const benchmarkErrors = computed(() =>
  selectedDefinitions.value
    .filter((definition) => benchmarkReads[definition.code].error)
    .map((definition) => ({
      code: definition.code,
      name: definition.name,
      error: benchmarkReads[definition.code].error,
    })),
);
const benchmarkCurrencyNotes = computed(() =>
  selectedDefinitions.value
    .filter((definition) => definition.currency !== props.account.currency)
    .map(
      (definition) =>
        `${definition.name} 以 ${definition.currency} 计价，与账户 ${props.account.currency} 未做汇率调整，仅比较涨跌幅。`,
    ),
);
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
      .filter((w) => w !== "twr_estimated_assets")
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
function isBenchmarkSelected(code: BenchmarkCode) {
  return selectedBenchmarks.value.includes(code);
}
function toggleBenchmark(code: BenchmarkCode) {
  selectedBenchmarks.value = isBenchmarkSelected(code)
    ? selectedBenchmarks.value.filter((selected) => selected !== code)
    : [...selectedBenchmarks.value, code];
}
function loadBenchmark(code: BenchmarkCode) {
  const read = benchmarkReads[code];
  read.clear();
  const r = result.value;
  if (!isBenchmarkSelected(code) || !r?.effective_from || !r.effective_to)
    return;
  const from = r.effective_from;
  const to = r.effective_to;
  void read.load(async (signal) => {
    const data = await request<unknown>(
      `/benchmark${query({ code, from, to })}`,
      { signal },
    );
    if (!validBenchmark(data, code, from, to))
      throw new LedgerError("invalid_response");
    return data;
  });
}
function loadBenchmarks() {
  for (const definition of benchmarkDefinitions) loadBenchmark(definition.code);
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
    selectedBenchmarks.value = [];
    for (const definition of benchmarkDefinitions)
      benchmarkReads[definition.code].clear();
    loadSummary();
    loadAnalysis();
  },
  { immediate: true },
);
watch(bounds, loadAnalysis);
watch(
  [
    () => selectedBenchmarks.value.join(","),
    () => result.value?.effective_from,
    () => result.value?.effective_to,
  ],
  loadBenchmarks,
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
        <small>{{ view === "personal" ? "Modified Dietz" : "TWR" }}</small>
      </div>
      <div data-test="annual-return">
        <span>{{
          view === "personal" ? "XIRR年化收益率" : "TWR年化收益率"
        }}</span
        ><strong :class="{ 'lp-negative': annual?.value?.startsWith('-') }">{{
          metricText(annual)
        }}</strong>
        <small>{{
          view === "personal" ? "XIRR · 个人年化" : "TWR · 复合年化"
        }}</small>
        <small
          v-if="annual && annual.status === 'unavailable'"
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
        :benchmarks="activeBenchmarks"
        @locate="locate"
      />
      <div class="lp-chart-footer">
        <div class="lp-legend" aria-label="图例">
          <template v-if="chartMode === 'profit'">
            <span><i class="lp-in" />转入</span
            ><span><i class="lp-out" />转出</span>
          </template>
          <template v-else>
            <span class="lp-account-legend">
              <svg
                class="lp-swatch"
                viewBox="0 0 34 12"
                aria-hidden="true"
                focusable="false"
              >
                <line
                  x1="1"
                  y1="6"
                  x2="33"
                  y2="6"
                  :stroke="accountEncoding.color"
                  stroke-width="1.8"
                />
              </svg>
              账户
            </span>
            <button
              v-for="definition in benchmarkDefinitions"
              :key="definition.code"
              type="button"
              class="lp-benchmark"
              :aria-pressed="isBenchmarkSelected(definition.code)"
              :disabled="locked"
              :title="`叠加${definition.name}（${definition.source}，${definition.currency}）`"
              @click="toggleBenchmark(definition.code)"
            >
              <svg
                class="lp-swatch"
                viewBox="0 0 34 12"
                aria-hidden="true"
                focusable="false"
              >
                <line
                  x1="1"
                  y1="6"
                  x2="33"
                  y2="6"
                  :stroke="benchmarkEncodings[definition.code].color"
                  stroke-width="1.5"
                  :stroke-dasharray="benchmarkDash(definition.code)"
                />
              </svg>
              {{ definition.name }}
              <small v-if="benchmarkReads[definition.code].loading"
                >读取中…</small
              >
            </button>
          </template>
        </div>
      </div>
      <p
        v-if="benchmarkCurrencyNotes.length"
        class="lp-chart-note"
        role="status"
      >
        {{ benchmarkCurrencyNotes.join(" ") }}
      </p>
      <p
        v-for="item in benchmarkErrors"
        :key="item.code"
        class="lp-error"
        role="alert"
      >
        {{ item.name }}：{{ errorText(item.error) }}
        <button
          :disabled="locked || benchmarkReads[item.code].loading"
          @click="loadBenchmark(item.code)"
        >
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

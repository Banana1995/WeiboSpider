<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from "vue";
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
  type AnalysisBasis,
} from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerCachedRead } from "./useLedgerCachedRead";
import {
  accountBasisRequest,
  accountSummaryRequest,
  defaultBenchmarks,
  peekBenchmark,
  readBenchmark,
} from "./ledgerCachedRequests";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import LedgerReturnChart from "./LedgerReturnChart.vue";
import LedgerAnnualReturns from "./LedgerAnnualReturns.vue";
import LedgerPortfolioContributions from "./LedgerPortfolioContributions.vue";
import {
  validatePortfolioBasis,
  type Portfolio,
  type PortfolioBasis,
} from "./ledgerPortfolios";
import {
  accountEncoding,
  benchmarkDash,
  benchmarkDefinitions,
  benchmarkEncodings,
  benchmarkLookbackFrom,
  type Benchmark,
  type BenchmarkCode,
} from "./ledgerBenchmark";

const props = defineProps<{
  account: Pick<Account, "id" | "name" | "currency">;
  portfolio?: Portfolio;
  refreshKey: number;
}>();
const emit = defineEmits<{
  locate: [event: { id: string; date: string; accountId: string }];
  account: [id: string];
  ready: [id: string];
  loading: [id: string];
}>();

type BenchmarkRead = {
  data?: Benchmark;
  error: string;
  loading: boolean;
  clear: () => void;
  load: (read: (signal: AbortSignal) => Promise<Benchmark>) => Promise<void>;
};
const { locked, accountCache, benchmarkCache, cacheEpoch } =
  useLedgerWorkspace();
const summary = reactive(useLedgerCachedRead<EffectiveSummary>(accountCache));
const basis = reactive(useLedgerCachedRead<AnalysisBasis>(accountCache));
const portfolioData = computed(() =>
  props.portfolio ? (basis.data as PortfolioBasis | undefined) : undefined,
);
const section = ref<"overall" | "members">("overall");
const titleID = computed(() =>
  props.portfolio ? `portfolio-income-${props.account.id}` : "income-title",
);
const annualOpened = ref(false);
const basisDetails = ref<HTMLDetailsElement>();
const entryPage = ref(0);
const entryRows = computed(
  () =>
    portfolioData.value?.entries.slice(
      entryPage.value * 20,
      (entryPage.value + 1) * 20,
    ) ?? [],
);
const benchmarkReads = Object.fromEntries(
  benchmarkDefinitions.map((definition) => [
    definition.code,
    reactive(useLedgerRead<Benchmark>()),
  ]),
) as unknown as Record<BenchmarkCode, BenchmarkRead>;
const selectedBenchmarks = ref<BenchmarkCode[]>([...defaultBenchmarks]);
const benchmarkRanges = new Map<BenchmarkCode, string>();
const range = ref("all");
const today = ref(todayShanghai());
const customFrom = ref("");
const customTo = ref(today.value);
const view = ref<"personal" | "manager">("personal");
const chartMode = ref<"rate" | "profit" | "assets">("rate");
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
      .filter(
        (w) => w !== "twr_estimated_assets" && w !== "portfolio_carried_assets",
      )
      .map((w) => warningLabels[w] ?? "当前结果仅供参考"),
  ]),
]);
const samples = computed(() => {
  if (chartMode.value === "assets") {
    const points = new Map(
      (basis.data?.points ?? [])
        .filter((p) => p.selected)
        .map((p) => [p.date, p]),
    );
    if (result.value?.opening)
      points.set(result.value.effective_from, {
        ...result.value.opening,
        date: result.value.effective_from,
      });
    return [...points.values()]
      .sort((a, b) => a.date.localeCompare(b.date))
      .map((p) => ({
        date: p.date,
        value: p.assets === null ? null : Number(p.assets),
        text: money(p.assets),
        note: "",
      }));
  }
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
  if (portfolioData.value)
    return portfolioData.value.entries
      .filter((e) => /[1-9]/.test(e.amount))
      .map((e) => ({
        id: e.id,
        date: e.date,
        amount: money(e.amount.replace(/^-/, "")),
        direction: e.amount.startsWith("-")
          ? ("out" as const)
          : ("in" as const),
        label: e.kind === "opening" ? "期初资产带入" : undefined,
      }));
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
  if (props.portfolio) {
    const entry = portfolioData.value?.entries.find((e) => e.id === event.id);
    if (!entry) return;
    if (entry.record_id) {
      emit("locate", {
        id: entry.record_id,
        date: entry.date,
        accountId: entry.account_id,
      });
    } else {
      entryPage.value = Math.floor(
        portfolioData.value!.entries.indexOf(entry) / 20,
      );
      if (basisDetails.value) basisDetails.value.open = true;
      void nextTick(() =>
        document
          .getElementById(`portfolio-entry-${entry.id}`)
          ?.scrollIntoView?.({ block: "center" }),
      );
    }
    return;
  }
  emit("locate", { ...event, accountId: props.account.id });
}
const memberNames = computed(
  () =>
    new Map(
      portfolioData.value?.members.map((m) => [m.account_id, m.name]) ?? [],
    ),
);
const joinMarkers = computed(() =>
  portfolioData.value?.members
    .filter(
      (m) =>
        m.first_date &&
        m.first_date >= (result.value?.effective_from ?? "") &&
        m.first_date <= (result.value?.effective_to ?? ""),
    )
    .map((m) => ({
      date: m.first_date,
      text: `${m.name} 开始计入，初始资产 ${money(m.initial_assets)} ${props.account.currency}`,
    })),
);
function isBenchmarkSelected(code: BenchmarkCode) {
  return selectedBenchmarks.value.includes(code);
}
function toggleBenchmark(code: BenchmarkCode) {
  selectedBenchmarks.value = isBenchmarkSelected(code)
    ? selectedBenchmarks.value.filter((selected) => selected !== code)
    : [...selectedBenchmarks.value, code];
}
function loadBenchmark(code: BenchmarkCode, force = false) {
  const read = benchmarkReads[code];
  const r = result.value;
  if (!isBenchmarkSelected(code) || !r?.effective_from || !r.effective_to)
    return;
  const from = benchmarkLookbackFrom(r.effective_from);
  const to = r.effective_to;
  const key = `${from}|${to}`;
  if (
    benchmarkRanges.get(code) === key &&
    (read.data || read.loading || read.error)
  )
    return;
  read.clear();
  benchmarkRanges.set(code, key);
  if (!force) read.data = peekBenchmark(benchmarkCache, code, from, to);
  if (!read.data)
    void read.load((signal) =>
      readBenchmark(benchmarkCache, code, from, to, signal, force),
    );
}
function retryBenchmark(code: BenchmarkCode) {
  benchmarkRanges.delete(code);
  loadBenchmark(code, true);
}
function loadBenchmarks() {
  for (const definition of benchmarkDefinitions) loadBenchmark(definition.code);
}
function loadAnalysis() {
  basis.clear();
  entryPage.value = 0;
  if (rangeError.value || locked.value) return;
  const a = props.account,
    { from, to } = bounds.value;
  emit("loading", a.id);
  if (props.portfolio) {
    const portfolio = props.portfolio;
    void basis.load({
      key: `portfolio|${portfolio.id}|${portfolio.version}|${portfolio.currency}|${from}|${to}`,
      read: async (signal) =>
        validatePortfolioBasis(
          await request<PortfolioBasis>(
            `/portfolios/${encodeURIComponent(a.id)}/analysis-basis${query({ from, to })}`,
            { signal },
          ),
          portfolio,
          from,
          to,
        ),
    });
    return;
  }
  void basis.load(accountBasisRequest(a, from, to)).then(() => {
    if (
      props.account.id === a.id &&
      bounds.value.from === from &&
      bounds.value.to === to &&
      basis.data &&
      !basis.loading &&
      !basis.error
    )
      emit("ready", a.id);
  });
}
function loadSummary() {
  summary.clear();
  if (props.portfolio || locked.value) return;
  void summary.load(accountSummaryRequest(props.account));
}
watch(
  () => props.account.id,
  () => {
    range.value = "all";
    today.value = todayShanghai();
    customFrom.value = "";
    customTo.value = today.value;
    selectedBenchmarks.value = [...defaultBenchmarks];
    benchmarkRanges.clear();
    for (const definition of benchmarkDefinitions)
      benchmarkReads[definition.code].clear();
    loadSummary();
    loadAnalysis();
  },
  { immediate: true },
);
watch(bounds, loadAnalysis);
watch(
  () => props.portfolio?.version,
  () => {
    if (props.portfolio) loadAnalysis();
  },
);
watch(
  [
    () => props.account.id,
    () => selectedBenchmarks.value.join(","),
    () => result.value?.effective_from,
    () => result.value?.effective_to,
  ],
  loadBenchmarks,
  { immediate: true },
);
watch(benchmarkErrors, (errors, _, onCleanup) => {
  if (!errors.length) return;
  // Only an unavailable curve polls locally while the independent worker
  // warms the DB; ordinary chart reads never contact market sources.
  const timer = setInterval(() => {
    for (const item of errors) retryBenchmark(item.code);
  }, 15000);
  onCleanup(() => clearInterval(timer));
});
watch([() => props.refreshKey, cacheEpoch], () => {
  today.value = todayShanghai();
  benchmarkRanges.clear();
  for (const definition of benchmarkDefinitions)
    benchmarkReads[definition.code].clear();
  loadSummary();
  loadAnalysis();
  loadBenchmarks();
});
</script>

<template>
  <div
    v-if="portfolio"
    class="lp-segment lp-analysis-tabs"
    aria-label="组合分析内容"
  >
    <button :aria-pressed="section === 'overall'" @click="section = 'overall'">
      整体表现
    </button>
    <button :aria-pressed="section === 'members'" @click="section = 'members'">
      账户贡献
    </button>
  </div>
  <section class="lp-income" :aria-labelledby="titleID">
    <div class="lp-section-title">
      <h2 :id="titleID">
        {{
          portfolio
            ? section === "members"
              ? "账户贡献"
              : "组合表现"
            : "收益曲线"
        }}
      </h2>
      <div class="lp-segment" aria-label="收益视角">
        <button :aria-pressed="view === 'personal'" @click="view = 'personal'">
          个人视角
        </button>
        <button :aria-pressed="view === 'manager'" @click="view = 'manager'">
          基金经理视角
        </button>
      </div>
    </div>
    <div
      v-if="portfolioData?.fx.length"
      class="lp-reference"
      data-test="portfolio-fx"
    >
      <p>
        金额统一折算为
        {{
          account.currency
        }}；历史资产、资金流使用本次最新可用汇率，不计历史汇率波动收益。
      </p>
      <p v-for="fx in portfolioData.fx" :key="fx.base">
        1 {{ fx.base }} = {{ fx.rate }} {{ fx.quote }} · 汇率日期
        {{ fx.date }} · {{ fx.source }}
      </p>
    </div>
    <div v-if="section === 'overall'" class="lp-overview">
      <div class="lp-assets">
        <span
          >{{ portfolio ? "组合期末资产" : "账本总资产" }}
          <small>{{ account.currency }}</small></span
        >
        <strong>{{
          money(
            portfolio ? result?.closing?.assets : summary.data?.latest_assets,
          )
        }}</strong>
        <small v-if="portfolio">{{
          basis.loading
            ? "正在读取"
            : result?.effective_to
              ? `截至 ${result.effective_to}，汇总所选成员资产`
              : "尚无可计算资产"
        }}</small>
        <small v-else>{{
          summary.loading && !summary.data
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
          portfolio
            ? "年化收益率"
            : view === "personal"
              ? "XIRR年化收益率"
              : "TWR年化收益率"
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
    <p
      v-if="(basis.loading && basis.data) || (summary.loading && summary.data)"
      class="lp-reference"
      role="status"
      data-test="cached-overview"
    >
      已显示上次读取的结果，正在核对最新数据…
    </p>
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
            ['all', portfolio ? '全部记录' : '成立以来'],
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
      <div
        v-if="section === 'overall'"
        class="lp-segment"
        aria-label="曲线指标"
      >
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
        <button
          v-if="portfolio"
          :aria-pressed="chartMode === 'assets'"
          @click="chartMode = 'assets'"
        >
          资产曲线
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
      v-if="basis.loading && !basis.data && !rangeError"
      class="lp-empty lp-chart-empty"
      role="status"
    >
      正在读取收益记录…
    </div>
    <LedgerPortfolioContributions
      v-else-if="!rangeError && portfolioData && section === 'members'"
      :basis="portfolioData"
      :view="view"
      @account="emit('account', $event)"
    />
    <div
      v-else-if="
        !rangeError &&
        result &&
        (result.days > 0 || chartMode === 'assets') &&
        plottedSamples.length
      "
      class="lp-chart"
    >
      <LedgerReturnChart
        :mode="chartMode"
        :from="result.effective_from"
        :samples="samples"
        :flows="flows"
        :benchmarks="activeBenchmarks"
        :series-name="portfolio ? '组合' : '账户'"
        :markers="joinMarkers"
        @locate="locate"
      />
      <div class="lp-chart-footer">
        <div class="lp-legend" aria-label="图例">
          <template v-if="chartMode === 'profit'">
            <span><i class="lp-in" />转入</span
            ><span><i class="lp-out" />转出</span>
          </template>
          <template v-else-if="chartMode === 'rate'">
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
              {{ portfolio ? "组合" : "账户" }}
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
          <span v-else>组合总资产，成员未更新期间按资产与净转入沿用</span>
        </div>
      </div>
      <p
        v-for="item in benchmarkErrors"
        :key="item.code"
        class="lp-error"
        role="alert"
      >
        {{ item.name }}：{{ errorText(item.error) }}
        <button
          :disabled="locked || benchmarkReads[item.code].loading"
          @click="retryBenchmark(item.code)"
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
  <LedgerAnnualReturns
    v-if="!portfolio"
    :account="account"
    :refresh-key="refreshKey"
    :view="view"
    @view="view = $event"
  />
  <template v-else-if="section === 'overall'">
    <details
      class="lp-portfolio-details"
      @toggle="annualOpened = ($event.target as HTMLDetailsElement).open"
    >
      <summary>年度收益对比</summary>
      <LedgerAnnualReturns
        v-if="annualOpened"
        :key="portfolio.version"
        :account="account"
        portfolio
        hide-heading
        :refresh-key="refreshKey"
        :view="view"
        @view="view = $event"
      />
    </details>
    <details ref="basisDetails" class="lp-portfolio-details">
      <summary>计算依据与资金记录</summary>
      <div class="lp-portfolio-basis">
        <p>
          组合从最早的成员记录开始；成员首次出现时补足期初资产带入，并扣除当日已记录的净转入。明确总资产包含当日全部出入金；两次更新之间按无投资涨跌处理。
        </p>
        <p>
          资金加权收益率采用 Modified Dietz，年化采用
          XIRR。以下列出所选区间的原始资金流和分析中的期初带入；计算收益时，统计起点当日的资金已包含在期初资产中。
        </p>
        <div
          v-for="entry in entryRows"
          :id="`portfolio-entry-${entry.id}`"
          :key="entry.id"
          class="lp-portfolio-entry"
        >
          <span
            >{{ entry.date }} · {{ memberNames.get(entry.account_id) }}</span
          >
          <span
            >{{
              entry.kind === "opening"
                ? "期初资产带入"
                : entry.amount.startsWith("-")
                  ? "转出"
                  : "转入"
            }}
            {{ money(entry.amount) }} {{ account.currency }}</span
          >
          <button
            v-if="entry.record_id"
            class="lp-text-button"
            @click="locate({ id: entry.id, date: entry.date })"
          >
            查看原始记录
          </button>
        </div>
        <p v-if="!entryRows.length" class="lp-muted">此区间没有资金记录。</p>
        <div
          v-if="portfolioData && portfolioData.entries.length > 20"
          class="lp-dialog-footer"
        >
          <button :disabled="entryPage === 0" @click="entryPage--">
            上一页
          </button>
          <span>第 {{ entryPage + 1 }} 页</span>
          <button
            :disabled="(entryPage + 1) * 20 >= portfolioData.entries.length"
            @click="entryPage++"
          >
            下一页
          </button>
        </div>
      </div>
    </details>
  </template>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { LedgerError, query, request, type Account } from "./ledger";
import type { EffectiveSummary } from "./accountRecords";
import { returnPercent, returnReasons, returnSourceNotes, returnWarnings, type ReturnMetric } from "./ledgerReturns";
import { money, todayShanghai, validDay, validSummary, validateBasis, type AnalysisBasis } from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{ account: Account; refreshKey: number }>();
const emit = defineEmits<{ locate: [event: { id: string; date: string; accountId: string }] }>();
const { locked } = useLedgerWorkspace();
const summary = reactive(useLedgerRead<EffectiveSummary>());
const basis = reactive(useLedgerRead<AnalysisBasis>());
const range = ref("all");
const today = ref(todayShanghai());
const customFrom = ref("");
const customTo = ref(today.value);
const view = ref("personal");
const chartMode = ref("rate");
const hovered = ref("");
const bounds = computed(() => {
  const year = today.value.slice(0, 4);
  const last = (BigInt(year) - 1n).toString().padStart(4, "0");
  return range.value === "year" ? { from: `${year}-01-01`, to: today.value } :
    range.value === "last" ? { from: `${last}-01-01`, to: `${last}-12-31` } :
    range.value === "custom" ? { from: customFrom.value, to: customTo.value } : { from: "", to: today.value };
});
const rangeError = computed(() => {
  const { from, to } = bounds.value;
  return (!from && range.value === "custom") || (from && !validDay(from)) || !validDay(to) || from > to || to > today.value
    ? "请选择有效的收益起止日期，结束日期不能晚于北京时间今天。" : "";
});
const result = computed(() => basis.data?.returns);
const rate = computed(() => view.value === "personal" ? result.value?.modified_dietz : result.value?.twr);
const annual = computed(() => view.value === "personal" ? result.value?.xirr : result.value?.twr_annualized);
const metricKey = computed(() => chartMode.value === "profit" ? "profit" : view.value === "personal" ? "modified_dietz" : "twr");
const metricText = (m?: ReturnMetric, amount = false) => !m || m.value === null ? "—" : amount ? money(m.value) : returnPercent(m.value, m.percentage);
const statusText = (m?: ReturnMetric) => !m ? "等待数据" : m.status === "reference" ? "仅供参考" : m.status === "unavailable" ? `不可计算：${returnReasons[m.reason] ?? "暂不可计算"}` : "可计算";
const effectiveRange = computed(() => result.value?.effective_from && result.value?.effective_to && result.value.effective_from <= result.value.effective_to ?
  `${result.value.effective_from} 至 ${result.value.effective_to}` : "尚无可计算区间");
const warningLabels: Record<string, string> = {
  carried_assets_unchanged: "收益金额及个人视角：部分资产沿用较早原值，未加上转入资金，仅供参考；请补充实际总资产。",
  twr_estimated_assets: returnWarnings.twr_estimated_assets!,
  sampled_valuation_not_daily_close: "自动估值是保存时的参考报价，不保证为当天收盘价。",
  short_period_extrapolation: "不足一年的年化会放大短期波动，不代表未来收益。",
};
const warnings = computed(() => [...new Set([
  ...[result.value?.profit, rate.value, annual.value].filter(m => m?.status === "unavailable").map(m => returnReasons[m!.reason] ?? "当前指标暂不可计算"),
  ...(result.value?.warnings ?? []).filter(w => w !== "twr_estimated_assets" || view.value === "manager").map(w => warningLabels[w] ?? "当前结果仅供参考"),
])]);
const samples = computed(() => {
  const notes = result.value ? returnSourceNotes(result.value, basis.data?.points ?? [], metricKey.value, props.account.currency) : new Map<string, string>();
  return (result.value?.curve ?? []).map(p => {
    return { ...p, metric: p[metricKey.value], value: p[metricKey.value].value === null ? null : Number(p[metricKey.value].value),
      sourceNote: notes.get(p.record_id) ?? "" };
  });
});
const gaps = computed(() => [...new Set(samples.value.filter(p => p.metric.status === "unavailable").map(p => statusText(p.metric)))]);
const extent = computed(() => {
  let min = 0, max = 0;
  for (const p of samples.value) if (p.value !== null) { min = Math.min(min, p.value); max = Math.max(max, p.value); }
  return min === max ? [min - 1, max + 1] : [min, max];
});
const topSample = computed(() => samples.value.reduce<(typeof samples.value)[number] | undefined>((top, p) =>
  p.value !== null && (top?.value == null || p.value > top.value) ? p : top, undefined));
const times = computed(() => {
  const first = result.value?.effective_from || bounds.value.to;
  const last = result.value?.effective_to || first;
  return [Date.parse(`${first}T00:00:00Z`), Date.parse(`${last}T00:00:00Z`)];
});
const x = (date: string) => 36 + (Date.parse(`${date}T00:00:00Z`) - times.value[0]!) / Math.max(86400000, times.value[1]! - times.value[0]!) * 828;
const y = (value: number) => 178 - (value - extent.value[0]!) / (extent.value[1]! - extent.value[0]!) * 150;
const paths = computed(() => {
  const paths = { available: "", reference: "" };
  for (let i = 1; i < samples.value.length; i++) {
    const a = samples.value[i - 1]!, b = samples.value[i]!;
    if (a.value === null || b.value === null) continue;
    const state = a.metric.status === "reference" || b.metric.status === "reference" ? "reference" : "available";
    paths[state] += `M${x(a.date)},${y(a.value)} L${x(b.date)},${y(b.value)} `;
  }
  return paths;
});
const events = computed(() => {
  const rows: { id: string; date: string; flow: string; lane: number }[] = [];
  const lanes = new Map<number, number>();
  for (const p of basis.data?.points ?? []) {
    if (p.flow === null || !/[1-9]/.test(p.flow) || !p.record || p.date < (result.value?.effective_from ?? "") || p.date > (result.value?.effective_to ?? "")) continue;
    const bin = Math.round(x(p.date) / 26);
    const lane = lanes.get(bin) ?? 0;
    lanes.set(bin, lane + 1);
    rows.push({ id: p.record.id, date: p.record.date, flow: p.flow, lane });
  }
  return rows;
});
const eventHeight = computed(() => events.value.reduce((n, e) => Math.max(n, (e.lane + 1) * 26), 28));
const tooltip = computed(() => samples.value.find(p => p.date === hovered.value));
function loadAnalysis() {
  basis.clear();
  hovered.value = "";
  if (rangeError.value) return;
  const a = props.account, { from, to } = bounds.value;
  void basis.load(async signal => validateBasis(await request<AnalysisBasis>(
    `/accounts/${encodeURIComponent(a.id)}/analysis-basis${query({ from, to })}`, { signal }), a, from, to));
}
function loadSummary() {
  summary.clear();
  const id = props.account.id;
  void summary.load(async signal => {
    const s = await request<EffectiveSummary>(`/accounts/${encodeURIComponent(id)}/effective-summary`, { signal });
    if (!validSummary(s)) throw new LedgerError("invalid_response");
    return s;
  });
}
watch(() => props.account.id, () => {
  range.value = "all";
  today.value = todayShanghai();
  customFrom.value = "";
  customTo.value = today.value;
  loadSummary();
  loadAnalysis();
}, { immediate: true });
watch(bounds, loadAnalysis);
watch(() => props.refreshKey, () => { today.value = todayShanghai(); loadSummary(); loadAnalysis(); });
</script>

<template>
  <section class="lp-income" aria-labelledby="income-title">
    <div class="lp-section-title"><h2 id="income-title">收益曲线</h2>
      <div class="lp-segment" aria-label="收益视角">
        <button :aria-pressed="view === 'personal'" @click="view = 'personal'">个人视角</button>
        <button :aria-pressed="view === 'manager'" @click="view = 'manager'">基金经理视角</button>
      </div>
    </div>
    <div class="lp-overview">
      <div class="lp-assets"><span>最新总资产 <small>{{ account.currency }}</small></span>
        <strong>{{ money(summary.data?.latest_assets) }}</strong>
        <small>{{ summary.loading ? '正在读取' : summary.data?.latest_asset_date ? `记录于 ${summary.data.latest_asset_date}` : summary.error ? '读取失败' : '尚未记录总资产' }}</small>
      </div>
      <div><span>区间收益 <small>{{ account.currency }}</small></span>
        <strong :class="{ 'lp-negative': result?.profit.value?.startsWith('-') }">{{ metricText(result?.profit, true) }}</strong><small>{{ effectiveRange }}</small>
        <small v-if="result && result.period_days > 0" data-test="period-days">统计时长 {{ result.period_days }} 天</small>
      </div>
      <div><span>{{ view === 'personal' ? '资金加权收益率' : '时间加权收益率' }}</span>
        <strong :class="{ 'lp-negative': rate?.value?.startsWith('-') }">{{ metricText(rate) }}</strong>
        <small>{{ view === 'personal' ? 'Modified Dietz' : 'TWR' }}{{ rate?.status === 'reference' ? ' · 仅供参考' : '' }}</small>
      </div>
      <div data-test="annual-return"><span>{{ view === 'personal' ? 'XIRR 年化参考' : 'TWR 年化参考' }}</span><strong :class="{ 'lp-negative': annual?.value?.startsWith('-') }">{{ metricText(annual) }}</strong>
        <small>{{ view === 'personal' ? 'XIRR · 个人年化' : 'TWR · 复合年化' }}</small>
        <small v-if="annual && annual.status !== 'available'" class="lp-metric-status">{{ statusText(annual) }}</small>
      </div>
    </div>
    <p v-if="summary.error" class="lp-error" role="alert">最新资产读取失败，请刷新。{{ summary.error }}</p>
    <div class="lp-chart-toolbar">
      <div class="lp-range" aria-label="收益区间"><button v-for="item in [['all', '成立以来'], ['year', '今年'], ['last', '去年'], ['custom', '自定义']]" :key="item[0]"
        :disabled="locked" :aria-pressed="range === item[0]" @click="range = item[0]!">{{ item[1] }}</button></div>
      <div class="lp-segment" aria-label="曲线指标"><button :aria-pressed="chartMode === 'rate'" @click="chartMode = 'rate'">收益率</button>
        <button :aria-pressed="chartMode === 'profit'" @click="chartMode = 'profit'">收益金额</button></div>
    </div>
    <div v-if="range === 'custom'" class="lp-date-range">
      <label>收益开始日期<input v-model="customFrom" type="date" :max="today" :disabled="locked" /></label><span>至</span>
      <label>收益结束日期<input v-model="customTo" type="date" :max="today" :disabled="locked" /></label>
    </div>
    <p v-if="rangeError || basis.error" class="lp-error" role="alert">{{ rangeError || basis.error }}</p>
    <div v-else-if="basis.loading" class="lp-empty lp-chart-empty" role="status">正在读取收益记录…</div>
    <div v-else-if="result && result.days > 0 && samples.some(p => p.value !== null)" class="lp-chart">
      <div class="lp-chart-readout" aria-live="polite" tabindex="0" aria-label="收益采样点说明"><template v-if="tooltip">{{ tooltip.date }}
        <strong>{{ metricText(tooltip.metric, chartMode === 'profit') }}</strong> {{ statusText(tooltip.metric) }}<span v-if="tooltip.sourceNote"> · {{ tooltip.sourceNote }}</span></template>
        <span v-else>沿曲线查看收益，点击资金标记定位记录</span></div>
      <div class="lp-plot"><svg viewBox="0 0 900 210" preserveAspectRatio="none" aria-label="账户收益曲线">
        <title>实际记录日期的收益采样连线，虚线仅供参考，不可用指标处断开</title>
        <line v-for="n in [0, 0.5, 1]" :key="n" x1="36" x2="864" :y1="28 + 150 * n" :y2="28 + 150 * n" class="lp-gridline" />
        <line x1="36" x2="864" :y1="y(0)" :y2="y(0)" class="lp-zero" />
        <path :d="paths.available" class="lp-curve" /><path :d="paths.reference" class="lp-curve lp-reference-line" />
        <template v-for="p in samples" :key="p.date"><circle v-if="p.value !== null" :cx="x(p.date)" :cy="y(p.value)" r="4" tabindex="0" class="lp-point" :class="`lp-point-${p.metric.status}`"
          :aria-label="`${p.date} ${metricText(p.metric, chartMode === 'profit')} ${statusText(p.metric)} ${p.sourceNote}`"
          @mouseenter="hovered = p.date" @focus="hovered = p.date" @click="hovered = p.date">
          <title>{{ p.date }} {{ metricText(p.metric, chartMode === 'profit') }} {{ statusText(p.metric) }} {{ p.sourceNote }}</title>
        </circle></template>
      </svg>
      <span class="lp-axis-top">{{ topSample && topSample.value !== null && topSample.value > 0 ? metricText(topSample.metric, chartMode === 'profit') : '0' }}</span>
      <span class="lp-axis-zero" :style="{ top: `${y(0)}px` }">0</span></div>
      <div class="lp-event-scroll"><div class="lp-event-lane" :style="{ height: `${eventHeight}px` }" aria-label="资金事件">
        <button v-for="e in events" :key="e.id" class="lp-event" :class="e.flow.startsWith('-') ? 'lp-out' : 'lp-in'"
          :style="{ left: `${x(e.date) / 9}%`, top: `${e.lane * 26}px` }" :disabled="locked"
          :aria-label="`${e.date} ${e.flow.startsWith('-') ? '转出' : '转入'} ${money(e.flow.replace(/^-/, ''))}，定位记录`"
          :title="`${e.date} ${money(e.flow)} ${account.currency}`" @click="emit('locate', { id: e.id, date: e.date, accountId: account.id })">{{ e.flow.startsWith('-') ? '−' : '+' }}</button>
      </div></div>
      <div class="lp-axis-dates"><span>{{ result.effective_from }}</span><span>{{ result.effective_to }}</span></div>
    </div>
    <div v-else-if="!rangeError && !basis.error" class="lp-empty lp-chart-empty"><h3>这个区间还画不出曲线</h3>
      <p>{{ result?.profit.reason === 'missing_opening' ? '缺少期初总资产，请补充开始日期之前的总资产，或选择成立以来。' : result?.days === 0 ? '需要两个不同日期的有效总资产记录，才能形成收益区间。' : '请调整收益区间或补充总资产记录。缺失金额不会按零计算。' }}</p></div>
    <p v-if="gaps.length" class="lp-gap-reasons" data-test="curve-gaps">曲线断点：{{ gaps.join('；') }}。缺失值不补零，也不跨过断点连线。</p>
    <div class="lp-chart-footer"><div class="lp-legends">
      <div class="lp-legend" aria-label="收益状态图例">
        <span><svg viewBox="0 0 36 12" aria-hidden="true"><path d="M0 6H36" class="lp-curve" /><circle cx="18" cy="6" r="3" class="lp-point lp-point-available" /></svg>可计算：实线实心</span>
        <span><svg viewBox="0 0 36 12" aria-hidden="true"><path d="M0 6H36" class="lp-curve lp-reference-line" /><circle cx="18" cy="6" r="3" class="lp-point lp-point-reference" /></svg>仅供参考：虚线空心</span>
        <span><svg viewBox="0 0 36 12" aria-hidden="true"><path d="M0 6H10 M26 6H36" class="lp-gap-line" /></svg>不可计算：断点</span>
      </div>
      <div class="lp-legend" aria-label="资金事件图例"><span><i class="lp-in" />转入</span><span><i class="lp-out" />转出</span></div>
      </div>
      <p>最新资产独立于收益区间，不代表实时行情。曲线仅连接已知采样点，不代表每日收盘；转入、转出同日填写的总资产为资金变动后的总额。</p></div>
    <p v-if="warnings.length" class="lp-reference">{{ warnings.join(' ') }}</p>
  </section>
</template>

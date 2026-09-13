<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { init, use, type EChartsType } from "echarts/core";
import { LineChart, ScatterChart } from "echarts/charts";
import {
  AxisPointerComponent,
  GridComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import { returnPercent } from "./ledgerReturns";
import {
  accountEncoding,
  benchmarkEncodings,
  type Benchmark,
  type BenchmarkCode,
  type BenchmarkEncoding,
} from "./ledgerBenchmark";

use([
  LineChart,
  ScatterChart,
  AxisPointerComponent,
  GridComponent,
  TooltipComponent,
  CanvasRenderer,
]);

const props = defineProps<{
  mode: "rate" | "profit";
  samples: {
    date: string;
    value: number | null;
    text: string;
    note?: string;
  }[];
  flows: {
    id: string;
    date: string;
    amount: string;
    direction: "in" | "out";
  }[];
  benchmarks: Benchmark[];
}>();
const emit = defineEmits<{ locate: [{ id: string; date: string }] }>();

const TRANSFER_IN = "#bc514c";
const TRANSFER_OUT = "#2f7d5b";

const element = ref<HTMLDivElement>();
const chartError = ref("");
let chart: EChartsType | undefined;
let observer: ResizeObserver | undefined;

const plotted = computed(() => props.samples.filter((s) => s.value !== null));
const activeBenchmarks = computed(() =>
  props.mode === "rate" ? props.benchmarks : [],
);
const encodingFor = (code: string): BenchmarkEncoding =>
  benchmarkEncodings[code as BenchmarkCode] ?? { color: "#6f5b8f", dash: [] };

const time = (date: string) => Date.parse(`${date}T00:00:00Z`);

// Baseline is 0 at effective_from: before the first index item the series is flat,
// and each account date takes the last index close on or before it.
function alignSeries(benchmark: Benchmark) {
  let index = -1;
  return plotted.value.map((sample) => {
    while (
      index + 1 < benchmark.items.length &&
      benchmark.items[index + 1]!.date <= sample.date
    )
      index++;
    const item = index >= 0 ? benchmark.items[index] : undefined;
    return {
      value: [time(sample.date), item ? Number(item.return) : 0],
      text: returnPercent(item ? item.return : "0.00000000"),
    };
  });
}

function curveValue(date: string): number | null {
  const samples = plotted.value;
  let low = 0;
  let high = samples.length - 1;
  let match: (typeof samples)[number] | undefined;
  while (low <= high) {
    const middle = (low + high) >> 1;
    if (samples[middle]!.date <= date) {
      match = samples[middle];
      low = middle + 1;
    } else high = middle - 1;
  }
  return match?.value ?? null;
}

const flowSeries = computed(() =>
  props.mode === "profit"
    ? (["in", "out"] as const).map((direction) => ({
        name: direction === "in" ? "转入" : "转出",
        type: "scatter" as const,
        symbol: "circle",
        symbolSize: 7,
        z: 4,
        itemStyle: { color: direction === "in" ? TRANSFER_IN : TRANSFER_OUT },
        data: props.flows
          .filter((flow) => flow.direction === direction)
          .map((flow) => ({
            value: [time(flow.date), curveValue(flow.date)],
            flow: { id: flow.id, date: flow.date },
            text: `${flow.date} ${direction === "in" ? "转入" : "转出"} ${flow.amount}`,
          }))
          .filter((point) => point.value[1] !== null),
      }))
    : [],
);

function percentLabel(value: number): string {
  return `${(value * 100).toFixed(2)}%`;
}
function amountLabel(value: number): string {
  const sign = value < 0 ? "-" : "";
  return (
    sign +
    Math.round(Math.abs(value))
      .toString()
      .replace(/\B(?=(\d{3})+(?!\d))/g, ",")
  );
}

function tooltip(raw: unknown): string {
  const items = (Array.isArray(raw) ? raw : [raw]) as {
    value?: [number, number];
    seriesName?: string;
    seriesType?: string;
    data?: { text?: string; flow?: { id: string; date: string } };
  }[];
  const first = items.find((item) => Array.isArray(item.value))?.value;
  if (!first) return "";
  const date = new Date(first[0]).toISOString().slice(0, 10);
  const rows = items
    .filter((item) => item.seriesType === "line")
    .map(
      (item) =>
        `${item.seriesName}：${item.data?.text ?? item.value?.[1] ?? ""}`,
    );
  const events = items
    .filter((item) => item.seriesType === "scatter" && item.data?.text)
    .map((item) => item.data!.text!);
  return [date, ...rows, ...events].join("\n");
}

function onClick(params: unknown) {
  const flow = (params as { data?: { flow?: { id: string; date: string } } })
    .data?.flow;
  if (flow) emit("locate", flow);
}

function render() {
  if (!chart) return;
  try {
    const account = {
      name: "账户",
      type: "line" as const,
      data: plotted.value.map((sample) => ({
        value: [time(sample.date), sample.value],
        text: sample.text,
      })),
      showSymbol: false,
      symbol: "none",
      smooth: false,
      connectNulls: false,
      sampling: "lttb" as const,
      animation: false,
      lineStyle: {
        width: 1.8,
        color: accountEncoding.color,
        type: "solid" as const,
      },
      itemStyle: { color: accountEncoding.color },
      triggerLineEvent: true,
      emphasis: {
        focus: "series" as const,
        scale: false,
        lineStyle: { width: 2.8 },
      },
      blur: {
        lineStyle: { opacity: 0.15 },
        itemStyle: { opacity: 0.15 },
      },
    };
    const benchmarks = activeBenchmarks.value.map((benchmark) => {
      const encoding = encodingFor(benchmark.code);
      return {
        name: benchmark.name,
        type: "line" as const,
        data: alignSeries(benchmark),
        showSymbol: false,
        symbol: "none",
        smooth: false,
        connectNulls: false,
        sampling: "lttb" as const,
        animation: false,
        lineStyle: {
          width: 1.5,
          color: encoding.color,
          type: encoding.dash.length ? encoding.dash : ("solid" as const),
        },
        itemStyle: { color: encoding.color },
        triggerLineEvent: true,
        emphasis: {
          focus: "series" as const,
          scale: false,
          lineStyle: { width: 2.8 },
        },
        blur: {
          lineStyle: { opacity: 0.15 },
          itemStyle: { opacity: 0.15 },
        },
      };
    });
    chart.setOption(
      {
        animation: false,
        useUTC: true,
        grid: { left: 56, right: 18, top: 18, bottom: 30 },
        tooltip: { trigger: "axis", confine: true, formatter: tooltip },
        xAxis: {
          type: "time",
          axisLabel: { hideOverlap: true, formatter: "{yyyy}-{MM}-{dd}" },
          splitLine: { show: false },
        },
        yAxis: {
          type: "value",
          scale: true,
          axisLabel: {
            formatter: props.mode === "rate" ? percentLabel : amountLabel,
          },
          splitLine: { lineStyle: { color: "#edf0ec", type: "dashed" } },
        },
        series: [account, ...benchmarks, ...flowSeries.value],
      },
      true,
    );
    chartError.value = "";
  } catch {
    chartError.value = "图形暂不可用，收益数值仍以记录明细为准。";
  }
}

watch(() => [props.mode, props.samples, props.flows, props.benchmarks], render);
onMounted(() => {
  if (!element.value) return;
  try {
    chart = init(element.value, undefined, {
      devicePixelRatio: window.devicePixelRatio || 1,
    });
    chart.on("click", onClick);
    observer = new ResizeObserver(() => chart?.resize());
    observer.observe(element.value);
    render();
  } catch {
    chartError.value = "图形暂不可用，收益数值仍以记录明细为准。";
  }
});
onBeforeUnmount(() => {
  observer?.disconnect();
  chart?.dispose();
});
</script>

<template>
  <section class="lp-return-chart">
    <p v-if="chartError" class="lp-chart-error" role="status">
      {{ chartError }}
    </p>
    <div
      ref="element"
      class="lp-return-canvas"
      role="img"
      :aria-label="
        mode === 'profit'
          ? '累计收益曲线；红点为转入、绿点为转出，点选可定位记录'
          : '收益率曲线；账户为实线，对比指数使用不同虚线样式以区分'
      "
    />
  </section>
</template>

<style scoped>
.lp-return-chart {
  min-width: 0;
}
.lp-return-canvas {
  width: 100%;
  height: 320px;
}
@media (max-width: 700px) {
  .lp-return-canvas {
    height: 260px;
  }
}
</style>

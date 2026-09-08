<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { init, use, type EChartsType } from "echarts/core";
import { LineChart, ScatterChart } from "echarts/charts";
import {
  DataZoomComponent,
  GridComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import {
  returnPercent,
  returnReasons,
  type LedgerReturns,
} from "./ledgerReturns";
import { trendLines, type TrendMetric } from "./ledgerReturnTrend";
import type { BasisPoint } from "./ledgerChart";
use([
  LineChart,
  ScatterChart,
  DataZoomComponent,
  GridComponent,
  TooltipComponent,
  CanvasRenderer,
]);
const props = defineProps<{
  result: LedgerReturns;
  currency: string;
  manager: boolean;
  points: BasisPoint[];
}>();
const mode = ref("rate");
const key = computed<TrendMetric>(() =>
  mode.value === "profit" ? "profit" : props.manager ? "twr" : "modified_dietz",
);
const page = ref(0);
const element = ref<HTMLDivElement>();
const error = ref("");
let chart: EChartsType | undefined;
let observer: ResizeObserver | undefined;
const states = {
  available: "可计算",
  reference: "仅供参考",
  unavailable: "不可用",
};
function render() {
  if (!chart) return;
  try {
    const lines = trendLines(props.result.curve, key.value);
    const series = (["available", "reference"] as const).flatMap((status) => [
      {
        type: "line",
        data: lines[status],
        symbol: "none",
        connectNulls: false,
        smooth: false,
        lineStyle: {
          color: status === "reference" ? "#a67828" : "#24745b",
          type: status === "reference" ? "dashed" : "solid",
        },
      },
      {
        type: "scatter",
        symbol: status === "reference" ? "emptyCircle" : "circle",
        symbolSize: 8,
        itemStyle: { color: status === "reference" ? "#a67828" : "#24745b" },
        data: props.result.curve
          .filter((p) => p[key.value].status === status)
          .map((p) => ({
            value: [
              Date.parse(`${p.date}T00:00:00Z`),
              Number(p[key.value].value),
            ],
            text: `${p.date}${p.baseline ? " 基准锚点" : ""}\n${states[status]}\n${key.value === "profit" ? p.profit.value : returnPercent(p[key.value].value, p[key.value].percentage)}`,
          })),
      },
    ]);
    const events = [false, true].map((out) => ({
      type: "scatter",
      xAxisIndex: 1,
      yAxisIndex: 1,
      symbol: out ? "diamond" : "triangle",
      symbolSize: 10,
      itemStyle: { color: out ? "#24745b" : "#bc514c" },
      data: props.points
        .filter(
          (f) =>
            f.flow !== null &&
            !/^-?0\.00$/.test(f.flow) &&
            f.flow.startsWith("-") === out,
        )
        .map((f) => ({
          value: [Date.parse(`${f.date}T00:00:00Z`), out ? 0 : 1],
          text: `${f.date}\n${out ? "转出" : "转入"} ${f.flow}`,
        })),
    }));
    const dates = [
      ...props.result.curve.map((p) => p.date),
      ...props.points.map((p) => p.date),
    ].sort();
    const min = dates[0],
      max = dates.at(-1);
    chart.setOption(
      {
        animation: false,
        useUTC: true,
        grid: [
          { left: 70, right: 20, top: 35, height: 200 },
          { left: 70, right: 20, top: 285, height: 45 },
        ],
        tooltip: {
          trigger: "item",
          renderMode: "richText",
          confine: true,
          formatter: (p: unknown) =>
            (p as { data?: { text?: string } }).data?.text ?? "",
        },
        xAxis: [0, 1].map((gridIndex) => ({
          type: "time",
          gridIndex,
          min: min ? Date.parse(`${min}T00:00:00Z`) - 43200000 : undefined,
          max: max ? Date.parse(`${max}T00:00:00Z`) + 43200000 : undefined,
          axisLabel: { hideOverlap: true, formatter: "{yyyy}-{MM}-{dd}" },
        })),
        yAxis: [
          {
            type: "value",
            scale: true,
            name:
              key.value === "profit"
                ? "收益坐标（近似）"
                : "收益率小数（近似）",
          },
          { type: "category", gridIndex: 1, data: ["转出", "转入"] },
        ],
        dataZoom: [
          { type: "slider", xAxisIndex: [0, 1], bottom: 5, filterMode: "none" },
        ],
        series: [...series, ...events],
      },
      true,
    );
    error.value = "";
  } catch {
    error.value = "图形暂不可用，请使用下方精确日期表。";
  }
}
watch(
  () => props.result,
  () => {
    page.value = 0;
    render();
  },
);
watch(key, render);
onMounted(() => {
  try {
    chart = init(element.value!);
    observer = new ResizeObserver(() => chart?.resize());
    observer.observe(element.value!);
    render();
  } catch {
    error.value = "图形暂不可用，请使用下方精确日期表。";
  }
});
onBeforeUnmount(() => {
  observer?.disconnect();
  chart?.dispose();
});
</script>

<template>
  <section aria-label="收益趋势" data-test="return-trend">
    <h4>收益趋势 · {{ manager ? "TWR" : "Modified Dietz" }}</h4>
    <label
      >趋势指标
      <select v-model="mode" name="return_trend_metric">
        <option value="rate">收益率</option>
        <option value="profit">累计收益</option>
      </select></label
    >
    <p>
      仅连接已知采样点，不代表每日观察或插值。实线实点可计算，虚线空心点仅供参考；不可用点真实断开，原因见表。基准锚点为零，不代表起始资金为零。事件带转入红色、转出绿色，不是收益纵坐标。
    </p>
    <p v-if="error" role="status">{{ error }}</p>
    <div
      ref="element"
      class="return-canvas"
      role="img"
      aria-label="收益采样连线与资金事件带，精确值和不可用原因见下方键盘可访问表格"
    />
    <div class="ledger-table-wrap">
      <table>
        <caption>
          同一快照收益采样点 ·
          {{
            result.revision
          }}
        </caption>
        <thead>
          <tr>
            <th>日期</th>
            <th>累计收益 {{ currency }}</th>
            <th>{{ manager ? "TWR" : "Modified Dietz" }}</th>
            <th>来源记录</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="p in result.curve.slice(page * 30, (page + 1) * 30)"
            :key="p.date"
          >
            <td>{{ p.date }}{{ p.baseline ? " 基准锚点" : "" }}</td>
            <td>
              {{ p.profit.value ?? "不可用" }} · {{ states[p.profit.status] }}
              {{ returnReasons[p.profit.reason] }}
            </td>
            <td>
              {{
                returnPercent(
                  p[manager ? "twr" : "modified_dietz"].value,
                  p[manager ? "twr" : "modified_dietz"].percentage,
                )
              }}
              · {{ states[p[manager ? "twr" : "modified_dietz"].status] }}
              {{ returnReasons[p[manager ? "twr" : "modified_dietz"].reason] }}
            </td>
            <td>{{ p.record_id }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="result.curve.length > 30" class="ledger-actions">
      <button :disabled="page === 0" @click="page--">上一页趋势</button
      ><span>{{ page + 1 }} / {{ Math.ceil(result.curve.length / 30) }}</span
      ><button
        :disabled="(page + 1) * 30 >= result.curve.length"
        @click="page++"
      >
        下一页趋势
      </button>
    </div>
  </section>
</template>

<style scoped>
.return-canvas {
  width: 100%;
  height: 390px;
}
td,
caption {
  overflow-wrap: anywhere;
}
section {
  min-width: 0;
}
@media (max-width: 700px) {
  .return-canvas {
    height: 390px;
  }
}
</style>

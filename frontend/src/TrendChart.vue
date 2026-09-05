<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from "vue";
import { init, use, type EChartsType } from "echarts/core";
import { LineChart } from "echarts/charts";
import { GridComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import { calendarSeries, type Quote } from "./market";

use([LineChart, GridComponent, TooltipComponent, CanvasRenderer]);
const props = defineProps<{
  items: Quote[];
  from: string;
  to: string;
  name: string;
  unit: string;
}>();
const element = ref<HTMLDivElement>();
let chart: EChartsType | undefined;
let observer: ResizeObserver | undefined;
function render() {
  const series = calendarSeries(props.items, props.from, props.to);
  chart?.setOption(
    {
      animation: false,
      grid: { left: 65, right: 22, top: 30, bottom: 40 },
      tooltip: { trigger: "axis", renderMode: "richText", confine: true },
      xAxis: {
        type: "category",
        data: series.map((point) => point[0]),
        boundaryGap: false,
        axisLine: { lineStyle: { color: "#dce3df" } },
        axisTick: { show: false },
        axisLabel: {
          color: "#73817a",
          formatter: (date: string) => date.slice(5),
          hideOverlap: true,
        },
      },
      yAxis: {
        type: "value",
        scale: true,
        axisLabel: { color: "#73817a" },
        splitLine: { lineStyle: { color: "#edf0ec", type: "dashed" } },
      },
      series: [
        {
          name: `${props.name}（${props.unit}）`,
          type: "line",
          data: series.map((point) => point[1]),
          connectNulls: false,
          smooth: false,
          symbol: "circle",
          symbolSize: 5,
          showSymbol: true,
          lineStyle: { width: 2.5, color: "#24745b" },
          itemStyle: { color: "#24745b" },
          areaStyle: { color: "#24745b", opacity: 0.045 },
        },
      ],
    },
    true,
  );
}
watch(
  () => [props.items, props.from, props.to, props.name, props.unit],
  render,
);
onMounted(() => {
  chart = init(element.value!);
  observer = new ResizeObserver(() => chart?.resize());
  observer.observe(element.value!);
  render();
});
onBeforeUnmount(() => {
  observer?.disconnect();
  chart?.dispose();
});
</script>

<template>
  <div
    ref="element"
    class="trend-chart"
    role="img"
    :aria-label="`${name}价格趋势，${from}至${to}，详细数据见下方历史明细`"
  ></div>
</template>

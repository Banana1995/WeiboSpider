<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { init, use, type EChartsType } from "echarts/core";
import { LineChart, ScatterChart } from "echarts/charts";
import {
  GridComponent,
  TooltipComponent,
  DataZoomComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import {
  assetDays,
  assetLine,
  flowLabel,
  pointLabels,
  pointNote,
  type BasisPoint,
} from "./ledgerChart";
import { kinds } from "./ledger";
import { accountRecordOriginLabels } from "./accountRecords";

use([
  LineChart,
  ScatterChart,
  GridComponent,
  TooltipComponent,
  DataZoomComponent,
  CanvasRenderer,
]);
const props = defineProps<{
  points: BasisPoint[];
  currency: string;
  from: string;
  to: string;
  revision: string;
}>();
const days = computed(() => assetDays(props.points));
const selected = ref("");
const day = computed(() => days.value.find((d) => d.date === selected.value));
const page = ref(0);
const detailPage = ref(0);
const element = ref<HTMLDivElement>();
const chartError = ref("");
let chart: EChartsType | undefined;
let observer: ResizeObserver | undefined;
const statuses = ["reported", "observed", "carried", "stale", "untracked"];
const colors = ["#24745b", "#24745b", "#a67828", "#ba534a", "#787583"];
function choose(date: string) {
  selected.value = date;
  detailPage.value = 0;
}
function render() {
  if (!chart) return;
  try {
    const series = statuses.map((status, i) => ({
      name: pointLabels[status],
      type: status === "carried" ? "scatter" : "line",
      data: assetLine(days.value, status),
      connectNulls: false,
      smooth: false,
      showSymbol: true,
      symbol: status === "carried" ? "emptyDiamond" : "circle",
      symbolSize: 9,
      lineStyle: {
        width: 2,
        type:
          status === "reported" || status === "observed" ? "solid" : "dashed",
      },
      itemStyle: { color: colors[i] },
    }));
    const other = props.points.filter(
      (p) =>
        !p.selected &&
        p.assets !== null &&
        statuses.includes(p.status) &&
        p.status !== "carried",
    );
    const eventKinds = ["转入", "转出", "零额记录", "日志 / 操作"];
    const events = eventKinds.map((kind, index) => ({
      name: kind,
      type: "scatter",
      xAxisIndex: 1,
      yAxisIndex: 1,
      symbol: ["triangle", "diamond", "circle", "rect"][index],
      symbolSize: 12,
      itemStyle: { color: ["#bc514c", "#24745b", "#a67828", "#787583"][index] },
      data: days.value
        .filter((d) =>
          d.points.some((p) =>
            index === 3
              ? !!pointNote(p) || !!p.operation
              : flowLabel(p.flow) === kind,
          ),
        )
        .map((d) => ({
          value: [Date.parse(`${d.date}T00:00:00Z`), index],
          date: d.date,
          event: kind,
        })),
    }));
    chart.setOption(
      {
        animation: false,
        useUTC: true,
        grid: [
          { left: 80, right: 20, top: 30, height: 210 },
          { left: 80, right: 20, top: 295, height: 85 },
        ],
        tooltip: {
          trigger: "item",
          renderMode: "richText",
          confine: true,
          // Only server calendar dates and exact decimal strings enter Canvas text.
          // Arbitrary notes/labels are rendered exclusively by Vue interpolation below.
          formatter: (params: unknown) => {
            const p = params as {
              data?: {
                date?: string;
                exact?: string | null;
                event?: string;
                status?: string;
              };
            };
            const d = p.data;
            if (!d?.date) return "";
            return `${d.date}\n${d.event ?? `${pointLabels[d.status ?? ""] ?? "明确观察"}\n资产原值 ${d.exact ?? "未记录"}`}\n点选查看全部同日记录`;
          },
        },
        xAxis: [0, 1].map((gridIndex) => ({
          type: "time",
          gridIndex,
          min: days.value.length
            ? Date.parse(`${days.value[0]!.date}T00:00:00Z`) - 43200000
            : undefined,
          max: days.value.length
            ? Date.parse(`${days.value.at(-1)!.date}T00:00:00Z`) + 43200000
            : undefined,
          axisLabel: { hideOverlap: true, formatter: "{yyyy}-{MM}-{dd}" },
          splitLine: { show: false },
        })),
        yAxis: [
          {
            type: "value",
            scale: true,
            name: "资产坐标（近似）",
            axisLabel: {
              formatter: (v: number) =>
                Math.abs(v) >= 1e9 ? v.toExponential(1) : String(v),
            },
            splitLine: { lineStyle: { color: "#edf0ec", type: "dashed" } },
          },
          {
            type: "category",
            gridIndex: 1,
            data: eventKinds,
            axisTick: { show: false },
            axisLine: { show: false },
          },
        ],
        dataZoom: [
          {
            type: "slider",
            xAxisIndex: [0, 1],
            bottom: 8,
            height: 22,
            filterMode: "none",
          },
        ],
        series: [
          ...series,
          {
            name: "同日其他明确观察",
            type: "scatter",
            symbolSize: 6,
            itemStyle: { color: "#787583", opacity: 0.6 },
            data: other.map((p) => ({
              value: [Date.parse(`${p.date}T00:00:00Z`), Number(p.assets)],
              date: p.date,
              exact: p.assets,
              status: p.status,
            })),
          },
          ...events,
        ],
      },
      true,
    );
    chartError.value = "";
  } catch {
    chartError.value = "图形暂不可用，请展开“查看资产日期数据”使用精确表格。";
  }
}
watch(
  () => props.points,
  () => {
    page.value = 0;
    choose(days.value.at(-1)?.date ?? "");
    if (!days.value.length) selected.value = "";
    render();
  },
);
onMounted(() => {
  choose(days.value.at(-1)?.date ?? "");
  try {
    chart = init(element.value!);
    chart.on("click", (params: unknown) => {
      const date = (params as { data?: { date?: string } }).data?.date;
      if (date) choose(date);
    });
    observer = new ResizeObserver(() => chart?.resize());
    observer.observe(element.value!);
    render();
  } catch {
    chartError.value = "图形暂不可用，请展开“查看资产日期数据”使用精确表格。";
  }
});
onBeforeUnmount(() => {
  observer?.disconnect();
  chart?.dispose();
});
</script>

<template>
  <section class="ledger-asset-chart" aria-label="资产图与同日明细">
    <p>
      账户有效资产 ·
      {{ currency }} · {{ from }} 至 {{ to }}（北京时间）
    </p>
    <p>
      实点为明确记录，空心菱形为沿用参考；资金流和日志显示在下方事件带。点选图中日期可查看当日摘要，精确金额以日期数据和来源明细为准。
    </p>
    <p v-if="!days.length" role="status">
      所选区间无记录；不把期初或截止参考值补成当日观察。
    </p>
    <p v-if="chartError" role="status">{{ chartError }}</p>
    <div
      ref="element"
      class="asset-canvas"
      role="img"
      aria-label="总资产走势和资金日志事件带，等价键盘操作及精确金额见下方可展开的日期表"
    />
    <label
      >点选日期（键盘 / 手机）<input
        type="date"
        name="chart_day"
        :value="selected"
        :min="from"
        :max="to"
        @change="choose(($event.target as HTMLInputElement).value)"
    /></label>
    <details data-test="asset-date-data">
      <summary>查看资产日期数据</summary>
      <p class="ledger-note">图与明细来自同次只读快照：{{ revision }}</p>
      <div class="ledger-table-wrap">
        <table>
          <caption>
            有记录日期 · 共
            {{
              days.length
            }}
            日（每页 30 日）
          </caption>
          <thead>
            <tr>
              <th>日期 / 查看全部</th>
              <th>日终资产依据（{{ currency }}）</th>
              <th>状态</th>
              <th>明细数</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="d in days.slice(page * 30, page * 30 + 30)"
              :key="d.date"
            >
              <td>
                <button
                  type="button"
                  :aria-pressed="selected === d.date"
                  @click="choose(d.date)"
                >
                  {{ d.date }}
                </button>
              </td>
              <td>{{ d.chosen?.assets ?? "未记录" }}</td>
              <td>
                {{ pointLabels[d.chosen?.status ?? ""] ?? "仅事件 / 日志" }}
              </td>
              <td>{{ d.points.length }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div
        v-if="days.length > 30"
        class="ledger-actions"
        data-test="asset-date-pagination"
      >
        <button type="button" :disabled="page === 0" @click="page--">
          上一页</button
        ><span>第 {{ page + 1 }} / {{ Math.ceil(days.length / 30) }} 页</span
        ><button
          type="button"
          :disabled="(page + 1) * 30 >= days.length"
          @click="page++"
        >
          下一页
        </button>
      </div>
    </details>
    <p v-if="selected && !day" role="status">
      {{ selected }} 当日无记录，不沿用其他日期的明细。
    </p>
    <section v-if="day" aria-label="同日全部记录" aria-live="polite">
      <h4>{{ day.date }} · 全部 {{ day.points.length }} 条记录</h4>
      <p>
        当日日终资产：{{ day.chosen?.assets ?? "未记录" }} {{ currency }} ·
        {{ pointLabels[day.chosen?.status ?? ""] ?? "仅有资金或日志记录" }}
      </p>
      <details data-test="same-day-records">
        <summary>查看数据来源与计算依据</summary>
        <p>当日共 {{ day.points.length }} 条记录。</p>
        <p>
          明确记录使用实点，沿用参考使用空心菱形；红色旧估值待重算，灰色旧估值未追踪。只有相邻自然日的同类日终点连线，缺日断开，不插值。小灰点保留同日其他明确观察。
        </p>
        <p>
          事件带没有资产纵坐标，不代表零资产。转入为红三角、转出为绿菱形，日志或操作为灰方块；图形坐标仅用于展示走势，不是精确金额或收益率。
        </p>
        <p>
          日终依据按同一账户的稳定序号选取最后有效资产/资金记录，日志不覆盖资产；更正不重排。序号用于同日排序，不代表报价的实际时间。
        </p>
        <article
          v-for="(p, i) in day.points.slice(
            detailPage * 30,
            detailPage * 30 + 30,
          )"
          :key="`${p.status}-${p.record_id}-${i}`"
          class="asset-detail"
        >
          <h5>
            {{ p.record_id }} · 序号 {{ p.sequence }} · 版本 {{ p.version }}
            <span v-if="p.selected">· 当日日终依据</span>
          </h5>
          <p>
            {{ pointLabels[p.status] ?? p.status }} · 资产依据
            {{ p.assets ?? "未记录" }} {{ currency }} · {{ flowLabel(p.flow) }}
            {{ p.flow ?? "" }}
          </p>
          <p v-if="p.record">
            原行资产 {{ p.record.total_assets ?? "未记录" }}；来源
            {{ accountRecordOriginLabels[p.record.origin] }}；当前记录日期
            {{ p.record.date }}
          </p>
          <p v-if="p.source_id">
            资产/事件来源 {{ p.source_date }} / {{ p.source_id }} / 版本
            {{ p.source_version }}
          </p>
          <p v-if="p.operation">
            {{ kinds[p.operation.operation.kind] }} ·
            {{ p.operation.operation.account_id }}
            <span v-if="p.operation.operation.to_account_id"
              >→ {{ p.operation.operation.to_account_id }}</span
            >
            · 操作金额 {{ p.operation.operation.amount }}；证券
            {{ p.operation.operation.instrument_id ?? "无" }}；数量
            {{ p.operation.operation.quantity }}；成交价
            {{ p.operation.operation.price }}；费用
            {{ p.operation.operation.fee ?? "未录入" }}
          </p>
          <p v-if="p.valuation">
            观察日期 {{ p.valuation.as_of }}；保存于
            {{ p.valuation.saved_at }}；现金 {{ p.valuation.cash }}；持仓市值
            {{ p.valuation.positions_value }}。这是原始参考观察，未重算。
          </p>
          <p class="ledger-note">投资日志：{{ pointNote(p) || "无" }}</p>
          <details v-if="p.record?.original">
            <summary>原始导入明细（未改写）</summary>
            <pre>{{ JSON.stringify(p.record.original, null, 2) }}</pre>
          </details>
        </article>
        <div
          v-if="day.points.length > 30"
          class="ledger-actions"
          data-test="same-day-pagination"
        >
          <button
            type="button"
            :disabled="detailPage === 0"
            @click="detailPage--"
          >
            上一页</button
          ><span
            >第 {{ detailPage + 1 }} /
            {{ Math.ceil(day.points.length / 30) }} 页</span
          ><button
            type="button"
            :disabled="(detailPage + 1) * 30 >= day.points.length"
            @click="detailPage++"
          >
            下一页
          </button>
        </div>
      </details>
    </section>
  </section>
</template>

<style scoped>
.asset-canvas {
  width: 100%;
  height: 440px;
}
.ledger-asset-chart {
  min-width: 0;
}
.ledger-asset-chart p,
.asset-detail {
  overflow-wrap: anywhere;
}
.asset-detail {
  border-top: 1px solid #dce3df;
  padding: 12px 0;
}
.asset-detail h5 {
  margin: 0;
  font-size: 1em;
}
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.ledger-table-wrap {
  overflow-x: auto;
}
</style>

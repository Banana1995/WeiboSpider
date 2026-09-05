<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from "vue";
import {
  request,
  money,
  change,
  tone,
  shiftDate,
  type Latest,
  type History,
  type SyncStatus,
} from "./market";

const TrendChart = defineAsyncComponent(() => import("./TrendChart.vue"));

const latest = ref<Latest>();
const sync = ref<SyncStatus>();
const selected = ref<number>();
const days = ref(30);
const search = ref("");
const loading = ref(false);
const error = ref("");
const syncError = ref("");
const history = ref<History>();
const historyLoading = ref(false);
const historyError = ref("");
const revision = ref(0);
let latestController: AbortController | undefined;
let historyController: AbortController | undefined;
const product = computed(() =>
  latest.value?.items.find((item) => item.id === selected.value),
);
const visibleProducts = computed(
  () =>
    latest.value?.items.filter((item) =>
      `${item.name} ${item.specifications}`
        .toLowerCase()
        .includes(search.value.toLowerCase().trim()),
    ) ?? [],
);
const to = computed(() => latest.value?.price_date ?? "");
const from = computed(() =>
  to.value ? shiftDate(to.value, 1 - days.value) : "",
);
const points = computed(() =>
  [...(history.value?.items ?? [])].sort((a, b) =>
    a.price_date.localeCompare(b.price_date),
  ),
);
const difference = computed(() =>
  points.value.length >= 2
    ? points.value.at(-1)!.price_cents - points.value[0]!.price_cents
    : undefined,
);
const dayFormatter = new Intl.DateTimeFormat("en-CA", {
  timeZone: "Asia/Shanghai",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});
const today = ref(dayFormatter.format(new Date()));
const updateToday = () => {
  today.value = dayFormatter.format(new Date());
};
const oldQuote = computed(() => to.value && to.value < today.value);
const lastSuccess = computed(() =>
  sync.value?.last_success_at
    ? new Intl.DateTimeFormat("zh-CN", {
        timeZone: "Asia/Shanghai",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      }).format(new Date(sync.value.last_success_at))
    : "",
);
const syncText = computed(
  () =>
    ({
      idle: "尚未同步",
      running: "采集中",
      succeeded: "最近同步成功",
      failed: "最近同步失败，保留已有行情",
      interrupted: "上次同步中断，保留已有行情",
    })[sync.value?.state ?? ""] ?? "同步状态未知",
);
const failure = (e: unknown) =>
  e instanceof Error && e.name !== "TypeError"
    ? e.message
    : "无法连接行情服务，请检查后端是否启动。";

async function refresh() {
  updateToday();
  latestController?.abort();
  const controller = new AbortController();
  latestController = controller;
  loading.value = true;
  error.value = "";
  syncError.value = "";
  await Promise.all([
    (async () => {
      try {
        const data = await request<Latest>("latest", controller.signal);
        if (controller.signal.aborted) return;
        latest.value = data;
        if (!data.items.some((item) => item.id === selected.value))
          selected.value = data.items[0]?.id;
        revision.value++;
      } catch (e) {
        if (!controller.signal.aborted) error.value = failure(e);
      }
    })(),
    (async () => {
      try {
        const data = await request<SyncStatus>("sync", controller.signal);
        if (!controller.signal.aborted) sync.value = data;
      } catch {
        if (!controller.signal.aborted) syncError.value = "同步状态读取失败";
      }
    })(),
  ]);
  if (!controller.signal.aborted) loading.value = false;
}
async function loadHistory() {
  historyController?.abort();
  const controller = new AbortController();
  historyController = controller;
  history.value = undefined;
  historyError.value = "";
  historyLoading.value = false;
  if (!product.value || !to.value) return;
  historyLoading.value = true;
  try {
    const query = new URLSearchParams({
      from: from.value,
      to: to.value,
      limit: "366",
    });
    const data = await request<History>(
      `products/${product.value.id}/history?${query}`,
      controller.signal,
    );
    if (!controller.signal.aborted) history.value = data;
  } catch (e) {
    if (!controller.signal.aborted) historyError.value = failure(e);
  } finally {
    if (!controller.signal.aborted) historyLoading.value = false;
  }
}
watch([selected, days, revision], loadHistory);
void refresh();
onMounted(() => document.addEventListener("visibilitychange", updateToday));
onBeforeUnmount(() => {
  document.removeEventListener("visibilitychange", updateToday);
  latestController?.abort();
  historyController?.abort();
});
</script>

<template>
  <header class="masthead">
    <a class="brand" href="/liquor"
      ><span class="brand-mark">观</span>观价
      <span class="brand-divider">/</span
      ><span class="brand-sub">市场观察</span></a
    ><span class="edition">LIQUOR MARKET · 白酒行情</span>
  </header>
  <main>
    <section class="page-heading">
      <div>
        <div class="eyebrow">MARKET OBSERVATORY / 01</div>
        <h1>白酒行情<span>看见价格的变化。</span></h1>
        <p>从每日行情到长期趋势，记录每一份真实报价。</p>
      </div>
      <button class="refresh" :disabled="loading" @click="refresh">
        {{ loading ? "正在刷新…" : "刷新数据 ↻" }}
      </button>
    </section>
    <div class="market-meta">
      <span><i></i> 新浪酒价内参</span
      ><span
        >报价日期 <strong>{{ to || "暂无" }}</strong></span
      ><span aria-live="polite"
        >{{ syncError || syncText
        }}<template v-if="lastSuccess && !syncError">
          · {{ lastSuccess }} 北京时间</template
        ></span
      ><span v-if="oldQuote" class="stale">非今日报价</span>
    </div>
    <div v-if="error" class="notice error" role="alert">
      {{ error }} {{ latest ? "当前保留上次读取的数据。" : "" }}
      <button @click="refresh">重试</button>
    </div>
    <div v-if="!latest && loading" class="empty" role="status">
      正在读取行情…
    </div>
    <div v-else-if="latest && !latest.items.length" class="empty">
      <h2>还没有行情数据</h2>
      <p>请先在 Go 后端执行一次同步，再刷新此页面。刷新不会触发采集。</p>
    </div>
    <div v-else-if="latest?.items.length" class="workspace">
      <aside class="catalog">
        <div class="catalog-heading">
          <h2>行情目录</h2>
          <span>{{ latest.items.length }} 款</span>
        </div>
        <label class="search"
          ><span class="sr-only">搜索酒品</span
          ><input v-model="search" placeholder="搜索酒名或规格" type="search"
        /></label>
        <div class="list-caption">
          <span>品种 / 规格</span><span>最新价 / 来源涨跌额</span>
        </div>
        <div class="product-list">
          <button
            v-for="item in visibleProducts"
            :key="item.id"
            class="product-row"
            :class="{ selected: item.id === selected }"
            :aria-pressed="item.id === selected"
            @click="selected = item.id"
          >
            <span class="product-name"
              >{{ item.name
              }}<small>{{ item.specifications }} · {{ item.unit }}</small></span
            ><span class="product-price"
              >{{ money(item.price_cents)
              }}<small :class="tone(item.change_cents)">{{
                change(item.change_cents)
              }}</small></span
            >
          </button>
          <p v-if="!visibleProducts.length" class="no-match">没有匹配的酒品</p>
        </div>
      </aside>
      <section v-if="product" class="detail">
        <div class="quote-heading">
          <div>
            <div class="eyebrow">PRICE OVERVIEW</div>
            <h2>{{ product.name }}</h2>
            <p>
              {{ product.specifications }} <span>·</span> {{ product.unit }}
            </p>
          </div>
          <span class="quote-badge">来源报价</span>
        </div>
        <div class="quote-metrics">
          <div class="primary-price">
            <span>最新价格 / 元</span
            ><strong>{{ money(product.price_cents) }}</strong>
          </div>
          <div>
            <span>来源涨跌额 / 元</span
            ><strong :class="tone(product.change_cents)">{{
              change(product.change_cents)
            }}</strong>
          </div>
          <div>
            <span>区间首尾变化 / 元</span
            ><strong :class="tone(difference ?? 0)">{{
              difference === undefined ? "暂无" : change(difference)
            }}</strong>
          </div>
        </div>
        <div class="chart-toolbar">
          <h3>
            价格趋势 <span>{{ product.unit }}</span>
          </h3>
          <div class="ranges" aria-label="时间范围">
            <button
              v-for="range in [7, 30, 90, 365]"
              :key="range"
              :aria-pressed="days === range"
              :class="{ active: days === range }"
              @click="days = range"
            >
              {{ range === 365 ? "近一年" : `近 ${range} 天` }}
            </button>
          </div>
        </div>
        <div v-if="historyLoading" class="chart-state" role="status">
          正在读取历史报价…
        </div>
        <div v-else-if="historyError" class="chart-state" role="alert">
          {{ historyError }}<button @click="loadHistory">重新加载</button>
        </div>
        <div v-else-if="!points.length" class="chart-state">
          所选区间暂无报价记录
        </div>
        <TrendChart
          v-else
          :items="points"
          :from="from"
          :to="to"
          :name="product.name"
          :unit="product.unit"
        />
        <div class="coverage">
          <span v-if="points.length"
            >实际可用 {{ points[0]!.price_date }} 至
            {{ points.at(-1)!.price_date }} · {{ points.length }} 条</span
          ><span v-else>请求区间 {{ from }} 至 {{ to }}</span
          ><span v-if="points.length && points.length < days"
            >历史未覆盖全部日期，不补齐缺失报价</span
          ><span v-else>按来源报价日期展示</span>
        </div>
        <details v-if="points.length" class="history">
          <summary>
            历史明细 <span>{{ points.length }} 条记录</span>
          </summary>
          <div class="table-wrap">
            <table>
              <caption class="sr-only">
                {{
                  product.name
                }}
                历史报价，日期倒序
              </caption>
              <thead>
                <tr>
                  <th>报价日期</th>
                  <th>价格（元）</th>
                  <th>来源涨跌额（元）</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="point in [...points].reverse()"
                  :key="point.price_date"
                >
                  <td>{{ point.price_date }}</td>
                  <td>{{ money(point.price_cents) }}</td>
                  <td :class="tone(point.change_cents)">
                    {{ change(point.change_cents) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </details>
      </section>
    </div>
    <footer>
      <strong>关于数据</strong>
      <p>
        来源声明的终端零售成交加权均价，不是批发价，亦非本平台独立核验的成交价格。来源涨跌额沿用原始数据；区间变化按可用报价的首尾值计算。刷新仅重新读取已入库行情。
      </p>
      <span>GUANJIA / MARKET OBSERVATORY</span>
    </footer>
  </main>
</template>

<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from "vue";
import {
  DEMO_TODAY,
  activeRecords,
  analyze,
  cents,
  createAccounts,
  createRecord,
  day,
  demoSecurities,
  importExample,
  kindLabels,
  money,
  percent,
  type DemoRecord,
  type Holding,
  type RecordKind,
} from "./ledgerPrototype";
import "./prototype.css";

const accounts = ref(createAccounts());
const securities = ref(demoSecurities());
const selected = ref(1);
const account = computed(() =>
  accounts.value.find((a) => a.id === selected.value)!,
);
const active = computed(() => activeRecords(account.value.records));
const latest = computed(() =>
  active.value.filter((r) => r.assets !== null).at(-1),
);
const manage = ref(false);
const manageTab = ref("info");
const range = ref("all");
const customFrom = ref("2026-01-01");
const customTo = ref(DEMO_TODAY);
const view = ref<"personal" | "manager">("personal");
const chartMode = ref<"rate" | "profit">("rate");
const bounds = computed(() =>
  range.value === "year"
    ? ["2026-01-01", DEMO_TODAY]
    : range.value === "last"
      ? ["2025-01-01", "2025-12-31"]
      : range.value === "custom"
        ? [customFrom.value, customTo.value]
        : ["0001-01-01", DEMO_TODAY],
);
const rangeError = computed(() =>
  !bounds.value[0] || !bounds.value[1] || bounds.value[0]! > bounds.value[1]!
    ? "请选择有效的起止日期，开始日期不能晚于结束日期。"
    : "",
);
const analysis = computed(() =>
  analyze(
    rangeError.value ? [] : account.value.records,
    bounds.value[0]!,
    bounds.value[1]!,
    view.value,
  ),
);
const tableType = ref("all");
const filterMenu = ref<HTMLDetailsElement>();
const tableFrom = ref("");
const tableTo = ref("");
const showVoided = ref(false);
const page = ref(1);
const pageSize = 6;
const highlighted = ref<number>();
const notice = ref("");
const filtered = computed(() =>
  [...account.value.records]
    .filter(
      (r) =>
        (showVoided.value || !r.voided) &&
        (tableType.value === "all" || r.kind === tableType.value) &&
        (!tableFrom.value || r.date >= tableFrom.value) &&
        (!tableTo.value || r.date <= tableTo.value),
    )
    .sort((a, b) => b.date.localeCompare(a.date) || b.id - a.id),
);
const pages = computed(() =>
  Math.max(1, Math.ceil(filtered.value.length / pageSize)),
);
const visible = computed(() =>
  filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize),
);
watch([tableType, tableFrom, tableTo, showVoided], () => {
  page.value = 1;
  highlighted.value = undefined;
});
watch(pages, () => {
  page.value = Math.min(page.value, pages.value);
});
function resetFilters() {
  if (filterMenu.value) filterMenu.value.open = false;
  tableType.value = "all";
  tableFrom.value = "";
  tableTo.value = "";
  showVoided.value = false;
  page.value = 1;
}
watch(selected, () => {
  resetFilters();
  range.value = "all";
  view.value = "personal";
  chartMode.value = "rate";
  manage.value = false;
  manageTab.value = "info";
  highlighted.value = undefined;
  hoverDate.value = "";
  notice.value = "";
});
const hoverDate = ref("");
const chartPoints = computed(() =>
  analysis.value.points.map((p) => ({
    ...p,
    value: chartMode.value === "rate" ? p.rate : p.profit,
  })),
);
const extent = computed(() => {
  const values = chartPoints.value.flatMap((p) =>
    p.value === null ? [] : [p.value],
  );
  const low = Math.min(0, ...values);
  const high = Math.max(0, ...values);
  const pad =
    (high - low || (chartMode.value === "rate" ? 0.01 : 10000)) * 0.15;
  return [low - pad, high + pad];
});
function x(date: string) {
  const { first, days } = analysis.value;
  return first ? 36 + ((day(date) - day(first.date)) / (days || 1)) * 828 : 36;
}
const y = (value: number) =>
  178 -
  ((value - extent.value[0]!) / (extent.value[1]! - extent.value[0]!)) * 150;
const curve = computed(() => {
  let connected = false;
  return chartPoints.value
    .map((p) => {
      if (p.value === null) {
        connected = false;
        return "";
      }
      const command = connected ? "L" : "M";
      connected = true;
      return `${command}${x(p.date)},${y(p.value)}`;
    })
    .join(" ");
});
const tooltip = computed(() =>
  chartPoints.value.find((p) => p.date === hoverDate.value),
);
const chartValue = (value: number | null) =>
  chartMode.value === "rate" ? percent(value) : money(value);
async function locate(record: DemoRecord) {
  resetFilters();
  await nextTick();
  page.value =
    Math.floor(filtered.value.findIndex((r) => r.id === record.id) / pageSize) +
    1;
  highlighted.value = record.id;
  notice.value = `已定位 ${record.date} 的${kindLabels[record.kind]}记录，已清除记录筛选。`;
  await nextTick();
  const row = document.getElementById(`demo-record-${record.id}`);
  row?.focus({ preventScroll: true });
  row?.scrollIntoView?.({
    behavior: window.matchMedia?.("(prefers-reduced-motion: reduce)").matches
      ? "auto"
      : "smooth",
    block: "nearest",
  });
}

type Modal =
  "record" | "detail" | "void" | "account" | "import" | "holdings";
const dialog = ref<HTMLDialogElement>();
const modal = ref<Modal>("record");
const isOpen = ref(false);
const discard = ref(false);
const error = ref("");
const editing = ref<DemoRecord>();
const isNewAccount = ref(false);
const form = ref({
  kind: "in" as RecordKind,
  date: DEMO_TODAY,
  amount: "",
  assets: "",
  note: "",
  name: "",
  currency: "CNY",
  reason: "",
});
const draftHoldings = ref<{ security: number; quantity: string }[]>([]);
const draftCash = ref("");
const newSecurity = ref({
  name: "",
  market: "沪深",
  code: "",
  currency: "CNY",
  price: "",
});
const addedSecurities = ref<ReturnType<typeof demoSecurities>>([]);
const securityFormOpen = ref(false);
const availableSecurities = computed(() =>
  [...securities.value, ...addedSecurities.value].filter(
    (s) => s.currency === account.value.currency,
  ),
);
const step = ref(1);
const importRows = importExample();
const initial = ref("");
let opener: HTMLElement | null = null;
const draftSnapshot = () =>
  JSON.stringify([
    form.value,
    draftHoldings.value,
    draftCash.value,
    newSecurity.value,
    addedSecurities.value,
  ]);
const dirty = computed(
  () =>
    isOpen.value &&
    !["detail", "void"].includes(modal.value) &&
    draftSnapshot() !== initial.value,
);
const modalTitle = computed(
  () =>
    ({
      record: editing.value ? "编辑记录" : "记一笔",
      detail: "记录详情",
      void: "作废记录",
      account: isNewAccount.value ? "新建账户" : "编辑账户资料",
      import: "导入示例账本",
      holdings: "编辑当前持仓",
    })[modal.value],
);
async function openModal(type: Modal, record?: DemoRecord, newAccount = false) {
  opener =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
  modal.value = type;
  editing.value = record;
  isNewAccount.value = newAccount;
  error.value = "";
  notice.value = "";
  discard.value = false;
  step.value = 1;
  securityFormOpen.value = false;
  form.value = {
    kind: record?.kind ?? "in",
    date: record?.date ?? DEMO_TODAY,
    amount: record?.amount == null ? "" : (record.amount / 100).toFixed(2),
    assets: record?.assets == null ? "" : (record.assets / 100).toFixed(2),
    note: record?.note ?? "",
    name: newAccount || type === "import" ? "" : account.value.name,
    currency: newAccount || type === "import" ? "CNY" : account.value.currency,
    reason: "",
  };
  draftCash.value = (account.value.cash / 100).toFixed(2);
  draftHoldings.value = account.value.holdings.map((h) => ({
    security: h.security,
    quantity: String(h.quantity),
  }));
  addedSecurities.value = [];
  newSecurity.value = {
    name: "",
    market: "沪深",
    code: "",
    currency: account.value.currency,
    price: "",
  };
  initial.value = draftSnapshot();
  isOpen.value = true;
  await nextTick();
  dialog.value?.showModal();
}
function closeModal() {
  dialog.value?.close();
  isOpen.value = false;
  discard.value = false;
  error.value = "";
  nextTick(() => {
    if (opener?.isConnected) opener.focus();
    else document.getElementById("demo-account")?.focus();
  });
}
function requestClose() {
  if (dirty.value || (modal.value === "void" && form.value.reason))
    discard.value = true;
  else closeModal();
}
function fail(message: string) {
  error.value = message;
  nextTick(() => document.getElementById("demo-form-error")?.focus());
}
function saveRecord() {
  const f = form.value;
  if (
    !/^\d{4}-\d{2}-\d{2}$/.test(f.date) ||
    !Number.isFinite(day(f.date)) ||
    new Date(day(f.date) * 86400000).toISOString().slice(0, 10) !== f.date ||
    f.date > DEMO_TODAY ||
    f.date < "1900-01-01"
  )
    return fail(`请选择 1900-01-01 至 ${DEMO_TODAY} 之间的有效日期。`);
  const kind = f.kind;
  const amount = kind === "in" || kind === "out" ? cents(f.amount) : null;
  const assets = kind === "note" || !f.assets.trim() ? null : cents(f.assets);
  if ((kind === "in" || kind === "out") && (amount === null || amount <= 0))
    return fail("请输入大于 0 的金额，最多两位小数，上限 100 亿元。");
  if (
    (kind === "asset" || (kind !== "note" && f.assets.trim())) &&
    assets === null
  )
    return fail("请输入有效的总资产，允许为 0，最多两位小数，上限 100 亿元。");
  if (kind === "note" && !f.note.trim())
    return fail("请填写备注内容。");
  const summary = `${f.date} ${kindLabels[kind]}${amount === null ? "" : ` ${money(amount)}`}；总资产 ${money(assets)}；${f.note.trim() || "无备注"}`;
  if (editing.value) {
    Object.assign(editing.value, {
      kind,
      date: f.date,
      amount,
      assets,
      note: f.note.trim(),
    });
    editing.value.history.push(`修改后：${summary}`);
  } else {
    const id = Math.max(0, ...account.value.records.map((r) => r.id)) + 1;
    const record = createRecord(
      id,
      f.date,
      kind,
      amount,
      assets,
      f.note.trim(),
    );
    record.history = [`创建记录：${summary}`];
    account.value.records.push(record);
  }
  closeModal();
  resetFilters();
  notice.value = "已保存记录。收益曲线与记录已更新，仅本页有效。";
}
function voidRecord() {
  if (!form.value.reason.trim())
    return fail("请填写作废原因，便于在变更记录中回看。");
  editing.value!.voided = true;
  editing.value!.history.push(`已作废：${form.value.reason.trim()}`);
  closeModal();
  notice.value = "已作废记录。可在筛选菜单中显示已作废记录。";
}
function saveAccount() {
  if (!form.value.name.trim()) return fail("请填写账户名称。");
  if (isNewAccount.value) {
    const id = Math.max(...accounts.value.map((a) => a.id)) + 1;
    accounts.value.push({
      id,
      name: form.value.name.trim(),
      currency: form.value.currency,
      records: [],
      cash: 0,
      holdings: [],
    });
    selected.value = id;
  } else {
    account.value.name = form.value.name.trim();
    if (
      !account.value.records.length &&
      !account.value.holdings.length &&
      !account.value.cash
    )
      account.value.currency = form.value.currency;
  }
  closeModal();
  nextTick(() => {
    notice.value = "已保存账户资料，仅本页有效。";
  });
}
function previewFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (!file) return;
  if (!file.name.toLowerCase().endsWith(".xlsx"))
    return fail("请选择 .xlsx 文件，或直接使用示例文件。不会读取文件内容。");
  error.value = "";
  step.value = 2;
}
function confirmImport() {
  if (!form.value.name.trim()) return fail("请为新的示例账户填写名称。");
  const id = Math.max(...accounts.value.map((a) => a.id)) + 1;
  accounts.value.push({
    id,
    name: form.value.name.trim(),
    currency: "CNY",
    cash: 0,
    holdings: [],
    records: importExample(),
  });
  selected.value = id;
  closeModal();
  nextTick(() => {
    notice.value = "已将 3 条内置示例记录导入新账户，没有读取或上传文件。";
  });
}
const holdingEstimate = computed(() => {
  const cash = cents(draftCash.value);
  if (cash === null) return null;
  let total = cash;
  for (const h of draftHoldings.value) {
    const security = availableSecurities.value.find((s) => s.id === h.security);
    if (
      !security ||
      !/^\d+(?:\.\d{1,4})?$/.test(h.quantity) ||
      +h.quantity <= 0 ||
      +h.quantity > 1e9
    )
      return null;
    total += Math.round(security.price * Number(h.quantity));
  }
  return Number.isSafeInteger(total) && total <= 1e12 ? total : null;
});
function addSecurity() {
  const s = newSecurity.value;
  const price = cents(s.price);
  if (
    !s.name.trim() ||
    !s.code.trim() ||
    !s.market.trim() ||
    price === null ||
    price <= 0
  )
    return fail(
      "请填写证券名称、市场、代码和大于 0 的示例单价（最多两位小数）。",
    );
  if (
    availableSecurities.value.some(
      (existing) =>
        existing.market === s.market.trim() && existing.code === s.code.trim(),
    )
  )
    return fail("同市场下已存在该代码，请从证券列表选择。");
  const id =
    Math.max(
      0,
      ...securities.value.map((s) => s.id),
      ...addedSecurities.value.map((s) => s.id),
    ) + 1;
  addedSecurities.value.push({
    id,
    name: s.name.trim(),
    market: s.market.trim(),
    code: s.code.trim(),
    currency: account.value.currency,
    price,
  });
  draftHoldings.value.push({ security: id, quantity: "1" });
  securityFormOpen.value = false;
  error.value = "";
  newSecurity.value = {
    name: "",
    market: "沪深",
    code: "",
    currency: account.value.currency,
    price: "",
  };
}
function previewHoldings() {
  if (holdingEstimate.value === null)
    return fail(
      "请检查现金和持仓：现金允许为 0；数量需大于 0、最多四位小数且不超过 10 亿；示例总估值不超过 100 亿元。",
    );
  if (
    new Set(draftHoldings.value.map((h) => h.security)).size !==
    draftHoldings.value.length
  )
    return fail("同一证券请合并数量，不要重复添加。");
  if (securityFormOpen.value) return fail("请先添加证券，或取消新增证券表单。");
  error.value = "";
  step.value = 2;
}
function saveHoldings() {
  const holdings: Holding[] = draftHoldings.value.map((h) => ({
    security: h.security,
    quantity: Number(h.quantity),
  }));
  account.value.holdings = holdings;
  account.value.cash = cents(draftCash.value)!;
  securities.value.push(...addedSecurities.value);
  closeModal();
  notice.value = "已保存当前持仓。没有新增资金流水或总资产记录。";
}
const securityName = (id: number) =>
  securities.value.find((s) => s.id === id)?.name ?? "";
const accountEstimate = computed(
  () =>
    account.value.cash +
    account.value.holdings.reduce(
      (sum, h) =>
        sum +
        Math.round(
          (securities.value.find((s) => s.id === h.security)?.price ?? 0) *
            h.quantity,
        ),
      0,
    ),
);
function beforeUnload(event: BeforeUnloadEvent) {
  if (
    dirty.value ||
    (isOpen.value && modal.value === "void" && form.value.reason)
  ) {
    event.preventDefault();
    event.returnValue = "";
  }
}
onMounted(() => window.addEventListener("beforeunload", beforeUnload));
onBeforeUnmount(() => window.removeEventListener("beforeunload", beforeUnload));
</script>

<template>
  <div class="ledger-prototype">
    <div class="lp-banner">
      <strong>交互原型 · 示例数据</strong><span>仅在本页演示，刷新后恢复</span>
    </div>
    <header class="lp-masthead">
      <div class="brand">
        <span class="brand-mark">观</span>观价<span class="brand-divider"
          >/</span
        ><span class="brand-sub">投资账本</span>
      </div>
      <nav aria-label="模块导航">
        <a href="/liquor">白酒行情</a><a href="/ledger">正式账本</a>
      </nav>
    </header>
    <main class="lp-main">
      <div class="lp-account-heading">
        <div>
          <label class="lp-caption" for="demo-account">当前账户</label>
          <h1>
            <select id="demo-account" v-model="selected" aria-label="选择账户">
              <option v-for="a in accounts" :key="a.id" :value="a.id">
                {{ a.name }}
              </option>
            </select>
          </h1>
        </div>
        <div class="lp-actions">
          <template v-if="!manage"
            ><button class="lp-text-button" @click="manage = true">
              管理账户</button
            ><button class="lp-primary" @click="openModal('record')">
              ＋ 记一笔
            </button></template
          >
          <button v-else @click="manage = false">返回账户</button>
        </div>
      </div>
      <p v-if="notice" class="lp-notice" role="status">{{ notice }}</p>

      <template v-if="!manage">
        <section class="lp-income" aria-labelledby="income-title">
          <div class="lp-section-title">
            <h2 id="income-title">收益曲线</h2>
            <div class="lp-segment" aria-label="收益视角">
              <button
                :aria-pressed="view === 'personal'"
                @click="view = 'personal'"
              >
                个人视角</button
              ><button
                :aria-pressed="view === 'manager'"
                @click="view = 'manager'"
              >
                基金经理视角
              </button>
            </div>
          </div>
          <div class="lp-overview">
            <div class="lp-assets">
              <span
                >最新总资产 <small>{{ account.currency }}</small></span
              ><strong data-testid="latest-assets">{{
                money(latest?.assets ?? null)
              }}</strong
              ><small>{{
                latest ? `更新于 ${latest.date}` : "尚未记录总资产"
              }}</small>
            </div>
            <div>
              <span
                >区间收益 <small>{{ account.currency }}</small></span
              ><strong
                data-testid="period-profit"
                :class="{ 'lp-negative': (analysis.profit ?? 0) < 0 }"
                >{{ money(analysis.profit) }}</strong
              ><small>{{
                analysis.first && analysis.last
                  ? `${analysis.first.date} 至 ${analysis.last.date}`
                  : "区间内暂无资产记录"
              }}</small>
            </div>
            <div>
              <span>{{
                view === "personal" ? "资金加权收益率" : "时间加权收益率"
              }}</span
              ><strong
                data-testid="period-rate"
                :class="{ 'lp-negative': (analysis.rate ?? 0) < 0 }"
                >{{ percent(analysis.rate) }}</strong
              ><small>{{
                view === "personal"
                  ? "Modified Dietz · 示例"
                  : "分段复合 · 示例"
              }}</small>
            </div>
            <div>
              <span>年化参考</span
              ><strong :class="{ 'lp-negative': (analysis.annual ?? 0) < 0 }">{{
                percent(analysis.annual)
              }}</strong
              ><small>非未来收益预测</small>
            </div>
          </div>
          <div class="lp-chart-toolbar">
            <div class="lp-range" aria-label="收益区间">
              <button
                v-for="item in [
                  ['all', '成立以来'],
                  ['year', '今年'],
                  ['last', '去年'],
                  ['custom', '自定义'],
                ]"
                :key="item[0]"
                :aria-pressed="range === item[0]"
                @click="range = item[0]!"
              >
                {{ item[1] }}
              </button>
            </div>
            <div class="lp-segment lp-small" aria-label="曲线指标">
              <button
                :aria-pressed="chartMode === 'rate'"
                @click="chartMode = 'rate'"
              >
                收益率</button
              ><button
                :aria-pressed="chartMode === 'profit'"
                @click="chartMode = 'profit'"
              >
                收益金额
              </button>
            </div>
          </div>
          <div v-if="range === 'custom'" class="lp-date-range">
            <label
              >收益开始日期<input
                v-model="customFrom"
                type="date"
                :max="DEMO_TODAY" /></label
            ><span>至</span
            ><label
              >收益结束日期<input
                v-model="customTo"
                type="date"
                :max="DEMO_TODAY"
            /></label>
          </div>
          <p v-if="rangeError" class="lp-error" role="alert">
            {{ rangeError }}
          </p>
          <div v-else-if="analysis.days > 0" class="lp-chart">
            <div class="lp-chart-readout" aria-live="polite">
              <template v-if="tooltip"
                >{{ tooltip.date }}
                <strong
                  >{{ chartValue(tooltip.value)
                  }}{{
                    chartMode === "profit" ? ` ${account.currency}` : ""
                  }}</strong
                ></template
              ><span v-else>沿曲线查看收益，点击资金标记定位记录</span>
            </div>
            <svg
              viewBox="0 0 900 210"
              preserveAspectRatio="none"
              aria-label="示例收益曲线"
              role="img"
            >
              <title>
                {{ view === "personal" ? "资金加权" : "时间加权"
                }}{{
                  chartMode === "rate" ? "收益率" : "收益金额"
                }}，仅连接已记录总资产的日期
              </title>
              <line
                v-for="n in [0, 0.5, 1]"
                :key="n"
                x1="36"
                x2="864"
                :y1="28 + 150 * n"
                :y2="28 + 150 * n"
                class="lp-gridline"
              />
              <line x1="36" x2="864" :y1="y(0)" :y2="y(0)" class="lp-zero" />
              <path :d="curve" class="lp-curve" />
              <template v-for="p in chartPoints" :key="p.date">
                <circle
                  v-if="p.value !== null"
                  :cx="x(p.date)"
                  :cy="y(p.value)"
                  r="4"
                  tabindex="0"
                  class="lp-point"
                  :aria-label="`${p.date} ${chartValue(p.value)}`"
                  @mouseenter="hoverDate = p.date"
                  @focus="hoverDate = p.date"
                  @mouseleave="hoverDate = ''"
                  @blur="hoverDate = ''"
                >
                  <title>{{ p.date }} {{ chartValue(p.value) }}</title>
                </circle>
              </template>
            </svg>
            <span class="lp-axis-top">{{ chartValue(extent[1]!) }}</span
            ><span
              class="lp-axis-zero"
              :style="{ top: `${38 + (y(0) / 210) * 210}px` }"
              >0</span
            >
            <div
              class="lp-event-lane"
              aria-label="资金事件，点击定位记录"
              :style="{
                minHeight: `${Math.max(1, ...analysis.events.map((event) => analysis.events.filter((e) => e.date === event.date).length)) * 24 + 4}px`,
              }"
            >
              <button
                v-for="(event, i) in analysis.events"
                :key="event.id"
                class="lp-event"
                :class="`lp-${event.kind}`"
                :style="{
                  left: `${x(event.date) / 9}%`,
                  top: `${analysis.events.slice(0, i).filter((e) => e.date === event.date).length * 22}px`,
                }"
                :aria-label="`${event.date} ${kindLabels[event.kind]} ${money(event.amount)}，定位记录`"
                :title="`${event.date} ${kindLabels[event.kind]} ${money(event.amount)}`"
                @click="locate(event)"
              >
                {{ event.kind === "in" ? "+" : "−" }}
              </button>
            </div>
            <div class="lp-axis-dates">
              <span>{{ analysis.first?.date }}</span
              ><span>{{ analysis.last?.date }}</span>
            </div>
          </div>
          <div v-else class="lp-empty lp-chart-empty">
            <h3>
              {{ !active.length ? "从第一笔记录开始" : "这个区间还画不出曲线" }}
            </h3>
            <p>
              {{
                !active.length
                  ? "记录一笔投入或总资产，慢慢留下自己的投资轨迹。"
                  : "需要两个不同日期的总资产记录，请调整区间或补充总资产。"
              }}
            </p>
            <button v-if="!active.length" @click="openModal('record')">
              记第一笔
            </button>
          </div>
          <div class="lp-chart-footer">
            <div class="lp-legend">
              <span><i class="lp-in" />转入</span
              ><span><i class="lp-out" />转出</span>
            </div>
            <p>
              示例计算，非核算结果。按区间内首末资产记录计算，首日资金不重复扣除；同日资产视为转入
              / 转出后的总额。{{
                analysis.days > 0 && analysis.days < 365
                  ? "短期年化可能放大波动。"
                  : ""
              }}
            </p>
          </div>
          <p
            v-if="analysis.missingBoundary || analysis.trailingFlow"
            class="lp-reference"
          >
            {{
              analysis.missingBoundary
                ? "部分转入 / 转出当天未记录总资产。个人收益率仅供参考，基金经理收益率待补全后展示。"
                : ""
            }}{{
              analysis.trailingFlow
                ? "最后一次总资产之后还有资金变动，当前区间结果尚未包含这部分变动，请更新总资产。"
                : ""
            }}
          </p>
        </section>

        <section class="lp-records" aria-labelledby="records-title">
          <div class="lp-section-title">
            <div class="lp-title-count">
              <h2 id="records-title">账户记录</h2>
              <span>{{ filtered.length }} 笔</span>
            </div>
            <div class="lp-record-tools">
              <select v-model="tableType" aria-label="记录类型">
                <option value="all">全部类型</option>
                <option
                  v-for="(label, key) in kindLabels"
                  :key="key"
                  :value="key"
                >
                  {{ label }}
                </option>
              </select>
              <details ref="filterMenu" class="lp-filter-menu">
                <summary>
                  筛选<span
                    v-if="tableFrom || tableTo || showVoided"
                    class="lp-filter-dot"
                  />
                </summary>
                <div>
                  <strong>仅筛选下方记录</strong
                  ><label
                    >记录开始日期<input
                      v-model="tableFrom"
                      type="date" /></label
                  ><label
                    >记录结束日期<input v-model="tableTo" type="date" /></label
                  ><label class="lp-check"
                    ><input
                      v-model="showVoided"
                      type="checkbox"
                    />显示已作废</label
                  ><button @click="resetFilters">清除筛选</button>
                </div>
              </details>
            </div>
          </div>
          <p
            v-if="tableFrom || tableTo || showVoided"
            class="lp-filter-summary"
          >
            记录筛选：{{ tableFrom || "不限开始" }} 至 {{ tableTo || "不限结束"
            }}{{ showVoided ? "，含已作废" : ""
            }}<button class="lp-text-button" @click="resetFilters">清除</button>
          </p>
          <table v-if="visible.length" class="lp-record-table">
            <thead>
              <tr>
                <th scope="col">日期</th>
                <th scope="col">类型</th>
                <th scope="col">转入</th>
                <th scope="col">转出</th>
                <th scope="col">
                  总资产 <small>{{ account.currency }}</small>
                </th>
                <th scope="col">备注</th>
                <th scope="col">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="record in visible"
                :id="`demo-record-${record.id}`"
                :key="record.id"
                tabindex="-1"
                :class="{
                  'lp-highlighted': highlighted === record.id,
                  'lp-voided': record.voided,
                }"
                :data-record-id="record.id"
              >
                <td data-label="日期" class="lp-record-date">
                  {{ record.date }}<small v-if="record.voided">已作废</small>
                </td>
                <td data-label="类型" class="lp-record-kind">
                  <span class="lp-kind" :class="`lp-${record.kind}`">{{
                    kindLabels[record.kind]
                  }}</span>
                </td>
                <td data-label="转入" class="lp-money lp-in">
                  {{ record.kind === "in" ? money(record.amount) : "—" }}
                </td>
                <td data-label="转出" class="lp-money lp-out">
                  {{ record.kind === "out" ? money(record.amount) : "—" }}
                </td>
                <td data-label="总资产" class="lp-money lp-record-assets">
                  {{ money(record.assets) }}
                </td>
                <td data-label="备注" class="lp-record-note">
                  {{ record.note || "—" }}
                </td>
                <td class="lp-row-actions">
                  <button
                    v-if="!record.voided"
                    class="lp-text-button"
                    @click="openModal('record', record)"
                  >
                    编辑</button
                  ><button
                    class="lp-text-button"
                    @click="openModal('detail', record)"
                  >
                    详情
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
          <div v-else class="lp-empty">
            <h3>
              {{
                !account.records.length
                  ? "还没有账户记录"
                  : "没有符合筛选条件的记录"
              }}
            </h3>
            <p>
              {{
                !account.records.length
                  ? "转入、转出、总资产和备注，都在这里一起回看。"
                  : "试试其他类型或日期，也可以显示已作废记录。"
              }}
            </p>
            <button v-if="account.records.length" @click="resetFilters">
              清除记录筛选</button
            ><button v-else @click="openModal('record')">记第一笔</button>
          </div>
          <footer v-if="filtered.length" class="lp-table-footer">
            <span>按日期从新到旧 · 未填写的总资产不沿用前值</span>
            <div v-if="pages > 1" class="lp-pagination">
              <span
                >{{ (page - 1) * pageSize + 1 }}–{{
                  Math.min(page * pageSize, filtered.length)
                }}
                / {{ filtered.length }}</span
              ><button
                aria-label="上一页记录"
                :disabled="page === 1"
                @click="page--"
              >
                上一页</button
              ><button
                aria-label="下一页记录"
                :disabled="page === pages"
                @click="page++"
              >
                下一页
              </button>
            </div>
          </footer>
        </section>
      </template>

      <section v-else class="lp-management" aria-label="账户管理">
        <div class="lp-management-tabs" aria-label="管理栏目">
          <button
            v-for="tab in [
              ['info', '账户资料'],
              ['holdings', '当前持仓'],
              ['weekly', '自动更新记录'],
            ]"
            :key="tab[0]"
            :aria-pressed="manageTab === tab[0]"
            @click="manageTab = tab[0]!"
          >
            {{ tab[1] }}
          </button>
        </div>
        <div v-if="manageTab === 'info'" class="lp-management-body">
          <div class="lp-section-title">
            <h2>账户资料</h2>
            <button @click="openModal('account')">编辑资料</button>
          </div>
          <dl class="lp-info">
            <div>
              <dt>账户名称</dt>
              <dd>{{ account.name }}</dd>
            </div>
            <div>
              <dt>记账币种</dt>
              <dd>{{ account.currency }}</dd>
            </div>
            <div>
              <dt>开始记录</dt>
              <dd>{{ active[0]?.date ?? "尚未开始" }}</dd>
            </div>
          </dl>
          <div class="lp-management-secondary">
            <div>
              <h3>开启另一份计划</h3>
              <p>账户之间独立记录，不合并资金和持仓。</p>
            </div>
            <button @click="openModal('account', undefined, true)">
              新建账户
            </button>
          </div>
          <div class="lp-management-secondary">
            <div>
              <h3>从文件开始</h3>
              <p>演示导入流程，使用内置示例数据，不读取或上传你的文件。</p>
            </div>
            <button @click="openModal('import')">导入示例账本</button>
          </div>
        </div>
        <div v-else-if="manageTab === 'holdings'" class="lp-management-body">
          <div class="lp-section-title">
            <div>
              <h2>当前持仓</h2>
              <p class="lp-muted">
                独立保存的持仓快照，不自动生成资金流水或更新总资产。
              </p>
            </div>
            <button @click="openModal('holdings')">编辑持仓</button>
          </div>
          <div class="lp-holding-summary">
            <span
              >示例估值 <small>{{ account.currency }}</small></span
            ><strong>{{ money(accountEstimate) }}</strong
            ><small>固定示例价格，无实时行情</small>
          </div>
          <ul class="lp-holdings-list">
            <li>
              <span>现金</span><strong>{{ money(account.cash) }}</strong>
            </li>
            <li v-for="holding in account.holdings" :key="holding.security">
              <span
                >{{ securityName(holding.security)
                }}<small
                  >{{ holding.quantity.toLocaleString("zh-CN") }} 份</small
                ></span
              ><strong>{{
                money(
                  Math.round(
                    (securities.find((s) => s.id === holding.security)?.price ??
                      0) * holding.quantity,
                  ),
                )
              }}</strong>
            </li>
          </ul>
          <p v-if="!account.holdings.length" class="lp-muted">
            尚无证券持仓，可以添加一笔示例持仓。
          </p>
        </div>
        <div v-else class="lp-management-body">
          <h2>自动更新记录</h2>
          <p class="lp-muted">
            只读流程示例。本页没有定时任务，也不会获取行情或写入资产。
          </p>
          <dl class="lp-info">
            <div>
              <dt>示例计划</dt>
              <dd>每周五 18:00（北京时间）</dd>
            </div>
            <div>
              <dt>当前状态</dt>
              <dd>演示模式，未启用</dd>
            </div>
          </dl>
          <div class="lp-weekly-example">
            <span>2026-09-04 18:00</span><strong>未执行</strong
            ><span>示例展示，不生成记录</span>
          </div>
        </div>
      </section>
      <footer class="lp-page-footer">
        观价 · 投资账本原型<span>所有操作仅在当前页面内存中生效</span>
      </footer>
    </main>

    <dialog
      ref="dialog"
      class="lp-dialog"
      aria-labelledby="demo-dialog-title"
      @cancel.prevent="requestClose"
      @click="$event.target === $event.currentTarget && requestClose()"
    >
      <template v-if="isOpen">
        <div class="lp-dialog-header">
          <div>
            <span class="lp-caption">{{ account.name }}</span>
            <h2 id="demo-dialog-title">{{ modalTitle }}</h2>
          </div>
          <button class="lp-close" aria-label="关闭弹窗" @click="requestClose">
            ×
          </button>
        </div>
        <p
          v-if="error"
          id="demo-form-error"
          class="lp-error"
          role="alert"
          tabindex="-1"
        >
          {{ error }}
        </p>
        <div v-if="discard" class="lp-discard" role="alert">
          <strong>放弃尚未保存的修改？</strong>
          <p>关闭后，本次填写的内容不会保留。</p>
          <div class="lp-actions">
            <button @click="discard = false">继续编辑</button
            ><button class="lp-danger-button" @click="closeModal">
              放弃修改
            </button>
          </div>
        </div>
        <form
          v-if="modal === 'record'"
          class="lp-form"
          novalidate
          @submit.prevent="saveRecord"
        >
          <div
            v-if="form.kind !== 'note'"
            class="lp-segment lp-form-tabs"
            aria-label="记录类型"
          >
            <button
              v-for="kind in ['in', 'out', 'asset'] as const"
              :key="kind"
              type="button"
              :aria-pressed="form.kind === kind"
              @click="
                form.kind = kind;
                error = '';
              "
            >
              {{ kind === "asset" ? "更新总资产" : kindLabels[kind] }}
            </button>
          </div>
          <label
            >日期<input
              v-model="form.date"
              name="date"
              type="date"
              :max="DEMO_TODAY"
              min="1900-01-01"
              required
          /></label>
          <label v-if="form.kind === 'in' || form.kind === 'out'"
            >{{ kindLabels[form.kind] }}金额
            <small>{{ account.currency }}</small
            ><input
              v-model="form.amount"
              name="amount"
              inputmode="decimal"
              placeholder="0.00"
              required
            /><span class="lp-field-hint">填写正数，最多两位小数</span></label
          >
          <label v-if="form.kind !== 'note'"
            >{{ form.kind === "asset" ? "总资产" : "变动后总资产" }}
            <small
              >{{ account.currency
              }}{{ form.kind === "asset" ? "" : " · 选填" }}</small
            ><input
              v-model="form.assets"
              name="assets"
              inputmode="decimal"
              :placeholder="form.kind === 'asset' ? '0.00' : '可稍后补充'"
              :required="form.kind === 'asset'"
            /><span class="lp-field-hint">{{
              form.kind === "asset"
                ? "填写这一天的资产总额。同日若有转入 / 转出，请填写变动后的金额。"
                : "包含本次转入 / 转出后的全部资产。留空时不推测资产数值。"
            }}</span></label
          >
          <label
            >备注
            <small>{{
              form.kind === "note" ? "必填" : "选填"
            }}</small
            ><textarea
              v-model="form.note"
              name="note"
              rows="3"
              maxlength="300"
              placeholder="为这笔记录留一句说明"
            />
          </label>
          <p class="lp-muted">
            示例日期截至 {{ DEMO_TODAY }}。保存仅影响本页。
          </p>
          <div class="lp-dialog-footer">
            <button type="button" @click="requestClose">取消</button
            ><button class="lp-primary" type="submit" :disabled="discard">
              保存记录
            </button>
          </div>
        </form>
        <div v-else-if="modal === 'detail' && editing" class="lp-dialog-body">
          <dl class="lp-info">
            <div>
              <dt>日期</dt>
              <dd>{{ editing.date }}</dd>
            </div>
            <div>
              <dt>类型</dt>
              <dd>
                {{ kindLabels[editing.kind]
                }}{{ editing.voided ? " · 已作废" : "" }}
              </dd>
            </div>
            <div v-if="editing.amount !== null">
              <dt>金额</dt>
              <dd>{{ money(editing.amount) }} {{ account.currency }}</dd>
            </div>
            <div>
              <dt>总资产</dt>
              <dd>{{ money(editing.assets) }}</dd>
            </div>
            <div>
              <dt>备注</dt>
              <dd>{{ editing.note || "—" }}</dd>
            </div>
          </dl>
          <h3>本页变更记录</h3>
          <ol class="lp-history">
            <li v-for="(item, i) in editing.history" :key="i">{{ item }}</li>
          </ol>
          <div class="lp-dialog-footer">
            <button
              v-if="!editing.voided"
              class="lp-danger-button"
              @click="modal = 'void'"
            >
              作废记录</button
            ><button @click="closeModal">关闭</button>
          </div>
        </div>
        <form
          v-else-if="modal === 'void' && editing"
          class="lp-form"
          novalidate
          @submit.prevent="voidRecord"
        >
          <p>
            作废 {{ editing.date }} 的{{
              kindLabels[editing.kind]
            }}记录后，将从收益计算和默认列表中移除。详情与变更记录仍保留在本页。
          </p>
          <label
            >作废原因<textarea
              v-model="form.reason"
              name="reason"
              rows="3"
              maxlength="200"
              placeholder="例如：重复记录"
              required
            />
          </label>
          <div class="lp-dialog-footer">
            <button type="button" @click="requestClose">取消</button
            ><button class="lp-danger-button" type="submit" :disabled="discard">
              确认作废
            </button>
          </div>
        </form>
        <form
          v-else-if="modal === 'account'"
          class="lp-form"
          novalidate
          @submit.prevent="saveAccount"
        >
          <label
            >账户名称<input
              v-model="form.name"
              name="accountName"
              maxlength="40"
              placeholder="例如：下一段旅程（示例）"
              required /></label
          ><label
            >记账币种<select
              v-model="form.currency"
              name="currency"
              :disabled="
                !isNewAccount &&
                !!(
                  account.records.length ||
                  account.holdings.length ||
                  account.cash
                )
              "
            >
              <option>CNY</option>
              <option>HKD</option>
              <option>USD</option></select
            ><span
              v-if="
                !isNewAccount &&
                (account.records.length ||
                  account.holdings.length ||
                  account.cash)
              "
              class="lp-field-hint"
              >已有记录或持仓，不能直接更换金额的币种。请新建独立账户。</span
            ></label
          >
          <div class="lp-dialog-footer">
            <button type="button" @click="requestClose">取消</button
            ><button class="lp-primary" type="submit" :disabled="discard">
              保存账户
            </button>
          </div>
        </form>
        <div v-else-if="modal === 'import'" class="lp-dialog-body">
          <p class="lp-reference">
            演示流程，使用内置示例数据，不读取 / 上传你的文件。
          </p>
          <div v-if="step === 1" class="lp-import-choice">
            <h3>选择一个开始方式</h3>
            <p>
              选择文件只演示选择步骤，下一步始终预览下方内置样例，不解析文件内容。
            </p>
            <label class="lp-file-label"
              >选择 .xlsx 文件<input
                type="file"
                accept=".xlsx"
                @change="previewFile" /></label
            ><span class="lp-muted">或</span
            ><button
              class="lp-primary"
              @click="
                step = 2;
                error = '';
              "
            >
              使用示例文件
            </button>
          </div>
          <form
            v-else
            class="lp-form"
            novalidate
            @submit.prevent="confirmImport"
          >
            <h3>内置示例预览 · 3 笔</h3>
            <ul class="lp-import-preview">
              <li v-for="row in importRows" :key="row.id">
                <div>
                  <strong>{{ row.date }}</strong
                  ><span
                    >{{ kindLabels[row.kind]
                    }}{{
                      row.amount === null ? "" : ` ${money(row.amount)}`
                    }}</span
                  >
                </div>
                <div>
                  <span>总资产 {{ money(row.assets) }} CNY</span
                  ><small>{{ row.note }}</small>
                </div>
              </li>
            </ul>
            <label
              >新示例账户名称<input
                v-model="form.name"
                name="importName"
                maxlength="40"
                placeholder="例如：导入体验（示例）"
                required
            /></label>
            <p class="lp-muted">
              始终新建独立 CNY 示例账户，不覆盖当前账户。确认后保存的就是以上 3
              笔。
            </p>
            <div class="lp-dialog-footer">
              <button type="button" @click="step = 1">上一步</button
              ><button class="lp-primary" type="submit" :disabled="discard">
                确认导入示例
              </button>
            </div>
          </form>
        </div>
        <form
          v-else-if="modal === 'holdings'"
          class="lp-form"
          novalidate
          @submit.prevent="step === 1 ? previewHoldings() : saveHoldings()"
        >
          <template v-if="step === 1"
            ><label
              >现金 <small>{{ account.currency }}</small
              ><input v-model="draftCash" name="cash" inputmode="decimal"
            /></label>
            <div class="lp-section-title">
              <h3>证券持仓</h3>
              <button
                type="button"
                :disabled="!availableSecurities.length"
                @click="
                  draftHoldings.push({
                    security: availableSecurities[0]!.id,
                    quantity: '',
                  })
                "
              >
                添加持仓
              </button>
            </div>
            <div
              v-for="(holding, i) in draftHoldings"
              :key="i"
              class="lp-holding-edit"
            >
              <label
                >证券<select
                  v-model="holding.security"
                  :aria-label="`第 ${i + 1} 笔证券`"
                >
                  <option
                    v-for="security in availableSecurities"
                    :key="security.id"
                    :value="security.id"
                  >
                    {{ security.name }} · {{ security.code }}
                  </option>
                </select></label
              ><label
                >数量<input
                  v-model="holding.quantity"
                  :aria-label="`第 ${i + 1} 笔数量`"
                  inputmode="decimal" /></label
              ><button
                type="button"
                class="lp-text-button lp-negative"
                :aria-label="`移除第 ${i + 1} 笔持仓`"
                @click="draftHoldings.splice(i, 1)"
              >
                移除
              </button>
            </div>
            <button
              v-if="!securityFormOpen"
              type="button"
              class="lp-text-button"
              @click="securityFormOpen = true"
            >
              找不到证券？添加示例证券
            </button>
            <fieldset v-else class="lp-security-form">
              <legend>新增示例证券</legend>
              <label
                >名称<input
                  v-model="newSecurity.name"
                  name="securityName"
                  maxlength="40"
              /></label>
              <div class="lp-two-fields">
                <label
                  >市场<input
                    v-model="newSecurity.market"
                    name="securityMarket"
                    maxlength="20" /></label
                ><label
                  >代码<input
                    v-model="newSecurity.code"
                    name="securityCode"
                    maxlength="20"
                /></label>
              </div>
              <label
                >币种<input :value="account.currency" readonly /><span
                  class="lp-field-hint"
                  >仅演示账户同币种证券，不进行汇率换算。</span
                ></label
              ><label
                >示例单价<input
                  v-model="newSecurity.price"
                  name="securityPrice"
                  inputmode="decimal"
              /></label>
              <div class="lp-actions">
                <button type="button" @click="securityFormOpen = false">
                  取消新增</button
                ><button type="button" @click="addSecurity">添加证券</button>
              </div>
            </fieldset></template
          >
          <template v-else
            ><h3>确认持仓快照</h3>
            <ul class="lp-holdings-list">
              <li>
                <span>现金</span><strong>{{ money(cents(draftCash)) }}</strong>
              </li>
              <li v-for="holding in draftHoldings" :key="holding.security">
                <span
                  >{{
                    availableSecurities.find((s) => s.id === holding.security)
                      ?.name
                  }}<small
                    >{{ holding.quantity }} 份 · 示例单价
                    {{
                      money(
                        availableSecurities.find(
                          (s) => s.id === holding.security,
                        )?.price ?? null,
                      )
                    }}</small
                  ></span
                >
              </li>
            </ul></template
          >
          <div class="lp-holding-summary">
            <span
              >示例估值 <small>{{ account.currency }}</small></span
            ><strong>{{ money(holdingEstimate) }}</strong
            ><small>固定示例价格，不代表实际市场价值</small>
          </div>
          <p class="lp-muted">
            保存持仓不会生成转入 /
            转出，也不会更新账户总资产。如需记录资产，请返回账户，在“记一笔”中选择“更新总资产”。
          </p>
          <div class="lp-dialog-footer">
            <button
              type="button"
              @click="step === 2 ? (step = 1) : requestClose()"
            >
              {{ step === 2 ? "返回修改" : "取消" }}</button
            ><button class="lp-primary" type="submit" :disabled="discard">
              {{ step === 1 ? "预览持仓" : "确认保存持仓" }}
            </button>
          </div>
        </form>
      </template>
    </dialog>
  </div>
</template>

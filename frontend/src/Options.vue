<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import {
  beijing,
  candidateSide,
  capitalLabel,
  costLabel,
  sideLabel,
  type OptionSide,
  clone,
  emptyFilter,
  identity,
  money,
  optionsRequest as api,
  percent,
  requestID,
  running,
  symbolLabel,
  write,
  type Candidate,
  type CandidatePage,
  type Favorite,
  type FavoritePage,
  type Filter,
  type Instrument,
  type Run,
  type Settings,
  type Status,
  type Watch,
} from "./options";
import OptionsDialog from "./options/OptionsDialog.vue";
import OptionsDetail from "./options/OptionsDetail.vue";
import OptionsResult from "./options/OptionsResult.vue";
import OptionsSettings from "./options/OptionsSettings.vue";
import OptionsNumber from "./options/OptionsNumber.vue";
import "./options.css";

const watchlist = ref<Watch[]>([]),
  settings = ref<Settings>();
const selected = ref(""),
  mode = ref<"full" | "net">("full"),
  feesEnabled = ref(false);
const optionType = ref<OptionSide>(
  new URLSearchParams(window.location.search).get("strategy") === "CALL"
    ? "CALL"
    : "PUT",
);
const isCall = computed(() => optionType.value === "CALL");
const strategyName = computed(() => sideLabel(optionType.value));
const filters = reactive(emptyFilter());
const sort = ref("date"),
  descending = ref(false);
const newPage = (): CandidatePage => ({
  items: [],
  total: 0,
  available: 0,
  excluded: {},
  snapshots: [],
  view: "",
});
const page = ref<CandidatePage>(newPage());
const status = ref<Status>(),
  loading = ref(true),
  ready = ref(false),
  busy = ref(false);
const error = ref(""),
  message = ref("");
let messageTimer: ReturnType<typeof setTimeout> | undefined;
// Transient feedback should not sit above the chain indefinitely.
watch(message, (value) => {
  clearTimeout(messageTimer);
  if (value)
    messageTimer = setTimeout(() => {
      message.value = "";
    }, 8000);
});
const modal = ref<
  | ""
  | "add"
  | "detail"
  | "compare"
  | "favorites"
  | "favorite"
  | "settings"
  | "watch"
  | "time"
  | "history"
>("");
const active = ref<Candidate>(),
  favoriteView = ref<Favorite>();
const favorites = ref<FavoritePage>({ items: [], total: 0 });
const comparison = ref<Candidate[]>([]),
  comparisonResults = ref<Candidate[]>([]);
const watchDraft = ref<Watch>();
const searchQuery = ref(""),
  searchResults = ref<Instrument[]>([]),
  searchLoading = ref(false);
const history = ref<Candidate["snapshot"][]>([]);
const favoriteCount = ref(0);
const stock = computed(() =>
  watchlist.value.find((v) => v.code === selected.value),
);
const snapshot = computed(() =>
  page.value.snapshots.find((v) => v.symbol === selected.value),
);
const heading = computed(() =>
  selected.value === "all"
    ? `全部自选 · ${strategyName.value} 期权链`
    : stock.value
      ? `${symbolLabel(stock.value.code)} · ${stock.value.name}`
      : "我的期权股票池",
);
const isRunning = computed(() => running(status.value?.run));
const pendingTargets = computed(() =>
  isRunning.value &&
  (status.value?.run?.option_type ?? "PUT") === optionType.value
    ? (status.value?.run?.targets ?? []).filter(
        (target) => target.state === "running" || target.state === "queued",
      )
    : [],
);
// Snapshots publish only after a whole chain is verified. While the current
// stock is still updating, an empty list means "not ready yet", not "failed".
const currentUpdate = computed(() =>
  selected.value === "all"
    ? (pendingTargets.value[0] ?? null)
    : (pendingTargets.value.find(
        (target) => target.symbol === selected.value,
      ) ?? null),
);
const currentUpdateTitle = computed(() =>
  currentUpdate.value
    ? selected.value === "all"
      ? "正在更新全部自选"
      : `正在更新 ${symbolLabel(currentUpdate.value.symbol)} 的行情`
    : "",
);
const title = computed(
  () =>
    ({
      add: "添加股票",
      detail: active.value
        ? `${symbolLabel(active.value.quote.contract.symbol)} · ${money(active.value.quote.contract.strike, 0)} ${sideLabel(active.value.quote.contract.type)}`
        : "合约详情",
      compare: "合约比较",
      favorites: `收藏 · ${favoriteCount.value}`,
      favorite: "收藏记录",
      settings: "个人设置与费用",
      watch: `${symbolLabel(stock.value?.code ?? "")} · 卖 ${strategyName.value} 条件`,
      time: "显示北京时间，按美东日期计天数",
      history: "手动更新历史",
    })[modal.value as Exclude<typeof modal.value, "">] ?? "",
);
const fields = computed<[keyof Filter, string, string][]>(() => [
  ["min_oi", "最低未平仓量", "份"],
  ["min_volume", "最低当日成交量", "份"],
  ["max_spread", "最大价差／中间价", "%"],
  ["max_delta", "最大绝对 Delta", "0–1"],
  isCall.value
    ? ["min_sale_price", "最低含权利金卖出价／股", "USD"]
    : ["max_cost", "最高净接货成本／股", "USD"],
  ["budget", isCall.value ? "备兑股票参考市值上限" : "单股资金预算", "USD"],
]);
const savedFilters = computed(
  () =>
    (isCall.value ? settings.value?.call_filter : settings.value?.filter) ??
    emptyFilter(),
);
const draftFilters = computed(() =>
  watchDraft.value
    ? isCall.value
      ? watchDraft.value.call_filter
      : watchDraft.value.filter
    : undefined,
);
const watchFields = computed<[keyof Filter, string][]>(() => [
  isCall.value
    ? ["min_strike", "最低行权价（USD）"]
    : ["max_strike", "最高行权价（USD）"],
  isCall.value
    ? ["min_sale_price", "最低含权利金卖出价（USD）"]
    : ["max_cost", "最高净接货成本（USD）"],
  ["budget", isCall.value ? "备兑股票参考市值上限（USD）" : "单股预算（USD）"],
  ["min_days", "最少天数"],
  ["max_days", "最多天数"],
]);
const feeVersion = computed(() =>
  feesEnabled.value && settings.value?.fees ? settings.value.version : 0,
);
let pageController: AbortController | undefined,
  searchController: AbortController | undefined;
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let disposed = false;
const composing = ref(false);
const pendingFavorites = new Map<
  string,
  {
    request_id: string;
    snapshot_id: string;
    contract_code: string;
    input: Candidate["input"];
  }
>();
let pendingScan:
  | { request_id: string; symbols: string[]; option_type: OptionSide }
  | undefined;

function fail(e: unknown) {
  if (!disposed)
    error.value = e instanceof Error ? e.message : "请求失败，请重试";
}
async function action(fn: () => Promise<void>) {
  busy.value = true;
  error.value = "";
  message.value = "";
  try {
    await fn();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}
async function loadPage(more = false) {
  pageController?.abort();
  const controller = new AbortController();
  pageController = controller;
  loading.value = true;
  const query = new URLSearchParams({
    symbol: selected.value,
    option_type: optionType.value,
    mode: mode.value,
    fee_version: String(feeVersion.value),
    sort: sort.value,
    direction: descending.value ? "desc" : "asc",
    limit: "100",
  });
  for (const [key, value] of Object.entries(filters))
    if (value) query.set(key, value);
  if (more && page.value.next_cursor) {
    query.set("cursor", page.value.next_cursor);
    query.set("view", page.value.view);
  }
  try {
    const data = await api<CandidatePage>(`/candidates?${query}`, {
      signal: controller.signal,
    });
    if (!controller.signal.aborted)
      page.value = more
        ? { ...data, items: [...page.value.items, ...data.items] }
        : data;
  } catch (e) {
    if (!controller.signal.aborted) {
      if (!more) page.value = newPage();
      fail(e);
    }
  } finally {
    if (!controller.signal.aborted) loading.value = false;
  }
}
watch(
  [
    selected,
    optionType,
    mode,
    feesEnabled,
    filters,
    sort,
    descending,
    () => settings.value?.version,
  ],
  () => {
    if (ready.value) void loadPage();
  },
  { deep: true },
);
// Absolute conditions (strike cap, budget, yield) rarely transfer between
// underlyings, so switching stock starts from that stock's full cached chain.
// Saved per-stock conditions still apply server-side.
function selectStock(code: string) {
  if (code === selected.value) return;
  selected.value = code;
  resetFilters();
  page.value = newPage();
}
function selectStrategy(side: OptionSide) {
  if (optionType.value === side) return;
  pageController?.abort();
  optionType.value = side;
  resetFilters();
  page.value = newPage();
  comparison.value = [];
  comparisonResults.value = [];
  error.value = "";
  message.value = "";
  const url = new URL(window.location.href);
  url.searchParams.set("strategy", side);
  window.history.replaceState(null, "", url);
}
function resetFilters() {
  Object.assign(filters, emptyFilter());
  sort.value = "date";
  descending.value = false;
}
function changeSort(key: string) {
  if (sort.value === key) descending.value = !descending.value;
  else {
    sort.value = key;
    descending.value = key !== "date";
  }
}
function schedulePoll() {
  clearTimeout(pollTimer);
  if (!disposed && isRunning.value && !document.hidden)
    pollTimer = setTimeout(() => void loadStatus(), 1000);
}
async function loadStatus() {
  try {
    const wasRunning = isRunning.value;
    status.value = await api<Status>("/status");
    favoriteCount.value = status.value.favorite_count;
    if (wasRunning && !isRunning.value) {
      await loadPage();
      message.value =
        status.value.run?.state === "succeeded"
          ? "行情更新完成"
          : "本轮更新结束，请查看各股票的结果";
    }
    schedulePoll();
  } catch (e) {
    fail(e);
    clearTimeout(pollTimer);
  }
}
function visibility() {
  if (document.hidden) clearTimeout(pollTimer);
  else if (isRunning.value) void loadStatus();
}
async function refresh(symbol?: string, side: OptionSide = optionType.value) {
  await action(async () => {
    const symbols = symbol
      ? [symbol]
      : selected.value === "all"
        ? []
        : [selected.value];
    if (
      !pendingScan ||
      pendingScan.option_type !== side ||
      JSON.stringify(pendingScan.symbols) !== JSON.stringify(symbols)
    )
      pendingScan = { request_id: requestID(), symbols, option_type: side };
    const run = await write<Run>("/scans", pendingScan);
    pendingScan = undefined;
    status.value = {
      provider: status.value?.provider ?? {
        connected: false,
        version: "",
        market_state: "",
        us_options: "unknown",
        checked_at: "",
      },
      run,
      favorite_count: favoriteCount.value,
      manual_only: true,
    };
    if (modal.value === "detail") modal.value = "";
    schedulePoll();
  });
}
async function search() {
  if (composing.value) return;
  searchController?.abort();
  const controller = new AbortController();
  searchController = controller;
  searchLoading.value = true;
  try {
    const data = await api<{ items: Instrument[] }>(
      `/instruments/search?q=${encodeURIComponent(searchQuery.value)}`,
      { signal: controller.signal },
    );
    if (!controller.signal.aborted) searchResults.value = data.items;
  } catch (e) {
    if (!controller.signal.aborted) {
      searchResults.value = [];
      fail(e);
    }
  } finally {
    if (!controller.signal.aborted) searchLoading.value = false;
  }
}
watch(searchQuery, (_, __, onCleanup) => {
  const timer = setTimeout(() => void search(), 220);
  onCleanup(() => clearTimeout(timer));
});
function openAdd() {
  error.value = "";
  searchQuery.value = "";
  modal.value = "add";
  void search();
}
function searchKey(event: KeyboardEvent) {
  if (event.isComposing || composing.value || event.keyCode === 229) return;
  if (
    event.key === "Enter" &&
    searchResults.value.length === 1 &&
    !searchLoading.value
  ) {
    event.preventDefault();
    void addStock(searchResults.value[0]!);
  }
  if (event.key === "ArrowDown") {
    event.preventDefault();
    document.querySelector<HTMLButtonElement>("[data-search-result]")?.focus();
  }
}
async function addStock(value: Instrument) {
  await action(async () => {
    await write<Watch>("/watchlist", { code: value.code });
    watchlist.value = (await api<{ items: Watch[] }>("/watchlist")).items;
    modal.value = "";
    selectStock(value.code);
    message.value = `已打开 ${symbolLabel(value.code)} 的 ${strategyName.value} 期权链`;
  });
}
function openDetail(value: Candidate) {
  active.value = clone(value);
  modal.value = "detail";
  error.value = "";
}
async function saveFavorite(value: Candidate) {
  await action(async () => {
    const key = identity(value) + JSON.stringify(value.input);
    if (!pendingFavorites.has(key))
      pendingFavorites.set(key, {
        request_id: requestID(),
        snapshot_id: value.snapshot.id,
        contract_code: value.quote.contract.code,
        input: clone(value.input),
      });
    const saved = await write<Favorite>(
      "/favorites",
      pendingFavorites.get(key),
    );
    pendingFavorites.delete(key);
    for (const row of page.value.items)
      if (identity(row) === identity(value)) row.favorite_id = saved.id;
    await loadStatus();
    message.value = "已收藏，可在顶部“收藏”查看原记录";
  });
}
async function favoriteRow(value: Candidate) {
  if (value.favorite_id) await openFavorite(value.favorite_id);
  else await saveFavorite(value);
}
async function openFavorites(more = false) {
  modal.value = "favorites";
  favoriteView.value = undefined;
  await action(async () => {
    const cursor =
      more && favorites.value.next_cursor
        ? `&cursor=${encodeURIComponent(favorites.value.next_cursor)}`
        : "";
    const data = await api<FavoritePage>(`/favorites?limit=30${cursor}`);
    favorites.value = more
      ? { ...data, items: [...favorites.value.items, ...data.items] }
      : data;
    favoriteCount.value = data.total;
  });
}
async function openFavorite(id: string) {
  modal.value = "favorite";
  favoriteView.value = undefined;
  await action(async () => {
    favoriteView.value = await api<Favorite>(
      `/favorites/${encodeURIComponent(id)}`,
    );
  });
}
async function deleteFavorite(id: string) {
  await action(async () => {
    await api(`/favorites/${encodeURIComponent(id)}`, { method: "DELETE" });
    favorites.value = await api<FavoritePage>("/favorites?limit=30");
    favoriteCount.value = favorites.value.total;
    await loadPage();
    message.value = "已取消收藏";
  });
}
function inTheMoney(value: Candidate) {
  const stock = value.snapshot.underlying;
  if (stock === null) return false;
  return candidateSide(value) === "CALL"
    ? Number(stock) > Number(value.quote.contract.strike)
    : Number(value.quote.contract.strike) > Number(stock);
}
function toggleCompare(value: Candidate) {
  const key = identity(value),
    i = comparison.value.findIndex((v) => identity(v) === key);
  if (i >= 0) comparison.value.splice(i, 1);
  else if (comparison.value.length < 3) comparison.value.push(clone(value));
  else message.value = "最多比较 3 份合约，请先移除一份";
}
async function compare() {
  modal.value = "compare";
  comparisonResults.value = [];
  await action(async () => {
    comparisonResults.value = await Promise.all(
      comparison.value.map((v) =>
        write<Candidate>("/calculations", {
          snapshot_id: v.snapshot.id,
          contract_code: v.quote.contract.code,
          input: {
            ...v.input,
            mode: mode.value,
            fee_version: feeVersion.value,
          },
        }),
      ),
    );
  });
}
async function saveSettings(value: Settings) {
  await action(async () => {
    settings.value = await write<Settings>("/settings", value, "PUT");
    mode.value = settings.value.mode;
    feesEnabled.value = !!settings.value.fees;
    modal.value = "";
    message.value = "设置已保存";
  });
}
async function saveFilter() {
  if (!settings.value) return;
  await action(async () => {
    settings.value = await write<Settings>(
      "/settings",
      {
        ...settings.value,
        [isCall.value ? "call_filter" : "filter"]: clone(filters),
        mode: mode.value,
      },
      "PUT",
    );
    message.value = `卖 ${strategyName.value} 的筛选和资金口径已保存`;
  });
}
function editWatch() {
  if (stock.value) {
    watchDraft.value = clone(stock.value);
    watchDraft.value.call_filter ??= emptyFilter();
    modal.value = "watch";
  }
}
async function saveWatch() {
  if (!watchDraft.value) return;
  await action(async () => {
    await write(
      `/watchlist/${encodeURIComponent(watchDraft.value!.code)}`,
      watchDraft.value,
      "PATCH",
    );
    watchlist.value = (await api<{ items: Watch[] }>("/watchlist")).items;
    modal.value = "";
    await loadPage();
  });
}
async function removeWatch() {
  if (!stock.value) return;
  const code = stock.value.code;
  await action(async () => {
    await api(`/watchlist/${encodeURIComponent(code)}`, { method: "DELETE" });
    watchlist.value = (await api<{ items: Watch[] }>("/watchlist")).items;
    modal.value = "";
    selectStock(watchlist.value[0]?.code ?? "");
    message.value = "已移出股票池，收藏记录仍保留";
  });
}
async function openHistory() {
  modal.value = "history";
  history.value = [];
  await action(async () => {
    history.value = (
      await api<{ items: Candidate["snapshot"][] }>(
        `/history?symbol=${encodeURIComponent(selected.value)}&option_type=${optionType.value}`,
      )
    ).items;
  });
}

onMounted(async () => {
  document.addEventListener("visibilitychange", visibility);
  try {
    const [list, config, state] = await Promise.all([
      api<{ items: Watch[] }>("/watchlist"),
      api<Settings>("/settings"),
      api<Status>("/status"),
    ]);
    watchlist.value = list.items;
    settings.value = config;
    mode.value = config.mode;
    feesEnabled.value = !!config.fees;
    Object.assign(
      filters,
      isCall.value ? (config.call_filter ?? emptyFilter()) : config.filter,
    );
    status.value = state;
    favoriteCount.value = state.favorite_count;
    selected.value = list.items[0]?.code ?? "";
    await nextTick();
    ready.value = true;
    await loadPage();
    schedulePoll();
  } catch (e) {
    fail(e);
    loading.value = false;
  }
});
onBeforeUnmount(() => {
  disposed = true;
  clearTimeout(pollTimer);
  clearTimeout(messageTimer);
  pageController?.abort();
  searchController?.abort();
  document.removeEventListener("visibilitychange", visibility);
});
</script>

<template>
  <div class="options-app">
    <header class="opt-masthead">
      <div class="opt-brand">
        <span class="opt-mark">观</span><span>观价</span
        ><span class="opt-brand-divider"></span
        ><span class="opt-brand-sub">期权</span>
      </div>
      <div class="opt-header-actions">
        <button class="opt-btn opt-quiet" @click="modal = 'settings'">
          设置</button
        ><button class="opt-btn" @click="openFavorites()">
          收藏 <span class="opt-count">{{ favoriteCount }}</span></button
        ><button
          class="opt-btn opt-primary"
          :disabled="busy || isRunning || !watchlist.length || !ready"
          :title="isRunning ? '当前已有行情任务在运行，完成后可再次更新' : ''"
          @click="refresh()"
        >
          {{ isRunning ? "更新中…" : "更新行情" }}
        </button>
      </div>
    </header>
    <div class="opt-workspace">
      <aside class="opt-sidebar">
        <p class="opt-label">我的股票池</p>
        <nav class="opt-watchlist" aria-label="选择股票">
          <button
            v-if="watchlist.length"
            class="opt-watch"
            :aria-pressed="selected === 'all'"
            @click="selectStock('all')"
          >
            <strong>全部自选</strong
            ><small
              >{{ watchlist.filter((v) => v.enabled).length }} 只启用</small
            ></button
          ><button
            v-for="item in watchlist"
            :key="item.code"
            class="opt-watch"
            :aria-pressed="selected === item.code"
            @click="selectStock(item.code)"
          >
            <strong
              >{{ symbolLabel(item.code)
              }}<span v-if="!item.enabled" class="opt-muted">
                · 暂停</span
              ></strong
            ><small>{{ item.name }}</small>
          </button>
        </nav>
        <button
          class="opt-btn opt-quiet opt-add"
          :disabled="!ready"
          @click="openAdd"
        >
          ＋ 添加股票
        </button>
        <div class="opt-sidebar-foot">
          富途 OpenAPI<br />美元计价 · 标准 {{ strategyName }}<br /><strong
            >仅手动更新</strong
          ><button class="opt-text-link" @click="modal = 'time'">
            日期怎么计算？
          </button>
        </div>
      </aside>
      <section class="opt-main">
        <div class="opt-heading">
          <div>
            <h1>{{ heading }}</h1>
            <p>{{ strategyName }} 期权链 · 按卖出参考价 Bid 估算权利金收入。</p>
          </div>
          <div v-if="snapshot" class="opt-stock-price">
            <span>正股参考价</span
            ><strong>USD {{ money(snapshot.underlying) }}</strong>
          </div>
        </div>
        <div class="opt-strategy-row">
          <div
            class="opt-segmented opt-strategy-switch"
            role="group"
            aria-label="卖出策略"
          >
            <button :aria-pressed="!isCall" @click="selectStrategy('PUT')">
              卖 Put
            </button>
            <button :aria-pressed="isCall" @click="selectStrategy('CALL')">
              卖 Call
            </button>
          </div>
          <p class="opt-help">
            {{
              isCall
                ? "备兑卖 Call：每份对应 100 股，年化按持股参考市值测算。"
                : "现金担保卖 Put：每份对应 100 股，按行权价预留接货资金。"
            }}
          </p>
        </div>
        <p v-if="error" class="opt-notice opt-error" role="alert">
          {{ error }}
          <button class="opt-text-link" @click="error = ''">关闭</button>
        </p>
        <p v-if="message" class="opt-notice" role="status">
          {{ message }}
          <button class="opt-text-link" @click="message = ''">关闭</button>
        </p>
        <p v-if="status?.history_limited" class="opt-notice">
          普通行情历史已达到容量限制，较旧快照已清理；收藏和最新行情仍保留。
        </p>
        <div
          v-if="status?.run"
          class="opt-run"
          :class="{ 'opt-run-active': isRunning }"
          role="status"
        >
          <span
            >卖 {{ sideLabel(status.run.option_type) }} ·
            {{ isRunning ? "本次手动更新进行中" : "上次更新结果" }}</span
          ><span v-for="target in status.run.targets" :key="target.symbol"
            ><strong>{{ symbolLabel(target.symbol) }}</strong>
            <template
              v-if="target.state === 'running' || target.state === 'queued'"
              >进行中 · </template
            >{{ target.error || target.progress }}</span
          >
        </div>
        <section
          v-if="watchlist.length"
          class="opt-filters"
          aria-label="筛选条件"
        >
          <div class="opt-filter-grid">
            <label class="opt-field"
              >剩余天数
              <div class="opt-range">
                <OptionsNumber
                  v-model="filters.min_days"
                  type="number"
                  min="0"
                  step="1"
                  placeholder="不限"
                  aria-label="最少剩余天数"
                /><span>至</span
                ><OptionsNumber
                  v-model="filters.max_days"
                  type="number"
                  min="0"
                  step="1"
                  placeholder="不限"
                  aria-label="最多剩余天数"
                /></div
            ></label>
            <label class="opt-field"
              >{{ isCall ? "行权价下限（USD）" : "行权价上限（USD）"
              }}<OptionsNumber
                v-model="filters[isCall ? 'min_strike' : 'max_strike']"
                type="number"
                min="0"
                step="0.01"
                placeholder="不限" /></label
            ><label class="opt-field"
              >最低权利金年化（%）<OptionsNumber
                v-model="filters.min_apr"
                type="number"
                min="0"
                step="0.1"
                placeholder="不限" /></label
            ><label class="opt-field"
              >价内／价外<select
                v-model="filters.moneyness"
                aria-label="价内／价外"
              >
                <option value="">全部</option>
                <option value="out">价外 {{ strategyName }}</option>
                <option value="in">价内 {{ strategyName }}</option>
              </select></label
            ><button class="opt-btn" @click="resetFilters">重置筛选</button>
          </div>
          <details class="opt-details">
            <summary>更多条件与费用</summary>
            <div class="opt-advanced">
              <label
                v-for="[key, label, unit] in fields"
                :key="key"
                class="opt-field"
                >{{ label }}（{{ unit }}）<OptionsNumber
                  v-model="filters[key]"
                  type="number"
                  min="0"
                  :step="
                    key === 'min_oi' || key === 'min_volume' ? '1' : '0.01'
                  "
                  placeholder="不限" /></label
              ><label class="opt-checkbox"
                ><input
                  v-model="feesEnabled"
                  type="checkbox"
                  :disabled="!settings?.fees"
                />
                计入预计开仓费用</label
              >
            </div>
            <div class="opt-filter-actions">
              <span class="opt-help">{{
                settings?.fees
                  ? `费用方案：${settings.fees.name}`
                  : "费用未配置，当前为税费前测算"
              }}</span
              ><button class="opt-text-link" @click="modal = 'settings'">
                费用设置</button
              ><button
                class="opt-text-link"
                :disabled="busy"
                @click="saveFilter"
              >
                保存筛选方案</button
              ><button
                class="opt-text-link"
                @click="Object.assign(filters, savedFilters)"
              >
                恢复已存方案</button
              ><button v-if="stock" class="opt-text-link" @click="editWatch">
                股票设置
              </button>
            </div>
          </details>
        </section>
        <div class="opt-result-toolbar">
          <div>
            {{ strategyName }} 期权链
            <small
              >{{ page.total }} / {{ page.available }} 份 ·
              {{ feeVersion ? "预计扣费后" : "税费前" }}</small
            >
          </div>
          <div class="opt-segmented" aria-label="年化资金口径">
            <button :aria-pressed="mode === 'full'" @click="mode = 'full'">
              {{ isCall ? "持股市值" : "全额资金" }}</button
            ><button :aria-pressed="mode === 'net'" @click="mode = 'net'">
              净投入
            </button>
          </div>
        </div>
        <div
          v-if="loading && !page.items.length"
          class="opt-empty"
          role="status"
        >
          读取已保存的行情…
        </div>
        <div v-else-if="!page.items.length" class="opt-empty">
          <template v-if="currentUpdate">
            <strong>{{ currentUpdateTitle }}</strong>
            <p>
              {{ currentUpdate.progress }}。正在核对整条 {{ strategyName }}
              期权链，完成后会自动显示，无需重复点击。
            </p>
            <button class="opt-btn" disabled>更新中…</button>
          </template>
          <template v-else>
            <strong>{{
              !watchlist.length
                ? "先添加愿意持有的股票"
                : !page.snapshots.length
                  ? "还没有这只股票的行情"
                  : !page.available
                    ? "当前快照没有可交易的有效报价"
                    : "没有符合当前条件的合约"
            }}</strong>
            <p>
              {{
                !watchlist.length
                  ? "支持中文名称、英文名称和代码搜索。"
                  : !page.snapshots.length
                    ? `点击“更新行情”，手动获取 ${strategyName} 期权链。`
                    : !page.available
                      ? "已到期和卖出价（Bid）不可用的合约不再显示，请更新行情获取最新报价。"
                      : "可降低年化门槛，或放宽期限和行权价。"
              }}
            </p>
            <button
              v-if="!watchlist.length"
              class="opt-btn opt-primary"
              :disabled="!ready"
              @click="openAdd"
            >
              添加股票</button
            ><button
              v-else-if="!page.snapshots.length || !page.available"
              class="opt-btn opt-primary"
              :disabled="
                busy || isRunning || (!stock?.enabled && selected !== 'all')
              "
              @click="refresh()"
            >
              {{ isRunning ? "更新中…" : "更新行情" }}</button
            ><button v-else class="opt-btn" @click="resetFilters">
              重置筛选
            </button>
          </template>
        </div>
        <template v-else
          ><div class="opt-table-scroll">
            <table class="opt-table">
              <thead>
                <tr>
                  <th><span class="opt-sr">比较</span></th>
                  <th>
                    <button @click="changeSort('date')">
                      到期日（美东）{{
                        sort === "date" ? (descending ? "↓" : "↑") : ""
                      }}
                    </button>
                  </th>
                  <th class="opt-num">剩余天数</th>
                  <th class="opt-num">
                    <button @click="changeSort('strike')">行权价</button>
                  </th>
                  <th class="opt-num">卖出 / 买入价</th>
                  <th class="opt-num">
                    <button @click="changeSort('premium')">
                      {{ feeVersion ? "净权利金" : "权利金" }}／份
                    </button>
                  </th>
                  <th class="opt-num">内在价值／份</th>
                  <th class="opt-num">时间价值／份</th>
                  <th class="opt-num">
                    <button @click="changeSort('yield')">
                      单利年化
                      {{ sort === "yield" ? (descending ? "↓" : "↑") : "" }}
                    </button>
                  </th>
                  <th class="opt-num">
                    <button @click="changeSort('capital')">
                      {{ isCall ? "股票参考市值／份" : "履约金额／份" }}
                    </button>
                  </th>
                  <th class="opt-favorite-cell">留档</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="row in page.items"
                  :key="identity(row)"
                  tabindex="0"
                  :aria-label="`查看 ${row.quote.contract.code}`"
                  @click="openDetail(row)"
                  @keydown.enter="
                    $event.target === $event.currentTarget && openDetail(row)
                  "
                >
                  <td @click.stop>
                    <input
                      type="checkbox"
                      :checked="
                        comparison.some((v) => identity(v) === identity(row))
                      "
                      :disabled="row.result.apr === null"
                      :aria-label="`比较 ${row.quote.contract.code}`"
                      @change="toggleCompare(row)"
                    />
                  </td>
                  <td>
                    <strong>{{ row.quote.contract.expiry }}</strong
                    ><small
                      >{{ symbolLabel(row.quote.contract.symbol)
                      }}<span v-if="row.stale"> · 历史快照</span></small
                    >
                  </td>
                  <td class="opt-num opt-dte">{{ row.result.dte }} 天</td>
                  <td class="opt-num">
                    {{ money(row.quote.contract.strike)
                    }}<small v-if="inTheMoney(row)" class="opt-flag"
                      >价内</small
                    >
                  </td>
                  <td class="opt-num">
                    <strong>{{ money(row.quote.bid) }}</strong
                    ><span class="opt-muted"> / {{ money(row.quote.ask) }}</span
                    ><small v-if="row.result.status !== 'ok'">{{
                      row.result.status === "quote_unavailable"
                        ? "Bid 不可用"
                        : row.result.status === "unsupported_contract"
                          ? "合约属性未确认"
                          : "年化不可用"
                    }}</small>
                  </td>
                  <td class="opt-num">{{ money(row.result.income) }}</td>
                  <td class="opt-num">
                    {{ money(row.result.intrinsic) }}
                  </td>
                  <td
                    class="opt-num"
                    :class="{
                      'opt-time-negative':
                        row.result.time_value !== null &&
                        Number(row.result.time_value) < 0,
                    }"
                    :title="
                      row.result.time_value !== null &&
                      Number(row.result.time_value) < 0
                        ? '拟成交价低于参考内在价值，请结合盘口价差与报价时点核对'
                        : ''
                    "
                  >
                    {{ money(row.result.time_value) }}
                  </td>
                  <td class="opt-num opt-apr">{{ percent(row.result.apr) }}</td>
                  <td class="opt-num">
                    {{ money(row.result.capital, 0)
                    }}<small v-if="row.budget_quantity"
                      >预算可覆盖 {{ row.budget_quantity }} 份*</small
                    >
                  </td>
                  <td class="opt-favorite-cell" @click.stop>
                    <button
                      class="opt-favorite"
                      :class="{ 'is-favorite': row.favorite_id }"
                      :aria-label="`${row.favorite_id ? '查看收藏' : '收藏'} ${row.quote.contract.code}`"
                      :disabled="busy"
                      @click="favoriteRow(row)"
                    >
                      {{ row.favorite_id ? "★ 已收藏" : "☆ 收藏" }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="opt-mobile-cards">
            <article
              v-for="row in page.items"
              :key="identity(row)"
              class="opt-contract-card"
            >
              <div class="opt-mobile-top">
                <button class="opt-contract-title" @click="openDetail(row)">
                  {{ symbolLabel(row.quote.contract.symbol) }}
                  {{ money(row.quote.contract.strike, 0) }}
                  {{ sideLabel(row.quote.contract.type)
                  }}<small
                    ><span v-if="inTheMoney(row)" class="opt-flag">价内</span
                    >{{ row.quote.contract.expiry }} ·
                    {{ row.result.dte }} 天</small
                  ></button
                ><label
                  ><input
                    type="checkbox"
                    :checked="
                      comparison.some((v) => identity(v) === identity(row))
                    "
                    :disabled="row.result.apr === null"
                    @change="toggleCompare(row)"
                  />
                  比较</label
                >
              </div>
              <div class="opt-mobile-values">
                <div>
                  <small>卖出 / 买入价</small
                  ><strong
                    >{{ money(row.quote.bid) }} /
                    {{ money(row.quote.ask) }}</strong
                  >
                </div>
                <div>
                  <small>权利金／份</small
                  ><strong>{{ money(row.result.income) }}</strong>
                </div>
                <div>
                  <small>单利年化</small
                  ><strong class="opt-apr">{{
                    percent(row.result.apr)
                  }}</strong>
                </div>
                <div>
                  <small>内在价值／份</small
                  ><strong>{{ money(row.result.intrinsic) }}</strong>
                </div>
                <div>
                  <small>时间价值／份</small
                  ><strong
                    :class="{
                      'opt-time-negative':
                        row.result.time_value !== null &&
                        Number(row.result.time_value) < 0,
                    }"
                    >{{ money(row.result.time_value) }}</strong
                  >
                </div>
              </div>
              <div class="opt-mobile-actions">
                <button class="opt-text-link" @click="openDetail(row)">
                  详情 · {{ isCall ? "股票市值" : "履约" }} USD
                  {{ money(row.result.capital, 0) }}</button
                ><button
                  class="opt-favorite"
                  :disabled="busy"
                  @click="favoriteRow(row)"
                >
                  {{ row.favorite_id ? "★ 已收藏" : "☆ 收藏" }}
                </button>
              </div>
            </article>
          </div>
        </template>
        <div v-if="page.next_cursor" class="opt-load-more">
          <button class="opt-btn" :disabled="loading" @click="loadPage(true)">
            {{
              loading
                ? "读取中…"
                : `加载更多（已显示 ${page.items.length} / ${page.total}）`
            }}
          </button>
        </div>
        <details v-if="Object.keys(page.excluded).length" class="opt-details">
          <summary>查看筛除原因</summary>
          <p class="opt-help">
            {{
              Object.entries(page.excluded)
                .map(([key, value]) => `${key}：${value} 份`)
                .join("；")
            }}。每份合约按第一个未通过的条件统计。
          </p>
        </details>
        <p class="opt-formula">
          按卖出参考价 Bid 估算 ·
          {{ mode === "full" ? capitalLabel(optionType) : "净投入参考值" }} ·
          {{ feeVersion ? "预计扣开仓费后" : "税费前" }}。年化 = 权利金收入 ÷
          资金基数 × 365 ÷ 剩余天数。金额均为 USD。
        </p>
        <p v-if="filters.budget" class="opt-help">
          *预算按{{
            isCall ? "每份备兑股票参考市值" : "每份全额履约金额"
          }}取整，费用另列；多份费用在详情重新计算。{{
            isCall ? "备兑仍需持有对应股数，预算不代表已持仓。" : ""
          }}
        </p>
        <footer class="opt-market-foot">
          <div v-for="value in page.snapshots" :key="value.id">
            <strong>{{ symbolLabel(value.symbol) }}</strong> 手动采集
            {{ beijing(value.captured_at) }}（北京）<span>
              · {{ value.from }} 至 {{ value.to }} 到期</span
            >
            <p v-if="value.warning" class="opt-warning">{{ value.warning }}</p>
          </div>
          <span v-if="!page.snapshots.length">仅手动更新 · 到期日按美东</span>
          <div class="opt-inline">
            <button class="opt-text-link" @click="modal = 'time'">
              日期口径</button
            ><button v-if="stock" class="opt-text-link" @click="openHistory">
              更新历史
            </button>
          </div>
        </footer>
      </section>
    </div>
    <div v-if="comparison.length" class="opt-comparison-bar">
      <div class="opt-selected-tags">
        <button
          v-for="value in comparison"
          :key="identity(value)"
          class="opt-btn"
          @click="toggleCompare(value)"
        >
          {{ symbolLabel(value.quote.contract.symbol) }}
          {{ money(value.quote.contract.strike, 0) }}
          {{ sideLabel(value.quote.contract.type) }} ·
          {{ value.quote.contract.expiry.slice(5) }} ×
        </button>
      </div>
      <button
        class="opt-btn opt-primary"
        :disabled="comparison.length < 2 || busy"
        @click="compare"
      >
        比较 {{ comparison.length }} 份合约
      </button>
    </div>
  </div>

  <OptionsDialog
    v-if="modal"
    :key="modal"
    :title="title"
    :wide="modal === 'compare'"
    @close="modal = ''"
  >
    <p v-if="error" class="opt-notice opt-error" role="alert">{{ error }}</p>
    <p v-if="message && modal === 'detail'" class="opt-notice" role="status">
      {{ message }}
    </p>
    <template v-if="modal === 'add'"
      ><label class="opt-field"
        >搜索股票<input
          v-model="searchQuery"
          autofocus
          placeholder="例如：英伟达 / NVDA / Microsoft"
          autocomplete="off"
          autocapitalize="off"
          maxlength="48"
          @compositionstart="composing = true"
          @compositionend="
            composing = false;
            search();
          "
          @keydown="searchKey"
      /></label>
      <p class="opt-help" aria-live="polite">
        {{ searchLoading ? "搜索证券名称…" : `${searchResults.length} 个结果` }}
      </p>
      <div class="opt-search-results">
        <button
          v-for="item in searchResults"
          :key="item.code"
          class="opt-search-result"
          data-search-result
          :disabled="busy"
          @click="addStock(item)"
        >
          <span
            ><strong>{{ item.name }}</strong
            ><small>{{ symbolLabel(item.code) }} · 美股 · USD</small></span
          ><span>{{
            watchlist.some((v) => v.code === item.code)
              ? "查看期权链"
              : "添加并查看"
          }}</span>
        </button>
        <p v-if="!searchResults.length && !searchLoading" class="opt-help">
          没有找到匹配股票，试试完整代码或名称。
        </p>
      </div>
      <p class="opt-help">
        搜索只查询证券名称；添加后读取已保存的行情，点击“更新行情”才获取报价。
      </p></template
    >
    <OptionsDetail
      v-else-if="modal === 'detail' && active"
      :value="active"
      :saving="busy"
      @favorite="saveFavorite"
      @refresh="refresh(active!.quote.contract.symbol, candidateSide(active!))"
    />
    <template v-else-if="modal === 'compare'"
      ><p class="opt-help">
        按相同资金口径、费用方案和 1 份合约比较；各自保留行情时间与估值日期。
      </p>
      <p v-if="busy" role="status">读取测算…</p>
      <div class="opt-compare-grid">
        <section v-for="value in comparisonResults" :key="identity(value)">
          <h3>
            {{ symbolLabel(value.quote.contract.symbol) }}
            {{ money(value.quote.contract.strike, 0) }}
            {{ sideLabel(value.quote.contract.type) }}
          </h3>
          <p class="opt-help">
            {{ value.quote.contract.expiry }} ·
            {{ value.result.dte }} 天<br />{{
              beijing(value.snapshot.captured_at)
            }}（北京）
          </p>
          <strong class="opt-compare-apr">{{
            percent(value.result.apr)
          }}</strong>
          <dl class="opt-metrics">
            <div>
              <dt>权利金（USD）</dt>
              <dd>{{ money(value.result.income) }}</dd>
            </div>
            <div>
              <dt>{{ capitalLabel(value.quote.contract.type) }}（USD）</dt>
              <dd>{{ money(value.result.capital) }}</dd>
            </div>
            <div>
              <dt>期间收入率</dt>
              <dd>{{ percent(value.result.rate) }}</dd>
            </div>
            <div>
              <dt>{{ costLabel(value.quote.contract.type) }}（USD）</dt>
              <dd>{{ money(value.result.cost) }}</dd>
            </div>
            <div v-if="candidateSide(value) === 'CALL'">
              <dt>含权利金卖出价／股（USD）</dt>
              <dd>{{ money(value.result.net_sale_price) }}</dd>
            </div>
            <div>
              <dt>未平仓量</dt>
              <dd>{{ money(value.quote.oi, 0) }}</dd>
            </div>
          </dl>
        </section>
      </div></template
    >
    <template v-else-if="modal === 'favorites'"
      ><p class="opt-help">
        收藏长期保存在服务器，打开查看当时的行情、参数和结果。
      </p>
      <div
        v-for="item in favorites.items"
        :key="item.id"
        class="opt-saved-item"
      >
        <div>
          <strong
            >{{ symbolLabel(item.candidate.quote.contract.symbol) }} ·
            {{ money(item.candidate.quote.contract.strike, 0) }}
            {{ sideLabel(item.candidate.quote.contract.type) }} ·
            {{ item.candidate.quote.contract.expiry }}</strong
          >
          <p>
            权利金 USD {{ money(item.candidate.result.income) }} · 年化
            {{ percent(item.candidate.result.apr) }}
          </p>
          <small
            >收藏于 {{ beijing(item.created_at) }}（北京）<br />行情采集
            {{ beijing(item.candidate.snapshot.captured_at) }}（北京）</small
          >
        </div>
        <div class="opt-saved-actions">
          <button class="opt-btn" @click="openFavorite(item.id)">查看</button
          ><button
            class="opt-btn opt-quiet"
            :disabled="busy"
            @click="deleteFavorite(item.id)"
          >
            取消收藏
          </button>
        </div>
      </div>
      <p v-if="!favorites.items.length && !busy" class="opt-empty">
        还没有收藏。点击期权链每一行右侧的“收藏”即可留档。
      </p>
      <button
        v-if="favorites.next_cursor"
        class="opt-btn"
        :disabled="busy"
        @click="openFavorites(true)"
      >
        加载更多（{{ favorites.items.length }} / {{ favorites.total }}）
      </button></template
    >
    <template v-else-if="modal === 'favorite' && favoriteView"
      ><h3>
        {{ symbolLabel(favoriteView.candidate.quote.contract.symbol) }} ·
        {{ money(favoriteView.candidate.quote.contract.strike, 0) }}
        {{ sideLabel(favoriteView.candidate.quote.contract.type) }}
      </h3>
      <p class="opt-help">
        收藏于 {{ beijing(favoriteView.created_at) }}（北京）
      </p>
      <OptionsResult :value="favoriteView.candidate" archived />
      <p v-if="favoriteView.fee_profile" class="opt-help">
        费用方案：{{ favoriteView.fee_profile.name }} ·
        {{ favoriteView.fee_profile.source }}
      </p>
      <div class="opt-dialog-actions">
        <button class="opt-btn" @click="openFavorites()">返回收藏</button>
      </div></template
    >
    <OptionsSettings
      v-else-if="modal === 'settings' && settings"
      :value="settings"
      :option-type="optionType"
      :saving="busy"
      @save="saveSettings"
    />
    <template v-else-if="modal === 'watch' && watchDraft && draftFilters"
      ><form @submit.prevent="saveWatch">
        <label class="opt-checkbox"
          ><input v-model="watchDraft.enabled" type="checkbox" />
          启用此股票，加入全部自选刷新</label
        >
        <p class="opt-help">
          卖 {{ strategyName }} 的每股条件会与当前临时筛选取交集，Put／Call
          分别保存。
        </p>
        <div class="opt-detail-form">
          <label
            v-for="[key, label] in watchFields"
            :key="key"
            class="opt-field"
            >{{ label
            }}<OptionsNumber
              v-model="draftFilters[key]"
              type="number"
              min="0"
              step="0.01"
              placeholder="不限"
          /></label>
        </div>
        <div class="opt-dialog-actions">
          <button
            class="opt-btn opt-quiet"
            type="button"
            :disabled="busy"
            @click="removeWatch"
          >
            移出股票池</button
          ><button class="opt-btn opt-primary" :disabled="busy" type="submit">
            保存股票设置
          </button>
        </div>
      </form></template
    >
    <template v-else-if="modal === 'time'"
      ><p>采集时间、收藏时间显示北京时间；合约到期日沿用美东日期。</p>
      <div class="opt-note">
        美东 2026-11-13 15:00<br />北京 2026-11-14 04:00<br />到期日为美东 11 月
        20 日，剩余 <strong>7 天</strong>。
      </div>
      <p>
        按纽约日历日期计算天数，包含正确的夏令时处理。旧行情及收藏始终保留原估值日期，不用旧报价配新的天数抬高年化。
      </p>
      <p class="opt-help">
        单利年化用于比较期限，不代表一年可以反复取得相同收入。
      </p></template
    >
    <template v-else-if="modal === 'history'"
      ><p class="opt-help">
        每天保留最后一次手动快照摘要；未刷新的日期没有记录。
      </p>
      <div v-for="value in history" :key="value.id" class="opt-saved-item">
        <div>
          <strong>{{ value.valuation_date_et }}（美东）</strong>
          <p>手动采集 {{ beijing(value.captured_at) }}（北京）</p>
          <small
            >正股 USD {{ money(value.underlying) }} · 目录
            {{ value.total_contracts }} 份
            {{ sideLabel(value.option_type) }}</small
          >
        </div>
      </div>
      <p v-if="!history.length && !busy" class="opt-empty">
        还没有手动更新历史。
      </p></template
    >
  </OptionsDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { errorText, LedgerError, query, request, type Account } from "./ledger";
import { benchmarkDefinitions, type BenchmarkCode } from "./ledgerBenchmark";
import {
  returnPercent,
  returnReasons,
  type ReturnMetric,
} from "./ledgerReturns";
import {
  validateAnnualReturns,
  yearDescription,
  type AnnualReturnRow,
  type AnnualReturns,
} from "./ledgerAnnualReturns";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import {
  useLedgerCachedRead,
  type CachedLedgerRequest,
} from "./useLedgerCachedRead";
import { annualReturnsRequest } from "./ledgerCachedRequests";
import { todayShanghai } from "./ledgerView";
import LedgerDialog from "./LedgerDialog.vue";

const props = defineProps<{
  account: Pick<Account, "id" | "name" | "currency">;
  portfolio?: boolean;
  refreshKey: number;
  view: "personal" | "manager";
  hideHeading?: boolean;
}>();
const emit = defineEmits<{ view: [value: "personal" | "manager"] }>();
const { locked, accountCache, cacheEpoch } = useLedgerWorkspace();
const read = reactive(useLedgerCachedRead<AnnualReturns>(accountCache));
const code = ref<BenchmarkCode>("H00300");
const expanded = ref(false);
const switching = ref(false);
const latest = computed(() => read.data?.years.slice(-2) ?? []);
const shortRows = computed(() =>
  read.data ? [read.data.annualized, ...latest.value, read.data.since] : [],
);
const selectedMetric = (row: AnnualReturnRow) =>
  props.view === "personal" ? row.money_weighted : row.time_weighted;
const percent = (value: ReturnMetric) =>
  value.value === null
    ? "—"
    : `${value.value.startsWith("-") ? "" : "+"}${returnPercent(value.value, value.percentage)}`;
const detail = (metric: ReturnMetric) =>
  metric.status === "unavailable"
    ? (returnReasons[metric.reason] ??
      (metric.reason === "benchmark_timeout"
        ? "指数读取超时"
        : metric.reason === "benchmark_unavailable"
          ? "指数暂时不可用"
          : "缺少该区间的指数收盘点位"))
    : "";
const title = (row: AnnualReturnRow) =>
  row === read.data?.annualized
    ? "年化收益率"
    : row === read.data?.since
      ? "记账以来"
      : `${row.year}年`;
const period = (row: AnnualReturnRow) =>
  row.year
    ? yearDescription(row)
    : row.from && row.to && row.to > row.from
      ? `${row.from} 至 ${row.to}`
      : "暂无有效区间";

function load(options: { preserve?: boolean } = {}) {
  const preserve = options.preserve === true && Boolean(read.data);
  if (!preserve) read.clear();
  if (locked.value) return;
  switching.value = preserve;
  const account = props.account;
  const selected = code.value;
  const cachedRequest: CachedLedgerRequest<AnnualReturns> = props.portfolio
    ? {
        key: `portfolio-annual|${account.id}|${account.currency}|${props.refreshKey}|${selected}|${todayShanghai()}`,
        read: async (signal: AbortSignal) => {
          const data = await request<AnnualReturns>(
            `/${props.portfolio ? "portfolios" : "accounts"}/${encodeURIComponent(account.id)}/annual-returns${query({ benchmark: selected })}`,
            { signal },
          );
          try {
            return validateAnnualReturns(data, account, selected);
          } catch {
            throw new LedgerError("invalid_response");
          }
        },
      }
    : annualReturnsRequest(account, selected);
  void read.load(cachedRequest, true, preserve).finally(() => {
    if (switching.value) switching.value = false;
  });
}
watch(
  [() => props.account.id, () => props.refreshKey, cacheEpoch, locked],
  () => load(),
  {
    immediate: true,
  },
);
watch(code, () => load({ preserve: true }));
</script>

<template>
  <section
    class="lp-annual"
    :aria-labelledby="`lp-${portfolio ? 'portfolio' : 'account'}-annual-${account.id}`"
  >
    <div
      class="lp-annual-toolbar"
      :class="{ 'lp-annual-toolbar-embedded': hideHeading }"
    >
      <div class="lp-section-title lp-annual-heading">
        <div>
          <h2
            :id="`lp-${portfolio ? 'portfolio' : 'account'}-annual-${account.id}`"
            :class="{ 'sr-only': hideHeading }"
          >
            年度收益对比
          </h2>
          <small v-if="!hideHeading">按每年的资产与资金记录独立计算</small>
        </div>
        <button
          type="button"
          class="lp-text-button"
          :disabled="locked || !read.data"
          @click="expanded = true"
        >
          查看全部年份 ›
        </button>
      </div>

      <div class="lp-annual-controls">
        <label>
          收益视角
          <select
            :value="view"
            :disabled="locked"
            @change="
              emit(
                'view',
                ($event.target as HTMLSelectElement).value as
                  'personal' | 'manager',
              )
            "
          >
            <option value="personal">资金加权收益率</option>
            <option value="manager">时间加权收益率</option>
          </select>
        </label>
        <label>
          对比指数
          <select v-model="code" :disabled="locked">
            <option
              v-for="definition in benchmarkDefinitions"
              :key="definition.code"
              :value="definition.code"
            >
              {{ definition.name }}
            </option>
          </select>
        </label>
      </div>
    </div>
    <p v-if="read.loading" class="lp-annual-status" role="status">
      <span class="lp-spinner" aria-hidden="true" />
      {{
        switching
          ? "正在切换指数…"
          : read.data
            ? "已显示上次年度结果，正在核对…"
            : "正在计算年度收益…"
      }}
    </p>
    <p v-if="read.error" class="lp-error" role="alert">
      {{ errorText(read.error) }}
      <button :disabled="locked || read.loading" @click="load()">
        重新读取
      </button>
    </p>
    <template v-if="read.data">
      <p v-if="read.data.benchmark_error" class="lp-annual-note" role="status">
        指数{{
          read.data.benchmark_error === "benchmark_timeout"
            ? "读取超时"
            : "暂不可用"
        }}，账户收益仍可查看。
        <button :disabled="locked" @click="load()">重试指数</button>
      </p>
      <div v-if="!read.data.years.length" class="lp-empty">
        记录两天及以上的总资产后，即可查看年度收益。
      </div>
      <div v-else class="lp-annual-scroll">
        <table class="lp-annual-table">
          <thead>
            <tr>
              <th scope="col">时间</th>
              <th scope="col">
                {{ view === "personal" ? "资金加权收益率" : "时间加权收益率" }}
              </th>
              <th scope="col">{{ read.data.benchmark_name }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in shortRows" :key="row.year || title(row)">
              <th scope="row">
                {{ title(row) }}
                <small v-if="row.year">{{ period(row) }}</small>
              </th>
              <td
                :class="{
                  'lp-annual-down': selectedMetric(row).value?.startsWith('-'),
                }"
                :title="detail(selectedMetric(row))"
              >
                {{ percent(selectedMetric(row)) }}
                <small v-if="selectedMetric(row).value === null">{{
                  detail(selectedMetric(row))
                }}</small>
              </td>
              <td
                :class="{
                  'lp-annual-down': row.benchmark.value?.startsWith('-'),
                }"
                :title="detail(row.benchmark)"
              >
                {{ percent(row.benchmark) }}
                <small v-if="row.benchmark.value === null">{{
                  detail(row.benchmark)
                }}</small>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <LedgerDialog
      v-if="expanded"
      title="年度收益对比"
      :caption="account.name"
      @close="expanded = false"
    >
      <div class="lp-annual-full">
        <div class="lp-annual-controls">
          <label
            >收益视角
            <select
              :value="view"
              :disabled="locked"
              @change="
                emit(
                  'view',
                  ($event.target as HTMLSelectElement).value as
                    'personal' | 'manager',
                )
              "
            >
              <option value="personal">资金加权收益率</option>
              <option value="manager">时间加权收益率</option>
            </select>
          </label>
          <label
            >对比指数
            <select v-model="code" :disabled="locked">
              <option
                v-for="definition in benchmarkDefinitions"
                :key="definition.code"
                :value="definition.code"
              >
                {{ definition.name }}
              </option>
            </select>
          </label>
        </div>
        <p v-if="read.loading" class="lp-annual-status" role="status">
          <span class="lp-spinner" aria-hidden="true" />
          {{
            switching
              ? "正在切换指数…"
              : read.data
                ? "已显示上次年度结果，正在核对…"
                : "正在计算年度收益…"
          }}
        </p>
        <p v-if="read.error" class="lp-error" role="alert">
          {{ errorText(read.error) }} <button @click="load()">重新读取</button>
        </p>
        <template v-if="read.data">
          <p
            v-if="read.data.benchmark_error"
            class="lp-annual-note"
            role="status"
          >
            指数暂不可用，账户收益仍可查看。
          </p>
          <div class="lp-annual-scroll">
            <table class="lp-annual-table">
              <thead>
                <tr>
                  <th scope="col">时间</th>
                  <th scope="col">账户</th>
                  <th scope="col">指数</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="row in [
                    read.data.annualized,
                    ...read.data.years,
                    read.data.since,
                  ]"
                  :key="row.year || title(row)"
                >
                  <th scope="row">
                    {{ title(row)
                    }}<small v-if="row.year">{{ period(row) }}</small>
                  </th>
                  <td
                    :class="{
                      'lp-annual-down':
                        selectedMetric(row).value?.startsWith('-'),
                    }"
                    :title="detail(selectedMetric(row))"
                  >
                    {{ percent(selectedMetric(row))
                    }}<small v-if="selectedMetric(row).value === null">{{
                      detail(selectedMetric(row))
                    }}</small>
                  </td>
                  <td
                    :class="{
                      'lp-annual-down': row.benchmark.value?.startsWith('-'),
                    }"
                    :title="detail(row.benchmark)"
                  >
                    {{ percent(row.benchmark)
                    }}<small v-if="row.benchmark.value === null">{{
                      detail(row.benchmark)
                    }}</small>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </div>
    </LedgerDialog>
  </section>
</template>

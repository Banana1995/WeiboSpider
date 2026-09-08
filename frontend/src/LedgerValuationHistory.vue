<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import LedgerValuation from "./LedgerValuation.vue";
import {
  query,
  request,
  type Page,
  type ValuationSummary,
  type ValuationHistory,
} from "./ledger";
import { useLedgerRead } from "./useLedgerRead";

const props = defineProps<{ accountId: string; refreshKey?: number }>();
const list = reactive(useLedgerRead<Page<ValuationSummary>>());
const detail = reactive(useLedgerRead<ValuationHistory>());
const filters = reactive({ from: "", to: "" });
const applied = ref({ ...filters });
const cursor = ref("");
const detailId = ref("");
function loadList() {
  const accountId = props.accountId;
  const params = query({ ...applied.value, cursor: cursor.value, limit: "30" });
  void list.load(async (signal) => {
    const result = await request<Page<ValuationSummary>>(
      `/accounts/${accountId}/valuations${params}`,
      { signal },
    );
    if (
      !Array.isArray(result?.items) ||
      result.items.some((item) => item.account_id !== accountId)
    )
      throw new Error("历史列表与所选账户不匹配或数据缺失");
    return result;
  });
}
function first() {
  cursor.value = "";
  list.clear();
  loadList();
}
function applyFilters() {
  applied.value = { ...filters };
  first();
}
function next() {
  if (!list.data?.next_cursor || list.loading || list.error) return;
  cursor.value = list.data.next_cursor;
  list.clear();
  loadList();
}
function loadDetail(id: string) {
  if (detailId.value !== id) detail.clear();
  detailId.value = id;
  const accountId = props.accountId;
  void detail.load(async (signal) => {
    const result = await request<ValuationHistory>(
      `/accounts/${accountId}/valuations/${id}`,
      { signal },
    );
    if (
      result?.id !== id ||
      result.valuation?.account_id !== accountId ||
      result.schema_version !== 1 ||
      !Array.isArray(result.instruments) ||
      !Array.isArray(result.valuation.items)
    )
      throw new Error("历史详情与所选记录不匹配或数据缺失");
    return result;
  });
}
watch(
  () => props.accountId,
  () => {
    list.clear();
    detail.clear();
    detailId.value = "";
    filters.from = filters.to = "";
    applied.value = { ...filters };
    first();
  },
  { immediate: true },
);
// A completed current-valuation read may have appended a row after our first read.
watch(() => props.refreshKey, first);
</script>

<template>
  <section
    class="ledger-panel"
    data-test="valuation-history"
    aria-live="polite"
  >
    <h2>历史估值记录</h2>
    <p class="ledger-notice">
      按请求时间采样，非当日收盘；同日可多条；交易更正后历史不自动重算。读取历史不查询行情、不新增记录；收益分析会检查端点版本。周六任务默认关闭，来源可在独立调度面板核对；历史行情回填已取消。
    </p>
    <form class="ledger-form" @submit.prevent="applyFilters">
      <label
        >账本日期起（as_of，含当天）<input
          v-model="filters.from"
          name="history_from"
          type="date"
      /></label>
      <label
        >账本日期止（as_of，含当天）<input
          v-model="filters.to"
          name="history_to"
          type="date"
          :min="filters.from || undefined"
      /></label>
      <button
        type="submit"
        :disabled="!!(filters.from && filters.to && filters.from > filters.to)"
      >
        筛选历史
      </button>
    </form>
    <button type="button" @click="loadList">刷新历史列表</button>
    <p v-if="list.loading" role="status">
      正在读取历史列表，保留的数据可能已过期…
    </p>
    <p v-if="list.error" role="alert">{{ list.error }}</p>
    <div v-if="list.data" class="ledger-table">
      <table>
        <caption>
          保存顺序倒序，每页最多 30 条；金额为账户本位币
        </caption>
        <thead>
          <tr>
            <th>记录 / 账本日期</th>
            <th>计算 / 保存时间</th>
            <th>现金</th>
            <th>持仓市值</th>
            <th>总资产</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in list.data.items" :key="row.id">
            <td>
              <button
                type="button"
                :disabled="list.loading || !!list.error"
                @click="loadDetail(row.id)"
              >
                历史记录 #{{ row.id }}</button
              ><small>{{ row.as_of }}</small
              ><small>{{ row.currency }}</small>
            </td>
            <td>
              {{ row.calculated_at }}<small>{{ row.saved_at }}</small>
            </td>
            <td>{{ row.cash }}</td>
            <td>{{ row.positions_value }}</td>
            <td>{{ row.total_assets }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="!list.data.items.length">此日期范围暂无已保存历史记录。</p>
    </div>
    <button type="button" :disabled="!cursor || list.loading" @click="first">
      历史首页
    </button>
    <button
      type="button"
      :disabled="!list.data?.next_cursor || list.loading || !!list.error"
      @click="next"
    >
      历史下一页
    </button>
    <section v-if="detailId" data-test="history-detail">
      <h3>已保存历史记录 #{{ detailId }}</h3>
      <button type="button" @click="loadDetail(detailId)">
        重新读取历史详情
      </button>
      <p v-if="detail.loading" role="status">
        正在读取历史详情，保留的数据可能已过期…
      </p>
      <p v-if="detail.error" role="alert">{{ detail.error }}</p>
      <template v-if="detail.data">
        <p>
          保存时账户名：{{ detail.data.account_name }}；保存时间：{{
            detail.data.saved_at
          }}；快照格式版本：{{ detail.data.schema_version }}
        </p>
        <LedgerValuation
          :account-id="accountId"
          :value="detail.data.valuation"
          :instruments="detail.data.instruments"
          :loading="false"
          error=""
          historical
        />
      </template>
    </section>
  </section>
</template>

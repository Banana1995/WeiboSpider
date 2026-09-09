<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { LedgerError, query, request, type Page } from "./ledger";
import type { ImportedRow, ImportedSummary } from "./ledgerImport";
import { useLedgerRead } from "./useLedgerRead";
import ImportSummary from "./ImportSummary.vue";
import ImportedRows from "./ImportedRows.vue";
const props = defineProps<{ accountId: string; refreshKey: number }>();
const summary = reactive(useLedgerRead<ImportedSummary | null>());
const records = reactive(useLedgerRead<Page<ImportedRow & { id: string }>>());
const from = ref("");
const to = ref("");
let applied = { from: "", to: "" };
let generation = 0;
function loadRows(cursor = "") {
  const id = props.accountId;
  records.clear();
  void records.load((signal) =>
    request(
      `/accounts/${id}/imported-records${query({ ...applied, limit: "30", cursor })}`,
      { signal },
    ),
  );
}
function filter() {
  applied = { from: from.value, to: to.value };
  loadRows();
}
async function load() {
  const current = ++generation;
  const id = props.accountId;
  summary.clear();
  records.clear();
  await summary.load(async (signal) => {
    try {
      return await request<ImportedSummary>(`/accounts/${id}/import-summary`, {
        signal,
      });
    } catch (e) {
      if (e instanceof LedgerError && e.status === 404) return null;
      throw e;
    }
  });
  if (
    current === generation &&
    id === props.accountId &&
    summary.data &&
    !summary.loading &&
    !summary.error
  )
    loadRows();
}
watch(
  () => props.accountId,
  () => {
    from.value = "";
    to.value = "";
    applied = { from: "", to: "" };
    void load();
  },
  { immediate: true },
);
watch(
  () => props.refreshKey,
  () => void load(),
);
</script>

<template>
  <details class="ledger-panel imported-records" data-test="imported-records">
    <summary>查看账户级导入记录（原始资料）</summary>
    <div class="imported-records-content">
      <p>
        仅展示不可变来源记录，更正与作废不会改变此处。当前有效统计见持续记账区域。导入的总资产不是现金，不代表实时资产。
      </p>
      <button type="button" @click="load">刷新导入记录</button>
      <p v-if="summary.loading">正在读取导入摘要…</p>
      <p v-if="summary.error" role="alert">{{ summary.error }}</p>
      <p v-if="summary.data === null">此账户尚未导入。</p>
      <template v-if="summary.data">
        <p>
          批次 {{ summary.data.batch_id }} · 导入于
          {{ summary.data.imported_at }}
        </p>
        <ImportSummary
          :metadata="summary.data.metadata"
          :summary="summary.data.summary"
        />
        <form
          class="ledger-grid"
          data-test="import-filters"
          @submit.prevent="filter"
        >
          <label
            >起始日期（含）<input v-model="from" type="date" name="import_from"
          /></label>
          <label
            >结束日期（含）<input v-model="to" type="date" name="import_to"
          /></label>
          <button type="submit" :disabled="!!from && !!to && from > to">
            筛选导入记录
          </button>
        </form>
        <p>按日期倒序，每页最多 30 条；同日来源行号不代表交易顺序。</p>
        <p v-if="records.loading">正在读取账户级导入记录…</p>
        <p v-if="records.error" role="alert">{{ records.error }}</p>
        <ImportedRows v-if="records.data" :rows="records.data.items" />
        <p v-if="records.data?.items.length === 0">此日期范围暂无导入记录。</p>
        <div class="ledger-actions">
          <button type="button" @click="loadRows()">导入记录首页</button
          ><button
            v-if="records.data?.next_cursor"
            type="button"
            :disabled="records.loading || !!records.error"
            data-test="import-next"
            @click="loadRows(records.data.next_cursor)"
          >
            下一页导入记录
          </button>
        </div>
      </template>
    </div>
  </details>
</template>

<style scoped>
.imported-records > summary {
  font-size: 16px;
  font-weight: 600;
}
.imported-records-content {
  border-top: 1px solid #dde3db;
  padding-top: 12px;
}
</style>

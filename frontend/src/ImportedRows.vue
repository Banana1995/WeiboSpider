<script setup lang="ts">
import type { ImportedRow } from "./ledgerImport";
defineProps<{ rows: ImportedRow[] }>();
</script>

<template>
  <p>
    来源行号仅代表文件顺序，不是交易顺序。资金流保留正负号；空资产不等于零。日志与明细只读，不推断证券、子账户或转账关系。
  </p>
  <div class="ledger-table">
    <table>
      <thead>
        <tr>
          <th>来源行 / 类型</th>
          <th>日期</th>
          <th>资金流</th>
          <th>记录的总资产</th>
          <th>日志与明细（只读）</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.source_row">
          <td>
            #{{ r.source_row }} · {{ r.kind === "asset" ? "资产" : "资金流" }}
          </td>
          <td>{{ r.date }}</td>
          <td>{{ r.flow ?? "未提供" }}</td>
          <td>{{ r.total_assets ?? "未提供" }}</td>
          <td>
            <details>
              <summary>查看原始记录</summary>
              <p>日志</p>
              <pre>{{ r.note || "未提供" }}</pre>
              <p>明细</p>
              <pre>{{ r.detail || "未提供" }}</pre>
              <p>来源创建时间：{{ r.source_created_at || "未提供" }}</p>
              <p>原始日期：{{ r.date_raw }}</p>
              <p>原始创建时间：{{ r.created_raw || "未提供" }}</p>
            </details>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>

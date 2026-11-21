<script setup lang="ts">
import { computed, ref } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import { importResultText, type ImportReport } from "./ledgerImport";
import { money, recordKind } from "./ledgerView";

const props = defineProps<{ report: ImportReport }>();
const emit = defineEmits<{ close: [] }>();
const group = ref<"added" | "skipped">(
  props.report.added ? "added" : "skipped",
);
const page = ref(1);
const expanded = ref<number>();
const pageSize = 20;
const rows = computed(() => props.report.rows?.[group.value] ?? []);
const pages = computed(() => Math.ceil(rows.value.length / pageSize));
const visible = computed(() =>
  rows.value.slice((page.value - 1) * pageSize, page.value * pageSize),
);
function turn(n: number) {
  page.value = n;
  expanded.value = undefined;
}
function select(next: "added" | "skipped") {
  if (next === group.value) return;
  group.value = next;
  turn(1);
}
</script>

<template>
  <LedgerDialog
    title="本次导入差异"
    :caption="report.accountName"
    :show-write-status="false"
    data-test="import-diff"
    @close="emit('close')"
  >
    <div class="lp-dialog-body">
      <p class="diff-summary" role="status">{{ importResultText(report) }}</p>
      <template v-if="report.rows">
        <div class="diff-toolbar">
          <div class="lp-segment" role="group" aria-label="筛选导入差异">
            <button
              type="button"
              :aria-pressed="group === 'added'"
              @click="select('added')"
            >
              新增记录
            </button>
            <button
              type="button"
              :aria-pressed="group === 'skipped'"
              @click="select('skipped')"
            >
              跳过的重复记录
            </button>
          </div>
          <small>金额单位：{{ report.currency }}</small>
        </div>
        <div
          v-if="visible.length"
          :key="`${group}:${page}`"
          class="diff-register"
        >
          <table
            :aria-label="
              group === 'added' ? '本次新增记录' : '本次跳过的重复记录'
            "
          >
            <thead>
              <tr>
                <th scope="col">记账记录</th>
                <th scope="col">资金变动</th>
                <th scope="col">总资产</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="row in visible" :key="row.source_row">
                <tr :data-source-row="row.source_row">
                  <td>
                    <time :datetime="row.date">{{ row.date }}</time>
                    <span class="diff-kind">{{ recordKind(row) }}</span>
                    <button
                      type="button"
                      class="diff-source"
                      :aria-expanded="expanded === row.source_row"
                      :aria-label="`Excel 第 ${row.source_row} 行的原始记录`"
                      @click="
                        expanded =
                          expanded === row.source_row
                            ? undefined
                            : row.source_row
                      "
                    >
                      <span aria-hidden="true">{{
                        expanded === row.source_row ? "▾" : "▸"
                      }}</span>
                      Excel 第 {{ row.source_row }} 行
                    </button>
                  </td>
                  <td>{{ money(row.flow) }}</td>
                  <td>{{ money(row.total_assets) }}</td>
                </tr>
                <tr v-if="expanded === row.source_row">
                  <td colspan="3" class="diff-detail">
                    <dl>
                      <dt>投资日志</dt>
                      <dd>{{ row.note || "未提供" }}</dd>
                      <dt>明细</dt>
                      <dd>{{ row.detail || "未提供" }}</dd>
                      <dt>来源创建时间</dt>
                      <dd>{{ row.source_created_at || "未提供" }}</dd>
                    </dl>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
        <p v-else class="lp-empty">
          {{
            group === "added"
              ? "本次没有新增记录。"
              : "本次没有跳过的重复记录。"
          }}
        </p>
      </template>
      <p v-else>
        导入已完成。此次回执无法确认每一行的结果，仅显示已确认的笔数。
      </p>
      <div class="lp-dialog-footer">
        <nav v-if="pages > 1" class="diff-pages" aria-label="导入差异分页">
          <button type="button" :disabled="page === 1" @click="turn(page - 1)">
            上一页
          </button>
          <span aria-live="polite">第 {{ page }} / {{ pages }} 页</span>
          <button
            type="button"
            :disabled="page === pages"
            @click="turn(page + 1)"
          >
            下一页
          </button>
        </nav>
        <button type="button" class="lp-primary" @click="emit('close')">
          完成
        </button>
      </div>
    </div>
  </LedgerDialog>
</template>

<style scoped>
.diff-summary {
  margin: 0 0 1rem;
  font-weight: 600;
}
.diff-toolbar,
.diff-pages {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  flex-wrap: wrap;
}
.diff-toolbar {
  justify-content: space-between;
  margin-bottom: 0.75rem;
}
.diff-toolbar small,
.diff-pages span,
.diff-kind {
  color: var(--lp-muted);
}
.diff-pages {
  margin-right: auto;
}
.diff-register {
  max-height: min(50dvh, 30rem);
  overflow: auto;
}
.diff-register table {
  width: 100%;
  border-spacing: 0;
}
.diff-register th,
.diff-register td {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.diff-register :is(th, td):first-child {
  text-align: left;
}
.diff-register thead th {
  position: sticky;
  top: 0;
  z-index: 1;
}
.diff-kind {
  margin-left: 0.35rem;
}
.diff-register .diff-source {
  display: block;
  min-height: 0;
  padding: 0.15rem 0;
  border: 0;
  background: transparent;
  color: var(--lp-primary);
  font-size: 0.75rem;
}
.diff-detail dl {
  margin: 0;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 0.25rem 1rem;
}
.diff-detail dt {
  color: var(--lp-muted);
}
.diff-detail dd {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
@media (max-width: 599px) {
  .diff-register :is(td, th) {
    padding: 0.5rem 0.35rem;
  }
  .diff-kind {
    display: block;
    margin-left: 0;
  }
}
</style>

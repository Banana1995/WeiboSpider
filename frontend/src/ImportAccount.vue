<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { failure, newID, request, type Account } from "./ledger";
import {
  PendingImport,
  type ImportPreview,
  type ImportResult,
} from "./ledgerImport";
import ImportSummary from "./ImportSummary.vue";
import ImportedRows from "./ImportedRows.vue";

function warningText(code: string) {
  switch (code) {
    case "reported_history_only":
      return "仅导入账户级历史记录，总资产不是现金，也不关联持仓。";
    case "incremental_import_not_supported":
      return "每个账户只支持首个导入批次，不支持增量合并。";
    case "creation_time_precision_nanoseconds_truncated":
      return "来源创建时间精度已截断，请以预览时间为准。";
    default:
      return `导入存在其他注意事项，请核对预览（警告代码：${/^[a-z][a-z0-9_]{0,79}$/.test(code) ? code : "unknown_warning"}）。`;
  }
}

const props = defineProps<{ accounts: Account[]; disabled: boolean }>();
const emit = defineEmits<{
  locked: [value: boolean];
  imported: [result: ImportResult];
}>();
const file = shallowRef<File>();
const preview = shallowRef<ImportPreview>();
const pending = shallowRef<PendingImport>();
const target = ref("");
const page = ref(0);
const loading = ref(false);
const busy = ref(false);
const error = ref("");
const message = ref("");
const input = ref<HTMLInputElement>();
let controller: AbortController | undefined;
let generation = 0;
const locked = computed(() => !!pending.value);
watch(locked, (value) => emit("locked", value), { flush: "sync" });
const targetAccount = computed(() =>
  props.accounts.find((a) => a.id === target.value),
);
const mismatch = computed(
  () =>
    !!preview.value &&
    !!targetAccount.value &&
    targetAccount.value.currency !== preview.value.metadata.currency,
);
function changeFile(event: Event) {
  if (locked.value || props.disabled) return;
  generation++;
  controller?.abort();
  loading.value = false;
  preview.value = undefined;
  file.value = undefined;
  page.value = 0;
  error.value = "";
  message.value = "";
  const chosen = (event.target as HTMLInputElement).files?.[0];
  if (!chosen) return;
  if (!/\.xlsx$/i.test(chosen.name)) error.value = "仅支持 .xlsx 文件";
  else if (chosen.size > 8 * 1024 * 1024) error.value = "文件超过 8 MiB 限制";
  else if (!chosen.size) error.value = "文件为空";
  else file.value = chosen;
}
async function loadPreview() {
  if (!file.value || locked.value || props.disabled || loading.value) return;
  controller?.abort();
  controller = new AbortController();
  const current = ++generation;
  loading.value = true;
  preview.value = undefined;
  error.value = "";
  const body = new FormData();
  body.append("file", file.value);
  try {
    const result = await request<ImportPreview>(
      "/imports/youzhiyouxing/preview",
      { method: "POST", body, signal: controller.signal },
    );
    if (current === generation) {
      preview.value = result;
      page.value = 0;
    }
  } catch (e) {
    if (current === generation) error.value = failure(e);
  } finally {
    if (current === generation) loading.value = false;
  }
}
async function confirm() {
  if (busy.value || (props.disabled && !pending.value)) return;
  if (!pending.value) {
    if (
      !preview.value ||
      !file.value ||
      mismatch.value ||
      (target.value && !targetAccount.value)
    )
      return;
    pending.value = new PendingImport(
      file.value,
      preview.value.digest,
      target.value || newID(),
      !target.value,
    );
  }
  busy.value = true;
  error.value = "";
  let result: ImportResult;
  try {
    result = await pending.value.run();
  } catch (e) {
    error.value = failure(e);
    if (!pending.value.uncertain) pending.value = undefined;
    return;
  } finally {
    busy.value = false;
  }
  pending.value = undefined;
  preview.value = undefined;
  file.value = undefined;
  if (input.value) input.value.value = "";
  message.value = `导入已确认成功${result.duplicate ? "（相同内容已存在，未重复导入）" : ""}。后续读取失败不影响成功，请勿重复导入。`;
  emit("imported", result);
}
onBeforeUnmount(() => {
  generation++;
  controller?.abort();
});
</script>

<template>
  <section class="ledger-panel" data-test="import-account">
    <h2>有知有行账户 XLSX 导入</h2>
    <p>先预览，再明确确认。文件仅保留在此页内存，不保存到浏览器存储。</p>
    <fieldset :disabled="locked || disabled">
      <label
        >选择文件（.xlsx，最多 8 MiB）<input
          ref="input"
          type="file"
          accept=".xlsx"
          data-test="import-file"
          @change="changeFile"
      /></label>
      <button
        type="button"
        :disabled="!file || loading"
        data-test="import-preview"
        @click="loadPreview"
      >
        {{ loading ? "正在预览…" : "预览文件" }}
      </button>
      <label
        >导入目标<select v-model="target" data-test="import-target">
          <option value="">新建总资产账户（使用来源原名）</option>
          <option v-for="a in accounts" :key="a.id" :value="a.id">
            {{ a.name }} · {{ a.currency }} ·
            {{ a.accounting_mode === "reported" ? "总资产账户" : "持仓账户" }}
          </option>
        </select></label
      >
    </fieldset>
    <p v-if="error" role="alert" class="ledger-error">{{ error }}</p>
    <p v-if="message" role="status" class="ledger-success">{{ message }}</p>
    <template v-if="preview">
      <ImportSummary :metadata="preview.metadata" :summary="preview.summary" />
      <p
        v-for="(warning, i) in preview.warnings"
        :key="i"
        class="ledger-notice"
      >
        {{ warningText(warning) }}
      </p>
      <ImportedRows :rows="preview.rows.slice(page * 30, (page + 1) * 30)" />
      <div class="ledger-actions">
        <button type="button" :disabled="page === 0" @click="page--">
          上一页预览</button
        ><span>第 {{ page + 1 }} 页 · 每页最多 30 行</span
        ><button
          type="button"
          :disabled="(page + 1) * 30 >= preview.rows.length"
          @click="page++"
        >
          下一页预览
        </button>
      </div>
      <p class="ledger-notice">
        确认目标：{{ targetAccount?.name ?? preview.metadata.name }} ·
        {{
          targetAccount?.currency ?? preview.metadata.currency
        }}。导入仅用于空账户初始化；相同规范内容重试不会新增记录，不同文件会被拒绝，不支持增量导入。历史和后续人工、自动记录使用同一账户、同一条曲线。导入的总资产不是现金，也不反推持仓；新建总资产账户没有自动估值来源。
      </p>
      <p v-if="mismatch" role="alert">文件币种与目标账户不一致，不能导入。</p>
      <button
        v-if="!pending"
        type="button"
        data-test="import-confirm"
        :disabled="disabled || mismatch || (!!target && !targetAccount)"
        @click="confirm"
      >
        确认导入
      </button>
    </template>
    <div v-if="pending" class="ledger-pending">
      <p>
        导入结果待确认：文件、预览摘要、目标账户
        ID、创建标志和幂等键已锁定。请勿关闭、刷新或另开页面；只能按原请求重试。
      </p>
      <p>
        固定目标 ID：{{ pending.accountID }} ·
        {{ pending.create ? "新建总资产账户" : "现有账户" }}
      </p>
      <button
        type="button"
        data-test="import-retry"
        :disabled="busy"
        @click="confirm"
      >
        {{ busy ? "正在确认导入…" : "按原请求重试导入" }}
      </button>
    </div>
  </section>
</template>

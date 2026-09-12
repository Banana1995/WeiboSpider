<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import {
  decimal,
  errorText,
  failure,
  LedgerError,
  newID,
  request,
  type Account,
} from "./ledger";
import {
  PendingImport,
  type ImportPreview,
  type ImportResult,
} from "./ledgerImport";
import { validDay } from "./ledgerView";

const props = defineProps<{ accounts: Account[]; disabled: boolean }>();
const emit = defineEmits<{
  locked: [value: boolean];
  imported: [result: ImportResult];
}>();
const file = shallowRef<File>();
const preview = shallowRef<ImportPreview>();
const pending = shallowRef<PendingImport>();
const target = ref("");
const loading = ref(false);
const busy = ref(false);
const error = ref("");
const message = ref("");
const input = ref<HTMLInputElement>();
let controller: AbortController | undefined;
let generation = 0;
const locked = computed(() => loading.value || busy.value || !!pending.value);
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
    if (
      !result ||
      typeof result.digest !== "string" ||
      !/^[a-f0-9]{64}$/.test(result.digest) ||
      !result.metadata ||
      typeof result.metadata.name !== "string" ||
      !["CNY", "HKD", "USD"].includes(result.metadata.currency) ||
      !Array.isArray(result.rows) ||
      result.rows.length > 10000 ||
      !Array.isArray(result.warnings) ||
      result.warnings.some((w) => typeof w !== "string") ||
      !result.summary ||
      result.summary.row_count !== result.rows.length ||
      result.rows.some(
        (r) =>
          !r ||
          !validDay(r.date) ||
          !["asset", "cash_flow"].includes(r.kind) ||
          typeof r.note !== "string" ||
          (r.flow !== null &&
            (typeof r.flow !== "string" || !decimal(r.flow, 2))) ||
          (r.total_assets !== null &&
            (typeof r.total_assets !== "string" ||
              !decimal(r.total_assets, 2))),
      )
    )
      throw new LedgerError("invalid_response");
    if (current === generation) {
      preview.value = result;
    }
  } catch (e) {
    if (current === generation) error.value = failure(e);
  } finally {
    if (current === generation) loading.value = false;
  }
}
async function confirm() {
  if (busy.value || loading.value || (props.disabled && !pending.value)) return;
  if (!pending.value) {
    if (!file.value) return;
    await loadPreview();
    if (mismatch.value) {
      error.value = "文件币种与目标账户不一致，不能导入。";
      return;
    }
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
    return;
  } finally {
    busy.value = false;
  }
  pending.value = undefined;
  preview.value = undefined;
  file.value = undefined;
  if (input.value) input.value.value = "";
  message.value = result.duplicate
    ? `这份文件已导入 ${result.imported_count} 笔记录，未重复添加。`
    : `已导入 ${result.imported_count} 笔记录。`;
  emit("imported", result);
}
onBeforeUnmount(() => {
  generation++;
  controller?.abort();
});
</script>

<template>
  <section class="ledger-panel" data-test="import-account">
    <p>
      支持有知有行导出的 Excel 账本。导入历史资金变动和总资产，不改变当前持仓。
    </p>
    <fieldset :disabled="locked || disabled">
      <label
        >导入到<select v-model="target" data-test="import-target">
          <option value="">新建账户（使用文件中的名称）</option>
          <option v-for="a in accounts" :key="a.id" :value="a.id">
            {{ a.name }} · {{ a.currency }}
          </option>
        </select></label
      >
      <label
        >选择文件（.xlsx，最多 8 MiB）<input
          ref="input"
          type="file"
          accept=".xlsx"
          data-test="import-file"
          @change="changeFile"
      /></label>
    </fieldset>
    <p class="lp-field-hint">
      仅支持新建或空账户，不支持追加文件。相同文件重试不会重复添加记录。
    </p>
    <p v-if="error" role="alert" class="ledger-error">{{ errorText(error) }}</p>
    <p v-if="message" role="status" class="ledger-success">{{ message }}</p>
    <p v-if="pending?.uncertain" class="ledger-pending" role="status">
      导入结果暂未确认，请保留此页。重试会使用同一文件和账户，不会重复添加。
    </p>
    <div class="lp-dialog-footer">
      <button
        v-if="pending && !pending.uncertain && !busy"
        type="button"
        @click="
          pending = undefined;
          error = '';
        "
      >
        更换文件或账户
      </button>
      <button
        type="button"
        class="lp-primary"
        data-test="import-submit"
        :disabled="
          loading ||
          busy ||
          (!pending && (disabled || !file || (!!target && !targetAccount)))
        "
        @click="confirm"
      >
        {{ loading || busy ? "正在导入…" : pending ? "重试导入" : "导入" }}
      </button>
    </div>
  </section>
</template>

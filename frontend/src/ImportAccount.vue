<script setup lang="ts">
import { accountKey } from "./siteAccount";
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
  submitImport,
  importResultText,
  type ImportPreview,
  type ImportReport,
} from "./ledgerImport";
import { validDay } from "./ledgerView";
import { validChannelName } from "./ledgerChannels";

const props = defineProps<{
  accounts: Account[];
  disabled: boolean;
  initialTarget?: string;
}>();
const emit = defineEmits<{
  locked: [value: boolean];
  imported: [report: ImportReport];
}>();
const file = shallowRef<File>();
const preview = shallowRef<ImportPreview>();
const uncertain = ref(false);
const targetChosen = ref(false);
const target = ref(props.initialTarget ?? "");
const loading = ref(false);
const busy = ref(false);
const error = ref("");
const message = ref("");
const dragging = ref(false);
const input = ref<HTMLInputElement>();
let controller: AbortController | undefined;
let generation = 0;
const locked = computed(() => loading.value || busy.value);
const droppable = computed(() => !locked.value && !props.disabled);
watch(locked, (value) => emit("locked", value), { flush: "sync" });
const targetAccount = computed(() =>
  props.accounts.find((a) => a.id === target.value),
);
function selectFile(chosen: File | undefined) {
  if (locked.value || props.disabled) return;
  generation++;
  controller?.abort();
  loading.value = false;
  preview.value = undefined;
  file.value = undefined;
  error.value = "";
  message.value = "";
  uncertain.value = false;
  if (input.value) input.value.value = "";
  if (!chosen) return;
  if (!/\.xlsx$/i.test(chosen.name)) error.value = "仅支持 .xlsx 文件";
  else if (chosen.size > 8 * 1024 * 1024) error.value = "文件超过 8 MiB 限制";
  else if (!chosen.size) error.value = "文件为空";
  else file.value = chosen;
}
function changeFile(event: Event) {
  selectFile((event.target as HTMLInputElement).files?.[0]);
}
function dragOver(event: DragEvent) {
  if (!droppable.value || !event.dataTransfer?.types.includes("Files")) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "copy";
  dragging.value = true;
}
function dragLeave(event: DragEvent) {
  if (!dragging.value) return;
  const next = event.relatedTarget as Node | null;
  if (next && (event.currentTarget as HTMLElement).contains(next)) return;
  dragging.value = false;
}
function drop(event: DragEvent) {
  dragging.value = false;
  if (!droppable.value) return;
  selectFile(event.dataTransfer?.files?.[0]);
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
      result.rows.length === 0 ||
      result.rows.length > 10000 ||
      !Array.isArray(result.warnings) ||
      result.warnings.some((w) => typeof w !== "string") ||
      (result.channels !== undefined &&
        (!result.channels ||
          !Array.isArray(result.channels.names) ||
          result.channels.names.some((name) => !validChannelName(name)) ||
          new Set(result.channels.names).size !==
            result.channels.names.length ||
          !Number.isInteger(result.channels.snapshot_count) ||
          result.channels.snapshot_count < 0 ||
          result.channels.snapshot_count > result.rows.length ||
          !Array.isArray(result.channels.unresolved_rows) ||
          result.channels.unresolved_rows.length > result.rows.length ||
          result.channels.unresolved_rows.some(
            (row) => !Number.isInteger(row) || row < 5 || row > 10004,
          ))) ||
      !result.summary ||
      result.summary.row_count !== result.rows.length ||
      result.rows.some(
        (r, index) =>
          !r ||
          !Number.isInteger(r.source_row) ||
          r.source_row < 5 ||
          r.source_row > 10004 ||
          (index > 0 && r.source_row <= result.rows[index - 1]!.source_row) ||
          !validDay(r.date) ||
          !["asset", "cash_flow"].includes(r.kind) ||
          typeof r.note !== "string" ||
          typeof r.detail !== "string" ||
          typeof r.source_created_at !== "string" ||
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
  if (locked.value || props.disabled || !file.value) return;
  message.value = "";
  uncertain.value = false;
  await loadPreview();
  if (!preview.value || !file.value || props.disabled) return;
  const marker = accountKey(`ledger:new-import:${preview.value.digest}`);
  let saved: string | null = null;
  try {
    saved = localStorage.getItem(marker);
    if (saved && !/^[A-Za-z0-9_-]{1,128}$/.test(saved)) saved = null;
  } catch {
    /* existing accounts work without storage */
  }
  // After a reload the parent's default selection may have changed. Prefer
  // the exact pending ID unless the user explicitly picked another account.
  if (saved && !targetChosen.value && target.value && target.value !== saved) {
    target.value = props.accounts.some((a) => a.id === saved) ? saved : "";
    error.value =
      "这份文件上次用于新建账户，已选回对应目标。请核对目标后再点导入。";
    return;
  }
  const recovering = !!saved && (!target.value || target.value === saved);
  const account = recovering
    ? props.accounts.find((a) => a.id === saved)
    : targetAccount.value;
  if (account && account.currency !== preview.value.metadata.currency) {
    error.value = "文件币种与目标账户不一致，不能导入。";
    return;
  }
  if (!recovering && target.value && !account) return;
  if (
    !account &&
    !saved &&
    !targetChosen.value &&
    props.accounts.some((a) => a.name === preview.value!.metadata.name)
  ) {
    error.value = "已有同名账户，请明确选择原账户，或明确选择新建账户。";
    target.value = "?";
    return;
  }
  const id = account?.id ?? (recovering ? saved! : newID());
  if (!account) {
    try {
      localStorage.setItem(marker, id);
    } catch {
      error.value = "无法保存新账户标识，请先创建并选择账户后导入。";
      return;
    }
  }
  busy.value = true;
  error.value = "";
  let result: ImportReport;
  try {
    result = await submitImport(
      file.value,
      preview.value,
      id,
      !account || recovering,
      account?.name ?? preview.value.metadata.name,
    );
  } catch (e) {
    error.value = failure(e);
    uncertain.value = !(e instanceof LedgerError) || e.uncertain;
    return;
  } finally {
    busy.value = false;
  }
  try {
    if (localStorage.getItem(marker) === id) localStorage.removeItem(marker);
  } catch {
    /* retaining the ID is safe */
  }
  preview.value = undefined;
  file.value = undefined;
  if (input.value) input.value.value = "";
  message.value = importResultText(result);
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
      支持有知有行导出的 Excel
      账本，保留资金变动、总资产和渠道明细，不改变当前持仓。
    </p>
    <fieldset :disabled="locked || disabled">
      <label
        >导入到<select
          v-model="target"
          data-test="import-target"
          @change="targetChosen = true"
        >
          <option v-if="target === '?'" value="?" disabled>
            请选择目标账户
          </option>
          <option value="">新建账户（使用文件中的名称）</option>
          <option v-for="a in accounts" :key="a.id" :value="a.id">
            {{ a.name }} · {{ a.currency }}
          </option>
        </select></label
      >
      <div
        class="lp-dropzone"
        :class="{ 'lp-dropzone-active': dragging }"
        data-test="import-dropzone"
        @dragenter="dragOver"
        @dragover="dragOver"
        @dragleave="dragLeave"
        @drop.prevent="drop"
      >
        <p class="lp-dropzone-hint">拖拽 .xlsx 文件到此处，或</p>
        <label class="lp-dropzone-picker"
          >选择文件（.xlsx，最多 8 MiB）<input
            ref="input"
            type="file"
            accept=".xlsx"
            data-test="import-file"
            @change="changeFile"
        /></label>
        <p v-if="file" class="lp-dropzone-file" data-test="import-file-name">
          {{ file.name }}
        </p>
      </div>
    </fieldset>
    <p class="lp-field-hint">
      再次导入时请选择原账户：只补入新增记录，已导入的会跳过。首次导入请选择新建账户或没有历史记录的账户。
      若要更正旧记录，请在账户记录中编辑；Excel
      里改了金额或备注的行会按新记录处理。
    </p>
    <p v-if="preview?.channels?.unresolved_rows.length" class="lp-field-hint">
      第
      {{ preview.channels.unresolved_rows.join("、") }}
      行的明细无法完整识别为渠道资产，已保留原文，请在记录详情中核对。
    </p>
    <p v-if="error" role="alert" class="ledger-error">{{ errorText(error) }}</p>
    <p v-if="message" role="status" class="ledger-success">{{ message }}</p>
    <p v-if="uncertain" class="ledger-pending" role="status">
      导入结果暂未确认。在原账户重新导入同一文件即可，已经写入的行会跳过。
    </p>
    <div class="lp-dialog-footer">
      <button
        type="button"
        class="lp-primary"
        data-test="import-submit"
        :disabled="
          loading || busy || disabled || !file || (!!target && !targetAccount)
        "
        @click="confirm"
      >
        {{ loading || busy ? "正在导入…" : "导入" }}
      </button>
    </div>
  </section>
</template>

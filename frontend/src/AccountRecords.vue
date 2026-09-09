<script setup lang="ts">
import { computed, reactive, ref, shallowRef, watch } from "vue";
import {
  decimal,
  failure,
  LedgerError,
  newID,
  PendingWrite,
  query,
  request,
  type Currency,
  type Page,
} from "./ledger";
import type {
  AccountEntry,
  AccountRecord,
  AccountRecordOrigin,
  AccountRecordRevision,
  EffectiveSummary,
} from "./accountRecords";
import { accountRecordOriginLabels } from "./accountRecords";
import { useLedgerRead } from "./useLedgerRead";
import LedgerAnalysisBasis from "./LedgerAnalysisBasis.vue";
import LedgerAudit from "./LedgerAudit.vue";
const props = defineProps<{
  accountId: string;
  accountName?: string;
  currency?: string;
  refreshKey: number;
  disabled: boolean;
  holdings?: boolean;
}>();
const emit = defineEmits<{ locked: [value: boolean]; created: [id: string] }>();
const summary = reactive(useLedgerRead<EffectiveSummary>());
const basisRefresh = ref(0);
const records = reactive(useLedgerRead<Page<AccountRecord>>());
const revisions = reactive(useLedgerRead<Page<AccountRecordRevision>>());
const pending = shallowRef<PendingWrite<{ id: string }>>();
const busy = ref(false);
const error = ref("");
const message = ref("");
const name = ref("");
const reportedCurrency = ref<Currency>("CNY");
const openingDate = ref("");
const fresh = (): AccountEntry => ({
  kind: "asset",
  date: "",
  flow: null,
  total_assets: null,
  note: "",
});
const draft = ref(fresh());
const editing = ref<AccountRecord>();
const inspected = ref<AccountRecord>();
const historyID = ref("");
const reason = ref("");
const from = ref("");
const to = ref("");
let applied = { from: "", to: "" };
const historyOpen = ref(false);
const recordPage = ref(0);
const recordCursors = ref([""]);
const revisionPage = ref(0);
const revisionCursors = ref([""]);
let expectedID = "";
let creating = false;
const locked = computed(() => !!pending.value);
const totalRecords = computed(() =>
  summary.data ? summary.data.row_count + summary.data.voided_count : undefined,
);
watch(locked, (value) => emit("locked", value), { flush: "sync" });
const kindLabels = {
  asset: "总资产",
  cash_flow: "资金转入",
  log: "投资日志",
} as const;
function kindLabel(record: AccountEntry) {
  if (record.kind === "cash_flow" && record.flow?.startsWith("-"))
    return "资金转出";
  return kindLabels[record.kind];
}
function originLabel(origin: AccountRecordOrigin) {
  return accountRecordOriginLabels[origin] ?? "其他来源";
}
function amountLabel(record: AccountEntry) {
  if (record.kind === "log") return "—";
  const value = record.kind === "asset" ? record.total_assets : record.flow;
  if (value === null || value === "") return "—";
  const signed =
    record.kind === "cash_flow" && !value.startsWith("-") ? `+${value}` : value;
  return `${signed} ${props.currency ?? "账户本位币"}`;
}
const valid = computed(() => {
  const d = draft.value;
  return (
    !!d.date &&
    (d.kind !== "log" || !!d.note.trim()) &&
    (d.kind !== "asset" ||
      (d.total_assets !== null && d.total_assets !== "")) &&
    (d.kind !== "cash_flow" ||
      (d.flow !== null && d.flow !== "" && decimal(d.flow, 2))) &&
    (d.kind === "log" ||
      d.total_assets === null ||
      d.total_assets === "" ||
      (decimal(d.total_assets, 2) && !d.total_assets.startsWith("-"))) &&
    (!editing.value || !!reason.value.trim())
  );
});
function loadRows(cursor = "", page = 0) {
  const id = props.accountId;
  recordPage.value = page;
  records.clear();
  if (id)
    void records.load(async (signal) => {
      const result = await request<Page<AccountRecord>>(
        `/accounts/${id}/records${query({ ...applied, limit: "30", cursor })}`,
        { signal },
      );
      if (
        !result ||
        !Array.isArray(result.items) ||
        result.items.some((record) => record.account_id !== id)
      )
        throw new LedgerError("invalid_response");
      return result;
    });
}
function loadHistory(cursor = "", page = 0) {
  const id = props.accountId,
    record = historyID.value;
  revisionPage.value = page;
  revisions.clear();
  if (id && record)
    void revisions.load(async (signal) => {
      const result = await request<Page<AccountRecordRevision>>(
        `/accounts/${id}/records/${record}/revisions${query({ limit: "30", cursor })}`,
        { signal },
      );
      if (
        !result ||
        !Array.isArray(result.items) ||
        result.items.some(
          (revision) =>
            revision.record.account_id !== id || revision.record.id !== record,
        )
      )
        throw new LedgerError("invalid_response");
      return result;
    });
}
function load() {
  basisRefresh.value++;
  const id = props.accountId;
  summary.clear();
  if (id)
    void summary.load(async (signal) => {
      const result = await request<EffectiveSummary>(
        `/accounts/${id}/effective-summary`,
        { signal },
      );
      if (
        !result ||
        typeof result.row_count !== "number" ||
        typeof result.total_in !== "string"
      )
        throw new LedgerError("invalid_response");
      return result;
    });
  recordPage.value = 0;
  recordCursors.value = [""];
  if (historyOpen.value) loadRows();
  else records.clear();
  loadHistory();
}
function reset() {
  draft.value = fresh();
  editing.value = undefined;
  inspected.value = undefined;
  reason.value = "";
  revisionPage.value = 0;
  revisionCursors.value = [""];
}
function select(record: AccountRecord) {
  if (locked.value || props.disabled) return;
  error.value = "";
  inspected.value = record;
  historyID.value = record.id;
  revisionPage.value = 0;
  revisionCursors.value = [""];
  if (record.operation_id) {
    editing.value = undefined;
    draft.value = fresh();
    reason.value = "";
    error.value =
      "此记录由持仓操作生成，请在“操作流水”中查看、更正或作废；转账两腿会一起更新。";
    loadHistory();
    return;
  }
  editing.value = record;
  draft.value = {
    kind: record.kind,
    date: record.date,
    flow: record.flow,
    total_assets: record.total_assets,
    note: record.note,
  };
  reason.value = "";
  loadHistory();
}
async function send(
  write?: PendingWrite<{ id: string }>,
  id = "",
  create = false,
) {
  if (busy.value || (write && (locked.value || props.disabled))) return;
  if (write) {
    pending.value = write;
    expectedID = id;
    creating = create;
  }
  if (!pending.value) return;
  busy.value = true;
  error.value = "";
  message.value = "";
  try {
    const result = await pending.value.run();
    if (!result || result.id !== expectedID) {
      pending.value.uncertain = true;
      throw new LedgerError("invalid_response");
    }
  } catch (e) {
    error.value = failure(e);
    if (!pending.value.uncertain) {
      pending.value = undefined;
      load();
    }
    return;
  } finally {
    busy.value = false;
  }
  pending.value = undefined;
  message.value =
    "写入已确认成功。当前数据独立刷新，读取失败不影响成功，请勿重复录入。";
  reset();
  if (creating) {
    name.value = "";
    openingDate.value = "";
    emit("created", expectedID);
  }
  load();
}
function createAccount() {
  if (!name.value.trim() || !openingDate.value) return;
  const id = newID();
  void send(
    new PendingWrite("/reported-accounts", "POST", {
      id,
      name: name.value,
      currency: reportedCurrency.value,
      opening_date: openingDate.value,
    }),
    id,
    true,
  );
}
function save() {
  if (!valid.value || !props.accountId || editing.value?.voided) return;
  const d = draft.value;
  const entry: AccountEntry = {
    ...d,
    flow: d.kind === "cash_flow" ? d.flow : null,
    total_assets:
      d.kind === "log" || d.total_assets === "" ? null : d.total_assets,
  };
  const id = editing.value?.id ?? `manual-${newID()}`;
  const path = `/accounts/${props.accountId}/records`;
  void send(
    new PendingWrite(
      editing.value ? `${path}/${id}` : path,
      editing.value ? "PUT" : "POST",
      {
        id,
        entry,
        reason: reason.value,
        expected_version: editing.value?.version,
      },
    ),
    id,
  );
}
function voidRecord() {
  if (!editing.value || editing.value.voided || !reason.value.trim()) return;
  void send(
    new PendingWrite(
      `/accounts/${props.accountId}/records/${editing.value.id}`,
      "DELETE",
      { expected_version: editing.value.version, reason: reason.value },
    ),
    editing.value.id,
  );
}
function filter() {
  applied = { from: from.value, to: to.value };
  recordPage.value = 0;
  recordCursors.value = [""];
  loadRows();
}
function toggleHistory(event: Event) {
  historyOpen.value = (event.currentTarget as HTMLDetailsElement).open;
  if (historyOpen.value && !records.data && !records.loading) loadRows();
}
function previousRecords() {
  if (recordPage.value > 0)
    loadRows(recordCursors.value[recordPage.value - 1]!, recordPage.value - 1);
}
function nextRecords() {
  const cursor = records.data?.next_cursor;
  if (!cursor || records.loading || records.error) return;
  recordCursors.value.splice(recordPage.value + 1, Infinity, cursor);
  loadRows(cursor, recordPage.value + 1);
}
function previousRevisions() {
  if (revisionPage.value > 0)
    loadHistory(
      revisionCursors.value[revisionPage.value - 1]!,
      revisionPage.value - 1,
    );
}
function nextRevisions() {
  const cursor = revisions.data?.next_cursor;
  if (!cursor || revisions.loading || revisions.error) return;
  revisionCursors.value.splice(revisionPage.value + 1, Infinity, cursor);
  loadHistory(cursor, revisionPage.value + 1);
}
watch(
  () => props.accountId,
  () => {
    reset();
    historyID.value = "";
    from.value = "";
    to.value = "";
    applied = { from: "", to: "" };
    historyOpen.value = false;
    recordPage.value = 0;
    recordCursors.value = [""];
    revisionPage.value = 0;
    revisionCursors.value = [""];
    load();
  },
  { immediate: true },
);
watch(() => props.refreshKey, load);
</script>

<template>
  <section class="ledger-panel" data-test="account-records">
    <h2>总资产账户 · 持续记账</h2>
    <p>
      用总资产、资金转入转出和投资日志持续记录账户。Excel
      导入、手工记录和估值会合并到同一条账户历史中。
    </p>
    <details>
      <summary>不使用 Excel，新建总资产账户</summary>
      <form data-test="reported-create" @submit.prevent="createAccount">
        <fieldset class="ledger-grid" :disabled="locked || disabled">
          <label
            >账户名称<input
              v-model="name"
              name="reported_name"
              required
              maxlength="512"
          /></label>
          <label
            >币种<select v-model="reportedCurrency" name="reported_currency">
              <option>CNY</option>
              <option>HKD</option>
              <option>USD</option>
            </select></label
          >
          <label
            >账户起始日期<input
              v-model="openingDate"
              name="reported_opening_date"
              type="date"
              required
          /></label>
          <p>起始日期仅为账户元数据，不生成资产或现金期初记录。</p>
          <button type="submit">创建总资产账户</button>
        </fieldset>
      </form>
    </details>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="message" role="status">{{ message }}</p>
    <div v-if="pending" role="status">
      <p>正在确认写入或结果未知。请保留此页，使用原请求重试，不要重新录入。</p>
      <button
        type="button"
        :disabled="busy"
        data-test="record-retry"
        @click="send()"
      >
        使用原请求重试确认
      </button>
    </div>
    <template v-if="accountId">
      <p>
        当前账户：{{ accountName ?? accountId }} · 金额单位：{{
          props.currency ?? "账户本位币"
        }}
      </p>
      <h3>当前有效统计（不含作废，全部记录日期）</h3>
      <p>
        不代表实时估值；未来日期原样保留。列表按日期、稳定序号倒序，同日取最后一条资产。
        导入按来源行顺序、手工按创建顺序分配序号，更正不改变序号。
      </p>
      <p v-if="summary.loading">正在读取有效统计…</p>
      <p v-if="summary.error" role="alert">{{ summary.error }}</p>
      <dl
        v-if="summary.data"
        class="ledger-stats"
        data-test="effective-summary"
      >
        <div>
          <dt>有效 / 作废</dt>
          <dd>
            {{ summary.data.row_count }} / {{ summary.data.voided_count }}
          </dd>
        </div>
        <div>
          <dt>资产 / 资金流 / 日志</dt>
          <dd>
            {{ summary.data.asset_count }} / {{ summary.data.flow_count }} /
            {{ summary.data.log_count }}
          </dd>
        </div>
        <div>
          <dt>日期范围</dt>
          <dd>
            {{ summary.data.from ?? "无" }} 至 {{ summary.data.to ?? "无" }}
          </dd>
        </div>
        <div>
          <dt>累计转入 / 转出</dt>
          <dd>{{ summary.data.total_in }} / {{ summary.data.total_out }}</dd>
        </div>
        <div>
          <dt>最新资产记录日期</dt>
          <dd>{{ summary.data.latest_asset_date ?? "无" }}</dd>
        </div>
        <div>
          <dt>该日资产</dt>
          <dd>
            {{
              summary.data.latest_assets ??
              (summary.data.latest_asset_count > 1
                ? "同日多条，未确定先后，请查看记录"
                : "未记录")
            }}
          </dd>
        </div>
      </dl>
      <form data-test="account-entry" @submit.prevent="save">
        <fieldset :disabled="locked || disabled" class="ledger-grid">
          <legend>
            {{
              editing
                ? `${kindLabel(editing)} · ${editing.voided ? "已作废" : "更正已选记录"}`
                : "新增账户级记录"
            }}
          </legend>
          <label
            >类型<select
              v-model="draft.kind"
              name="entry_kind"
              :disabled="!!editing?.quote_audit_id"
            >
              <option value="asset">记总资产</option>
              <option value="cash_flow">转入 / 转出</option>
              <option value="log">投资日志</option>
            </select></label
          >
          <label
            >记录日期<input
              v-model="draft.date"
              name="entry_date"
              type="date"
              required
          /></label>
          <label v-if="draft.kind === 'cash_flow'"
            >资金流（转入正数，转出负数，零保留）<input
              v-model="draft.flow"
              name="entry_flow"
              inputmode="decimal"
              required
          /></label>
          <label v-if="draft.kind !== 'log'"
            >总资产（资金流可留空，零不等于缺失）<input
              v-model="draft.total_assets"
              name="entry_assets"
              inputmode="decimal"
              :required="draft.kind === 'asset'"
          /></label>
          <details class="entry-help">
            <summary>填写说明</summary>
            <p>
              资金流正数为转入、负数为转出；同行总资产填写资金进出后的金额。人工记录不会生成持仓交易或改变持仓现金，持仓操作生成的记录需从原操作更正。
            </p>
            <p>
              资金流未填总资产时，分析会沿用此前最近的有效资产原值，但不会把沿用值保存成新的资产观察；此前没有资产记录时，相关收益无法计算。
            </p>
          </details>
          <label
            >投资日志<textarea
              v-model="draft.note"
              name="entry_note"
              :required="draft.kind === 'log'"
            />
          </label>
          <label v-if="editing"
            >更正 / 作废原因<input
              v-model="reason"
              name="entry_reason"
              required
          /></label>
          <button type="submit" :disabled="!valid || editing?.voided">
            {{ editing ? "保存更正" : "新增记录" }}
          </button>
          <button
            v-if="editing && !editing.voided"
            type="button"
            :disabled="!reason.trim()"
            @click="voidRecord"
          >
            作废此记录
          </button>
          <button v-if="editing" type="button" @click="reset">返回新增</button>
        </fieldset>
      </form>
      <details v-if="inspected" data-test="record-technical">
        <summary>查看所选记录的来源与版本</summary>
        <dl class="ledger-record">
          <dt>记录编号</dt>
          <dd>{{ inspected.id }}</dd>
          <dt>当前版本</dt>
          <dd>{{ inspected.version }}</dd>
          <dt>同日顺序</dt>
          <dd>{{ inspected.sequence ?? "历史回执未附顺序" }}</dd>
          <dt>记录来源</dt>
          <dd>{{ originLabel(inspected.origin) }}</dd>
          <dt>记录日期</dt>
          <dd>{{ inspected.date }}</dd>
          <dt>同行总资产</dt>
          <dd>
            {{ inspected.total_assets ?? "—" }}
            {{
              inspected.total_assets === null
                ? ""
                : (props.currency ?? "账户本位币")
            }}
          </dd>
          <template v-if="inspected.carried_from">
            <dt>沿用来源</dt>
            <dd>
              {{ inspected.carried_from.date }} /
              {{ inspected.carried_from.id }} / 版本
              {{ inspected.carried_from.version }}
            </dd>
          </template>
          <template v-if="inspected.operation_id">
            <dt>关联持仓操作</dt>
            <dd>{{ inspected.operation_id }}</dd>
          </template>
          <template v-if="inspected.quote_audit_id">
            <dt>关联估值审计</dt>
            <dd>{{ inspected.quote_audit_id }}</dd>
          </template>
        </dl>
        <p v-if="inspected.manual_assertion">
          此金额经过人工确认，原报价证据仍保留。
        </p>
        <details v-if="inspected.original">
          <summary>查看原始 Excel 导入行</summary>
          <pre>{{ JSON.stringify(inspected.original, null, 2) }}</pre>
        </details>
      </details>
      <LedgerAnalysisBasis
        :account-id="accountId"
        :holdings="holdings"
        :refresh-key="basisRefresh"
      />
      <details
        :key="accountId"
        class="records-history"
        data-test="records-history"
        @toggle="toggleHistory"
      >
        <summary>
          查看全部历史记录<span v-if="totalRecords !== undefined"
            >（{{ totalRecords }} 条）</span
          >
        </summary>
        <form
          class="ledger-grid"
          data-test="record-filters"
          @submit.prevent="filter"
        >
          <label
            >起始日期<input v-model="from" name="record_from" type="date"
          /></label>
          <label
            >结束日期<input v-model="to" name="record_to" type="date"
          /></label>
          <button :disabled="!!from && !!to && from > to">筛选历史记录</button>
          <button type="button" @click="load">刷新当前统计和记录</button>
        </form>
        <p v-if="records.loading">正在读取记录…</p>
        <p v-if="records.error" role="alert">{{ records.error }}</p>
        <div class="ledger-table">
          <table v-if="records.data" data-test="effective-records">
            <caption>
              按记录日期倒序，金额为账户本位币；更正与作废入口在每行末尾
            </caption>
            <thead>
              <tr>
                <th>日期</th>
                <th>记录类型</th>
                <th>金额</th>
                <th>备注</th>
                <th>状态 / 来源</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="record in records.data.items" :key="record.id">
                <td>{{ record.date }}</td>
                <td>{{ kindLabel(record) }}</td>
                <td class="record-amount">{{ amountLabel(record) }}</td>
                <td class="ledger-note">{{ record.note || "—" }}</td>
                <td class="record-badges">
                  <span class="record-badge" :data-voided="record.voided">
                    {{ record.voided ? "已作废" : "有效" }}
                  </span>
                  <span class="record-badge record-source">
                    {{ originLabel(record.origin) }}
                  </span>
                  <small v-if="record.manual_assertion">人工确认</small>
                </td>
                <td>
                  <button
                    type="button"
                    :disabled="
                      locked || disabled || records.loading || !!records.error
                    "
                    @click="select(record)"
                  >
                    查看 / 更正 / 修订
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="records.data?.items.length === 0">此范围暂无记录。</p>
        <div
          v-if="recordPage > 0 || records.data?.next_cursor"
          class="ledger-actions"
          data-test="record-pagination"
        >
          <button
            type="button"
            :disabled="recordPage === 0 || records.loading"
            @click="previousRecords"
          >
            上一页</button
          ><span>第 {{ recordPage + 1 }} 页</span
          ><button
            type="button"
            :disabled="
              !records.data?.next_cursor || records.loading || !!records.error
            "
            @click="nextRecords"
          >
            下一页
          </button>
        </div>
        <details v-if="historyID" :key="historyID" data-test="record-revisions">
          <summary>查看更正与作废历史</summary>
          <p>原始 Excel 导入快照不会被更正覆盖；以下各版本均保留。</p>
          <p v-if="revisions.loading">正在读取修订…</p>
          <p v-if="revisions.error" role="alert">{{ revisions.error }}</p>
          <article
            v-for="revision in revisions.data?.items"
            :key="revision.record.version"
          >
            <h4>
              版本 {{ revision.record.version }} ·
              {{ revision.record.voided ? "已作废" : "有效" }}
            </h4>
            <p>
              {{ revision.record.date }} · {{ kindLabel(revision.record) }} ·
              {{ amountLabel(revision.record) }}
            </p>
            <p class="ledger-note">{{ revision.record.note || "—" }}</p>
            <p>
              原因：{{ revision.reason || "原始记录" }} ·
              {{ revision.record.updated_at }}
            </p>
            <details v-if="revision.record.original">
              <summary>查看原始 Excel 导入行</summary>
              <pre>{{ JSON.stringify(revision.record.original, null, 2) }}</pre>
            </details>
          </article>
          <div
            v-if="revisionPage > 0 || revisions.data?.next_cursor"
            class="ledger-actions"
            data-test="revision-pagination"
          >
            <button
              type="button"
              :disabled="revisionPage === 0 || revisions.loading"
              @click="previousRevisions"
            >
              上一页</button
            ><span>第 {{ revisionPage + 1 }} 页</span
            ><button
              type="button"
              :disabled="
                !revisions.data?.next_cursor ||
                revisions.loading ||
                !!revisions.error
              "
              @click="nextRevisions"
            >
              下一页
            </button>
          </div>
        </details>
      </details>
      <LedgerAudit :account-id="accountId" />
    </template>
  </section>
</template>

<style scoped>
.ledger-stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 16px;
  padding: 16px 0;
}
.ledger-stats dt {
  font-size: 12px;
  color: #64786a;
}
.ledger-stats dd {
  margin: 8px 0 0;
  overflow-wrap: anywhere;
}
.ledger-note,
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
td.ledger-note {
  min-width: 160px;
  max-width: 320px;
}
.entry-help,
.records-history,
[data-test="record-technical"] {
  margin: 12px 0;
}
.records-history {
  border-top: 1px solid #dde3db;
  padding-top: 8px;
}
.records-history > summary,
[data-test="record-technical"] > summary {
  font-weight: 600;
}
.record-badges {
  white-space: normal;
}
.record-badge {
  display: inline-block;
  border: 1px solid #bac9be;
  border-radius: 999px;
  padding: 3px 8px;
  margin: 2px 4px 2px 0;
  background: #eaf3eb;
  white-space: nowrap;
}
.record-badge[data-voided="true"] {
  border-color: #d7b4ab;
  background: #fff0ed;
}
.record-source {
  background: #f3f6f1;
}
.record-amount {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
article {
  border-top: 1px solid #dde3db;
  padding: 12px 0;
}
</style>

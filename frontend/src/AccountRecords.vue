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
  AccountRecordRevision,
  EffectiveSummary,
} from "./accountRecords";
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
const currency = ref<Currency>("CNY");
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
const historyID = ref("");
const reason = ref("");
const from = ref("");
const to = ref("");
let applied = { from: "", to: "" };
let expectedID = "";
let creating = false;
const locked = computed(() => !!pending.value);
watch(locked, (value) => emit("locked", value), { flush: "sync" });
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
function loadRows(cursor = "") {
  const id = props.accountId;
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
function loadHistory(cursor = "") {
  const id = props.accountId,
    record = historyID.value;
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
  loadRows();
  loadHistory();
}
function reset() {
  draft.value = fresh();
  editing.value = undefined;
  reason.value = "";
}
function select(record: AccountRecord) {
  if (locked.value || props.disabled) return;
  if (record.operation_id) {
    error.value = `此记录由持仓操作 ${record.operation_id} 管理，请在持仓操作列表修正或作废；转账两腿会一起更新。`;
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
  historyID.value = record.id;
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
      currency: currency.value,
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
  loadRows();
}
watch(
  () => props.accountId,
  () => {
    reset();
    historyID.value = "";
    from.value = "";
    to.value = "";
    applied = { from: "", to: "" };
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
      导入、人工记录与自动估值保存在同一账户记录表。人工记录不生成持仓买卖或改变持仓现金；转出无需持仓现金余额。持仓操作生成的记录须从原操作修正。
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
            >币种<select v-model="currency" name="reported_currency">
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
          currency ?? "账户本位币"
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
      <LedgerAnalysisBasis
        :account-id="accountId"
        :holdings="holdings"
        :refresh-key="basisRefresh"
      />
      <LedgerAudit :account-id="accountId" />
      <form data-test="account-entry" @submit.prevent="save">
        <fieldset :disabled="locked || disabled" class="ledger-grid">
          <legend>
            {{
              editing
                ? `记录 ${editing.id} · 版本 ${editing.version}${editing.voided ? " · 已作废" : ""}`
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
          <p>
            同行总资产为资金进出之后的金额。资金流未填资产时，分析沿用该日期和序号之前最近的有效资产原值，不加本笔资金流；
            无此前资产则不可用。沿用值不保存为新资产观察。
          </p>
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
        <button :disabled="!!from && !!to && from > to">
          筛选有效记录列表
        </button>
        <button type="button" @click="load">刷新当前统计和记录</button>
      </form>
      <p v-if="records.loading">正在读取记录…</p>
      <p v-if="records.error" role="alert">{{ records.error }}</p>
      <div class="ledger-table">
        <table v-if="records.data" data-test="effective-records">
          <thead>
            <tr>
              <th>日期 / 来源</th>
              <th>类型 / 状态</th>
              <th>资金流</th>
              <th>总资产</th>
              <th>日志</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="record in records.data.items" :key="record.id">
              <td>
                {{ record.date }} /
                {{
                  {
                    import: "导入初始化",
                    manual: "手工",
                    currentrefresh: "当前估值",
                    weekly: "每周估值",
                    weekly_carry: "每周沿用",
                    operation: "持仓操作",
                  }[record.origin]
                }}
                {{
                  record.manual_assertion ? "（人工修正，原报价证据保留）" : ""
                }}
                <span v-if="record.carried_from">
                  （沿用 {{ record.carried_from.date }} /
                  {{ record.carried_from.id }} v{{
                    record.carried_from.version
                  }}）
                </span>
                · 序号 {{ record.sequence ?? "历史回执未附序号" }}
              </td>
              <td>
                {{ record.kind }} / {{ record.voided ? "已作废" : "有效" }} ·
                v{{ record.version }}
              </td>
              <td>{{ record.flow ?? "未记录" }}</td>
              <td>{{ record.total_assets ?? "未记录" }}</td>
              <td class="ledger-note">{{ record.note }}</td>
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
      <div class="ledger-actions">
        <button type="button" @click="loadRows()">记录首页</button
        ><button
          v-if="records.data?.next_cursor"
          type="button"
          :disabled="records.loading || !!records.error"
          @click="loadRows(records.data.next_cursor)"
        >
          下一页记录
        </button>
      </div>
      <template v-if="historyID">
        <h3>修订历史 · {{ historyID }}</h3>
        <p>原始导入快照不会被更正覆盖；以下各版本均保留。</p>
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
            {{ revision.record.date }} · {{ revision.record.kind }} · 资金流
            {{ revision.record.flow ?? "未记录" }} · 总资产
            {{ revision.record.total_assets ?? "未记录" }}
          </p>
          <p class="ledger-note">{{ revision.record.note }}</p>
          <p>
            原因：{{ revision.reason || "原始记录" }} ·
            {{ revision.record.updated_at }}
          </p>
          <details v-if="revision.record.original">
            <summary>不可变来源行</summary>
            <pre>{{ JSON.stringify(revision.record.original, null, 2) }}</pre>
          </details>
        </article>
        <button type="button" @click="loadHistory()">修订首页</button
        ><button
          v-if="revisions.data?.next_cursor"
          type="button"
          :disabled="revisions.loading || !!revisions.error"
          @click="loadHistory(revisions.data.next_cursor)"
        >
          下一页修订
        </button>
      </template>
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
article {
  border-top: 1px solid #dde3db;
  padding: 12px 0;
}
</style>

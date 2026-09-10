<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import OperationForm from "./LedgerOperationForm.vue";
import { kinds, LedgerError, PendingWrite, query, request, type Account, type Instrument, type LedgerRecord, type Mutation, type Page, type Position, type Revision } from "./ledger";
import { money, validDay, versionString } from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
const props = defineProps<{ account: Account; accounts: Account[]; instruments: Instrument[]; positions: Position[]; positionsFresh: boolean; refreshKey: number; operationId?: string }>();
const { locked, error, send } = useLedgerWorkspace();
const list = reactive(useLedgerRead<Page<LedgerRecord>>());
const detail = reactive(useLedgerRead<LedgerRecord>());
const revisions = reactive(useLedgerRead<Page<Revision>>());
const cursorHistory = ref([""]);
const page = ref(0);
const from = ref("");
const to = ref("");
const status = ref("active");
const filterError = ref("");
const modal = ref<"" | "new" | "detail" | "edit" | "void">("");
const activeID = ref("");
const reason = ref("");
const dirty = ref(false);
const conflict = computed(() => error.value.includes("[version_conflict]"));
const accountList = computed(() => props.accounts.filter(a => a.accounting_mode === "holdings"));
const instrumentName = (id?: string) => props.instruments.find(i => i.id === id)?.name ?? "未登记证券";
let applied = { from: "", to: "", status: "active" };
const valid = (r: LedgerRecord, id?: string) => !!(r && r.operation && (!id || r.operation.id === id) &&
  (r.operation.account_id === props.account.id || r.operation.to_account_id === props.account.id) && versionString(r.version) &&
  validDay(r.operation.date) && Object.hasOwn(kinds, r.operation.kind) && typeof r.note === "string");
function load(cursor = "", index = 0) {
  list.clear(); page.value = index;
  const account_id = props.account.id;
  void list.load(async signal => {
    const result = await request<Page<LedgerRecord>>(`/operations${query({ ...applied, account_id, limit: "10", cursor })}`, { signal });
    if (!result || !Array.isArray(result.items) || result.items.some(r => !valid(r)) || (result.next_cursor !== undefined && typeof result.next_cursor !== "string")) throw new LedgerError("invalid_response");
    return result;
  });
}
function filter() {
  if (locked.value) return;
  if ((from.value && !validDay(from.value)) || (to.value && !validDay(to.value)) || (from.value && to.value && from.value > to.value)) { filterError.value = "请选择有效日期。"; return; }
  filterError.value = ""; applied = { from: from.value, to: to.value, status: status.value }; cursorHistory.value = [""]; load();
}
function next() { const cursor = list.data?.next_cursor; if (!cursor || locked.value) return; cursorHistory.value.splice(page.value + 1, Infinity, cursor); load(cursor, page.value + 1); }
async function inspect(id: string) {
  if (locked.value) return;
  activeID.value = id; modal.value = "detail"; dirty.value = false; error.value = "";
  detail.clear(); revisions.clear();
  await detail.load(async signal => {
    const r = await request<LedgerRecord>(`/operations/${encodeURIComponent(id)}`, { signal });
    if (!valid(r, id)) throw new LedgerError("invalid_response");
    return r;
  });
}
function history(cursor = "") {
  revisions.clear(); const id = activeID.value;
  void revisions.load(async signal => {
    const r = await request<Page<Revision>>(`/operations/${encodeURIComponent(id)}/revisions${query({ limit: "30", cursor })}`, { signal });
    if (!r || !Array.isArray(r.items) || r.items.some(v => !v || !valid(v.record, id) || typeof v.reason !== "string")) throw new LedgerError("invalid_response");
    return r;
  });
}
function save(payload: Mutation) {
  if (locked.value || conflict.value) return;
  const edit = !!payload.expected_version;
  send(new PendingWrite<LedgerRecord>(edit ? `/operations/${encodeURIComponent(payload.operation.id)}` : "/operations", edit ? "PUT" : "POST", payload),
    `${edit ? '修改' : '记录'}${kinds[payload.operation.kind]}`, r => !!r && r.operation?.id === payload.operation.id && versionString(r.version), () => { modal.value = ""; dirty.value = false; });
}
function voidOperation() {
  const r = detail.data;
  if (!r || !reason.value.trim() || locked.value || conflict.value) return;
  send(new PendingWrite<LedgerRecord>(`/operations/${encodeURIComponent(r.operation.id)}`, "DELETE", { expected_version: r.version, reason: reason.value }),
    "作废持仓交易", receipt => !!receipt && receipt.operation?.id === r.operation.id && receipt.operation.voided, () => { modal.value = ""; dirty.value = false; });
}
function reloadDetail() {
  if (locked.value || (dirty.value && !window.confirm("重新读取会放弃当前交易草稿，是否继续？"))) return;
  void inspect(activeID.value);
}
watch(() => props.account.id, () => { modal.value = ""; applied = { from: "", to: "", status: "active" }; from.value = ""; to.value = ""; status.value = "active"; cursorHistory.value = [""]; load(); }, { immediate: true });
watch(() => props.refreshKey, () => { cursorHistory.value = [""]; load(); });
watch(() => props.operationId, id => { if (id) void inspect(id); }, { immediate: true });
</script>

<template>
  <div>
    <div class="lp-section-title"><h2>持仓交易</h2><button class="lp-primary" :disabled="locked" @click="modal = 'new'; dirty = false; error = ''">录入交易</button></div>
    <p class="lp-muted">交易会重放现金与持仓。账户记录中的关联资金记录随整笔交易一起更新。</p>
    <form class="ledger-grid" @submit.prevent="filter"><label>交易开始日期<input v-model="from" type="date" :disabled="locked" /></label>
      <label>交易结束日期<input v-model="to" type="date" :disabled="locked" /></label><label>状态<select v-model="status" :disabled="locked"><option value="active">有效</option><option value="all">含已作废</option><option value="voided">已作废</option></select></label><button :disabled="locked">筛选交易</button></form>
    <p v-if="filterError" class="lp-error" role="alert">{{ filterError }}</p><p v-if="list.loading" role="status">正在读取交易…</p><p v-if="list.error" class="lp-error" role="alert">{{ list.error }}</p>
    <ul class="lp-business-list"><li v-for="r in list.data?.items" :key="r.operation.id"><div><strong>{{ r.operation.date }} · {{ kinds[r.operation.kind] }}</strong>
      <small>{{ r.operation.voided ? '已作废' : '' }} {{ r.operation.instrument_id ? instrumentName(r.operation.instrument_id) : '' }}</small>
      <p v-if="r.operation.amount">现金金额 {{ money(r.operation.amount) }}</p><p v-if="r.operation.quantity">数量 {{ money(r.operation.quantity) }} · 成交价 {{ money(r.operation.price) }}</p><p>{{ r.note || '无备注' }}</p></div>
      <button :disabled="locked" @click="inspect(r.operation.id)">详情</button></li></ul>
    <p v-if="list.data?.items.length === 0" class="lp-empty">没有符合条件的持仓交易。</p>
    <div class="lp-pagination"><span>第 {{ page + 1 }} 页</span><button :disabled="locked || list.loading || page === 0" @click="load(cursorHistory[page - 1]!, page - 1)">上一页</button><button :disabled="locked || list.loading || !!list.error || !list.data?.next_cursor" @click="next">下一页</button></div>
    <LedgerDialog v-if="modal" :title="modal === 'new' ? '录入持仓交易' : modal === 'edit' ? '修改持仓交易' : modal === 'void' ? '作废持仓交易' : '持仓交易详情'" :caption="account.name" :dirty="dirty || !!reason" @close="modal = ''; reason = ''">
      <div class="lp-dialog-body">
        <div v-if="modal === 'new' || modal === 'edit'" @input="dirty = true" @change="dirty = true">
          <OperationForm :key="modal + (detail.data?.version ?? '')" :record="modal === 'edit' ? detail.data : undefined" :initial-account="account.id" :accounts="accountList" :instruments="instruments" :positions="positions" :positions-account="account.id" :positions-fresh="positionsFresh" :locked="locked || conflict" @save="save" @cancel="reloadDetail" />
          <button v-if="modal === 'edit'" class="lp-text-button" :disabled="locked" @click="reloadDetail">重新读取最新交易（放弃草稿）</button>
        </div>
        <form v-else-if="modal === 'void'" class="lp-form" @submit.prevent="voidOperation"><p>将作废整笔交易，转账包含双方，现金与持仓会重新计算。确认继续？</p><label>作废原因<input v-model="reason" required maxlength="160" :disabled="locked" /></label><button v-if="conflict" type="button" @click="reloadDetail">重新读取交易</button><button class="lp-danger-button" :disabled="locked || conflict || !reason.trim()">确认作废整笔交易</button></form>
        <template v-else><p v-if="detail.loading" role="status">正在读取交易详情…</p><p v-if="detail.error" class="lp-error" role="alert">{{ detail.error }}</p>
          <template v-if="detail.data"><dl class="lp-info"><div><dt>日期</dt><dd>{{ detail.data.operation.date }}</dd></div><div><dt>类型</dt><dd>{{ kinds[detail.data.operation.kind] }}{{ detail.data.operation.voided ? ' · 已作废' : '' }}</dd></div>
            <div><dt>账户</dt><dd>{{ accounts.find(a => a.id === detail.data?.operation.account_id)?.name }}<template v-if="detail.data.operation.to_account_id"> 转至 {{ accounts.find(a => a.id === detail.data?.operation.to_account_id)?.name }}</template></dd></div>
            <div v-if="detail.data.operation.instrument_id"><dt>证券</dt><dd>{{ instrumentName(detail.data.operation.instrument_id) }}</dd></div>
            <div><dt>现金金额</dt><dd>{{ money(detail.data.operation.amount) }}</dd></div><div><dt>数量 / 成交价</dt><dd>{{ money(detail.data.operation.quantity) }} / {{ money(detail.data.operation.price) }}</dd></div>
            <div><dt>费用</dt><dd>{{ money(detail.data.operation.fee) }}</dd></div><div><dt>备注</dt><dd class="lp-note-text">{{ detail.data.note || '—' }}</dd></div>
            <div v-if="detail.data.operation.fx"><dt>成交汇率</dt><dd>{{ detail.data.operation.fx.rate }} · {{ detail.data.operation.fx.date }}<small>{{ detail.data.operation.fx.source }}</small></dd></div>
          </dl><div v-if="!detail.data.operation.voided" class="lp-actions"><button :disabled="locked" @click="modal = 'edit'; dirty = false">修改整笔交易</button><button class="lp-danger-button" :disabled="locked" @click="reason = ''; modal = 'void'">作废交易</button></div></template>
          <button class="lp-text-button" :disabled="locked" @click="reloadDetail">重新读取详情</button>
          <details v-if="detail.data" class="lp-history-section" @toggle="($event.currentTarget as HTMLDetailsElement).open && !revisions.data && history()"><summary>修改历史</summary><p v-if="revisions.error" role="alert">{{ revisions.error }}</p>
            <ol class="lp-history"><li v-for="r in revisions.data?.items" :key="r.record.version"><strong>第 {{ r.record.version }} 版 · {{ r.record.operation.voided ? '已作废' : '有效' }}</strong><p>{{ r.reason }}</p><p>{{ r.record.operation.date }} · {{ kinds[r.record.operation.kind] }} · 金额 {{ money(r.record.operation.amount) }} · 数量 {{ money(r.record.operation.quantity) }} · 成交价 {{ money(r.record.operation.price) }}</p><p>{{ r.record.note || '无备注' }}</p></li></ol>
            <div class="lp-actions"><button :disabled="locked || revisions.loading" @click="history()">历史首页</button><button v-if="revisions.data?.next_cursor" :disabled="locked || revisions.loading" @click="history(revisions.data.next_cursor)">下一页历史</button></div></details>
        </template>
      </div>
    </LedgerDialog>
  </div>
</template>

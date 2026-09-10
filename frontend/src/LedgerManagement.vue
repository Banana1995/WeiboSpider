<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import CurrentHoldings from "./CurrentHoldings.vue";
import ImportAccount from "./ImportAccount.vue";
import LedgerTransactions from "./LedgerTransactions.vue";
import LedgerAutoUpdates from "./LedgerAutoUpdates.vue";
import { all, decimal, LedgerError, newID, PendingWrite, request, type Account, type AccountDetail, type Currency, type Instrument, type Position, type Valuation } from "./ledger";
import { money, validDay } from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import type { ImportResult } from "./ledgerImport";
const props = defineProps<{ account?: Account; accounts: Account[]; instruments: Instrument[]; instrumentsError: string; instrumentsLoading: boolean; initialTab: string; operationId: string; refreshKey: number }>();
const emit = defineEmits<{ locked: [boolean]; create: []; imported: [ImportResult]; instruments: []; changed: [] }>();
const { locked, error, send } = useLedgerWorkspace();
const tab = ref(props.initialTab === "import" ? "info" : props.initialTab);
const subtab = ref(props.operationId ? "transactions" : "positions");
const importOpen = ref(props.initialTab === "import");
const importDirty = ref(false);
const registerOpen = ref(false);
const instrument = ref({ name: "", market: "SH", code: "", currency: "CNY" as Currency });
const registrationError = ref("");
const accountRead = reactive(useLedgerRead<AccountDetail>());
const positions = reactive(useLedgerRead<Position[]>());
const valuation = reactive(useLedgerRead<Valuation>());
const configured = ref(false);
const currentAudit = ref("");
const localRefresh = ref(0);
const manual = computed(() => props.account?.current_holdings_input === "manual_snapshot");
const replay = computed(() => props.account?.current_holdings_input === "transaction_replay");
const canValue = computed(() => replay.value || (manual.value && configured.value));
const instrumentName = (id: string) => props.instruments.find(i => i.id === id)?.name ?? "未知证券";
const instrumentCurrency = (id: string) => props.instruments.find(i => i.id === id)?.currency ?? "原币";
function loadHoldings() {
  valuation.clear(); accountRead.clear(); positions.clear();
  const a = props.account;
  if (tab.value !== "holdings" || !a || a.current_holdings_input !== "transaction_replay") return;
  void accountRead.load(async signal => {
    const result = await request<AccountDetail>(`/accounts/${encodeURIComponent(a.id)}`, { signal });
    if (!result || result.id !== a.id || result.currency !== a.currency || typeof result.cash !== "string" || !decimal(result.cash, 2)) throw new LedgerError("invalid_response");
    return result;
  });
  void positions.load(signal => all<Position>(`/accounts/${encodeURIComponent(a.id)}/positions`, signal));
}
function configuredHoldings(value: boolean, audit: string) {
  configured.value = value;
  if (audit !== currentAudit.value || !value) valuation.clear();
  currentAudit.value = audit;
}
function currentSaved() { valuation.clear(); emit("changed"); }
function validValuation(v: Valuation, a: Account) {
  return !!(v && v.account_id === a.id && v.currency === a.currency && validDay(v.as_of) &&
    (v.source === "manual_snapshot" || v.source === "transaction_replay") &&
    v.source === a.current_holdings_input && typeof v.complete === "boolean" &&
    typeof v.cash === "string" && decimal(v.cash, 2) && typeof v.known_positions_value === "string" && decimal(v.known_positions_value, 2) &&
    (v.positions_value === null || (typeof v.positions_value === "string" && decimal(v.positions_value, 2))) &&
    (v.total_assets === null || (typeof v.total_assets === "string" && decimal(v.total_assets, 2))) &&
    (!v.complete || v.total_assets !== null) && Array.isArray(v.items) && v.items.length <= 200 &&
    v.items.every(p => p && typeof p.instrument_id === "string" && typeof p.quantity === "string" && decimal(p.quantity, 6) &&
      ["current", "prior_date", "unavailable", "closed"].includes(p.status) &&
      (p.market_value === null || (typeof p.market_value === "string" && decimal(p.market_value, 2)))));
}
function preview() {
  if (locked.value || !props.account || !canValue.value) return;
  const a = props.account;
  valuation.clear();
  void valuation.load(async signal => {
    const v = await request<Valuation>(`/accounts/${encodeURIComponent(a.id)}/valuation`, { signal });
    if (!validValuation(v, a) || v.history_id) throw new LedgerError("invalid_response");
    return v;
  });
}
function saveValuation() {
  const a = props.account;
  if (!a || locked.value || !canValue.value || valuation.loading || valuation.error || !valuation.data?.complete) return;
  send(new PendingWrite<Valuation>(`/accounts/${encodeURIComponent(a.id)}/valuation`, "POST", {}), "更新总资产", v => validValuation(v, a) && v.complete && /^[1-9]\d*$/.test(v.history_id ?? ""), () => valuation.clear());
}
function normalizeInstrument() {
  const i = instrument.value;
  i.code = i.code.trim().toUpperCase();
  if (i.market === "HK") { if (i.code) i.code = i.code.padStart(5, "0"); i.currency = "HKD"; }
  if (i.market === "US") i.currency = "USD";
  if (i.market === "SH") i.currency = i.code.startsWith("900") ? "USD" : "CNY";
  if (i.market === "SZ") i.currency = i.code.startsWith("200") ? "HKD" : "CNY";
}
function register() {
  if (locked.value) return;
  normalizeInstrument();
  const i = instrument.value;
  if (!i.name.trim() || !i.code.trim() || ((i.market === "SH" || i.market === "SZ") && !/^\d{6}$/.test(i.code)) || (i.market === "HK" && !/^\d{5}$/.test(i.code))) { registrationError.value = "请填写名称与正确的证券代码：沪深六位、港股五位。"; return; }
  const payload: Instrument = { ...i, id: newID(), name: i.name.trim() };
  send(new PendingWrite<Instrument>("/instruments", "POST", payload), "登记证券", result => result?.id === payload.id && result.market === payload.market && result.code === payload.code && result.currency === payload.currency,
    () => { registerOpen.value = false; emit("instruments"); });
}
function openRegistration() {
  if (locked.value) return;
  instrument.value = { name: "", market: "SH", code: "", currency: "CNY" }; registrationError.value = ""; error.value = ""; registerOpen.value = true;
}
watch(() => props.account?.id, () => { configured.value = false; currentAudit.value = ""; loadHoldings(); }, { immediate: true });
watch(tab, () => { if (tab.value === "holdings") loadHoldings(); });
watch(() => props.initialTab, value => { tab.value = value === "import" ? "info" : value; if (value === "import") importOpen.value = true; });
watch(() => props.operationId, id => { if (id) { tab.value = "holdings"; subtab.value = "transactions"; } });
watch(() => props.refreshKey, () => { localRefresh.value++; loadHoldings(); });
</script>

<template>
  <section class="lp-management" aria-label="账户管理">
    <div class="lp-management-tabs" aria-label="管理栏目"><button v-for="item in [['info', '账户资料'], ['holdings', '当前持仓'], ['weekly', '自动更新记录']]" :key="item[0]" :aria-pressed="tab === item[0]" :disabled="locked || registerOpen || importOpen" @click="tab = item[0]!">{{ item[1] }}</button></div>
    <div v-if="tab === 'info'" class="lp-management-body">
      <h2>账户资料</h2><dl v-if="account" class="lp-info"><div><dt>账户名称</dt><dd>{{ account.name }}</dd></div><div><dt>记账币种</dt><dd>{{ account.currency }}</dd></div><div><dt>开始记录</dt><dd>{{ account.opening_date }}</dd></div>
        <div><dt>持仓来源</dt><dd>{{ account.current_holdings_input === 'manual_snapshot' ? '独立维护当前持仓' : '由交易记录计算' }}</dd></div></dl>
      <p class="lp-muted">账户名称、币种和期初创建后不可修改，当前服务不提供资料编辑。记录可以单独更正。</p>
      <div class="lp-management-secondary"><div><h3>开启另一份计划</h3><p>账户之间独立记录，不合并资金和持仓。</p></div><button :disabled="locked" @click="emit('create')">新建账户</button></div>
      <div class="lp-management-secondary"><div><h3>从文件开始</h3><p>导入真实 Excel 账本，先预览再确认，仅用于空账户初始化。</p></div><button :disabled="locked" @click="importOpen = true; importDirty = false; error = ''">导入 Excel 账本</button></div>
    </div>
    <div v-else-if="tab === 'holdings'" class="lp-management-body">
      <div class="lp-section-title"><div v-if="replay" class="lp-segment" aria-label="持仓管理"><button :aria-pressed="subtab === 'positions'" :disabled="locked" @click="subtab = 'positions'">当前持仓</button><button :aria-pressed="subtab === 'transactions'" :disabled="locked" @click="subtab = 'transactions'">持仓交易</button></div><span v-else />
        <button :disabled="locked || instrumentsLoading || !!instrumentsError" @click="openRegistration">登记证券</button></div>
      <p v-if="instrumentsLoading" role="status">正在读取证券…</p><div v-if="instrumentsError"><p class="lp-error" role="alert">{{ instrumentsError }}</p><button :disabled="locked" @click="emit('instruments')">重新读取证券</button></div>
      <template v-if="account">
        <LedgerTransactions v-if="replay && subtab === 'transactions'" :account="account" :accounts="accounts" :instruments="instruments" :positions="positions.data ?? []" :positions-fresh="!!positions.data && !positions.loading && !positions.error" :refresh-key="refreshKey" :operation-id="operationId" />
        <template v-else>
          <CurrentHoldings v-if="manual" :account-id="account.id" :currency="account.currency" :instruments="instruments" :disabled="locked || instrumentsLoading || !!instrumentsError" :refresh-key="localRefresh" @locked="emit('locked', $event)" @configured="configuredHoldings" @saved="currentSaved" />
          <template v-else-if="replay"><h2>当前持仓</h2><p class="lp-muted">由期初与有效交易计算，不允许用手工持仓覆盖。</p>
            <p v-if="accountRead.loading || positions.loading" role="status">正在读取当前持仓…</p><p v-if="accountRead.error || positions.error" class="lp-error" role="alert">{{ accountRead.error || positions.error }}</p>
            <div class="lp-holding-summary"><span>当前现金 <small>{{ account.currency }}</small></span><strong>{{ money(accountRead.data?.cash) }}</strong><small>现金不是总资产</small></div>
            <ul class="lp-business-list"><li v-for="p in positions.data" :key="p.instrument_id"><div><strong>{{ instrumentName(p.instrument_id) }}</strong><small>数量 {{ money(p.quantity) }}</small>
              <details><summary>成本与损益</summary><p>剩余成本 {{ money(p.remaining_cost) }} · 移动平均成本 {{ money(p.moving_average) }}</p><p>摊薄基数 {{ money(p.diluted_basis) }} · 摊薄单位成本 {{ money(p.diluted_cost) }}</p><p>已实现价差 {{ money(p.realized_profit) }} · 分红 {{ money(p.dividends) }} {{ account.currency }}</p><small>分红归属周期：{{ p.cycle_id }}</small></details></div></li></ul>
            <p v-if="positions.data?.length === 0" class="lp-muted">暂无证券持仓。</p></template>
          <div class="lp-management-secondary"><div><h3>更新总资产</h3><p>先预览参考估值，再明确保存为一笔总资产记录。保存时服务会重新取价。</p></div><button :disabled="locked || !canValue || valuation.loading" @click="preview">预览当前估值</button></div>
          <p v-if="!canValue" class="lp-muted">先保存当前现金与证券数量，才能预览估值；零现金、无证券也可明确保存。</p>
          <p v-if="valuation.loading" role="status">正在获取参考报价，仅预览，不保存…</p><p v-if="valuation.error" class="lp-error" role="alert">{{ valuation.error }}</p>
          <div v-if="valuation.data" class="lp-valuation-preview"><div class="lp-holding-summary"><span>参考总资产 <small>{{ account.currency }}</small></span><strong>{{ money(valuation.data.total_assets) }}</strong><small>{{ valuation.data.as_of }} · {{ valuation.data.complete ? '估值完整，尚未保存' : '报价不完整，不能保存总额' }}</small></div>
            <p>现金 {{ money(valuation.data.cash) }} · 已知证券市值 {{ money(valuation.data.known_positions_value) }} {{ account.currency }}</p>
            <ul class="lp-business-list"><li v-for="item in valuation.data.items" :key="item.instrument_id"><div><strong>{{ instrumentName(item.instrument_id) }}</strong><small>数量 {{ money(item.quantity) }} · {{ item.status === 'unavailable' ? '报价不可用' : item.status === 'prior_date' ? '较早日期参考价' : item.status === 'closed' ? '已清仓' : '参考报价' }}</small><p v-if="item.quote">价格 {{ money(item.quote.price) }} {{ instrumentCurrency(item.instrument_id) }} · {{ item.quote.date }}</p><small v-if="item.fx">参考汇率 {{ item.fx.rate }} · {{ item.fx.date }}</small></div><strong>{{ money(item.market_value) }}</strong></li></ul>
            <p class="lp-field-hint">参考报价不代表券商结算价。缺少任一必要报价时，已知市值小计不等于总资产。</p><button class="lp-primary" :disabled="locked || !valuation.data.complete || valuation.loading" @click="saveValuation">更新并保存总资产</button></div>
        </template>
      </template>
      <div v-else class="lp-empty"><p>先创建账户；证券可以提前登记。</p><button :disabled="locked" @click="emit('create')">新建账户</button></div>
    </div>
    <div v-else class="lp-management-body"><LedgerAutoUpdates :account="account" /></div>
    <LedgerDialog v-if="importOpen" title="导入 Excel 账本" :caption="account?.name" :dirty="importDirty" @close="importOpen = false">
      <div class="lp-dialog-body" @input="importDirty = true" @change="importDirty = true"><ImportAccount :accounts="accounts" :disabled="locked" @locked="emit('locked', $event)" @imported="importOpen = false; emit('imported', $event)" /></div>
    </LedgerDialog>
    <LedgerDialog v-if="registerOpen" title="登记证券" caption="供持仓与交易使用" :dirty="!!instrument.name || !!instrument.code || instrument.market !== 'SH'" @close="registerOpen = false" v-slot="{ requestClose }">
      <form class="lp-form" @submit.prevent="register"><fieldset :disabled="locked"><label>证券名称<input v-model="instrument.name" required maxlength="160" /></label>
        <div class="lp-two-fields"><label>市场<select v-model="instrument.market" @change="normalizeInstrument"><option value="SH">上海 SH</option><option value="SZ">深圳 SZ</option><option value="HK">香港 HK</option><option value="US">美国 US（无自动行情）</option></select></label>
          <label>证券代码<input v-model="instrument.code" required @blur="normalizeInstrument" /></label></div>
        <label>证券原币<select v-model="instrument.currency" :disabled="['SH', 'SZ', 'HK'].includes(instrument.market)"><option>CNY</option><option>HKD</option><option>USD</option></select></label>
        <p class="lp-field-hint">市场、代码和币种登记后不可修改。沪 B 股为 USD、深 B 股为 HKD；其他沪深股票为 CNY，港股为 HKD。不填写模拟价格。</p>
        <p v-if="registrationError" class="lp-error" role="alert">{{ registrationError }}</p><div class="lp-dialog-footer"><button type="button" @click="requestClose">取消</button><button type="submit" class="lp-primary">登记证券</button></div>
      </fieldset></form>
    </LedgerDialog>
  </section>
</template>

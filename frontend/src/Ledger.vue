<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  shallowRef,
  watch,
} from "vue";
import OperationForm from "./LedgerOperationForm.vue";
import LedgerValuation from "./LedgerValuation.vue";
import LedgerValuationHistory from "./LedgerValuationHistory.vue";
import LedgerWeekly from "./LedgerWeekly.vue";
import ImportAccount from "./ImportAccount.vue";
import ImportedRecords from "./ImportedRecords.vue";
import AccountRecords from "./AccountRecords.vue";
import CurrentHoldings from "./CurrentHoldings.vue";
import type { ImportResult } from "./ledgerImport";
import {
  all,
  decimal,
  failure,
  kinds,
  newID,
  PendingWrite,
  LedgerError,
  query,
  request,
  type Account,
  type AccountDetail,
  type AccountInput,
  type Instrument,
  type LedgerRecord,
  type Mutation,
  type Page,
  type Position,
  type Revision,
  type Valuation,
} from "./ledger";
import { useLedgerRead } from "./useLedgerRead";
import "./ledger.css";

const emit = defineEmits<{ locked: [value: boolean] }>();
const accounts = reactive(useLedgerRead<Account[]>());
const instruments = reactive(useLedgerRead<Instrument[]>());
const account = reactive(useLedgerRead<AccountDetail>());
const positions = reactive(useLedgerRead<Position[]>());
const valuation = reactive(useLedgerRead<Valuation>());
const historyRefresh = ref(0);
const operations = reactive(useLedgerRead<Page<LedgerRecord>>());
const detail = reactive(useLedgerRead<LedgerRecord>());
const revisions = reactive(useLedgerRead<Page<Revision>>());
const selected = ref("");
const detailID = ref("");
const editing = ref(false);
const filters = reactive({ account_id: "", from: "", to: "", status: "all" });
const applied = ref({ ...filters });
const operationCursor = ref("");
const revisionCursor = ref("");
const formKey = ref(0);
const message = ref("");
const writeError = ref("");
const busy = ref(false);
const pending = shallowRef<PendingWrite<unknown>>();
let afterWrite: ((result: unknown) => void) | undefined;
const importPending = ref(false);
const recordsPending = ref(false);
const currentPending = ref(false);
const currentConfigured = ref(false);
const currentAuditID = ref("");
const importRefresh = ref(0);
const locked = computed(
  () =>
    !!pending.value ||
    importPending.value ||
    recordsPending.value ||
    currentPending.value,
);
const holdingsAccounts = computed(() =>
  (accounts.data ?? []).filter((a) => a.accounting_mode === "holdings"),
);
const selectedMode = computed(
  () =>
    accounts.data?.find((a) => a.id === selected.value)?.accounting_mode ??
    (account.data?.id === selected.value
      ? account.data.accounting_mode
      : undefined),
);
const selectedHoldings = computed(
  () => !!selected.value && selectedMode.value === "holdings",
);
const selectedManual = computed(
  () =>
    (
      accounts.data?.find((a) => a.id === selected.value) ??
      (account.data?.id === selected.value ? account.data : undefined)
    )?.current_holdings_input === "manual_snapshot",
);
const selectedValuable = computed(
  () =>
    selectedHoldings.value || (selectedManual.value && currentConfigured.value),
);
function currentSaved() {
  valuation.clear();
  importRefresh.value++;
  historyRefresh.value++;
}
function currentLoaded(configured: boolean, auditId: string) {
  currentConfigured.value = configured;
  if (configured && currentAuditID.value !== auditId) {
    valuation.clear();
    currentAuditID.value = auditId;
  }
}
watch(locked, (value) => emit("locked", value), { immediate: true });
const newAccount = (): AccountInput => ({
  id: newID(),
  name: "",
  currency: "CNY",
  opening_date: "",
  opening_cash: "",
  positions: [],
});
const accountDraft = ref(newAccount());
const newInstrument = (): Instrument => ({
  id: newID(),
  name: "",
  market: "",
  code: "",
  currency: "CNY",
});
const instrumentDraft = ref(newInstrument());
const voidReason = ref("");
const accountName = (id?: string) =>
  accounts.data?.find((a) => a.id === id)?.name ?? id ?? "";
const currency = (id: string) =>
  accounts.data?.find((a) => a.id === id)?.currency ?? "本位币";
const instrumentName = (id: string) =>
  instruments.data?.find((i) => i.id === id)?.name ?? id;
const show = (value: string | null | undefined) => value ?? "未知 / 不适用";
const operationLabels: Record<string, string> = {
  id: "操作 ID",
  date: "业务日期",
  sequence: "全局日内序号",
  kind: "操作类型",
  account_id: "源账户 ID",
  to_account_id: "目标账户 ID",
  instrument_id: "证券 ID",
  amount: "现金金额（分红为原币，其他为本位币）",
  quantity: "股数",
  price: "成交价（原币）",
  fee: "费用（原币）",
  fx: "固定汇率快照",
  cycle_id: "归属周期 ID",
  voided: "是否作废",
};
function loadAccounts() {
  return accounts.load((signal) => all<Account>("/accounts", signal));
}
function loadInstruments() {
  return instruments.load((signal) => all<Instrument>("/instruments", signal));
}
let accountGeneration = 0;
async function loadAccount() {
  const generation = ++accountGeneration;
  const id = selected.value;
  if (!id) return;
  const known = accounts.data?.find((a) => a.id === id);
  const metadata = account.load((signal) =>
    request<AccountDetail>(`/accounts/${id}`, { signal }),
  );
  if (!known) await metadata;
  if (generation !== accountGeneration || selected.value !== id) return;
  const mode =
    known?.accounting_mode ??
    (!account.error && account.data?.id === id
      ? account.data.accounting_mode
      : undefined);
  if (mode !== "holdings") return;
  previewValuation();
  void positions.load((signal) =>
    all<Position>(`/accounts/${id}/positions`, signal),
  );
}
function previewValuation() {
  if (locked.value || !selectedValuable.value) return;
  const id = selected.value;
  void valuation.load(async (signal) => {
    const result = await request<Valuation>(`/accounts/${id}/valuation`, {
      signal,
    });
    if (!result || result.account_id !== id || !Array.isArray(result.items))
      throw new Error("估值响应与所选账户不匹配或数据缺失");
    if (result.history_id) throw new Error("只读估值预览不应返回保存回执");
    return result;
  });
}
function loadOperations() {
  const params = query({
    ...applied.value,
    cursor: operationCursor.value,
    limit: "30",
  });
  void operations.load((signal) =>
    request<Page<LedgerRecord>>(`/operations${params}`, { signal }),
  );
}
function loadDetail() {
  const id = detailID.value;
  if (!id) return;
  void detail.load((signal) =>
    request<LedgerRecord>(`/operations/${id}`, { signal }),
  );
  loadRevisions();
}
function loadRevisions() {
  const id = detailID.value;
  if (!id) return;
  const params = query({ cursor: revisionCursor.value, limit: "30" });
  void revisions.load((signal) =>
    request<Page<Revision>>(`/operations/${id}/revisions${params}`, { signal }),
  );
}
function refresh() {
  importRefresh.value++;
  void loadAccounts();
  void loadInstruments();
  loadAccount();
  loadOperations();
  loadDetail();
}
async function imported(result: ImportResult) {
  // The import receipt is already final; these independent reads cannot undo it.
  await loadAccounts();
  if (selected.value === result.account_id) {
    void loadAccount();
    importRefresh.value++;
  } else selected.value = result.account_id;
}
function reportedCreated(id: string) {
  selected.value = id;
  void loadAccounts();
}
watch(selected, () => {
  currentConfigured.value = false;
  currentAuditID.value = "";
  account.clear();
  positions.clear();
  valuation.clear();
  loadAccount();
});
function selectDetail(id: string) {
  if (locked.value) return;
  editing.value = false;
  voidReason.value = "";
  detailID.value = id;
  revisionCursor.value = "";
  detail.clear();
  revisions.clear();
  loadDetail();
}
function applyFilters() {
  applied.value = { ...filters };
  operationCursor.value = "";
  operations.clear();
  loadOperations();
}
function nextOperations(cursor: string) {
  operationCursor.value = cursor;
  operations.clear();
  loadOperations();
}
function nextRevisions(cursor: string) {
  revisionCursor.value = cursor;
  revisions.clear();
  loadRevisions();
}
async function send(
  write?: PendingWrite<unknown>,
  success?: (result: unknown) => void,
) {
  if (busy.value || (write && pending.value)) return;
  if (write) {
    pending.value = write;
    afterWrite = success;
  }
  if (!pending.value) return;
  busy.value = true;
  writeError.value = "";
  message.value = "";
  let result: unknown;
  try {
    result = await pending.value.run();
    if (pending.value.path.endsWith("/valuation")) {
      const v = result as Valuation;
      if (
        !v ||
        v.account_id !== pending.value.path.split("/")[2] ||
        !v.complete ||
        !/^[1-9]\d*$/.test(v.history_id ?? "") ||
        typeof v.total_assets !== "string" ||
        !decimal(v.total_assets, 2)
      ) {
        pending.value.uncertain = true;
        throw new LedgerError("invalid_response");
      }
    }
  } catch (e) {
    writeError.value = failure(e);
    if (!pending.value?.uncertain) {
      pending.value = undefined;
      afterWrite = undefined;
    }
    return;
  } finally {
    busy.value = false;
  }
  // A receipt may be an old version. Refresh failures are not mutation failures.
  message.value =
    "写入已确认成功。正在独立读取当前记录；读取失败不影响本次成功，请勿重复录入。";
  pending.value = undefined;
  afterWrite?.(result);
  afterWrite = undefined;
  operationCursor.value = "";
  revisionCursor.value = "";
  refresh();
}
function saveValuation() {
  if (locked.value || !selectedValuable.value) return;
  void send(
    new PendingWrite<Valuation>(
      `/accounts/${selected.value}/valuation`,
      "POST",
      {},
    ),
    (result) => {
      if (selectedManual.value) {
        valuation.clear();
        valuation.data = result as Valuation;
      }
      historyRefresh.value++;
      message.value = `总资产已保存为记录 #${(result as Valuation).history_id}。重试返回原回执，不覆盖后续更正。`;
    },
  );
}
function createAccount() {
  if (locked.value) return;
  const a = accountDraft.value;
  if (
    !decimal(a.opening_cash, 2) ||
    a.positions.some(
      (p) =>
        !decimal(p.quantity, 6) ||
        (p.cost !== null && p.cost !== "" && !decimal(p.cost, 2)) ||
        (p.diluted_basis !== null &&
          p.diluted_basis !== "" &&
          !decimal(p.diluted_basis, 2)),
    )
  ) {
    writeError.value = "请检查期初精度：现金/成本 2 位，股数 6 位";
    return;
  }
  const payload: AccountInput = {
    ...a,
    positions: a.positions.map((p) => ({
      ...p,
      cost: p.cost || null,
      diluted_basis: p.diluted_basis || null,
    })),
  };
  void send(new PendingWrite("/accounts", "POST", payload, payload), () => {
    selected.value = payload.id;
    accountDraft.value = newAccount();
  });
}
function registerInstrument() {
  if (locked.value) return;
  void send(
    new PendingWrite("/instruments", "POST", instrumentDraft.value),
    () => {
      instrumentDraft.value = newInstrument();
    },
  );
}
function saveOperation(payload: Mutation) {
  if (locked.value) return;
  const edit = !!payload.expected_version;
  void send(
    new PendingWrite(
      edit ? `/operations/${payload.operation.id}` : "/operations",
      edit ? "PUT" : "POST",
      payload,
    ),
    () => {
      detailID.value = payload.operation.id;
      detail.clear();
      revisions.clear();
      editing.value = false;
      formKey.value++;
    },
  );
}
function voidOperation() {
  if (
    locked.value ||
    !detail.data ||
    detail.error ||
    detail.loading ||
    !voidReason.value.trim()
  )
    return;
  void send(
    new PendingWrite(`/operations/${detail.data.operation.id}`, "DELETE", {
      expected_version: detail.data.version,
      reason: voidReason.value,
    }),
    () => {
      voidReason.value = "";
      editing.value = false;
    },
  );
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (locked.value) {
    event.preventDefault();
    event.returnValue = "";
  }
}
onMounted(() => {
  refresh();
  window.addEventListener("beforeunload", beforeUnload);
});
onBeforeUnmount(() => window.removeEventListener("beforeunload", beforeUnload));
</script>

<template>
  <main class="ledger">
    <header class="page-heading">
      <div>
        <div class="eyebrow">INVESTMENT LEDGER</div>
        <h1>投资账本</h1>
        <p>记录资金与持仓，保留每一次更正。</p>
      </div>
      <button
        type="button"
        data-test="refresh"
        :disabled="locked"
        @click="refresh"
      >
        刷新当前数据
      </button>
    </header>
    <p class="ledger-notice">
      公开共享账本：无需登录，任何人都能查看、导入和修改全部数据，请勿上传私人财务信息。外币操作自动获取腾讯参考汇率并保存固定快照，失败可明确手工录入，不代表券商结算价。浏览和刷新估值只预览，不改变账本；点击“更新并保存总资产”才重新计算并保存。收益分析提供累计收益、Modified
      Dietz 与
      XIRR，按端点完整性标注状态。历史初始化、人工和自动记录共用一条账户曲线。周六任务默认关闭，下方可只读查询；TWR
      已实现，历史行情回填已取消。
    </p>
    <p v-if="message" role="status" class="ledger-success">{{ message }}</p>
    <p v-if="writeError" role="alert" class="ledger-error">{{ writeError }}</p>
    <section v-if="pending" class="ledger-pending" aria-live="polite">
      <h2>{{ busy ? "正在确认写入" : "写入结果待确认" }}</h2>
      <p>
        请求内容、ID、版本和幂等键已锁定。请勿关闭、刷新或另开页面重复录入；数据仅保存在此页内存。账户重试会先查询固定
        ID 确认。
      </p>
      <p>
        待确认请求：{{ pending.method }} {{ pending.path }} · 幂等标识
        {{ pending.key }}
      </p>
      <button type="button" :disabled="busy" data-test="retry" @click="send()">
        按原请求重试确认
      </button>
    </section>

    <section class="ledger-panel">
      <h2>账户与期初</h2>
      <p v-if="accounts.loading">正在读取账户…</p>
      <p v-if="accounts.error" role="alert">{{ accounts.error }}</p>
      <div class="ledger-account-list">
        <button
          v-for="a in accounts.data"
          :key="a.id"
          type="button"
          :disabled="locked"
          :aria-pressed="selected === a.id"
          @click="selected = a.id"
        >
          <strong>{{ a.name }}</strong
          ><span
            >{{ a.currency }} ·
            {{
              a.accounting_mode === "reported"
                ? "总资产账户（无期初现金）"
                : `期初现金 ${show(a.opening_cash)}`
            }}</span
          ><small>{{ a.opening_date }} · {{ a.id }}</small>
        </button>
      </div>
      <p v-if="accounts.data?.length === 0">
        暂无账户。可在下方直接新建总资产账户或导入
        Excel；需要持仓记账时再登记证券并创建期初。
      </p>
      <details>
        <summary>创建账户与多项期初持仓</summary>
        <form
          data-test="account-form"
          class="ledger-form"
          @submit.prevent="createAccount"
        >
          <fieldset :disabled="locked">
            <div class="ledger-grid">
              <label
                >账户名称<input
                  v-model="accountDraft.name"
                  name="account_name"
                  required
              /></label>
              <label
                >本位币<select v-model="accountDraft.currency" name="currency">
                  <option>CNY</option>
                  <option>HKD</option>
                  <option>USD</option>
                </select></label
              >
              <label
                >期初日期<input
                  v-model="accountDraft.opening_date"
                  name="opening_date"
                  type="date"
                  required
              /></label>
              <label
                >期初现金（不是总资产）<input
                  v-model="accountDraft.opening_cash"
                  name="opening_cash"
                  inputmode="decimal"
                  required
              /></label>
            </div>
            <p>
              成本均为本位币。留空表示未知，与明确输入 0.00
              不同；剩余成本与摊薄基数独立填写。期初创建后不可覆盖。
            </p>
            <div
              v-for="(p, index) in accountDraft.positions"
              :key="index"
              class="ledger-opening ledger-grid"
            >
              <label
                >期初证券<select
                  v-model="p.instrument_id"
                  :name="`opening_instrument_${index}`"
                  required
                >
                  <option
                    v-for="i in instruments.data"
                    :key="i.id"
                    :value="i.id"
                  >
                    {{ i.name }} · {{ i.currency }}
                  </option>
                </select></label
              >
              <label
                >期初股数<input
                  v-model="p.quantity"
                  :name="`opening_quantity_${index}`"
                  required
                  inputmode="decimal"
              /></label>
              <label
                >剩余成本<input
                  v-model="p.cost"
                  :name="`opening_cost_${index}`"
                  placeholder="未知"
                  inputmode="decimal"
              /></label>
              <label
                >摊薄基数<input
                  v-model="p.diluted_basis"
                  :name="`opening_basis_${index}`"
                  placeholder="未知"
                  inputmode="decimal"
              /></label>
              <button
                type="button"
                @click="accountDraft.positions.splice(index, 1)"
              >
                移除此持仓
              </button>
            </div>
            <button
              type="button"
              :disabled="accountDraft.positions.length >= 200"
              data-test="add-position"
              @click="
                accountDraft.positions.push({
                  instrument_id: '',
                  quantity: '',
                  cost: null,
                  diluted_basis: null,
                })
              "
            >
              添加期初持仓
            </button>
            <button type="submit">创建账户</button>
          </fieldset>
        </form>
      </details>
    </section>

    <LedgerWeekly :accounts="accounts.data ?? []" :disabled="locked" />
    <ImportAccount
      :accounts="accounts.data ?? []"
      :disabled="
        !!pending ||
        recordsPending ||
        currentPending ||
        accounts.loading ||
        !!accounts.error
      "
      @locked="importPending = $event"
      @imported="imported"
    />
    <AccountRecords
      :holdings="selectedHoldings"
      :account-id="selected"
      :account-name="accountName(selected)"
      :currency="currency(selected)"
      :refresh-key="importRefresh + historyRefresh"
      :disabled="!!pending || importPending || currentPending"
      @locked="recordsPending = $event"
      @created="reportedCreated"
    />
    <ImportedRecords
      v-if="selected"
      :key="selected"
      :account-id="selected"
      :refresh-key="importRefresh"
    />
    <p v-if="selected && account.loading">正在读取账户信息…</p>
    <p v-if="selected && account.error" role="alert">{{ account.error }}</p>
    <CurrentHoldings
      v-if="selectedManual"
      :account-id="selected"
      :currency="currency(selected)"
      :instruments="instruments.data ?? []"
      :refresh-key="importRefresh"
      :disabled="
        !!pending ||
        importPending ||
        recordsPending ||
        instruments.loading ||
        !!instruments.error
      "
      @locked="currentPending = $event"
      @configured="currentLoaded"
      @saved="currentSaved"
    />
    <p v-if="selectedHoldings">
      当前持仓由期初和交易自动重放；不允许手工快照覆盖，请通过交易记录更正。
    </p>
    <button
      v-if="selectedValuable"
      :disabled="locked || valuation.loading"
      data-test="preview-valuation"
      @click="previewValuation"
    >
      预览当前估值（不保存）
    </button>
    <button
      v-if="selectedValuable"
      data-test="save-valuation"
      :disabled="locked || busy"
      @click="saveValuation"
    >
      更新并保存总资产
    </button>
    <LedgerValuation
      v-if="selectedValuable"
      :account-id="selected"
      :value="valuation.data"
      :loading="valuation.loading"
      :error="valuation.error"
      :instruments="instruments.data ?? []"
    />
    <LedgerValuationHistory
      v-if="selectedValuable"
      :account-id="selected"
      :refresh-key="historyRefresh"
    />

    <section v-if="selectedHoldings" class="ledger-panel">
      <h2>账户详情 · {{ accountName(selected) }}</h2>
      <p v-if="account.loading || positions.loading">正在读取当前现金与持仓…</p>
      <p v-if="account.error" role="alert">{{ account.error }}</p>
      <p v-if="positions.error" role="alert">{{ positions.error }}</p>
      <p v-if="account.data" class="ledger-cash">
        当前现金 <strong>{{ show(account.data.cash) }}</strong>
        {{ account.data.currency }} <small>不是总资产</small>
      </p>
      <p>
        成本及周期损益以账户本位币展示；未知 /
        不适用不等于零。仅显示最新周期（包括已清仓周期）。市值请看独立估值快照，账户收益请看分析依据中的资金加权指标，不以持仓成本反推。
      </p>
      <div class="ledger-table">
        <table>
          <thead>
            <tr>
              <th>证券 / 周期</th>
              <th>股数</th>
              <th>剩余成本</th>
              <th>移动平均成本</th>
              <th>摊薄基数</th>
              <th>摊薄单位成本</th>
              <th>已实现价差损益</th>
              <th>分红</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in positions.data" :key="p.instrument_id">
              <td>
                {{ instrumentName(p.instrument_id)
                }}<small>{{ p.cycle_id }}</small>
              </td>
              <td>{{ p.quantity }}</td>
              <td>{{ show(p.remaining_cost) }}</td>
              <td>{{ show(p.moving_average) }}</td>
              <td>{{ show(p.diluted_basis) }}</td>
              <td>{{ show(p.diluted_cost) }}</td>
              <td>{{ show(p.realized_profit) }}</td>
              <td>{{ p.dividends }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="positions.data?.length === 0">暂无持仓周期。</p>
    </section>

    <section class="ledger-panel">
      <h2>证券登记</h2>
      <p v-if="instruments.loading">正在读取证券…</p>
      <p v-if="instruments.error" role="alert">{{ instruments.error }}</p>
      <details>
        <summary>
          查看证券 / 登记证券（{{ instruments.data?.length ?? 0 }}）
        </summary>
        <ul>
          <li v-for="i in instruments.data" :key="i.id">
            {{ i.name }} · {{ i.market }} / {{ i.code }} · {{ i.currency }} ·
            {{ i.id }}
          </li>
        </ul>
        <form
          class="ledger-form"
          data-test="instrument-form"
          @submit.prevent="registerInstrument"
        >
          <fieldset :disabled="locked">
            <div class="ledger-grid">
              <label
                >名称<input
                  v-model="instrumentDraft.name"
                  name="instrument_name"
                  required /></label
              ><label
                >市场<input
                  v-model="instrumentDraft.market"
                  name="market"
                  required /></label
              ><label
                >代码<input
                  v-model="instrumentDraft.code"
                  name="code"
                  required /></label
              ><label
                >原币<select v-model="instrumentDraft.currency">
                  <option>CNY</option>
                  <option>HKD</option>
                  <option>USD</option>
                </select></label
              >
            </div>
            <p>证券身份和币种登记后不可改写。</p>
            <p>
              行情支持 SH / SZ 六位代码、HK 五位代码。SH 普通股票原币 CNY，SH
              900 开头 B 股为 USD；SZ 普通股票原币 CNY，SZ 200 开头 B 股为
              HKD；HK 原币 HKD。其他市场仍可登记记账，不支持的行情不会阻止记账。
            </p>
            <button type="submit">登记证券</button>
          </fieldset>
        </form>
      </details>
    </section>

    <section class="ledger-panel">
      <OperationForm
        :key="formKey"
        :accounts="holdingsAccounts"
        :instruments="instruments.data ?? []"
        :positions="positions.data ?? []"
        :positions-account="selected"
        :positions-fresh="!positions.error && !positions.loading"
        :locked="
          locked ||
          !!accounts.error ||
          !!instruments.error ||
          accounts.loading ||
          instruments.loading
        "
        @save="saveOperation"
      />
    </section>

    <section class="ledger-panel">
      <h2>操作流水</h2>
      <form
        class="ledger-grid"
        data-test="filters"
        @submit.prevent="applyFilters"
      >
        <label
          >账户范围<select v-model="filters.account_id" name="filter_account">
            <option value="">全部账户</option>
            <option v-for="a in holdingsAccounts" :key="a.id" :value="a.id">
              {{ a.name }}
            </option>
          </select></label
        ><label
          >起始日期<input
            v-model="filters.from"
            name="from"
            type="date" /></label
        ><label
          >结束日期<input v-model="filters.to" name="to" type="date" /></label
        ><label
          >状态<select v-model="filters.status" name="status">
            <option value="all">全部（含作废）</option>
            <option value="active">有效</option>
            <option value="voided">已作废</option>
          </select></label
        ><button type="submit">应用筛选</button>
      </form>
      <p>
        按日期、全局序号倒序。游标不是冻结快照，期间有人更正日期/序号时请回到首页刷新。
      </p>
      <p v-if="operations.loading">正在读取流水…</p>
      <p v-if="operations.error" role="alert">{{ operations.error }}</p>
      <div class="ledger-table">
        <table>
          <thead>
            <tr>
              <th>日期 / 序号</th>
              <th>类型 / 状态</th>
              <th>账户 / 现金金额</th>
              <th>证券 / 股数 / 价格</th>
              <th>详情</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in operations.data?.items" :key="r.operation.id">
              <td>
                {{ r.operation.date }}<small>#{{ r.operation.sequence }}</small>
              </td>
              <td>
                {{ kinds[r.operation.kind]
                }}<small
                  >{{ r.operation.voided ? "已作废" : "有效" }} · v{{
                    r.version
                  }}</small
                >
              </td>
              <td>
                <template v-if="r.operation.kind === 'transfer'"
                  >{{ accountName(r.operation.account_id) }} 转出
                  {{ r.operation.amount }} {{ currency(r.operation.account_id)
                  }}<br />{{ accountName(r.operation.to_account_id) }} 转入
                  {{ r.operation.amount }}
                  {{ currency(r.operation.to_account_id!) }}</template
                ><template v-else
                  >{{ accountName(r.operation.account_id)
                  }}<small>{{
                    r.operation.kind === "buy" || r.operation.kind === "sell"
                      ? "无外部资金流入/流出"
                      : `${r.operation.amount} ${r.operation.kind === "dividend" ? (instruments.data?.find((i) => i.id === r.operation.instrument_id)?.currency ?? "证券原币") : currency(r.operation.account_id)}`
                  }}</small></template
                >
              </td>
              <td>
                {{
                  r.operation.instrument_id
                    ? instrumentName(r.operation.instrument_id)
                    : "不适用"
                }}<small
                  v-if="
                    ['buy', 'sell', 'deposit_buy', 'sell_withdraw'].includes(
                      r.operation.kind,
                    )
                  "
                  >股数 {{ r.operation.quantity }} · 价格
                  {{ r.operation.price }}</small
                >
                <small v-else-if="r.operation.kind === 'dividend'"
                  >归属周期 {{ r.operation.cycle_id }}</small
                >
              </td>
              <td>
                <button
                  type="button"
                  :disabled="locked"
                  data-test="view-operation"
                  @click="selectDetail(r.operation.id)"
                >
                  查看
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="operations.data?.items.length === 0">此筛选下暂无操作。</p>
      <div class="ledger-actions">
        <button type="button" @click="nextOperations('')">流水首页</button
        ><button
          v-if="operations.data?.next_cursor"
          type="button"
          :disabled="operations.loading || !!operations.error"
          @click="nextOperations(operations.data.next_cursor)"
        >
          下一页流水
        </button>
      </div>
    </section>

    <section v-if="detailID" class="ledger-panel" data-test="operation-detail">
      <h2>操作详情</h2>
      <button
        type="button"
        :disabled="locked"
        @click="
          editing = false;
          loadDetail();
        "
      >
        重新读取详情
      </button>
      <p v-if="detail.loading">正在读取最新版本…</p>
      <p v-if="detail.error" role="alert">{{ detail.error }}</p>
      <template v-if="detail.data"
        ><p>
          当前版本 {{ detail.data.version }} ·
          {{ detail.data.operation.voided ? "已作废" : "有效" }} · 更新于
          {{ detail.data.updated_at }}
        </p>
        <p>备注：{{ detail.data.note || "无" }}</p>
        <dl class="ledger-record">
          <template v-for="(value, key) in detail.data.operation" :key="key"
            ><dt>{{ operationLabels[key] ?? key }}</dt>
            <dd>
              {{
                value === null
                  ? key === "fee"
                    ? "未录入（按零计算）"
                    : "无"
                  : value
              }}
            </dd></template
          >
        </dl>
        <button
          v-if="!editing && !detail.data.operation.voided"
          type="button"
          data-test="edit"
          :disabled="locked || !!detail.error || detail.loading"
          @click="editing = true"
        >
          更正整笔操作
        </button>
        <OperationForm
          v-if="editing"
          :key="`${detail.data.operation.id}:${detail.data.version}`"
          :record="detail.data"
          :accounts="holdingsAccounts"
          :instruments="instruments.data ?? []"
          :positions="positions.data ?? []"
          :positions-account="selected"
          :positions-fresh="!positions.error && !positions.loading"
          :locked="
            locked ||
            !!detail.error ||
            detail.loading ||
            !!accounts.error ||
            !!instruments.error ||
            accounts.loading ||
            instruments.loading
          "
          @save="saveOperation"
          @cancel="editing = false"
        />
        <form
          v-if="!detail.data.operation.voided"
          class="ledger-form"
          data-test="void-form"
          @submit.prevent="voidOperation"
        >
          <fieldset :disabled="locked || !!detail.error || detail.loading">
            <label
              >作废原因（必填，保留审计历史）<input
                v-model="voidReason"
                name="void_reason"
                required
            /></label>
            <p>
              将以预期版本
              {{ detail.data.version }}
              作废整笔操作（转账包含双方），不物理删除。
            </p>
            <button type="submit">确认作废</button>
          </fieldset>
        </form>
      </template>
      <h3>修订历史</h3>
      <p v-if="revisions.loading">正在读取修订…</p>
      <p v-if="revisions.error" role="alert">{{ revisions.error }}</p>
      <details v-for="r in revisions.data?.items" :key="r.record.version">
        <summary>
          版本 {{ r.record.version }} · {{ r.reason }} ·
          {{ r.record.operation.voided ? "已作废" : "有效" }}
        </summary>
        <pre>{{ JSON.stringify(r.record, null, 2) }}</pre>
      </details>
      <div class="ledger-actions">
        <button type="button" @click="nextRevisions('')">修订首页</button
        ><button
          v-if="revisions.data?.next_cursor"
          type="button"
          :disabled="revisions.loading || !!revisions.error"
          @click="nextRevisions(revisions.data.next_cursor)"
        >
          下一页修订
        </button>
      </div>
    </section>
  </main>
</template>

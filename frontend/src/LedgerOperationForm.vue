<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import {
  decimal,
  failure,
  query,
  request,
  type FX,
  type FXQuote,
  kinds,
  newID,
  type Account,
  type Instrument,
  type LedgerRecord,
  type Mutation,
  type Operation,
  type Position,
} from "./ledger";

const props = defineProps<{
  accounts: Account[];
  instruments: Instrument[];
  positions: Position[];
  positionsAccount: string;
  positionsFresh: boolean;
  locked: boolean;
  record?: LedgerRecord;
}>();
const emit = defineEmits<{ save: [mutation: Mutation]; cancel: [] }>();
const old = props.record;
const op = reactive<Operation>(
  old
    ? JSON.parse(JSON.stringify(old.operation))
    : { id: newID(), account_id: "", kind: "deposit", date: "", sequence: "" },
);
const note = ref(old?.note ?? "");
const reason = ref("");
const validation = ref("");
const cycleMode = ref(old?.operation.cycle_id ? "explicit" : "current");
const fx = reactive(
  old?.operation.fx
    ? { ...old.operation.fx }
    : { rate: "", date: "", source: "", fetched_at: "" },
);
const trade = computed(() =>
  ["buy", "sell", "deposit_buy", "sell_withdraw"].includes(op.kind),
);
const security = computed(() => trade.value || op.kind === "dividend");
const hasAmount = computed(() => !["buy", "sell"].includes(op.kind));
const holdingsAccounts = computed(() =>
  props.accounts.filter((a) => a.accounting_mode === "holdings"),
);
const account = computed(() =>
  holdingsAccounts.value.find((a) => a.id === op.account_id),
);
const instrument = computed(() =>
  props.instruments.find((i) => i.id === op.instrument_id),
);
const foreign = computed(
  () =>
    security.value &&
    account.value &&
    instrument.value &&
    account.value.currency !== instrument.value.currency,
);
const fxMode = ref<"auto" | "manual">("auto");
const fxLoading = ref(false);
const fxError = ref("");
const quoteInfo = ref<FXQuote>();
const preserved = ref(!!old?.operation.fx);
const context = computed(() =>
  JSON.stringify([
    security.value,
    instrument.value?.currency,
    account.value?.currency,
    op.date,
  ]),
);
const snapshotContext = ref(old?.operation.fx ? context.value : "");
let generation = 0;
let controller: AbortController | undefined;
function validDate(value: string) {
  return (
    /^\d{4}-\d{2}-\d{2}$/.test(value) &&
    Number.isFinite(Date.parse(value)) &&
    new Date(value).toISOString().slice(0, 10) === value
  );
}
function today() {
  return new Date(Date.now() + 8 * 3600000).toISOString().slice(0, 10);
}
function validSnapshot(value: FX) {
  return (
    typeof value.rate === "string" &&
    decimal(value.rate, 8) &&
    Number(value.rate) > 0 &&
    validDate(value.date) &&
    value.date <= op.date &&
    typeof value.source === "string" &&
    !!value.source.trim() &&
    /^\d{4}-\d{2}-\d{2}T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
      value.fetched_at,
    ) &&
    validDate(value.fetched_at.slice(0, 10)) &&
    Number.isFinite(Date.parse(value.fetched_at))
  );
}
const eligible = computed(
  () => !!foreign.value && validDate(op.date) && op.date <= today(),
);
const fxReady = computed(
  () =>
    eligible.value &&
    !fxLoading.value &&
    snapshotContext.value === context.value &&
    validSnapshot(fx),
);
function abortFX() {
  generation++;
  controller?.abort();
  controller = undefined;
  fxLoading.value = false;
}
function clearFX() {
  Object.assign(fx, { rate: "", date: "", source: "", fetched_at: "" });
  snapshotContext.value = "";
  quoteInfo.value = undefined;
  preserved.value = false;
  fxError.value = "";
}
async function fetchFX() {
  if (props.locked) return;
  abortFX();
  clearFX();
  fxMode.value = "auto";
  if (!eligible.value) return;
  const captured = context.value;
  const businessDate = op.date;
  const base = instrument.value!.currency;
  const quote = account.value!.currency;
  const mode = businessDate === today() ? "latest" : "historical";
  const token = generation;
  controller = new AbortController();
  fxLoading.value = true;
  try {
    const result = await request<FXQuote>(
      "/fx" +
        query({
          base,
          quote,
          mode,
          ...(mode === "historical" ? { date: businessDate } : {}),
        }),
      { signal: controller.signal },
    );
    if (token !== generation || captured !== context.value || props.locked)
      return;
    if (
      !result ||
      result.base !== base ||
      result.quote !== quote ||
      result.mode !== mode ||
      result.requested_date !== businessDate ||
      !validSnapshot(result)
    )
      throw new Error(
        "腾讯汇率响应与当前币种、业务日期不匹配或快照无效，请重试或手工录入",
      );
    // The write contract accepts exactly these four snapshot fields.
    Object.assign(fx, {
      rate: result.rate,
      date: result.date,
      source: result.source,
      fetched_at: result.fetched_at,
    });
    snapshotContext.value = captured;
    quoteInfo.value = result;
  } catch (e) {
    if (token === generation && captured === context.value)
      fxError.value = failure(e);
  } finally {
    if (token === generation) fxLoading.value = false;
  }
}
function manualFX() {
  if (props.locked) return;
  abortFX();
  clearFX();
  fxMode.value = "manual";
  snapshotContext.value = context.value;
  fx.fetched_at = new Date().toISOString();
}
watch(
  context,
  () => {
    abortFX();
    clearFX();
    if (!props.locked) void fetchFX();
  },
  { flush: "sync" },
);
watch(
  () => props.locked,
  (locked) => {
    if (locked) abortFX();
  },
);
if (!preserved.value) void fetchFX();
onBeforeUnmount(abortFX);
const currentCycle = computed(() =>
  props.positionsFresh && props.positionsAccount === op.account_id
    ? props.positions.find((p) => p.instrument_id === op.instrument_id)
        ?.cycle_id
    : undefined,
);
function save() {
  validation.value = "";
  if (props.locked) return;
  if (
    !account.value ||
    (op.kind === "transfer" &&
      !holdingsAccounts.value.some((a) => a.id === op.to_account_id))
  ) {
    validation.value = "交易和转账仅支持持仓账户";
    return;
  }
  if (security.value && (!account.value || !instrument.value)) {
    validation.value = "请选择有效账户和证券";
    return;
  }
  if (foreign.value && !fxReady.value) {
    validation.value =
      "请确认当前币种及业务日期的有效汇率快照：正汇率、来源、实际汇率日期不得晚于业务日期、有效获取时间；获取中不能提交";
    return;
  }
  if (!reason.value.trim()) {
    validation.value = "请填写操作原因";
    return;
  }
  if (!/^[1-9]\d*$/.test(op.sequence)) {
    validation.value = "请明确填写全局日内正整数序号";
    return;
  }
  if (
    (hasAmount.value && !decimal(op.amount ?? "", 2)) ||
    (trade.value &&
      (!decimal(op.quantity ?? "", 6) ||
        !decimal(op.price ?? "", 6) ||
        (op.fee && !decimal(op.fee, 2)))) ||
    (foreign.value && !decimal(fx.rate, 8))
  ) {
    validation.value =
      "精度错误：金额/费用最多 2 位，股数/价格最多 6 位，汇率最多 8 位小数，且须在服务范围内";
    return;
  }
  const operation: Operation = {
    id: op.id,
    account_id: op.account_id,
    date: op.date,
    sequence: op.sequence,
    kind: op.kind,
  };
  if (hasAmount.value) operation.amount = op.amount;
  if (security.value) operation.instrument_id = op.instrument_id;
  if (trade.value) {
    operation.quantity = op.quantity;
    operation.price = op.price;
    operation.fee = op.fee || null;
  }
  if (op.kind === "transfer") operation.to_account_id = op.to_account_id;
  if (op.kind === "dividend") {
    operation.cycle_id =
      cycleMode.value === "current" ? currentCycle.value : op.cycle_id;
    if (!operation.cycle_id) {
      validation.value = "请选择已读取的当前周期，或明确填写旧周期 ID";
      return;
    }
  }
  if (foreign.value) operation.fx = { ...fx };
  emit("save", {
    operation,
    note: note.value,
    reason: reason.value,
    ...(old ? { expected_version: old.version } : {}),
  });
}
</script>

<template>
  <form class="ledger-form" data-test="operation-form" @submit.prevent="save">
    <h3>{{ old ? "整笔更正操作" : "录入操作" }}</h3>
    <p v-if="old">
      操作 {{ op.id }} · 预期版本
      {{ old.version }}。整笔替换，不是局部补丁；提交前核对所有字段。
    </p>
    <fieldset :disabled="locked">
      <div class="ledger-grid">
        <label
          >操作类型<select v-model="op.kind" name="kind">
            <option v-for="(label, kind) in kinds" :key="kind" :value="kind">
              {{ label }}
            </option>
          </select></label
        >
        <label
          >源账户<select v-model="op.account_id" name="account_id" required>
            <option value="" disabled>选择账户</option>
            <option v-for="a in holdingsAccounts" :key="a.id" :value="a.id">
              {{ a.name }} · {{ a.currency }}
            </option>
          </select></label
        >
        <label
          >业务日期<input v-model="op.date" name="date" type="date" required
        /></label>
        <label
          >全局日内序号<input
            v-model="op.sequence"
            name="sequence"
            inputmode="numeric"
            pattern="[1-9][0-9]*"
            required
        /></label>
        <label v-if="op.kind === 'transfer'"
          >目标账户<select
            v-model="op.to_account_id"
            name="to_account_id"
            required
          >
            <option
              v-for="a in holdingsAccounts.filter(
                (a) => a.id !== op.account_id,
              )"
              :key="a.id"
              :value="a.id"
            >
              {{ a.name }} · {{ a.currency }}
            </option>
          </select></label
        >
        <label v-if="security"
          >证券<select v-model="op.instrument_id" name="instrument_id" required>
            <option v-for="i in instruments" :key="i.id" :value="i.id">
              {{ i.name }} · {{ i.code }} · {{ i.currency }}
            </option>
          </select></label
        >
        <label v-if="hasAmount"
          >{{
            op.kind === "dividend"
              ? "分红到账金额（证券原币）"
              : op.kind === "sell_withdraw"
                ? "独立转出金额（本位币）"
                : op.kind === "deposit_buy"
                  ? "独立转入金额（本位币）"
                  : "现金金额（本位币）"
          }}<input
            v-model="op.amount"
            name="amount"
            inputmode="decimal"
            required
        /></label>
        <template v-if="trade">
          <label
            >股数<input
              v-model="op.quantity"
              name="quantity"
              inputmode="decimal"
              required
          /></label>
          <label
            >成交价（证券原币）<input
              v-model="op.price"
              name="price"
              inputmode="decimal"
              required
          /></label>
          <label
            >费用（证券原币）<input
              v-model="op.fee"
              name="fee"
              inputmode="decimal"
              placeholder="留空为未录入；0.00 为明确零"
          /></label>
        </template>
      </div>
      <p>
        序号在整个账本同一日期内唯一，作废也占用。请根据真实先后填写，不按账户编号，也不猜测历史顺序。日期不得晚于服务北京时间今天或早于期初。
      </p>
      <p v-if="op.kind === 'transfer'">
        仅支持同币种：源账户转出
        {{ op.amount || "待填" }}，目标账户转入相同金额；不是两笔独立操作。
      </p>
      <div v-if="op.kind === 'dividend'" class="ledger-grid">
        <label
          >归属周期<select v-model="cycleMode" name="cycle_mode">
            <option value="current">当前已读取周期</option>
            <option value="explicit">明确指定旧周期 ID</option>
          </select></label
        >
        <label v-if="cycleMode === 'explicit'"
          >周期 ID<input v-model="op.cycle_id" name="cycle_id" required
        /></label>
        <p v-else>
          当前周期：{{
            currentCycle || "不可用，请在账户详情选择此源账户并刷新持仓"
          }}
        </p>
        <p>
          迟到分红应填原周期
          ID。服务没有周期列表；请从历史记录核对，不自动归入新周期。
        </p>
      </div>
      <fieldset v-if="foreign" class="fx-fields">
        <legend>确认固定汇率快照</legend>
        <p>
          1 {{ instrument?.currency }} 折合多少
          {{
            account?.currency
          }}。自动获取使用腾讯参考汇率，不是券商结算汇率；保存固定快照，写入重试不会重新取汇率。
        </p>
        <p>
          北京时间今天使用最新报价；更早业务日期使用不晚于该日的最近日收盘价，不补造缺失日期，不切换备用来源。
        </p>
        <p>
          业务日期：{{ op.date || "待填" }}；实际汇率日期：{{
            fx.date || "待获取"
          }}；报价时间：{{
            quoteInfo?.quoted_at || "未提供（日收盘或原有快照）"
          }}；获取时间不是报价时间。
        </p>
        <p v-if="preserved">保留原有快照，备注和费用更正不会自动替换汇率。</p>
        <p v-if="quoteInfo">
          腾讯参考：{{
            quoteInfo.mode === "latest" ? "最新报价" : "历史日收盘"
          }}
        </p>
        <p v-if="!eligible" role="alert">
          请填写有效账户、证券及不晚于北京时间今天的业务日期。
        </p>
        <p v-if="fxLoading" role="status">正在获取腾讯汇率，暂不能提交。</p>
        <p v-if="fxError" role="alert">{{ fxError }}</p>
        <div class="ledger-actions">
          <button
            type="button"
            data-test="fx-fetch"
            :disabled="locked || !eligible"
            @click="fetchFX"
          >
            {{ fxError ? "重试获取" : "重新获取" }}
          </button>
          <button
            type="button"
            data-test="fx-manual"
            :disabled="locked"
            @click="manualFX"
          >
            手工录入
          </button>
        </div>
        <p v-if="fxMode === 'manual'">
          手工模式：请明确填写来源（例如
          manual:券商成交单）、正汇率和实际汇率日期。获取时间已设为本次手工记录时间，可修改。
        </p>
        <div class="ledger-grid">
          <label
            >汇率<input
              v-model="fx.rate"
              :readonly="fxMode !== 'manual'"
              name="fx_rate"
              inputmode="decimal"
              required
          /></label>
          <label
            >汇率日期<input
              v-model="fx.date"
              :readonly="fxMode !== 'manual'"
              name="fx_date"
              type="date"
              required
          /></label>
          <label
            >汇率来源<input
              v-model="fx.source"
              :readonly="fxMode !== 'manual'"
              name="fx_source"
              required
          /></label>
          <label
            >获取时间（含时区 RFC3339）<input
              v-model="fx.fetched_at"
              :readonly="fxMode !== 'manual'"
              name="fx_fetched_at"
              placeholder="2026-01-02T12:00:00Z"
              required
          /></label>
        </div>
      </fieldset>
      <label>备注<textarea v-model="note" name="note" /></label>
      <label
        >操作原因（必填）<input v-model="reason" name="reason" required
      /></label>
      <p v-if="validation" role="alert">{{ validation }}</p>
      <div class="ledger-actions">
        <button type="submit" :disabled="locked || (!!foreign && !fxReady)">
          {{ old ? "提交整笔更正" : "保存操作" }}</button
        ><button v-if="old" type="button" @click="emit('cancel')">
          取消更正
        </button>
      </div>
    </fieldset>
  </form>
</template>

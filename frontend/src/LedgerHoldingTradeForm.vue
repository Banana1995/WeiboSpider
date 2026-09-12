<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import {
  decimal,
  failure,
  PendingWrite,
  query,
  request,
  type Account,
  type FX,
  type FXQuote,
  type Instrument,
} from "./ledger";
import { opaqueID, todayShanghai, validDay, versionString } from "./ledgerView";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{
  account: Account;
  instrument: Instrument;
  expectedVersion: string;
  minDate: string;
  disabled: boolean;
}>();
const emit = defineEmits<{ saved: []; dirty: [boolean]; cancel: [] }>();
const workspace = useLedgerWorkspace();
const { locked, error, pending } = workspace;
const form = reactive({
  kind: "buy" as "buy" | "sell" | "dividend",
  date: todayShanghai(),
  quantity: "",
  price: "",
  fee: "",
  amount: "",
  note: "",
  reason: "",
});
const validation = ref("");
const writeError = ref("");
const basis = computed(() =>
  JSON.stringify([
    props.account.id,
    props.instrument.id,
    props.expectedVersion,
  ]),
);
const blockedBasis = ref("");
const refreshRequired = computed(() => blockedBasis.value === basis.value);
const unavailable = computed(
  () => props.disabled || locked.value || refreshRequired.value,
);
let ownWrite: PendingWrite<unknown> | undefined;
let ownBasis = "";
// Workspace exposes formatted errors, including their stable bracketed code.
watch(
  error,
  (value) => {
    if (!ownWrite || pending.value !== ownWrite) return;
    writeError.value = value;
    if (value.includes("[version_conflict]")) blockedBasis.value = ownBasis;
  },
  { flush: "sync" },
);
watch(basis, () => {
  validation.value = "";
  writeError.value = "";
});

const foreign = computed(
  () => props.instrument.currency !== props.account.currency,
);
const fx = reactive<FX>({ rate: "", date: "", source: "", fetched_at: "" });
const fxMode = ref<"reference" | "manual">("reference");
const fxLoading = ref(false);
const fxError = ref("");
const context = computed(() =>
  JSON.stringify([
    basis.value,
    props.instrument.currency,
    props.account.currency,
    form.date,
  ]),
);
const snapshotContext = ref("");
let generation = 0;
let controller: AbortController | undefined;
const draft = computed(() => JSON.stringify([form, fxMode.value, fx]));
const original = ref(draft.value);
watch(
  () => draft.value !== original.value,
  (value) => emit("dirty", value),
  { immediate: true, flush: "sync" },
);

function nonnegative(value: unknown, digits: number): value is string {
  return (
    typeof value === "string" &&
    decimal(value, digits) &&
    !value.startsWith("-")
  );
}
function positive(value: unknown, digits: number): value is string {
  return nonnegative(value, digits) && /[1-9]/.test(value);
}
function units(value: string, digits: number) {
  const [whole, fraction = ""] = value.split(".");
  return BigInt(whole! + fraction.padEnd(digits, "0"));
}
function sameDecimal(value: unknown, expected: string, digits: number) {
  return (
    nonnegative(value, digits) &&
    units(value, digits) === units(expected, digits)
  );
}
function validSnapshot(value: FX) {
  return (
    positive(value.rate, 8) &&
    validDay(value.date) &&
    value.date <= form.date &&
    typeof value.source === "string" &&
    !!value.source.trim() &&
    typeof value.fetched_at === "string" &&
    /^\d{4}-\d{2}-\d{2}T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
      value.fetched_at,
    ) &&
    validDay(value.fetched_at.slice(0, 10)) &&
    Number.isFinite(Date.parse(value.fetched_at)) &&
    Date.parse(value.fetched_at) <= Date.now()
  );
}
function validTradeDate() {
  return (
    validDay(props.minDate) &&
    validDay(form.date) &&
    form.date >= props.minDate &&
    form.date <= todayShanghai()
  );
}
const eligible = computed(() => foreign.value && validTradeDate());
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
  fxError.value = "";
}
async function fetchFX() {
  if (unavailable.value || !foreign.value || !validTradeDate()) return;
  abortFX();
  clearFX();
  fxMode.value = "reference";
  const captured = context.value;
  const businessDate = form.date;
  const base = props.instrument.currency;
  const quote = props.account.currency;
  const mode = businessDate === todayShanghai() ? "latest" : "historical";
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
    if (token !== generation || captured !== context.value || unavailable.value)
      return;
    const pair = base + quote;
    const inverse = !["USDCNY", "USDHKD", "HKDCNY"].includes(pair);
    const source = `Tencent/${mode === "latest" ? "spot" : "close"}/${inverse ? quote + base : pair}${inverse ? "/inverse" : ""}`;
    if (
      !result ||
      result.base !== base ||
      result.quote !== quote ||
      result.mode !== mode ||
      result.requested_date !== businessDate ||
      result.source !== source ||
      !validSnapshot(result)
    )
      throw new Error(
        "参考汇率响应与币种、模式、日期或来源不匹配，请重试或手工填写汇率",
      );
    Object.assign(fx, {
      rate: result.rate,
      date: result.date,
      source: result.source,
      fetched_at: result.fetched_at,
    });
    snapshotContext.value = captured;
  } catch (e) {
    if (token === generation && captured === context.value)
      fxError.value = failure(e);
  } finally {
    if (token === generation) fxLoading.value = false;
  }
}
function manualFX() {
  if (unavailable.value) return;
  abortFX();
  clearFX();
  fxMode.value = "manual";
  fx.source = "manual";
  fx.fetched_at = new Date().toISOString();
  snapshotContext.value = context.value;
}
watch(
  context,
  () => {
    abortFX();
    clearFX();
    fxMode.value = "reference";
  },
  { flush: "sync" },
);
watch(
  unavailable,
  (value) => {
    if (value) abortFX();
  },
  { flush: "sync" },
);
onBeforeUnmount(abortFX);

function save() {
  if (unavailable.value) return;
  validation.value = "";
  if (
    props.account.accounting_mode !== "reported" ||
    props.account.current_holdings_input !== "manual_snapshot"
  ) {
    validation.value =
      "仅手工 reported 持仓支持新增买入、卖出和分红，请刷新账户详情。";
    return;
  }
  if (
    !opaqueID(props.account.id) ||
    !opaqueID(props.instrument.id) ||
    !/^(0|[1-9]\d{0,18})$/.test(props.expectedVersion) ||
    BigInt(props.expectedVersion) >= 9223372036854775807n
  ) {
    validation.value = "账户、证券或持仓版本无效，请刷新详情。";
    return;
  }
  if (!validTradeDate()) {
    validation.value =
      "交易日期不得早于最早可录入日期，也不得晚于北京时间今天。";
    return;
  }
  if (
    form.kind === "dividend"
      ? !positive(form.amount, 2)
      : !positive(form.quantity, 6) ||
        !positive(form.price, 6) ||
        (form.fee !== "" && !nonnegative(form.fee, 2))
  ) {
    validation.value =
      "数量、成交价须为正数且最多 6 位小数；费用须非负、分红金额须为正数且最多 2 位小数，均不得超出服务范围。";
    return;
  }
  const encoder = new TextEncoder();
  if (
    !form.reason.trim() ||
    encoder.encode(form.reason).length > 512 ||
    encoder.encode(form.note).length > 4096
  ) {
    validation.value = "操作原因必填且最多 512 字节，备注最多 4096 字节。";
    return;
  }
  if (foreign.value && (!fxReady.value || !validSnapshot(fx))) {
    validation.value =
      "请查询参考汇率或手工填写有效汇率，实际汇率日期不得晚于交易日期；查询中不能保存。";
    return;
  }
  if (foreign.value && fxMode.value === "manual")
    fx.fetched_at = new Date().toISOString();
  const input = {
    expected_version: props.expectedVersion,
    kind: form.kind,
    date: form.date,
    ...(form.kind === "dividend"
      ? { amount: form.amount }
      : {
          quantity: form.quantity,
          price: form.price,
          ...(form.fee !== "" ? { fee: form.fee } : {}),
        }),
    ...(foreign.value ? { fx: { ...fx } } : {}),
    note: form.note,
    reason: form.reason,
  };
  const accountID = props.account.id;
  const instrumentID = props.instrument.id;
  ownBasis = basis.value;
  const submittedBasis = ownBasis;
  ownWrite = new PendingWrite(
    `/accounts/${accountID}/holdings/${instrumentID}/transactions`,
    "POST",
    input,
  );
  writeError.value = "";
  workspace.send(
    ownWrite,
    "持仓交易",
    (result) => {
      if (!result || typeof result !== "object") return false;
      const r = result as Record<string, unknown>;
      if (
        r.account_id !== accountID ||
        r.instrument_id !== instrumentID ||
        !versionString(r.version) ||
        BigInt(r.version) !== BigInt(input.expected_version) + 1n ||
        !r.transaction ||
        typeof r.transaction !== "object"
      )
        return false;
      const t = r.transaction as Record<string, unknown>;
      if (
        !opaqueID(t.id) ||
        t.source !== "manual" ||
        t.kind !== input.kind ||
        t.date !== input.date ||
        t.note !== input.note ||
        typeof t.cycle_id !== "string" ||
        !t.cycle_id.trim() ||
        encoder.encode(t.cycle_id).length > 512 ||
        /[\u0000-\u001f\u007f]/.test(t.cycle_id) ||
        !nonnegative(t.amount, 2)
      )
        return false;
      return "amount" in input
        ? t.quantity === null &&
            t.price === null &&
            t.fee === null &&
            sameDecimal(t.amount, input.amount, 2)
        : sameDecimal(t.quantity, input.quantity, 6) &&
            sameDecimal(t.price, input.price, 6) &&
            (input.fee === undefined
              ? t.fee === null
              : sameDecimal(t.fee, input.fee, 2));
    },
    () => {
      blockedBasis.value = submittedBasis;
      original.value = draft.value;
      emit("saved");
    },
  );
}
</script>

<template>
  <form class="lp-form" data-test="holding-trade-form" @submit.prevent="save">
    <p class="lp-field-hint">
      {{ account.name }} / {{ instrument.name }}（{{ instrument.code }}）
    </p>
    <p class="lp-field-hint">
      按含手续费的实际收付计算成本，保存仅更新当前现金和持仓数量，不改历史账户记录。现金不足时请先调整当前现金；请按成交单录入，不使用网络现价代替成交价。
    </p>
    <fieldset :disabled="unavailable">
      <div class="lp-segment lp-form-tabs" role="group" aria-label="操作类型">
        <button
          v-for="(label, kind) in {
            buy: '买入',
            sell: '卖出',
            dividend: '分红',
          }"
          :key="kind"
          type="button"
          name="kind"
          :value="kind"
          :aria-pressed="form.kind === kind"
          @click="form.kind = kind"
        >
          {{ label }}
        </button>
      </div>
      <label
        >交易日期<input
          v-model="form.date"
          name="date"
          type="date"
          :min="minDate"
          :max="todayShanghai()"
          required
      /></label>
      <p class="lp-field-hint">
        最早可录入 {{ minDate }}；同日按录入顺序追加，不允许插入更早日期。
      </p>
      <template v-if="form.kind !== 'dividend'">
        <div class="lp-two-fields">
          <label
            >数量<input
              v-model="form.quantity"
              name="quantity"
              inputmode="decimal"
              required
          /></label>
          <label
            >成交价（{{ instrument.currency }}）<input
              v-model="form.price"
              name="price"
              inputmode="decimal"
              required
          /></label>
        </div>
        <label
          >手续费（{{ instrument.currency }}，可选）<input
            v-model="form.fee"
            name="fee"
            inputmode="decimal"
            placeholder="留空为未录入；0 为明确零费用"
        /></label>
        <p class="lp-field-hint">
          手续费留空时，计算不计入费用，但保留“未录入”标记，不代表实际费用为零。请尽量填写真实费用。
        </p>
      </template>
      <label v-else
        >分红到账总额（{{ instrument.currency }}）<input
          v-model="form.amount"
          name="amount"
          inputmode="decimal"
          required
      /></label>
      <section v-if="foreign" class="lp-security-fields" aria-label="交易汇率">
        <p class="lp-field-hint">
          1 {{ instrument.currency }} 折合多少
          {{
            account.currency
          }}。参考汇率不是券商结算汇率；补录只查询不晚于交易日期的历史汇率，不以今天报价替代。
        </p>
        <div class="lp-segment lp-form-tabs">
          <button
            type="button"
            data-test="fx-fetch"
            :aria-pressed="fxMode === 'reference'"
            :disabled="!eligible || fxLoading"
            @click="fetchFX"
          >
            查询参考汇率
          </button>
          <button
            type="button"
            data-test="fx-manual"
            :aria-pressed="fxMode === 'manual'"
            @click="manualFX"
          >
            手工填写汇率
          </button>
        </div>
        <div class="lp-two-fields">
          <label
            >汇率<input
              v-model="fx.rate"
              name="fx_rate"
              inputmode="decimal"
              :readonly="fxMode !== 'manual'"
              required
          /></label>
          <label
            >实际汇率日期<input
              v-model="fx.date"
              name="fx_date"
              type="date"
              :max="form.date"
              :readonly="fxMode !== 'manual'"
              required
          /></label>
        </div>
        <p class="lp-field-hint">
          实际汇率日期：{{ fx.date || "未填写" }}；来源：{{
            fx.source || "未选择"
          }}；获取时间：{{ fx.fetched_at || "未获取" }}
        </p>
        <p v-if="fxLoading" role="status" class="lp-field-hint">
          正在查询参考汇率，暂不能保存。
        </p>
        <p v-if="fxError" role="alert" class="lp-error">{{ fxError }}</p>
      </section>
      <label
        >备注<textarea v-model="form.note" name="note" maxlength="1000" />
      </label>
      <label
        >操作原因（必填）<input
          v-model="form.reason"
          name="reason"
          maxlength="120"
          required
      /></label>
    </fieldset>
    <p v-if="validation" class="lp-error" role="alert">{{ validation }}</p>
    <p v-if="writeError" class="lp-error" role="alert">{{ writeError }}</p>
    <p v-if="refreshRequired" class="lp-error" role="alert">
      当前持仓版本已失效，请点击上方“刷新持仓依据（保留草稿）”，核对最新现金、数量及汇率后再保存；不能使用旧版本继续保存。
    </p>
    <p
      v-if="pending === ownWrite && locked"
      class="lp-field-hint"
      role="status"
    >
      正在确认本次保存。结果不确定时，请使用页面全局的原请求重试，不要重复录入。
    </p>
    <div class="lp-dialog-footer lp-form-tabs">
      <button
        type="submit"
        class="lp-primary"
        :disabled="unavailable || fxLoading"
      >
        保存
      </button>
      <button
        type="button"
        :disabled="disabled || locked"
        @click="emit('cancel')"
      >
        取消
      </button>
    </div>
  </form>
</template>

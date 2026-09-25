<script setup lang="ts">
import { computed, ref } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import LedgerSecurityDialog from "./LedgerSecurityDialog.vue";
import {
  decimal,
  errorText,
  failure,
  newID,
  PendingWrite,
  request,
  type Account,
  type FXQuote,
  type Instrument,
} from "./ledger";
import { holdingNumber } from "./holdings";
import { todayShanghai, validDay } from "./ledgerView";
import {
  positiveDecimal,
  stockLabels,
  tradePreview,
  validateStockBook,
  type StockBook,
  type StockEntry,
  type StockItem,
  type StockWriteResult,
} from "./stockBook";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{
  account: Account;
  book: StockBook;
  item?: StockItem;
  entry?: StockEntry;
  mode:
    "buy" | "sell" | "dividend" | "cash" | "void" | "opening" | "clear_opening";
}>();
const emit = defineEmits<{ close: []; saved: [] }>();
const { locked, error, send } = useLedgerWorkspace();
const version = ref(props.book.version);
const basis = ref(props.book);
const security = ref<Instrument | undefined>(props.item?.instrument);
const kind = ref(
  props.entry?.kind ??
    (props.mode === "sell"
      ? "sell"
      : props.mode === "dividend"
        ? "dividend"
        : "buy"),
);
const date = ref(props.entry?.date ?? todayShanghai());
const quantity = ref(
  props.entry?.quantity ??
    (props.mode === "opening" ? (props.item?.quantity ?? "") : ""),
);
const price = ref(props.entry?.price ?? "");
const fee = ref(props.entry?.fee ?? "0");
const amount = ref(
  props.mode === "cash" ? props.book.cash : (props.entry?.amount ?? ""),
);
const fx = ref(
  props.entry?.fx && positiveDecimal(props.entry.fx, 8)
    ? props.entry.fx
    : props.item?.instrument.currency === props.account.currency
      ? "1"
      : "",
);
const note = ref(props.entry?.note ?? "");
const validation = ref("");
const fxLoading = ref(false);
const fxInfo = ref("");
const refreshing = ref(false);
const initial = JSON.stringify([
  security.value,
  kind.value,
  date.value,
  quantity.value,
  price.value,
  fee.value,
  amount.value,
  fx.value,
  note.value,
]);
const dirty = computed(
  () =>
    initial !==
    JSON.stringify([
      security.value,
      kind.value,
      date.value,
      quantity.value,
      price.value,
      fee.value,
      amount.value,
      fx.value,
      note.value,
    ]),
);
const title = computed(() =>
  props.mode === "cash"
    ? "更新现金余额"
    : props.mode === "void"
      ? "删除交易记录"
      : props.mode === "clear_opening"
        ? "移除旧持仓"
        : props.mode === "opening"
          ? "补录初始买入"
          : props.entry
            ? "修改交易记录"
            : props.mode === "buy"
              ? "买入 / 加仓"
              : props.mode === "sell"
                ? "卖出 / 减仓"
                : "记录分红",
);
const preview = computed(() =>
  tradePreview(quantity.value, price.value, fee.value, kind.value),
);
const foreign = computed(
  () => security.value && security.value.currency !== props.account.currency,
);
const requiresFX = computed(
  () =>
    !!foreign.value &&
    (!!props.entry?.event ||
      (!!basis.value.cash_date &&
        props.mode !== "opening" &&
        !props.entry?.opening &&
        date.value >= basis.value.cash_date &&
        !(
          props.entry?.fx === "0.00000000" && date.value === props.entry.date
        ))),
);
error.value = "";
function selected(i: Instrument) {
  security.value =
    basis.value.items.find(
      (row) =>
        row.instrument.market === i.market && row.instrument.code === i.code,
    )?.instrument ?? i;
  fx.value = i.currency === props.account.currency ? "1" : "";
}
async function loadFX() {
  if (!security.value || !foreign.value || !validDay(date.value)) return;
  fxLoading.value = true;
  validation.value = "";
  try {
    const day = props.entry?.event?.pay_date ?? date.value;
    const mode = day >= todayShanghai() ? "latest" : "historical";
    const q = await request<FXQuote>(
      `/fx?base=${security.value.currency}&quote=${props.account.currency}&mode=${mode}${mode === "historical" ? `&date=${day}` : ""}`,
    );
    if (
      q.base !== security.value.currency ||
      q.quote !== props.account.currency ||
      !positiveDecimal(q.rate, 8)
    )
      throw new Error("汇率响应无效");
    fx.value = q.rate;
    fxInfo.value = `${q.date} ${q.source}，可按实际结算汇率修改`;
  } catch (e) {
    validation.value = errorText(failure(e));
  } finally {
    fxLoading.value = false;
  }
}
async function reloadBasis() {
  if (locked.value || refreshing.value) return;
  refreshing.value = true;
  try {
    const b = validateStockBook(
      await request<StockBook>(`/accounts/${props.account.id}/stock-book`),
      props.account,
    );
    version.value = b.version;
    basis.value = b;
    error.value = "";
    if (security.value && !props.entry)
      security.value =
        b.items.find(
          (i) =>
            i.instrument.market === security.value?.market &&
            i.instrument.code === security.value?.code,
        )?.instrument ?? security.value;
    validation.value = `依据已刷新，当前现金 ${holdingNumber(b.cash, 2)} ${b.currency}。请核对填写内容后保存。`;
  } catch (e) {
    validation.value = errorText(failure(e));
  } finally {
    refreshing.value = false;
  }
}
function save() {
  if (locked.value || refreshing.value || fxLoading.value) return;
  validation.value = "";
  const action =
    props.mode === "cash"
      ? "cash"
      : props.mode === "clear_opening"
        ? "clear_opening"
        : props.mode === "void"
          ? "void"
          : props.entry
            ? "replace"
            : "create";
  const id =
    action === "cash"
      ? ""
      : action === "clear_opening"
        ? props.item!.instrument.id
        : (props.entry?.id ?? newID());
  const payload: Record<string, unknown> = {
    action,
    expected_version: version.value,
    id,
  };
  if (action === "cash") {
    if (!decimal(amount.value, 2) || amount.value.startsWith("-")) {
      validation.value = "请填写非负现金余额，最多两位小数。";
      return;
    }
    payload.cash = amount.value;
  } else if (action !== "void" && action !== "clear_opening") {
    const i = security.value;
    if (
      !i ||
      !validDay(date.value) ||
      date.value > todayShanghai() ||
      (requiresFX.value && !positiveDecimal(fx.value, 8)) ||
      (kind.value === "dividend"
        ? !positiveDecimal(amount.value, 2)
        : preview.value === null)
    ) {
      validation.value = "请填写有效日期、数量、价格及汇率；分红填写正金额。";
      return;
    }
    payload.entry = {
      instrument_id: i.id,
      kind: kind.value,
      date: date.value,
      quantity: kind.value === "dividend" ? "0" : quantity.value,
      price: kind.value === "dividend" ? "0" : price.value,
      fee: kind.value === "dividend" ? "0" : fee.value,
      amount: kind.value === "dividend" ? amount.value : "0",
      fx: foreign.value
        ? requiresFX.value || positiveDecimal(fx.value, 8)
          ? fx.value
          : "0"
        : "1",
      note: note.value,
    };
    if (
      action === "create" &&
      !basis.value.items.some((row) => row.instrument.id === i.id)
    )
      payload.security = i;
    if (props.mode === "opening") payload.replace_opening = true;
  }
  const expected = (BigInt(version.value) + 1n).toString();
  send(
    new PendingWrite<StockWriteResult>(
      `/accounts/${props.account.id}/stock-book`,
      "POST",
      payload,
    ),
    title.value,
    (r) =>
      r?.account_id === props.account.id &&
      r.id === id &&
      r.action === action &&
      r.version === expected,
    () => {
      emit("saved");
      emit("close");
    },
  );
}
</script>

<template>
  <LedgerDialog
    :title="title"
    :caption="security ? `${security.name} ${security.code}` : account.name"
    :dirty="dirty"
    @close="emit('close')"
    v-slot="{ requestClose }"
  >
    <form class="lp-form stock-trade-form" @submit.prevent="save">
      <fieldset :disabled="locked || refreshing">
        <template v-if="mode === 'void' || mode === 'clear_opening'">
          <p>
            {{
              mode === "void"
                ? "删除后会按剩余有效交易重新计算股数、成本、分红和现金。修改历史保留。"
                : "移除这条没有买入明细的旧持仓，之后可以重新填写买入日期、数量和价格。"
            }}
          </p>
          <p v-if="entry">
            {{ entry.date }} · {{ stockLabels[entry.kind] }} ·
            {{ holdingNumber(entry.amount, 2) }} {{ security?.currency }}
          </p>
        </template>
        <template v-else-if="mode === 'cash'">
          <label
            >当前现金余额（{{ account.currency }}）<input
              v-model="amount"
              name="cash"
              inputmode="decimal"
              required
              autofocus
          /></label>
          <p class="lp-field-hint">
            填写现在实际剩余的现金。以这笔余额为基准处理之后的买卖和到账分红，之前的交易不再追扣。
          </p>
        </template>
        <template v-else>
          <LedgerSecurityDialog
            v-if="!security"
            @selected="selected"
            @close="emit('close')"
          />
          <template v-else>
            <div class="lp-two-fields">
              <label
                >交易日期<input
                  v-model="date"
                  name="date"
                  type="date"
                  required
                  :max="todayShanghai()"
                  :disabled="!!entry?.event"
              /></label>
              <label v-if="kind === 'dividend'"
                >分红金额（{{ security.currency }}）<input
                  v-model="amount"
                  name="amount"
                  inputmode="decimal"
                  required
              /></label>
              <label v-else
                >数量（股）<input
                  v-model="quantity"
                  name="quantity"
                  inputmode="decimal"
                  required
                  :disabled="mode === 'opening'"
              /></label>
            </div>
            <div v-if="kind !== 'dividend'" class="lp-two-fields">
              <label
                >成交价格（{{ security.currency }}）<input
                  v-model="price"
                  name="price"
                  inputmode="decimal"
                  required
              /></label>
              <label
                >手续费（{{ security.currency }}）<input
                  v-model="fee"
                  name="fee"
                  inputmode="decimal"
                  required
              /></label>
            </div>
            <p
              v-if="preview && kind !== 'dividend'"
              class="stock-trade-preview"
            >
              {{ kind === "buy" ? "买入支出" : "卖出净回款" }}
              <strong>{{ holdingNumber(preview, 2) }}</strong>
              {{ security.currency }}
            </p>
            <div v-if="requiresFX" class="stock-fx-field">
              <label
                >结算汇率（1 {{ security.currency }} =
                {{ account.currency }}）<input
                  v-model="fx"
                  name="fx"
                  inputmode="decimal"
                  required
              /></label>
              <button type="button" :disabled="fxLoading" @click="loadFX">
                {{ fxLoading ? "查询中…" : "读取参考汇率" }}
              </button>
              <small v-if="fxInfo">{{ fxInfo }}</small>
            </div>
            <p v-if="entry?.event" class="lp-field-hint">
              公告派息日
              {{
                entry.event.pay_date
              }}。修改金额后保留您的填写，自动同步不再覆盖这笔分红。
            </p>
            <p v-else-if="kind === 'dividend'" class="lp-field-hint">
              填写实际到账日期。系统会自动匹配同一日期的分红，已有自动分红请直接修改原记录。
            </p>
            <p
              v-if="!book.cash_date || mode === 'opening'"
              class="lp-field-hint"
            >
              初始持仓计入股票成本和收益。现金以您填写的余额为基准，历史买入不追扣。
            </p>
            <label
              >备注（选填）<textarea
                v-model="note"
                name="note"
                rows="2"
                maxlength="2000"
              />
            </label>
          </template>
        </template>
        <p v-if="validation" class="lp-error" role="alert">{{ validation }}</p>
        <button
          v-if="error.includes('version_conflict')"
          type="button"
          @click="reloadBasis"
        >
          刷新依据并保留填写
        </button>
        <div class="lp-dialog-footer" v-if="security || mode === 'cash'">
          <button type="button" @click="requestClose">取消</button>
          <button
            type="submit"
            :class="
              mode === 'void' || mode === 'clear_opening'
                ? 'lp-danger-button'
                : 'lp-primary'
            "
            :disabled="fxLoading || error.includes('version_conflict')"
          >
            {{
              mode === "void" || mode === "clear_opening"
                ? "确认删除"
                : "保存记录"
            }}
          </button>
        </div>
      </fieldset>
    </form>
  </LedgerDialog>
</template>

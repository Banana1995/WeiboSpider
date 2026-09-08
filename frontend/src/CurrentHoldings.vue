<script setup lang="ts">
import { reactive, ref, shallowRef, watch } from "vue";
import {
  decimal,
  failure,
  LedgerError,
  PendingWrite,
  request,
  type Instrument,
} from "./ledger";
import {
  validateCurrentHoldings,
  type CurrentHoldings,
  type CurrentHoldingsInput,
  type CurrentPosition,
} from "./currentHoldings";
import { useLedgerRead } from "./useLedgerRead";

const props = defineProps<{
  accountId: string;
  currency: string;
  instruments: Instrument[];
  disabled: boolean;
  refreshKey: number;
}>();
const emit = defineEmits<{
  locked: [boolean];
  configured: [boolean, string];
  saved: [];
}>();
const read = reactive(useLedgerRead<CurrentHoldings>());
const cash = ref("0.00");
const positions = ref<CurrentPosition[]>([]);
const pending = shallowRef<PendingWrite<CurrentHoldings>>();
const busy = ref(false);
const error = ref("");
const message = ref("");
let generation = 0;
async function load() {
  if (pending.value) return;
  const current = ++generation;
  const id = props.accountId;
  read.clear();
  emit("configured", false, "");
  await read.load(async (signal) =>
    validateCurrentHoldings(
      await request<CurrentHoldings>(`/accounts/${id}/current-holdings`, {
        signal,
      }),
      id,
    ),
  );
  if (
    current !== generation ||
    props.accountId !== id ||
    !read.data ||
    read.error
  )
    return;
  cash.value = read.data.snapshot?.cash ?? "0.00";
  positions.value = read.data.snapshot?.positions.map((p) => ({ ...p })) ?? [];
  emit("configured", read.data.snapshot !== null, read.data.audit_id);
}
watch(
  () => [props.accountId, props.refreshKey],
  () => {
    error.value = "";
    message.value = "";
    void load();
  },
  { immediate: true },
);
async function save() {
  if (
    busy.value ||
    (!pending.value &&
      (props.disabled || read.loading || read.error || !read.data))
  )
    return;
  error.value = "";
  if (!pending.value) {
    if (
      !decimal(cash.value, 2) ||
      positions.value.some(
        (p) =>
          !props.instruments.some((i) => i.id === p.instrument_id) ||
          !decimal(p.quantity, 6) ||
          !/[1-9]/.test(p.quantity),
      ) ||
      new Set(positions.value.map((p) => p.instrument_id)).size !==
        positions.value.length
    ) {
      error.value =
        "请填写非负现金（最多 2 位小数）、正数量（最多 6 位小数），证券不得重复。";
      return;
    }
    const input: CurrentHoldingsInput = {
      expected_version: read.data!.snapshot?.version ?? "0",
      cash: cash.value,
      positions: positions.value.map((p) => ({ ...p })),
    };
    pending.value = new PendingWrite(
      `/accounts/${props.accountId}/current-holdings`,
      "PUT",
      input,
    );
    emit("locked", true);
  }
  busy.value = true;
  try {
    const result = await pending.value.run();
    try {
      validateCurrentHoldings(result, props.accountId);
      const input = JSON.parse(pending.value.body) as CurrentHoldingsInput;
      const units = (v: string, scale: number) => {
        const [w, f = ""] = v.split(".");
        return BigInt(w! + f.padEnd(scale, "0"));
      };
      const s = result.snapshot;
      if (
        !s ||
        BigInt(s.version) !== BigInt(input.expected_version) + 1n ||
        units(s.cash, 2) !== units(input.cash, 2) ||
        s.positions.length !== input.positions.length ||
        input.positions.some(
          (p) =>
            !s.positions.some(
              (q) =>
                q.instrument_id === p.instrument_id &&
                units(q.quantity, 6) === units(p.quantity, 6),
            ),
        )
      )
        throw new LedgerError("invalid_response");
    } catch (e) {
      pending.value.uncertain = true;
      throw e;
    }
  } catch (e) {
    error.value = failure(e);
    if (!pending.value.uncertain) {
      pending.value = undefined;
      emit("locked", false);
    }
    return;
  } finally {
    busy.value = false;
  }
  pending.value = undefined;
  emit("locked", false);
  emit("saved");
  await load();
  message.value =
    "当前持仓已保存；未新增交易、资金流或总资产记录。可另行预览或更新并保存总资产。";
}
</script>

<template>
  <section class="ledger-panel" data-test="current-holdings">
    <h2>当前持仓</h2>
    <p>
      在这个账户内维护当前现金与证券数量，无需补录历史交易或成本。保存只更新今后的估值来源，不改变
      Excel 原始记录、历史总资产或资金流。手工总资产记录仍独立填写。
    </p>
    <p v-if="read.loading" role="status">正在读取当前持仓…</p>
    <p v-if="read.error || error" role="alert">{{ read.error || error }}</p>
    <p v-if="message" role="status">{{ message }}</p>
    <p v-if="read.data?.snapshot">
      版本 {{ read.data.snapshot.version }} · 保存于
      {{ read.data.snapshot.saved_at }} · 审计 #{{ read.data.audit_id }}
    </p>
    <p v-else-if="read.data">
      尚未设置当前持仓。零现金且无证券也可保存为明确的空持仓来源。
    </p>
    <button
      type="button"
      :disabled="disabled || !!pending || read.loading"
      @click="load"
    >
      重新读取当前持仓（放弃未保存编辑）
    </button>
    <form data-test="current-holdings-form" @submit.prevent="save">
      <fieldset
        :disabled="
          disabled || !!pending || read.loading || !!read.error || !read.data
        "
      >
        <label
          >当前现金（{{ currency }}）<input
            v-model="cash"
            name="current_cash"
            inputmode="decimal"
            required
        /></label>
        <div
          v-for="(p, index) in positions"
          :key="index"
          class="ledger-grid ledger-opening"
        >
          <label
            >证券<select
              v-model="p.instrument_id"
              :name="`current_instrument_${index}`"
              required
            >
              <option value="">请选择已登记证券</option>
              <option v-for="i in instruments" :key="i.id" :value="i.id">
                {{ i.name }} · {{ i.market }} / {{ i.code }} · {{ i.currency }}
              </option>
            </select></label
          >
          <label
            >当前数量<input
              v-model="p.quantity"
              :name="`current_quantity_${index}`"
              inputmode="decimal"
              required
          /></label>
          <button type="button" @click="positions.splice(index, 1)">
            移除此证券
          </button>
        </div>
        <div class="ledger-actions">
          <button
            type="button"
            :disabled="positions.length >= 200"
            data-test="current-add"
            @click="positions.push({ instrument_id: '', quantity: '' })"
          >
            添加当前证券
          </button>
          <button type="submit">保存当前持仓</button>
        </div>
      </fieldset>
    </form>
    <div v-if="pending" class="ledger-pending" role="status">
      <p>
        写入结果待确认，账户、原始内容、版本和幂等键已锁定。请勿关闭或刷新页面。
      </p>
      <button
        type="button"
        :disabled="busy"
        data-test="current-retry"
        @click="save"
      >
        按原请求重试确认持仓
      </button>
    </div>
  </section>
</template>

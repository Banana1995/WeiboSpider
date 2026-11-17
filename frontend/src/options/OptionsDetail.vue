<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import {
  clone,
  candidateSide,
  capitalLabel,
  money,
  write,
  type CalculationInput,
  type Candidate,
} from "../options";
import OptionsResult from "./OptionsResult.vue";
import OptionsNumber from "./OptionsNumber.vue";
const props = defineProps<{ value: Candidate; saving: boolean }>();
const isCall = computed(() => candidateSide(props.value) === "CALL");
const emit = defineEmits<{ favorite: [value: Candidate]; refresh: [] }>();
const input = reactive<CalculationInput>(clone(props.value.input));
const result = ref<Candidate | null>(clone(props.value));
const error = ref("");
const loading = ref(false);
const override = ref(false),
  interest = ref(false),
  scenario = ref(false);
const fee = ref("");
const interestValues = reactive({
  principal: isCall.value ? "" : (props.value.result.capital ?? ""),
  annual_rate: "",
  days: Math.max(0, props.value.result.dte),
});
const scenarioValues = reactive<{
  kind: "expiry" | "close";
  price: string;
  fee: string;
  interest: string;
  stockCost: string;
}>({ kind: "expiry", price: "", fee: "", interest: "", stockCost: "" });
let controller: AbortController | undefined;
const requestInput = computed<CalculationInput>(() => ({
  ...input,
  price: input.basis === "custom" ? input.price : null,
  fee_version: override.value ? 0 : input.fee_version,
  fee_override: override.value && fee.value !== "" ? fee.value : null,
  interest: interest.value ? { ...interestValues } : null,
  scenario: scenario.value
    ? {
        kind: scenarioValues.kind,
        price: scenarioValues.price,
        fee: scenarioValues.fee || null,
        interest: scenarioValues.interest || null,
        ...(isCall.value &&
        scenarioValues.kind === "expiry" &&
        scenarioValues.stockCost !== ""
          ? { stock_cost: scenarioValues.stockCost }
          : {}),
      }
    : null,
}));
watch(
  requestInput,
  (value, _, onCleanup) => {
    controller?.abort();
    const current = new AbortController();
    controller = current;
    result.value = null;
    error.value = "";
    loading.value = true;
    const timer = setTimeout(async () => {
      try {
        const next = await write<Candidate>(
          "/calculations",
          {
            snapshot_id: props.value.snapshot.id,
            contract_code: props.value.quote.contract.code,
            input: value,
          },
          "POST",
          current.signal,
        );
        if (!current.signal.aborted) result.value = next;
      } catch (e) {
        if (!current.signal.aborted)
          error.value = e instanceof Error ? e.message : "测算失败";
      } finally {
        if (!current.signal.aborted) loading.value = false;
      }
    }, 180);
    onCleanup(() => {
      clearTimeout(timer);
      current.abort();
    });
  },
  { deep: true },
);
onBeforeUnmount(() => controller?.abort());
</script>

<template>
  <OptionsResult v-if="result" :value="result" />
  <p v-else class="opt-notice" role="status">
    {{ loading ? "正在测算…" : error || "请输入有效测算参数" }}
  </p>
  <div class="opt-detail-form">
    <label class="opt-field"
      >成交价格假设<select v-model="input.basis">
        <option value="bid">卖出参考价 Bid（对手方买价）</option>
        <option value="mid">中间价 Mid</option>
        <option value="custom">自定义价格</option>
      </select></label
    >
    <label class="opt-field"
      >每股权利金（USD）<OptionsNumber
        v-if="input.basis === 'custom'"
        v-model="input.price"
        type="number"
        min="0.000001"
        step="0.000001"
        placeholder="输入假设价格" /><input
        v-else
        :value="result?.result.price ?? ''"
        readonly
        aria-label="快照成交参考价"
    /></label>
    <label class="opt-field"
      >卖出份数<input
        v-model.number="input.quantity"
        type="number"
        min="1"
        max="10000"
        step="1"
    /></label>
    <label class="opt-field"
      >资金口径<select v-model="input.mode">
        <option value="full">
          {{ capitalLabel(value.quote.contract.type) }}
        </option>
        <option value="net">净投入参考值</option>
      </select></label
    >
  </div>
  <div class="opt-inline">
    <label
      ><input v-model="override" type="checkbox" /> 自定义本单开仓费用</label
    ><label v-if="override" class="opt-field"
      >开仓费用（USD）<OptionsNumber
        v-model="fee"
        type="number"
        min="0"
        step="0.01"
        placeholder="留空：税费前"
    /></label>
  </div>
  <p class="opt-help">
    卖出价 Bid / 买入价 Ask：{{ money(value.quote.bid) }} /
    {{ money(value.quote.ask) }} USD。修改假设只重新计算当前快照。
  </p>
  <div class="opt-note">
    <label><input v-model="interest" type="checkbox" /> 另外测算现金利息</label>
    <p v-if="isCall && interest" class="opt-help">
      此处仅填写独立现金的计息假设，持有的股票市值不作为现金计息。
    </p>
    <div v-if="interest" class="opt-detail-form">
      <label class="opt-field"
        >计息本金（USD）<OptionsNumber
          v-model="interestValues.principal"
          type="number"
          min="0"
          step="0.01" /></label
      ><label class="opt-field"
        >假设年利率（%）<OptionsNumber
          v-model="interestValues.annual_rate"
          type="number"
          min="0"
          max="100"
          step="0.01" /></label
      ><label class="opt-field"
        >计息日数<input
          v-model.number="interestValues.days"
          type="number"
          min="0"
          max="3650"
          step="1"
      /></label>
    </div>
  </div>
  <details class="opt-details">
    <summary>
      {{
        isCall ? "到期交股与提前平仓情景，选看" : "到期接货与提前平仓情景，选看"
      }}
    </summary>
    <label><input v-model="scenario" type="checkbox" /> 启用独立情景</label>
    <div v-if="scenario" class="opt-detail-form">
      <label class="opt-field"
        >情景<select v-model="scenarioValues.kind">
          <option value="expiry">持有至到期</option>
          <option value="close">提前平仓</option>
        </select></label
      ><label class="opt-field"
        >{{
          scenarioValues.kind === "expiry" ? "假设到期股价" : "假设回购价"
        }}（USD）<OptionsNumber
          v-model="scenarioValues.price"
          type="number"
          min="0"
          step="0.01"
          :placeholder="
            scenarioValues.kind === 'close'
              ? `当前 Ask ${money(value.quote.ask)}`
              : '输入假设股价'
          " /></label
      ><label class="opt-field"
        >{{
          scenarioValues.kind === "expiry" ? "指派等额外费用" : "平仓费用"
        }}（USD）<OptionsNumber
          v-model="scenarioValues.fee"
          type="number"
          min="0"
          step="0.01"
          placeholder="留空：按费用方案／未知" /></label
      ><label class="opt-field"
        >情景期间利息（USD）<OptionsNumber
          v-model="scenarioValues.interest"
          type="number"
          min="0"
          step="0.01"
          placeholder="留空：未计入"
      /></label>
      <label v-if="isCall && scenarioValues.kind === 'expiry'" class="opt-field"
        >假设持股成本／股（USD）<OptionsNumber
          v-model="scenarioValues.stockCost"
          min="0"
          step="0.01"
          placeholder="留空：只算期权部分"
      /></label>
    </div>
  </details>
  <div class="opt-dialog-actions">
    <button class="opt-btn" type="button" @click="emit('refresh')">
      更新该股行情</button
    ><button
      class="opt-btn opt-primary"
      :disabled="!result || loading || saving"
      @click="result && emit('favorite', result)"
    >
      {{ saving ? "收藏中…" : "收藏此记录" }}
    </button>
  </div>
</template>

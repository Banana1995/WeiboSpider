<script setup lang="ts">
import {
  clone,
  emptyFee,
  capitalLabel,
  type OptionSide,
  type FeeRule,
  type Settings,
} from "../options";
import { reactive, ref } from "vue";
import OptionsNumber from "./OptionsNumber.vue";
const props = defineProps<{
  value: Settings;
  saving: boolean;
  optionType: OptionSide;
}>();
const emit = defineEmits<{ save: [value: Settings] }>();
const draft = reactive<Settings>(clone(props.value));
const enabled = ref(!!draft.fees);
const fees = reactive(
  draft.fees ?? {
    name: "我的费用方案",
    source: "",
    open: emptyFee(),
    close: null,
    assignment: null,
  },
);
const fields: [keyof FeeRule, string][] = [
  ["per_contract", "每份佣金"],
  ["minimum", "每单最低佣金"],
  ["fixed", "每单固定／平台费"],
  ["regulatory_per_contract", "每份监管／交易所费"],
];
function save() {
  emit("save", { ...clone(draft), fees: enabled.value ? clone(fees) : null });
}
</script>

<template>
  <form @submit.prevent="save">
    <div class="opt-detail-form">
      <label class="opt-field"
        >默认年化资金口径<select v-model="draft.mode">
          <option value="full">{{ capitalLabel(optionType) }}</option>
          <option value="net">净投入参考值</option>
        </select></label
      ><label class="opt-field"
        >手动刷新覆盖未来天数<input
          v-model.number="draft.horizon_days"
          type="number"
          min="1"
          max="1095"
          required
      /></label>
    </div>
    <p class="opt-help">
      采集范围会在下次手动更新时生效。仅点击“更新行情”获取报价。
    </p>
    <label class="opt-checkbox"
      ><input v-model="enabled" type="checkbox" />
      已确认我的费用方案，计入预计费用</label
    >
    <template v-if="enabled"
      ><div class="opt-detail-form">
        <label class="opt-field"
          >方案名称<input v-model="fees.name" maxlength="120" required /></label
        ><label class="opt-field"
          >来源／账户套餐说明<input
            v-model="fees.source"
            maxlength="1000"
            placeholder="按你的实际账户费率填写"
        /></label>
      </div>
      <h3>开仓费用（USD）</h3>
      <div class="opt-detail-form">
        <label v-for="[key, label] in fields" :key="key" class="opt-field"
          >{{ label
          }}<OptionsNumber
            v-model="fees.open[key]"
            type="number"
            min="0"
            step="0.000001"
            required
            placeholder="已知免收时填 0"
        /></label>
      </div>
      <p class="opt-help">
        总费用 = max（每份佣金 ×
        份数，每单最低佣金）＋每单固定费＋每份监管／交易所费 ×
        份数。未知费用保持未配置。
      </p>
      <div
        v-for="[kind, title] in [
          ['close', '平仓'],
          ['assignment', '指派'],
        ] as const"
        :key="kind"
        class="opt-note"
      >
        <label
          ><input
            :checked="!!fees[kind]"
            type="checkbox"
            @change="
              fees[kind] = ($event.target as HTMLInputElement).checked
                ? emptyFee()
                : null
            "
          />
          已确认{{ title }}费用</label
        >
        <div v-if="fees[kind]" class="opt-detail-form">
          <label v-for="[key, label] in fields" :key="key" class="opt-field"
            >{{ label
            }}<OptionsNumber
              v-model="fees[kind]![key]"
              type="number"
              min="0"
              step="0.000001"
              required
          /></label>
        </div>
      </div>
    </template>
    <div class="opt-dialog-actions">
      <button class="opt-btn opt-primary" :disabled="saving" type="submit">
        {{ saving ? "保存中…" : "保存设置" }}
      </button>
    </div>
  </form>
</template>

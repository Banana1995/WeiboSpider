<script setup lang="ts">
import { computed } from "vue";
import type { ChannelAsset } from "./accountRecords";
import { channelAssetsTotal, maxAccountChannels } from "./ledgerChannels";
import { money } from "./ledgerView";

const props = defineProps<{
  modelValue: ChannelAsset[];
  previous: ChannelAsset[];
  sourceDate: string;
  currency: string;
}>();
const emit = defineEmits<{ "update:modelValue": [value: ChannelAsset[]] }>();
const total = computed(() => channelAssetsTotal(props.modelValue));
const previous = computed(
  () => new Map(props.previous.map((c) => [c.name, c.amount])),
);
function update(index: number, field: "name" | "amount", event: Event) {
  const value = (event.target as HTMLInputElement).value;
  emit(
    "update:modelValue",
    props.modelValue.map((item, i) =>
      i === index ? { ...item, [field]: value } : item,
    ),
  );
}
function reuse() {
  emit(
    "update:modelValue",
    props.modelValue.map((item) => ({
      ...item,
      amount: previous.value.get(item.name) ?? item.amount,
    })),
  );
}
</script>

<template>
  <section class="lp-channel-editor" aria-label="渠道资产">
    <div class="lp-channel-heading">
      <strong
        >渠道资产 <small>{{ currency }}</small></strong
      >
      <button
        v-if="previous.size"
        type="button"
        class="lp-text-button"
        @click="reuse"
      >
        填入上次金额
      </button>
    </div>
    <p v-if="sourceDate" class="lp-field-hint">
      上次渠道记录：{{ sourceDate }}
    </p>
    <div v-for="(item, i) in modelValue" :key="i" class="lp-channel-row">
      <label>
        <span class="sr-only">渠道名称 {{ i + 1 }}</span>
        <input
          :value="item.name"
          :aria-label="`渠道名称 ${i + 1}`"
          placeholder="例如：证券账户、钱包"
          maxlength="128"
          required
          @input="update(i, 'name', $event)"
        />
        <small v-if="previous.has(item.name)"
          >上次：{{ money(previous.get(item.name)) }}</small
        >
      </label>
      <label>
        <span class="sr-only">{{ item.name || `渠道 ${i + 1}` }}资产金额</span>
        <input
          :value="item.amount"
          :aria-label="`${item.name || `渠道 ${i + 1}`}资产金额`"
          inputmode="decimal"
          placeholder="填写当前金额"
          required
          @input="update(i, 'amount', $event)"
        />
      </label>
      <button
        type="button"
        class="lp-text-button"
        :aria-label="`移除渠道 ${item.name || i + 1}`"
        @click="
          emit(
            'update:modelValue',
            modelValue.filter((_, n) => n !== i),
          )
        "
      >
        移除
      </button>
    </div>
    <div class="lp-channel-heading lp-channel-total">
      <button
        type="button"
        class="lp-text-button"
        :disabled="modelValue.length >= maxAccountChannels"
        @click="
          emit('update:modelValue', [...modelValue, { name: '', amount: '' }])
        "
      >
        ＋ 添加渠道
      </button>
      <span
        >合计资产 <output aria-live="polite">{{ money(total) }}</output>
        <small>{{ currency }}</small></span
      >
    </div>
  </section>
</template>

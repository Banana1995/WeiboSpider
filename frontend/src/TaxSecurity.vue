<script setup lang="ts">
import { computed } from "vue";
import { securityIdentity, type TaxRecord } from "./tax";
const props = defineProps<{
  record: TaxRecord;
  names: Record<string, string>;
  loading?: boolean;
}>();
const identity = computed(() => securityIdentity(props.record));
const name = computed(() =>
  identity.value.key
    ? props.names[identity.value.key] ||
      (props.loading ? "查询中…" : "名称暂缺")
    : props.record.reason || props.record.kind,
);
</script>
<template>
  <span class="tax-security"
    ><strong>{{ name }}</strong
    ><small v-if="identity.code"
      >{{ identity.code
      }}<span v-if="identity.market"> · {{ identity.market }}</span></small
    ></span
  >
</template>

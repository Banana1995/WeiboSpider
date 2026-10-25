<script setup lang="ts">
import { computed, ref } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import PortfolioForm from "./PortfolioForm.vue";
import type { Account } from "./ledger";
import type { Portfolio } from "./ledgerPortfolios";

const props = defineProps<{
  accounts: Account[];
  portfolio?: Portfolio;
  remove?: boolean;
}>();
const emit = defineEmits<{
  close: [];
  saved: [id: string];
  deleted: [];
  reload: [];
}>();
const dirty = ref(false);
const title = computed(() =>
  props.remove
    ? "删除账户组合"
    : props.portfolio
      ? "编辑账户组合"
      : "新建账户组合",
);
</script>

<template>
  <LedgerDialog
    :title="title"
    :dirty="dirty"
    @close="emit('close')"
    v-slot="{ requestClose }"
  >
    <PortfolioForm
      :accounts="accounts"
      :portfolio="portfolio"
      :remove="remove"
      @update:dirty="dirty = $event"
      @close="requestClose"
      @saved="emit('saved', $event)"
      @deleted="emit('deleted')"
      @reload="emit('reload')"
    />
  </LedgerDialog>
</template>

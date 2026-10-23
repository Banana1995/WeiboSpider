<script setup lang="ts">
import { defineAsyncComponent } from "vue";
const App = defineAsyncComponent(() => import("./App.vue"));
const Ledger = defineAsyncComponent(() => import("./Ledger.vue"));
const Tax = defineAsyncComponent(() => import("./Tax.vue"));
const LedgerPrototype = defineAsyncComponent(
  () => import("./prototype/LedgerPrototype.vue"),
);
const path = window.location.pathname.replace(/\/$/, "");
const ledger = path === "/ledger";
const tax = path === "/tax";
const prototype = path === "/ledger-prototype";
document.title = tax
  ? "税务计算 · 观价"
  : prototype
    ? "账本交互原型 · 观价"
    : ledger
      ? "投资账本 · 观价"
      : "白酒行情 · 观价";
</script>

<template>
  <Tax v-if="tax" />
  <LedgerPrototype v-else-if="prototype" />
  <Ledger v-else-if="ledger" />
  <App v-else />
</template>

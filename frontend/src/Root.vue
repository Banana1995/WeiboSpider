<script setup lang="ts">
import { defineAsyncComponent } from "vue";
const App = defineAsyncComponent(() => import("./App.vue"));
const Ledger = defineAsyncComponent(() => import("./Ledger.vue"));
const LedgerPrototype = defineAsyncComponent(
  () => import("./prototype/LedgerPrototype.vue"),
);
const path = window.location.pathname.replace(/\/$/, "");
const ledger = path === "/ledger";
const prototype = path === "/ledger-prototype";
document.title = prototype
  ? "账本交互原型 · 观价"
  : ledger
    ? "投资账本 · 观价"
    : "白酒行情 · 观价";
</script>

<template>
  <LedgerPrototype v-if="prototype" />
  <Ledger v-else-if="ledger" />
  <App v-else />
</template>

<script setup lang="ts">
import { defineAsyncComponent } from "vue";
const App = defineAsyncComponent(() => import("./App.vue"));
const Ledger = defineAsyncComponent(() => import("./Ledger.vue"));
const Tax = defineAsyncComponent(() => import("./Tax.vue"));
const Options = defineAsyncComponent(() => import("./Options.vue"));
const LedgerPrototype = defineAsyncComponent(
  () => import("./prototype/LedgerPrototype.vue"),
);
const path = window.location.pathname.replace(/\/$/, "");
const ledger = path === "/ledger";
const tax = path === "/tax";
const options = path === "/options";
const prototype = path === "/ledger-prototype";
document.title = options
  ? "期权权利金 · 观价"
  : tax
    ? "税务计算 · 观价"
    : prototype
      ? "账本交互原型 · 观价"
      : ledger
        ? "投资账本 · 观价"
        : "白酒行情 · 观价";
</script>

<template>
  <Options v-if="options" />
  <Tax v-else-if="tax" />
  <LedgerPrototype v-else-if="prototype" />
  <Ledger v-else-if="ledger" />
  <App v-else />
</template>

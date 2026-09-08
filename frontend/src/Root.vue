<script setup lang="ts">
import { defineAsyncComponent, ref } from "vue";
import App from "./App.vue";
const Ledger = defineAsyncComponent(() => import("./Ledger.vue"));
const ledger = window.location.pathname.replace(/\/$/, "") === "/ledger";
document.title = ledger ? "投资账本 · 观价" : "白酒行情 · 观价";
const locked = ref(false);
</script>

<template>
  <nav class="root-nav" aria-label="模块导航">
    <a
      href="/liquor"
      :aria-current="!ledger ? 'page' : undefined"
      @click="locked && $event.preventDefault()"
      >白酒行情</a
    >
    <a
      href="/ledger"
      :aria-current="ledger ? 'page' : undefined"
      @click="locked && $event.preventDefault()"
      >投资账本</a
    >
    <span v-if="locked">请先确认待处理写入，暂不可离开</span>
  </nav>
  <Ledger v-if="ledger" @locked="locked = $event" />
  <App v-else />
</template>

<style scoped>
.root-nav {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  padding: 14px 4vw;
  border-bottom: 1px solid #dde3db;
  font-size: 14px;
}
a[aria-current] {
  color: #285b46;
  font-weight: 700;
  border-bottom: 2px solid;
}
</style>

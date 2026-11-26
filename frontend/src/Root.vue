<script setup lang="ts">
import { defineAsyncComponent, onMounted, ref } from "vue";
import SiteLogin from "./SiteLogin.vue";
import {
  accountRequest,
  afterLogin,
  siteUser,
  sessionSignal,
} from "./siteAccount";
const App = defineAsyncComponent(() => import("./App.vue"));
const Ledger = defineAsyncComponent(() => import("./Ledger.vue"));
const Tax = defineAsyncComponent(() => import("./Tax.vue"));
const Options = defineAsyncComponent(() => import("./Options.vue"));
const LedgerPrototype = defineAsyncComponent(
  () => import("./prototype/LedgerPrototype.vue"),
);
const path = window.location.pathname.replace(/\/$/, "");
const fragment = new URLSearchParams(location.hash.slice(1));
const code = path === "/login" ? fragment.get("setup") || undefined : undefined;
const initialName = code ? fragment.get("user") || "" : "";
if (code) history.replaceState(null, "", location.pathname + location.search);
const ready = ref(false),
  failure = ref("");
async function loadSession() {
  failure.value = "";
  try {
    siteUser.value = await accountRequest("me");
    if (siteUser.value && path === "/login" && !code) {
      location.replace(afterLogin(location.search));
      return;
    }
    ready.value = true;
  } catch {
    failure.value = "无法确认登录状态，请检查连接后重试。";
  }
}
onMounted(() => {
  window.addEventListener("storage", (e) => {
    if (e.key === sessionSignal) location.reload();
  });
  window.addEventListener("pageshow", (e) => {
    if (e.persisted) location.reload();
  });
  if (code) {
    ready.value = true;
  } else void loadSession();
});
const ledger = path === "/ledger";
const tax = path === "/tax";
const options = path === "/options";
const prototype = path === "/ledger-prototype";
document.title =
  path === "/login"
    ? "账户登录 · 观价"
    : path === "/account"
      ? "我的账户 · 观价"
      : options
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
  <main v-if="!ready" class="workspace-frame" aria-live="polite">
    <p>{{ failure || "正在打开工作空间…" }}</p>
    <button v-if="failure" @click="loadSession">重试</button>
  </main>
  <SiteLogin
    v-else-if="!siteUser || code || path === '/account'"
    :account="!!siteUser && path === '/account' && !code"
    :setup-code="code"
    :initial-name="initialName"
  />
  <Options v-else-if="options" />
  <Tax v-else-if="tax" />
  <LedgerPrototype v-else-if="prototype" />
  <Ledger v-else-if="ledger" />
  <App v-else />
</template>

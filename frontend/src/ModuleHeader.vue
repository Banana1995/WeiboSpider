<script setup lang="ts">
import { siteUser } from "./siteAccount";
const modules = {
  liquor: "白酒行情",
  ledger: "投资账本",
  tax: "税务计算",
  options: "期权",
} as const;
defineProps<{ current: keyof typeof modules; locked?: boolean }>();
</script>

<template>
  <header class="module-header">
    <div class="workspace-frame module-header-inner">
      <component
        :is="current === 'liquor' || current === 'tax' ? 'a' : 'div'"
        :href="
          current === 'liquor' || current === 'tax' ? `/${current}` : undefined
        "
        class="module-brand"
      >
        <span class="module-mark">观</span>观价
        <span class="module-divider" aria-hidden="true">/</span>
        <span class="module-name">{{ modules[current] }}</span>
      </component>
      <nav aria-label="模块导航">
        <slot />
        <a v-if="siteUser?.id === 'site-owner-v1'" href="/weibo/">微博管理</a>
        <a
          v-for="(label, key) in modules"
          :key="key"
          :href="`/${key}`"
          :aria-current="current === key ? 'page' : undefined"
          :aria-disabled="locked || undefined"
          >{{ label }}</a
        >
        <a
          v-if="siteUser"
          href="/account"
          :aria-disabled="locked || undefined"
          >{{ siteUser.username }}</a
        >
      </nav>
    </div>
  </header>
</template>

<style scoped>
.module-header {
  background: var(--lp-surface);
  color: var(--lp-text);
  border-bottom: 1px solid var(--lp-rule);
  font-family: var(--workspace-font);
  line-height: 1.6;
}
.module-header-inner,
.module-brand,
nav {
  display: flex;
  align-items: center;
}
.module-header-inner {
  justify-content: space-between;
  gap: 20px;
  padding-block: 18px;
}
.module-brand {
  gap: 10px;
  font-size: 20px;
  font-weight: 650;
  white-space: nowrap;
}
.module-mark {
  background: var(--lp-primary);
  color: white;
  padding: 3px 8px;
  border-radius: 4px;
  font-family: serif;
}
.module-divider {
  font-weight: 400;
  color: #9cabbc;
}
.module-name {
  font-size: 14px;
  font-weight: 400;
}
nav {
  flex-wrap: wrap;
  gap: 8px 24px;
  color: var(--lp-muted);
  font-size: 13px;
}
nav [aria-current] {
  color: var(--lp-primary);
  font-weight: 650;
}
.module-header :deep(a:hover) {
  color: var(--lp-primary);
  text-decoration: underline;
  text-underline-offset: 4px;
}
.module-header :deep(a:focus-visible) {
  outline: 3px solid #8bb6e5;
  outline-offset: 2px;
}
@media (max-width: 720px) {
  .module-header-inner {
    align-items: flex-start;
    flex-direction: column;
    gap: 12px;
    padding-block: 14px;
  }
  nav {
    column-gap: 18px;
  }
}
</style>

<script setup lang="ts">
defineProps<{ open: boolean }>();
</script>

<template>
  <svg
    class="lp-disclosure"
    :class="{ 'lp-disclosure-open': open }"
    viewBox="0 0 12 12"
    width="11"
    height="11"
    aria-hidden="true"
    focusable="false"
  >
    <path
      d="M4 2.5 8 6 4 9.5"
      fill="none"
      stroke="currentColor"
      stroke-width="1.6"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>

<style scoped>
.lp-disclosure {
  flex: none;
  color: var(--lp-muted);
  transition: transform 0.15s ease;
}
.lp-disclosure-open {
  transform: rotate(90deg);
}
@media (prefers-reduced-motion: reduce) {
  .lp-disclosure {
    transition: none;
  }
}
</style>

<script setup lang="ts">
defineProps<{ username: string }>();
</script>

<template>
  <span class="site-identity">
    <span class="site-avatar" aria-hidden="true">{{
      username.charAt(0).toUpperCase()
    }}</span>
    <span class="site-identity-name">{{ username }}</span>
  </span>
</template>

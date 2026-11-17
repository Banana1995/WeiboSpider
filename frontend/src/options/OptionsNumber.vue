<script setup lang="ts">
// Native v-model on type=number coerces to doubles; retain financial input text.
defineProps<{ modelValue: string | null }>();
defineEmits<{ "update:modelValue": [value: string] }>();
</script>
<template>
  <input
    type="number"
    :value="modelValue ?? ''"
    @input="
      $emit('update:modelValue', ($event.target as HTMLInputElement).value)
    "
  />
</template>

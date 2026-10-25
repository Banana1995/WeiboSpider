<script setup lang="ts">
import type { StockBook } from "./stockBook";

defineProps<{ book: StockBook }>();
</script>

<template>
  <details class="stock-sync">
    <summary>
      自动分红{{
        book.sync_checked_at
          ? ` · 上次检查 ${book.sync_checked_at.slice(0, 16).replace("T", " ")} UTC`
          : " · 等待后台检查"
      }}
    </summary>
    <p>
      {{
        book.sync_message ||
        "添加买入记录后后台自动检查分红，稍后刷新即可查看。分红会按历史股数计算。"
      }}
    </p>
    <p>
      数据覆盖来源可提供的历史范围。未提供完整日期、含送转或拆合股的记录需要核对；已有手工记录会保留。
    </p>
  </details>
</template>

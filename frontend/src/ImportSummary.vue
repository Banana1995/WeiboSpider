<script setup lang="ts">
import type { ImportMetadata, ImportSummary } from "./ledgerImport";
import { money } from "./ledgerView";
defineProps<{ metadata: ImportMetadata; summary: ImportSummary }>();
</script>

<template>
  <dl class="ledger-record">
    <dt>来源账户原名</dt>
    <dd>{{ metadata.name }}</dd>
    <dt>目标</dt>
    <dd>{{ metadata.goal || "未提供" }}</dd>
    <dt>预期收益</dt>
    <dd>{{ metadata.expected_return || "未提供" }}</dd>
    <dt>投资期限</dt>
    <dd>{{ metadata.investment_horizon || "未提供" }}</dd>
    <dt>币种</dt>
    <dd>{{ metadata.currency }}</dd>
    <dt>资金分类</dt>
    <dd>{{ metadata.money_bucket || "未提供" }}</dd>
    <dt>记录数 / 资产记录 / 资金流记录</dt>
    <dd>
      {{ summary.row_count }} / {{ summary.asset_count }} /
      {{ summary.flow_count }}
    </dd>
    <dt>日期范围（含首尾）</dt>
    <dd>{{ summary.from }} 至 {{ summary.to }}</dd>
    <dt>累计流入</dt>
    <dd>{{ money(summary.total_in) }} {{ metadata.currency }}</dd>
    <dt>累计流出（正数）</dt>
    <dd>{{ money(summary.total_out) }} {{ metadata.currency }}</dd>
    <dt>最近记录的总资产（不是当前自动估值或现金）</dt>
    <dd>
      {{ money(summary.latest_assets) }} {{ metadata.currency }} ·
      实际记录日期 {{ summary.latest_asset_date ?? "未提供" }}
    </dd>
  </dl>
</template>

<script setup lang="ts">
import { ref } from "vue";
import { money } from "./ledgerView";
import {
  returnPercent,
  returnReasons,
  type ReturnMetric,
} from "./ledgerReturns";
import type { PortfolioBasis } from "./ledgerPortfolios";
defineProps<{ basis: PortfolioBasis; view: "personal" | "manager" }>();
const emit = defineEmits<{ account: [id: string] }>();
const expanded = ref<string[]>([]);
function toggle(id: string) {
  expanded.value = expanded.value.includes(id)
    ? expanded.value.filter((value) => value !== id)
    : [...expanded.value, id];
}
const percent = (m: ReturnMetric) =>
  m.value === null ? "—" : returnPercent(m.value, m.percentage);
const reason = (m: ReturnMetric) =>
  m.value === null ? returnReasons[m.reason] : "";
</script>

<template>
  <div class="lp-contributions" data-test="portfolio-contributions">
    <p class="lp-muted">
      各成员按所选区间计算；较晚开始记录的账户，从其首次记录起计入。
    </p>
    <table class="lp-contribution-table">
      <thead>
        <tr>
          <th>账户</th>
          <th>期末资产</th>
          <th class="lp-contribution-extra">资产占比</th>
          <th>区间收益</th>
          <th class="lp-contribution-extra">自身收益率</th>
          <th><span class="lp-visually-hidden">操作</span></th>
        </tr>
      </thead>
      <tbody>
        <template v-for="m in basis.members" :key="m.account_id">
          <tr>
            <th scope="row">
              <button
                class="lp-text-button lp-contribution-name"
                :aria-expanded="expanded.includes(m.account_id)"
                @click="toggle(m.account_id)"
              >
                {{ m.name }}
                <span aria-hidden="true">{{
                  expanded.includes(m.account_id) ? "−" : "＋"
                }}</span></button
              ><small
                >{{ m.currency }} ·
                {{
                  m.first_date ? `${m.first_date} 起计入` : "此区间尚无记录"
                }}</small
              >
            </th>
            <td>{{ money(m.assets) }}</td>
            <td class="lp-contribution-extra">{{ percent(m.asset_share) }}</td>
            <td :class="{ 'lp-negative': m.profit.value?.startsWith('-') }">
              {{ money(m.profit.value) }}
            </td>
            <td
              class="lp-contribution-extra"
              :title="reason(view === 'personal' ? m.modified_dietz : m.twr)"
            >
              {{ percent(view === "personal" ? m.modified_dietz : m.twr) }}
            </td>
            <td>
              <button
                class="lp-text-button"
                :aria-label="`查看${m.name}`"
                @click="emit('account', m.account_id)"
              >
                查看
              </button>
            </td>
          </tr>
          <tr
            v-if="expanded.includes(m.account_id)"
            class="lp-contribution-detail"
          >
            <td colspan="6">
              <dl>
                <div>
                  <dt>资产占比</dt>
                  <dd>{{ percent(m.asset_share) }}</dd>
                </div>
                <div>
                  <dt>
                    {{
                      view === "personal" ? "资金加权收益率" : "时间加权收益率"
                    }}
                  </dt>
                  <dd>
                    {{
                      percent(view === "personal" ? m.modified_dietz : m.twr)
                    }}
                  </dd>
                </div>
                <div>
                  <dt>年化收益率</dt>
                  <dd>
                    {{
                      percent(view === "personal" ? m.xirr : m.twr_annualized)
                    }}
                  </dd>
                </div>
                <div>
                  <dt>实际统计区间</dt>
                  <dd>
                    {{ m.from && m.to ? `${m.from} 至 ${m.to}` : "尚未开始" }}
                  </dd>
                </div>
                <div>
                  <dt>最近明确资产日期</dt>
                  <dd>
                    {{ m.source_date || "按转入记录推算"
                    }}{{ m.carried ? "，其后计入净转入" : "" }}
                  </dd>
                </div>
              </dl>
            </td>
          </tr>
        </template>
      </tbody>
      <tfoot>
        <tr>
          <th>组合合计</th>
          <td>{{ money(basis.closing?.assets) }}</td>
          <td class="lp-contribution-extra">
            {{
              basis.closing?.assets && /[1-9]/.test(basis.closing.assets)
                ? "100.00%"
                : "—"
            }}
          </td>
          <td>{{ money(basis.returns.profit.value) }}</td>
          <td class="lp-contribution-extra">—</td>
          <td />
        </tr>
      </tfoot>
    </table>
    <p class="lp-field-hint">
      金额单位
      {{ basis.currency }}。成员收益金额与组合收益核对；成员自身收益率分别计算。
    </p>
  </div>
</template>

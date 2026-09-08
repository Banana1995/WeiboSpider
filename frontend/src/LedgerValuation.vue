<script setup lang="ts">
import { computed } from "vue";
import type { Instrument, Valuation, ValuationItem } from "./ledger";

const props = defineProps<{
  accountId: string;
  value?: Valuation;
  loading: boolean;
  error: string;
  instruments: Instrument[];
  historical?: boolean;
  formatTime?: (value: string | null) => string;
}>();
const snapshot = computed(() =>
  props.value?.account_id === props.accountId ? props.value : undefined,
);
const complete = computed(
  () =>
    snapshot.value?.complete === true && snapshot.value.total_assets != null,
);
const stale = computed(() => props.loading || !!props.error);
const show = (value: string | null | undefined) => value || "未知 / 不适用";
const time = (value: string | null | undefined) =>
  props.formatTime ? props.formatTime(value ?? null) : show(value);
const statuses: Record<ValuationItem["status"], string> = {
  current: "当日参考",
  prior_date: "非当日报价 / 汇率",
  unavailable: "不可估值",
  closed: "已清仓",
};
const errors: Record<string, string> = {
  unsupported_instrument: "不支持该市场或证券代码行情（不影响记账）",
  currency_mismatch: "证券登记币种与报价币种不匹配",
  quote_unavailable: "证券行情暂不可用",
  quote_timeout: "证券行情查询超时",
  quote_inactive: "证券已退市或报价为无成交占位，不能据此估值",
  fx_unavailable: "当前参考汇率暂不可用",
  fx_timeout: "当前参考汇率查询超时",
};
</script>

<template>
  <section
    class="ledger-panel ledger-valuation"
    data-test="valuation"
    aria-live="polite"
  >
    <h2>
      {{
        historical
          ? "历史估值快照（只读）"
          : snapshot && stale
            ? "过期估值快照"
            : "当前参考估值"
      }}
    </h2>
    <p class="ledger-notice">
      {{
        historical
          ? "保存时参考估值，非当前资产、非收益率"
          : "当前参考估值，非收益率"
      }}；现金+持仓数量×原币报价×{{
        historical ? "保存时参考汇率" : "最新参考汇率"
      }}；成本不参与估值；行情与汇率可能不同日期，实际成交汇率不随刷新改写
    </p>
    <p v-if="snapshot && stale" role="alert">
      过期：以下为上次读取快照，不能视为当前资产。{{
        loading ? "正在重新读取…" : "请手动刷新重试。"
      }}
    </p>
    <p v-else-if="loading" role="status">正在读取估值快照…</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <template v-if="snapshot">
      <p v-if="!historical" data-test="valuation-saved">
        {{
          !complete
            ? "本次估值不完整，不写入历史记录。"
            : snapshot.history_id
              ? `此快照已保存为历史记录 #${snapshot.history_id}`
              : "只读预览，尚未保存，不改变账本。请使用“更新并保存总资产”明确保存。"
        }}
      </p>
      <p v-if="!complete" role="alert">
        不能完整估值：总资产未知，已知持仓小计不代表全部资产。
      </p>
      <p class="valuation-total" data-test="valuation-total">
        {{
          historical
            ? "历史快照总资产"
            : stale
              ? "上次快照总资产（过期）"
              : "参考总资产"
        }}
        <strong>{{ complete ? snapshot.total_assets : "—" }}</strong>
        {{ snapshot.currency }}
      </p>
      <p data-test="valuation-cash">
        快照现金 {{ show(snapshot.cash) }} {{ snapshot.currency }}
      </p>
      <p>
        持仓总市值 {{ show(snapshot.positions_value) }} {{ snapshot.currency }}
      </p>
      <p>
        已知持仓市值小计{{ complete ? "（完整）" : "（不完整，仅已知部分）" }}
        {{ show(snapshot.known_positions_value) }} {{ snapshot.currency }}
      </p>
      <p>
        账本截止日期（as_of）{{ show(snapshot.as_of) }}<br />
        账本快照时间（ledger_at）{{ time(snapshot.ledger_at) }}<br />
        估值计算时间（calculated_at）{{ time(snapshot.calculated_at) }}
      </p>
      <details>
        <summary>账本版本标识</summary>
        <p style="overflow-wrap: anywhere">{{ snapshot.ledger_revision }}</p>
      </details>
      <div class="ledger-table">
        <table>
          <caption>
            估值快照持仓（股数来自同一估值响应）
          </caption>
          <thead>
            <tr>
              <th>证券 / 快照股数</th>
              <th>原币报价 / 来源</th>
              <th>
                {{ historical ? "保存时参考汇率" : "最新参考汇率" }} / 来源
              </th>
              <th>本位币市值 / 状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in snapshot.items" :key="item.instrument_id">
              <td>
                {{
                  instruments.find((i) => i.id === item.instrument_id)?.name ??
                  item.instrument_id
                }}<small>{{ item.instrument_id }}</small
                ><small v-if="historical"
                  >{{
                    instruments.find((i) => i.id === item.instrument_id)?.market
                  }}
                  /
                  {{
                    instruments.find((i) => i.id === item.instrument_id)?.code
                  }}
                  /
                  {{
                    instruments.find((i) => i.id === item.instrument_id)
                      ?.currency
                  }}</small
                ><small>股数 {{ show(item.quantity) }}</small>
              </td>
              <td>
                <template v-if="item.quote">
                  原币价格 {{ show(item.quote.price) }} {{ item.quote.currency
                  }}<small
                    >{{ item.quote.symbol }} ·
                    {{ show(item.quote.source) }}</small
                  >
                  <small>报价日期 {{ show(item.quote.date) }}</small
                  ><small>报价时间 {{ time(item.quote.quoted_at) }}</small>
                  <small v-if="item.quote.fetched_at"
                    >获取时间 {{ time(item.quote.fetched_at) }}</small
                  >
                </template>
                <template v-else>{{
                  item.status === "closed"
                    ? "已清仓，无需报价"
                    : "行情未知 / 不可用"
                }}</template>
              </td>
              <td>
                <template v-if="item.fx">
                  {{ item.fx.base }}/{{ item.fx.quote }} {{ show(item.fx.rate)
                  }}<small>汇率日期 {{ show(item.fx.date) }}</small
                  ><small>{{ show(item.fx.source) }}</small>
                  <small
                    >汇率模式 {{ item.fx.mode }} / 请求日期
                    {{ show(item.fx.requested_date) }}</small
                  >
                  <small v-if="item.fx.quoted_at"
                    >汇率报价时间 {{ time(item.fx.quoted_at) }}</small
                  ><small v-if="item.fx.fetched_at"
                    >获取时间 {{ time(item.fx.fetched_at) }}</small
                  >
                </template>
                <template v-else>{{
                  item.status === "closed"
                    ? "已清仓，无需汇率"
                    : item.quote?.currency === snapshot.currency
                      ? "同币种，无需换汇"
                      : "汇率未知 / 不可用"
                }}</template>
              </td>
              <td>
                {{ show(item.market_value) }} {{ snapshot.currency
                }}<small
                  >{{ statuses[item.status] ?? "未知状态" }} ·
                  {{ item.status }}</small
                >
                <p v-if="item.error_code" role="alert">
                  {{ errors[item.error_code] ?? "估值数据不可用" }} [{{
                    item.error_code
                  }}]
                </p>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="snapshot.items.length === 0">快照中暂无持仓。</p>
    </template>
    <p v-else-if="!loading">暂无可用估值快照，总资产未知。</p>
  </section>
</template>

<style scoped>
.valuation-total {
  padding: 20px;
  background: #eaf3eb;
  color: #285b46;
}
.valuation-total strong {
  display: block;
  font-size: clamp(24px, 4vw, 38px);
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
caption {
  text-align: left;
  margin-bottom: 12px;
}
</style>

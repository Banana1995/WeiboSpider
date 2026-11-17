<script setup lang="ts">
import { computed } from "vue";
import {
  beijing,
  candidateSide,
  capitalLabel,
  costLabel,
  sideLabel,
  money,
  percent,
  statusText,
  type Candidate,
} from "../options";
const props = defineProps<{ value: Candidate; archived?: boolean }>();
const isCall = computed(() => candidateSide(props.value) === "CALL");
const shares = computed(
  () => props.value.quote.contract.multiplier * props.value.input.quantity,
);
const priceBasis = computed(() =>
  props.value.input.basis === "bid"
    ? "卖出参考价 Bid"
    : props.value.input.basis === "mid"
      ? "中间价假设"
      : "自定义价格假设",
);
// Descriptive decomposition, not an assignment probability or a guarantee.
const moneyness = computed(() => {
  const stock = props.value.snapshot.underlying;
  const price = props.value.result.price;
  if (stock == null || price == null) return null;
  const stockValue = Number(stock);
  const strike = Number(props.value.quote.contract.strike);
  const premium = Number(price);
  if (![stockValue, strike, premium].every(Number.isFinite)) return null;
  const intrinsic = isCall.value ? stockValue - strike : strike - stockValue;
  if (intrinsic <= 0) return null;
  return {
    intrinsic: intrinsic.toFixed(2),
    timeValue: (premium - intrinsic).toFixed(2),
  };
});
</script>

<template>
  <div class="opt-result" data-testid="calculation-result">
    <p
      v-if="!value.snapshot.complete && value.snapshot.warning"
      class="opt-notice"
    >
      {{ value.snapshot.warning }}
    </p>
    <p v-if="moneyness" class="opt-notice opt-moneyness">
      <strong
        >价内 {{ sideLabel(value.quote.contract.type) }}：行权价{{
          isCall ? "低于" : "高于"
        }}正股参考价 {{ money(value.snapshot.underlying) }} USD</strong
      >。参考内在价值为 {{ moneyness.intrinsic }} 美元／股；按{{ priceBasis }}
      {{ money(value.result.price) }} 计算的时间价值为
      {{ moneyness.timeValue }} 美元／股。 被指派时需按行权价{{
        isCall ? "交付股票" : "买入股票"
      }}；权利金收入率不等于整笔持仓收益率。
    </p>
    <p v-if="archived" class="opt-help">
      收藏时冻结的报价、参数和结果，保持原估值日期。
    </p>
    <div class="opt-result-hero">
      <div class="opt-hero-primary">
        <span>每股权利金（USD）</span
        ><strong>{{ money(value.result.price) }}</strong
        ><small
          >{{ value.input.quantity }} 份 ×
          {{ value.quote.contract.multiplier }} 股 · 毛权利金 USD
          {{ money(value.result.gross)
          }}<template v-if="value.result.fee !== null">
            · 扣开仓费后 USD {{ money(value.result.income) }}</template
          ><template v-else> · 税费前</template></small
        >
      </div>
      <div class="opt-hero-days">
        <span>估值时距到期</span>
        <strong v-if="value.result.dte > 0"
          >{{ value.result.dte }}<em>天</em></strong
        >
        <strong v-else>{{
          value.result.dte === 0 ? "到期当日" : "已过期"
        }}</strong>
        <small>{{ value.quote.contract.expiry }}（美东）</small>
      </div>
      <div>
        <span>权利金年化（单利）</span
        ><strong>{{ percent(value.result.apr) }}</strong
        ><small
          >{{
            value.input.mode === "full"
              ? capitalLabel(value.quote.contract.type)
              : "净投入参考值"
          }}
          ·
          {{
            value.input.basis === "bid"
              ? "按卖出价 Bid"
              : value.input.basis === "mid"
                ? "中间价假设"
                : "自定义价格"
          }}</small
        >
      </div>
    </div>
    <p v-if="value.result.price !== null" class="opt-hero-caption">
      <template v-if="value.result.dte > 0"
        >按 {{ value.snapshot.valuation_date_et }}（美东）的快照估算，期限
        {{ value.result.dte }} 天；若按{{ priceBasis }}成交，卖出
        {{ value.input.quantity }} 份，每股收
        {{ money(value.result.price) }} 美元，{{ shares }} 股合计
        {{ money(value.result.gross) }} 美元（税费前）。</template
      ><template v-else
        >估值日为到期日或更晚，年化不计算，上述金额仅作历史参考。</template
      >{{
        isCall ? `按备兑卖 Call 测算，需对应 ${shares} 股。` : ""
      }}标准股票期权按整份交易，最少 1 份。
    </p>
    <p v-if="statusText(value.result.status)" class="opt-notice">
      {{ statusText(value.result.status) }}
    </p>
    <dl class="opt-metrics">
      <div>
        <dt>{{ capitalLabel(value.quote.contract.type) }}</dt>
        <dd>USD {{ money(value.result.capital) }}</dd>
      </div>
      <div v-if="isCall">
        <dt>备兑所需股数</dt>
        <dd>{{ money(value.result.covered_shares, 0) }} 股</dd>
      </div>
      <div v-if="isCall">
        <dt>被指派时股票卖出金额</dt>
        <dd>USD {{ money(value.result.strike_proceeds) }}</dd>
      </div>
      <div>
        <dt>本次资金基数</dt>
        <dd>USD {{ money(value.result.base) }}</dd>
      </div>
      <div>
        <dt>期间权利金收入率</dt>
        <dd>{{ percent(value.result.rate) }}</dd>
      </div>
      <div v-if="value.result.intrinsic != null">
        <dt>内在价值／时间价值（{{ value.input.quantity }} 份）</dt>
        <dd>
          USD {{ money(value.result.intrinsic) }} /
          <span
            :class="{
              'opt-time-negative':
                value.result.time_value !== null &&
                Number(value.result.time_value) < 0,
            }"
            >{{ money(value.result.time_value) }}</span
          >
        </dd>
      </div>
      <div>
        <dt>{{ costLabel(value.quote.contract.type) }}</dt>
        <dd>USD {{ money(value.result.cost) }}</dd>
      </div>
      <div v-if="isCall">
        <dt>含权利金卖出价／股</dt>
        <dd>USD {{ money(value.result.net_sale_price) }}</dd>
      </div>
      <div>
        <dt>预计开仓费用</dt>
        <dd>
          {{
            value.result.fee === null
              ? "未配置"
              : `USD ${money(value.result.fee)}`
          }}
        </dd>
      </div>
      <div>
        <dt>估值日期／到期日（美东）</dt>
        <dd>
          {{ value.snapshot.valuation_date_et }} /
          {{ value.quote.contract.expiry }}
        </dd>
      </div>
    </dl>
    <p v-if="value.result.apr !== null" class="opt-equation">
      年化 = {{ money(value.result.income) }} ÷ {{ money(value.result.base) }} ×
      365 ÷ {{ value.result.dte }} = {{ percent(value.result.apr) }}
    </p>
    <div v-if="value.input.interest" class="opt-note">
      <strong>独立利息假设</strong>
      <p>
        本金 USD {{ money(value.input.interest.principal) }}，年利率
        {{ value.input.interest.annual_rate }}%，{{ value.input.interest.days }}
        天，ACT/365 单利。
      </p>
      <p>
        预计利息 USD {{ money(value.result.interest)
        }}<template v-if="value.result.combined_apr !== null"
          >；权利金加利息年化 {{ percent(value.result.combined_apr) }}</template
        ><template v-else>；计息期间不同，不合并年化。</template>
      </p>
    </div>
    <div v-if="value.input.scenario" class="opt-note">
      <strong>{{
        value.input.scenario.kind === "expiry"
          ? isCall
            ? "到期交股情景"
            : "到期接货情景"
          : "提前平仓情景"
      }}</strong>
      <p>
        假设{{
          value.input.scenario.kind === "expiry" ? "到期股价" : "回购价"
        }}
        USD {{ money(value.input.scenario.price) }}，{{
          isCall ? "期权部分盈亏" : "经济盈亏"
        }}
        USD {{ money(value.result.scenario_pnl) }}。
      </p>
      <p v-if="isCall && value.input.scenario.kind === 'expiry'">
        <template v-if="value.result.covered_pnl != null"
          >按假设持股成本 USD
          {{ money(value.input.scenario.stock_cost) }}／股，持股＋卖 Call
          合计情景盈亏 USD {{ money(value.result.covered_pnl) }}。</template
        >
        <template v-else
          >未填写持股成本，暂不计算持股＋卖 Call 的合计盈亏。</template
        >
      </p>
      <p class="opt-help">
        {{
          value.result.scenario_fees_known
            ? "已计所填开仓和情景费用。"
            : "费用未完整配置，结果未包含全部费用。"
        }}{{
          value.result.scenario_interest_known
            ? "已计所填情景利息。"
            : "未计情景利息。"
        }}未卖出股票的市值变化属于账面损益；此情景不改变上方权利金收入。
      </p>
    </div>
    <details class="opt-details" :open="archived">
      <summary>原始行情与计算依据</summary>
      <dl class="opt-metrics">
        <div>
          <dt>卖出价 Bid / 买入价 Ask（USD）</dt>
          <dd>{{ money(value.quote.bid) }} / {{ money(value.quote.ask) }}</dd>
        </div>
        <div>
          <dt>正股参考价（USD）</dt>
          <dd>{{ money(value.snapshot.underlying) }}</dd>
        </div>
        <div>
          <dt>买量 / 卖量</dt>
          <dd>
            {{ money(value.quote.bid_size, 0) }} /
            {{ money(value.quote.ask_size, 0) }}
          </dd>
        </div>
        <div>
          <dt>未平仓量 / 成交量</dt>
          <dd>
            {{ money(value.quote.oi, 0) }} / {{ money(value.quote.volume, 0) }}
          </dd>
        </div>
        <div>
          <dt>IV / Delta（合约多头）</dt>
          <dd>
            {{ percent(value.quote.iv) }} / {{ money(value.quote.delta, 4) }}
          </dd>
        </div>
        <div>
          <dt>每份合约</dt>
          <dd>
            {{ value.quote.contract.multiplier }} 股 ·
            {{
              value.quote.contract.standard === "STANDARD"
                ? "标准"
                : "非标准／未知"
            }}
          </dd>
        </div>
        <div>
          <dt>采集时间（北京）</dt>
          <dd>{{ beijing(value.snapshot.captured_at) }}</dd>
        </div>
        <div>
          <dt>源盘口时间（北京）</dt>
          <dd>{{ beijing(value.quote.quote_at) }}</dd>
        </div>
        <div>
          <dt>当前价时间（北京）</dt>
          <dd>{{ beijing(value.quote.last_trade_at) }}</dd>
        </div>
        <div>
          <dt>市场状态／数据来源</dt>
          <dd>
            {{ value.snapshot.market_state }} / {{ value.snapshot.source }}
          </dd>
        </div>
        <div>
          <dt>计算版本</dt>
          <dd>{{ value.result.version }}</dd>
        </div>
        <div>
          <dt>费用设置版本</dt>
          <dd>
            {{
              value.input.fee_override !== null
                ? "自定义每单费用"
                : value.input.fee_version || "未配置"
            }}
          </dd>
        </div>
      </dl>
      <p class="opt-help">
        源盘口时间未知时，采集时间不代表报价刚刚发生。{{
          isCall
            ? "年化基数采用对应股票的快照参考市值；股票成本假设只用于独立盈亏情景。含权利金卖出价未计指派费用。"
            : "净投入参考值不等于券商冻结购买力；接货成本未包含指派费用。"
        }}
      </p>
    </details>
  </div>
</template>

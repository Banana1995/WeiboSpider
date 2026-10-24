<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from "vue";
import {
  taxDay,
  taxExact,
  taxMoney,
  taxNumber,
  taxPrice,
  currencyName,
  securityIdentity,
  type TaxDetail,
} from "./tax";
import TaxSecurity from "./TaxSecurity.vue";

const props = defineProps<{
  year: number;
  entries: TaxDetail[];
  names: Record<string, string>;
  namesLoading: boolean;
}>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>();
const page = ref(1);
const pageCount = computed(() =>
  Math.max(1, Math.ceil(props.entries.length / 50)),
);
const visible = computed(() =>
  props.entries.slice((page.value - 1) * 50, page.value * 50),
);
const date = (value: string) => (value.length === 8 ? taxDay(value) : value);
const quantity = (value: string) =>
  taxNumber(taxExact(value.replace(/^-/, "")));
const cents = (value: string) => {
  const negative = value.startsWith("-");
  const [whole = "0", fraction = ""] = (
    negative ? value.slice(1) : value
  ).split(".");
  const amount =
    BigInt(whole || "0") * 100n + BigInt((fraction + "00").slice(0, 2));
  return negative ? -amount : amount;
};
const money = (value: bigint) => {
  const negative = value < 0n;
  const absolute = negative ? -value : value;
  return `${negative ? "-" : ""}${absolute / 100n}.${(absolute % 100n).toString().padStart(2, "0")}`;
};
const openingQuantity = (entry: TaxDetail) => {
  const total = entry.openings.reduce(
    (sum, row) => sum + Number(row.quantity),
    0,
  );
  return total.toFixed(6).replace(/\.?0+$/, "") || "0";
};
const openingAmountCents = (entry: TaxDetail) =>
  entry.openings.reduce((sum, row) => sum + cents(row.amount), 0n);
const openingFeeCents = (entry: TaxDetail) =>
  entry.openings.reduce((sum, row) => sum + cents(row.fee), 0n);
const openingAmount = (entry: TaxDetail) =>
  taxMoney(money(openingAmountCents(entry)), entry.record.currency);
const openingFees = (entry: TaxDetail) =>
  taxMoney(money(openingFeeCents(entry)), entry.record.currency);
const openingAverage = (entry: TaxDetail) => {
  const total = Number(openingQuantity(entry));
  if (!total) return "—";
  const average = Number(openingAmountCents(entry)) / 100 / total;
  return `${taxNumber(average.toFixed(2))} ${currencyName(entry.record.currency)}`;
};
let focus: HTMLElement | null = null;
let overflow = "";
onMounted(() => {
  focus = document.activeElement as HTMLElement;
  overflow = document.body.style.overflow;
  document.body.style.overflow = "hidden";
  dialog.value?.showModal();
});
onBeforeUnmount(() => {
  dialog.value?.close();
  document.body.style.overflow = overflow;
  focus?.focus();
});
function outside(event: MouseEvent) {
  const element = dialog.value;
  if (!element || event.target !== element) return;
  const bounds = element.getBoundingClientRect();
  if (
    event.clientX < bounds.left ||
    event.clientX > bounds.right ||
    event.clientY < bounds.top ||
    event.clientY > bounds.bottom
  )
    emit("close");
}
</script>

<template>
  <Teleport to="body">
    <dialog
      ref="dialog"
      class="tax-page tax-detail-dialog"
      aria-labelledby="tax-detail-title"
      @cancel.prevent="emit('close')"
      @click="outside"
    >
      <header class="tax-detail-header">
        <h2 id="tax-detail-title">{{ year }} 年交易明细</h2>
        <button
          autofocus
          class="tax-dialog-close"
          aria-label="关闭年度明细"
          @click="emit('close')"
        >
          ×
        </button>
      </header>
      <p class="tax-detail-hint">
        开仓费用计入持仓成本，平仓时按配对数量分摊扣除；本行计入费用包含对应开仓费与平仓费，股息单列。
      </p>
      <div class="tax-detail-scroll" tabindex="0" aria-label="年度开平仓明细表">
        <table class="tax-detail-table">
          <thead>
            <tr>
              <th rowspan="2" class="tax-index">#</th>
              <th rowspan="2" class="tax-detail-security">证券／项目</th>
              <th colspan="6" scope="colgroup" class="tax-close-band">
                平仓信息
              </th>
              <th colspan="6" scope="colgroup" class="tax-open-band">
                配对开仓信息
              </th>
              <th rowspan="2" class="tax-num">本行计入费用</th>
              <th rowspan="2" class="tax-num tax-result-profit">净利润</th>
              <th rowspan="2" class="tax-num tax-result-dividend">股息收入</th>
            </tr>
            <tr>
              <th class="tax-detail-date">平仓日期</th>
              <th>平仓账户</th>
              <th class="tax-num tax-detail-quantity">数量</th>
              <th class="tax-num">价格</th>
              <th class="tax-num">金额</th>
              <th class="tax-num tax-close-end">平仓费用</th>
              <th class="tax-detail-date">开仓日期</th>
              <th>开仓账户</th>
              <th class="tax-num tax-detail-quantity">配对数量</th>
              <th class="tax-num">价格</th>
              <th class="tax-num">配对金额</th>
              <th class="tax-num tax-open-end">开仓费用</th>
            </tr>
          </thead>
          <tbody
            v-for="(entry, i) in visible"
            :key="`${entry.record.source}-${entry.record.line}-${i}`"
            class="tax-entry-group"
          >
            <tr v-if="entry.record.kind === '资金进出'" class="tax-cash-entry">
              <td class="tax-index">{{ (page - 1) * 50 + i + 1 }}</td>
              <td class="tax-detail-security">
                <TaxSecurity
                  :record="entry.record"
                  :names="names"
                  :loading="namesLoading"
                /><small
                  >{{ entry.record.kind }} · {{ entry.record.direction }}</small
                >
                <small v-if="securityIdentity(entry.record).key">{{
                  entry.record.reason
                }}</small>
              </td>
              <td class="tax-detail-date">{{ date(entry.record.date) }}</td>
              <td class="tax-detail-account">
                {{ entry.record.account
                }}<small>{{ entry.record.account_id }}</small>
              </td>
              <td
                v-for="column in 10"
                :key="column"
                :class="{
                  'tax-num': column !== 5 && column !== 6,
                  'tax-detail-date': column === 5,
                  'tax-detail-account': column === 6,
                  'tax-detail-quantity': column === 1 || column === 7,
                  'tax-close-end': column === 4,
                  'tax-open-end': column === 10,
                }"
              >
                —
              </td>
              <td class="tax-num">
                {{ taxMoney(entry.fees, entry.record.currency) }}
              </td>
              <td class="tax-num tax-result-profit">
                {{ taxMoney(entry.profit, entry.record.currency) }}
              </td>
              <td class="tax-num tax-emphasis tax-result-dividend">
                {{ taxMoney(entry.dividends, entry.record.currency) }}
              </td>
            </tr>
            <template v-else>
              <tr v-if="entry.openings.length > 1" class="tax-pair-summary">
                <td :rowspan="entry.openings.length + 1" class="tax-index">
                  {{ (page - 1) * 50 + i + 1 }}
                </td>
                <td
                  :rowspan="entry.openings.length + 1"
                  class="tax-detail-security"
                >
                  <TaxSecurity
                    :record="entry.record"
                    :names="names"
                    :loading="namesLoading"
                  /><small
                    >{{ entry.record.kind }} ·
                    {{ entry.record.direction }}</small
                  ><small v-if="!entry.complete" class="tax-error-text"
                    >成本待补齐</small
                  >
                </td>
                <template v-if="entry.has_close">
                  <td
                    :rowspan="entry.openings.length + 1"
                    class="tax-detail-date"
                  >
                    {{ date(entry.record.date) }}
                  </td>
                  <td
                    :rowspan="entry.openings.length + 1"
                    class="tax-detail-account"
                  >
                    {{ entry.record.account
                    }}<small>{{ entry.record.account_id }}</small>
                  </td>
                  <td
                    :rowspan="entry.openings.length + 1"
                    class="tax-num tax-detail-quantity"
                  >
                    {{ quantity(entry.record.quantity) }}
                  </td>
                  <td :rowspan="entry.openings.length + 1" class="tax-num">
                    {{ taxPrice(entry.record.price, entry.record.currency) }}
                  </td>
                  <td :rowspan="entry.openings.length + 1" class="tax-num">
                    {{
                      taxMoney(
                        entry.record.amount.replace(/^-/, ""),
                        entry.record.currency,
                      )
                    }}
                  </td>
                  <td
                    :rowspan="entry.openings.length + 1"
                    class="tax-num tax-close-end"
                  >
                    {{ taxMoney(entry.record.fee, entry.record.currency) }}
                  </td>
                </template>
                <td
                  v-else
                  colspan="6"
                  :rowspan="entry.openings.length + 1"
                  class="tax-close-end tax-unclosed"
                >
                  开仓流水
                </td>
                <td colspan="2" class="tax-pair-summary-label">
                  {{ entry.openings.length }} 笔开仓
                </td>
                <td class="tax-num tax-pair-summary-value">
                  <small>共</small><b>{{ openingQuantity(entry) }}</b>
                </td>
                <td class="tax-num tax-pair-summary-value">
                  <small>成交均价</small><b>{{ openingAverage(entry) }}</b>
                </td>
                <td class="tax-num tax-pair-summary-value">
                  <small>总配对金额</small><b>{{ openingAmount(entry) }}</b>
                </td>
                <td class="tax-num tax-pair-summary-value tax-open-end">
                  <small>总开仓费用</small><b>{{ openingFees(entry) }}</b>
                </td>
                <td :rowspan="entry.openings.length + 1" class="tax-num">
                  {{ taxMoney(entry.fees, entry.record.currency) }}
                  <small
                    v-if="
                      entry.capitalized_fee &&
                      /[1-9]/.test(entry.capitalized_fee)
                    "
                    >开仓费计入成本</small
                  >
                </td>
                <td
                  :rowspan="entry.openings.length + 1"
                  class="tax-num tax-emphasis tax-result-profit"
                >
                  {{ taxMoney(entry.profit, entry.record.currency)
                  }}<small v-if="!entry.complete">仅含已匹配部分</small>
                </td>
                <td
                  :rowspan="entry.openings.length + 1"
                  class="tax-num tax-result-dividend"
                >
                  {{ taxMoney(entry.dividends, entry.record.currency) }}
                </td>
              </tr>
              <tr
                v-for="(opening, j) in entry.openings.length
                  ? entry.openings
                  : [null]"
                :key="j"
              >
                <template v-if="entry.openings.length <= 1">
                  <td class="tax-index">{{ (page - 1) * 50 + i + 1 }}</td>
                  <td class="tax-detail-security">
                    <TaxSecurity
                      :record="entry.record"
                      :names="names"
                      :loading="namesLoading"
                    /><small
                      >{{ entry.record.kind }} ·
                      {{ entry.record.direction }}</small
                    ><small v-if="!entry.complete" class="tax-error-text"
                      >成本待补齐</small
                    >
                  </td>
                  <template v-if="entry.has_close">
                    <td class="tax-detail-date">
                      {{ date(entry.record.date) }}
                    </td>
                    <td class="tax-detail-account">
                      {{ entry.record.account
                      }}<small>{{ entry.record.account_id }}</small>
                    </td>
                    <td class="tax-num tax-detail-quantity">
                      {{ quantity(entry.record.quantity) }}
                    </td>
                    <td class="tax-num">
                      {{ taxPrice(entry.record.price, entry.record.currency) }}
                    </td>
                    <td class="tax-num">
                      {{
                        taxMoney(
                          entry.record.amount.replace(/^-/, ""),
                          entry.record.currency,
                        )
                      }}
                    </td>
                    <td class="tax-num tax-close-end">
                      {{ taxMoney(entry.record.fee, entry.record.currency) }}
                    </td>
                  </template>
                  <td v-else colspan="6" class="tax-close-end tax-unclosed">
                    开仓流水
                  </td>
                </template>
                <template v-if="opening">
                  <td class="tax-detail-date">
                    {{ date(opening.record.date) }}
                  </td>
                  <td class="tax-detail-account">
                    {{ opening.record.account
                    }}<small>{{ opening.record.account_id }}</small>
                  </td>
                  <td class="tax-num tax-detail-quantity">
                    {{ quantity(opening.quantity)
                    }}<small
                      v-if="
                        quantity(opening.quantity) !==
                        quantity(opening.record.quantity)
                      "
                      >原笔 {{ quantity(opening.record.quantity) }}</small
                    >
                  </td>
                  <td class="tax-num">
                    {{ taxPrice(opening.record.price, entry.record.currency) }}
                  </td>
                  <td class="tax-num">
                    {{ taxMoney(opening.amount, entry.record.currency) }}
                  </td>
                  <td class="tax-num tax-open-end">
                    {{ taxMoney(opening.fee, entry.record.currency) }}
                  </td>
                </template>
                <td v-else colspan="6" class="tax-open-end tax-error-text">
                  未找到对应开仓
                </td>
                <template v-if="entry.openings.length <= 1">
                  <td class="tax-num">
                    {{ taxMoney(entry.fees, entry.record.currency) }}
                    <small
                      v-if="
                        entry.capitalized_fee &&
                        /[1-9]/.test(entry.capitalized_fee)
                      "
                      >开仓费计入成本</small
                    >
                  </td>
                  <td class="tax-num tax-emphasis tax-result-profit">
                    {{ taxMoney(entry.profit, entry.record.currency)
                    }}<small v-if="!entry.complete">仅含已匹配部分</small>
                  </td>
                  <td class="tax-num tax-result-dividend">
                    {{ taxMoney(entry.dividends, entry.record.currency) }}
                  </td>
                </template>
              </tr>
            </template>
          </tbody>
          <tbody v-if="!entries.length">
            <tr>
              <td colspan="17" class="tax-empty">
                当前筛选范围没有交易、费用或股息明细。
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <footer class="tax-detail-footer">
        <div class="tax-actions">
          <button :disabled="page === 1" @click="page--">上一页</button
          ><span>{{ page }} / {{ pageCount }}</span
          ><button :disabled="page === pageCount" @click="page++">
            下一页
          </button>
        </div>
        <button class="tax-primary" @click="emit('close')">关闭</button>
      </footer>
    </dialog>
  </Teleport>
</template>

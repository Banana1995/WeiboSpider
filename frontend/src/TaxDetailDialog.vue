<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from "vue";
import {
  taxDay,
  taxExact,
  taxMoney,
  taxNumber,
  taxPrice,
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
        先看开仓批次，再看平仓结果。开仓分摊费供核对，本行计入费用按年度口径扣除，股息单列。
      </p>
      <div class="tax-detail-scroll" tabindex="0" aria-label="年度开平仓明细表">
        <table class="tax-detail-table">
          <thead>
            <tr>
              <th rowspan="2" class="tax-index">#</th>
              <th rowspan="2" class="tax-detail-security">证券／项目</th>
              <th colspan="6" scope="colgroup" class="tax-open-band">
                开仓信息
              </th>
              <th colspan="6" scope="colgroup" class="tax-close-band">
                平仓信息
              </th>
              <th rowspan="2" class="tax-num">本行计入费用</th>
              <th rowspan="2" class="tax-num tax-result-profit">净利润</th>
              <th rowspan="2" class="tax-num tax-result-dividend">股息收入</th>
            </tr>
            <tr>
              <th>开仓日期</th>
              <th>开仓账户</th>
              <th class="tax-num">配对数量</th>
              <th class="tax-num">价格</th>
              <th class="tax-num">配对金额</th>
              <th class="tax-num tax-open-end">分摊费用</th>
              <th>平仓日期</th>
              <th>平仓账户</th>
              <th class="tax-num">数量</th>
              <th class="tax-num">价格</th>
              <th class="tax-num">金额</th>
              <th class="tax-num tax-close-end">费用</th>
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
              </td>
              <td colspan="12">
                <div class="tax-cash-account">
                  <span>{{ date(entry.record.date) }}</span
                  ><span
                    >{{ entry.record.account
                    }}<small>{{ entry.record.account_id }}</small></span
                  ><span v-if="securityIdentity(entry.record).key">{{
                    entry.record.reason
                  }}</span>
                </div>
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
              <tr
                v-for="(opening, j) in entry.openings.length
                  ? entry.openings
                  : [null]"
                :key="j"
              >
                <template v-if="j === 0"
                  ><td :rowspan="entry.openings.length || 1" class="tax-index">
                    {{ (page - 1) * 50 + i + 1 }}
                  </td>
                  <td
                    :rowspan="entry.openings.length || 1"
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
                  </td></template
                >
                <template v-if="opening"
                  ><td>{{ date(opening.record.date) }}</td>
                  <td class="tax-detail-account">
                    {{ opening.record.account
                    }}<small>{{ opening.record.account_id }}</small>
                  </td>
                  <td class="tax-num">
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
                  <td
                    class="tax-num"
                    :title="taxPrice(opening.amount, entry.record.currency)"
                  >
                    {{ taxMoney(opening.amount, entry.record.currency) }}
                  </td>
                  <td class="tax-num tax-open-end">
                    {{ taxMoney(opening.fee, entry.record.currency) }}
                  </td></template
                >
                <td v-else colspan="6" class="tax-open-end tax-error-text">
                  未找到对应开仓
                </td>
                <template v-if="j === 0">
                  <template v-if="entry.has_close"
                    ><td :rowspan="entry.openings.length || 1">
                      {{ date(entry.record.date) }}
                    </td>
                    <td
                      :rowspan="entry.openings.length || 1"
                      class="tax-detail-account"
                    >
                      {{ entry.record.account
                      }}<small>{{ entry.record.account_id }}</small>
                    </td>
                    <td :rowspan="entry.openings.length || 1" class="tax-num">
                      {{ quantity(entry.record.quantity) }}
                    </td>
                    <td :rowspan="entry.openings.length || 1" class="tax-num">
                      {{ taxPrice(entry.record.price, entry.record.currency) }}
                    </td>
                    <td :rowspan="entry.openings.length || 1" class="tax-num">
                      {{
                        taxMoney(
                          entry.record.amount.replace(/^-/, ""),
                          entry.record.currency,
                        )
                      }}
                    </td>
                    <td
                      :rowspan="entry.openings.length || 1"
                      class="tax-num tax-close-end"
                    >
                      {{ taxMoney(entry.record.fee, entry.record.currency) }}
                    </td></template
                  >
                  <td
                    v-else
                    colspan="6"
                    :rowspan="entry.openings.length || 1"
                    class="tax-close-end tax-unclosed"
                  >
                    开仓流水
                  </td>
                  <td :rowspan="entry.openings.length || 1" class="tax-num">
                    {{ taxMoney(entry.fees, entry.record.currency) }}
                  </td>
                  <td
                    :rowspan="entry.openings.length || 1"
                    class="tax-num tax-emphasis tax-result-profit"
                  >
                    {{ taxMoney(entry.profit, entry.record.currency)
                    }}<small v-if="!entry.complete">仅含已匹配部分</small>
                  </td>
                  <td
                    :rowspan="entry.openings.length || 1"
                    class="tax-num tax-result-dividend"
                  >
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

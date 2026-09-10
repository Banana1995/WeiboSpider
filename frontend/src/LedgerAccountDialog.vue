<script setup lang="ts">
import { computed, ref } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import { decimal, newID, PendingWrite, type Account, type AccountInput, type Currency, type Instrument, type OpeningPosition } from "./ledger";
import { todayShanghai, validDay } from "./ledgerView";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
const props = defineProps<{ instruments: Instrument[] }>();
const emit = defineEmits<{ close: []; created: [id: string] }>();
const { locked, error, send } = useLedgerWorkspace();
const name = ref("");
const currency = ref<Currency>("CNY");
const openingDate = ref(todayShanghai());
const mode = ref("reported");
const cash = ref("");
const positions = ref<OpeningPosition[]>([]);
const validation = ref("");
const initial = openingDate.value;
const dirty = computed(() => !!name.value || currency.value !== "CNY" || openingDate.value !== initial || mode.value !== "reported" || !!cash.value || !!positions.value.length);
error.value = "";
function save() {
  if (locked.value) return;
  validation.value = "";
  if (!name.value.trim() || !validDay(openingDate.value)) { validation.value = "请填写账户名称和有效的开始日期。"; return; }
  const id = newID();
  const base = { id, name: name.value.trim(), currency: currency.value, opening_date: openingDate.value };
  const done = () => { emit("created", id); emit("close"); };
  if (mode.value === "reported") {
    send(new PendingWrite<Account>("/reported-accounts", "POST", base), "创建账户", a => a?.id === id && a.name === base.name && a.currency === base.currency, done);
    return;
  }
  if (!decimal(cash.value, 2) || cash.value.startsWith("-") || positions.value.some(p =>
    !props.instruments.some(i => i.id === p.instrument_id) || !decimal(p.quantity, 6) || p.quantity.startsWith("-") || !/[1-9]/.test(p.quantity) ||
    [p.cost, p.diluted_basis].some(v => v !== null && v !== "" && (!decimal(v, 2) || v.startsWith("-")))) ||
    new Set(positions.value.map(p => p.instrument_id)).size !== positions.value.length) {
    validation.value = "请检查期初现金、正数量及成本精度，证券不可重复。金额最多两位、数量最多六位小数。"; return;
  }
  const payload: AccountInput = { ...base, opening_cash: cash.value, positions: positions.value.map(p => ({ ...p, cost: p.cost || null, diluted_basis: p.diluted_basis || null })) };
  send(new PendingWrite<Account>("/accounts", "POST", payload, payload), "创建持仓交易账户", a => a?.id === id && a.currency === base.currency, done);
}
</script>

<template>
  <LedgerDialog title="新建账户" caption="开启另一份投资计划" :dirty="dirty" @close="emit('close')" v-slot="{ requestClose }">
    <form class="lp-form" @submit.prevent="save"><fieldset :disabled="locked">
      <label>账户名称<input v-model="name" required maxlength="160" placeholder="给这份计划起个名字" /></label>
      <div class="lp-two-fields"><label>记账币种<select v-model="currency"><option>CNY</option><option>HKD</option><option>USD</option></select></label>
        <label>开始日期<input v-model="openingDate" type="date" required /></label></div>
      <label>记录方式<select v-model="mode"><option value="reported">记录转入、转出和总资产</option><option value="holdings">按持仓交易记账（含期初）</option></select></label>
      <p class="lp-field-hint">账户名称、币种和期初创建后不可修改。普通账户的开始日期不会自动生成资金或资产。</p>
      <template v-if="mode === 'holdings'">
        <label>期初现金（不是总资产）<input v-model="cash" required inputmode="decimal" /></label>
        <p class="lp-field-hint">需要登记证券时，请先到“管理账户 / 当前持仓 / 登记证券”。成本留空表示未知，零表示明确无成本。</p>
        <div v-for="(p, index) in positions" :key="index" class="lp-opening-position">
          <label>期初证券<select v-model="p.instrument_id" required><option value="">请选择</option><option v-for="i in instruments" :key="i.id" :value="i.id">{{ i.name }} · {{ i.currency }}</option></select></label>
          <label>数量<input v-model="p.quantity" required inputmode="decimal" /></label>
          <div class="lp-two-fields"><label>剩余成本<input v-model="p.cost" inputmode="decimal" placeholder="未知" /></label><label>摊薄基数<input v-model="p.diluted_basis" inputmode="decimal" placeholder="未知" /></label></div>
          <button type="button" class="lp-text-button" @click="positions.splice(index, 1)">移除此持仓</button>
        </div>
        <button type="button" :disabled="positions.length >= 200" @click="positions.push({ instrument_id: '', quantity: '', cost: null, diluted_basis: null })">添加期初持仓</button>
      </template>
      <p v-if="validation" class="lp-error" role="alert">{{ validation }}</p>
      <div class="lp-dialog-footer"><button type="button" @click="requestClose">取消</button><button type="submit" class="lp-primary">创建账户</button></div>
    </fieldset></form>
  </LedgerDialog>
</template>

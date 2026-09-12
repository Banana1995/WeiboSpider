<script setup lang="ts">
import { computed, ref } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import {
  newID,
  PendingWrite,
  type Account,
  type Currency,
  type Instrument,
} from "./ledger";
import { todayShanghai, validDay } from "./ledgerView";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
defineProps<{ instruments: Instrument[] }>();
const emit = defineEmits<{ close: []; created: [id: string] }>();
const { locked, error, send } = useLedgerWorkspace();
const name = ref("");
const currency = ref<Currency>("CNY");
const openingDate = ref(todayShanghai());
const validation = ref("");
const initial = openingDate.value;
const dirty = computed(
  () =>
    !!name.value || currency.value !== "CNY" || openingDate.value !== initial,
);
error.value = "";
function save() {
  if (locked.value) return;
  validation.value = "";
  if (!name.value.trim() || !validDay(openingDate.value)) {
    validation.value = "请填写账户名称和有效的开始日期。";
    return;
  }
  const id = newID();
  const base = {
    id,
    name: name.value.trim(),
    currency: currency.value,
    opening_date: openingDate.value,
  };
  const done = () => {
    emit("created", id);
    emit("close");
  };
  send(
    new PendingWrite<Account>("/accounts", "POST", base),
    "创建账户",
    (a) => a?.id === id && a.name === base.name && a.currency === base.currency,
    done,
  );
}
</script>

<template>
  <LedgerDialog
    title="新建账户"
    caption="开启另一份投资计划"
    :dirty="dirty"
    @close="emit('close')"
    v-slot="{ requestClose }"
  >
    <form class="lp-form" @submit.prevent="save">
      <fieldset :disabled="locked">
        <label
          >账户名称<input
            v-model="name"
            required
            maxlength="160"
            placeholder="给这份计划起个名字"
        /></label>
        <div class="lp-two-fields">
          <label
            >记账币种<select v-model="currency">
              <option>CNY</option>
              <option>HKD</option>
              <option>USD</option>
            </select></label
          >
          <label
            >开始日期<input v-model="openingDate" type="date" required
          /></label>
        </div>
        <p class="lp-field-hint">
          名称、币种和开始日期创建后不可修改。创建后可添加历史记录和当前持仓。
        </p>
        <p v-if="validation" class="lp-error" role="alert">{{ validation }}</p>
        <div class="lp-dialog-footer">
          <button type="button" @click="requestClose">取消</button
          ><button type="submit" class="lp-primary">创建账户</button>
        </div>
      </fieldset>
    </form>
  </LedgerDialog>
</template>

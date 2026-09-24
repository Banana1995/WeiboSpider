<script setup lang="ts">
import { computed, ref } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import { newID, PendingWrite, type Account, type Currency } from "./ledger";
import { validPortfolio, type Portfolio } from "./ledgerPortfolios";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{
  accounts: Account[];
  portfolio?: Portfolio;
  remove?: boolean;
}>();
const emit = defineEmits<{
  close: [];
  saved: [id: string];
  deleted: [];
  reload: [];
}>();
const { locked, error, send } = useLedgerWorkspace();
// The draft and its CAS version belong to the same opening snapshot. A late
// list refresh must not silently upgrade that version underneath edited fields.
const original = props.portfolio
  ? { ...props.portfolio, account_ids: [...props.portfolio.account_ids] }
  : undefined;
const name = ref(original?.name ?? "");
const currency = ref<Currency>(original?.currency ?? "CNY");
const selected = ref([...(original?.account_ids ?? [])]);
const search = ref("");
const validation = ref("");
const missing = computed(() =>
  selected.value.filter((id) => !props.accounts.some((a) => a.id === id)),
);
const filtered = computed(() =>
  props.accounts.filter((a) =>
    a.name
      .toLocaleLowerCase()
      .includes(search.value.trim().toLocaleLowerCase()),
  ),
);
const dirty = computed(
  () =>
    !props.remove &&
    (name.value !== (original?.name ?? "") ||
      currency.value !== (original?.currency ?? "CNY") ||
      [...selected.value].sort().join(",") !==
        (original?.account_ids ?? []).join(",")),
);
const title = props.remove
  ? "删除账户组合"
  : original
    ? "编辑账户组合"
    : "新建账户组合";
error.value = "";

function save() {
  if (locked.value) return;
  const accountIDs = [...selected.value].sort();
  if (
    !name.value.trim() ||
    !accountIDs.length ||
    accountIDs.length > 50 ||
    missing.value.length ||
    !["CNY", "HKD", "USD"].includes(currency.value)
  ) {
    validation.value = "请填写组合名称、选择组合币种及 1～50 个仍存在的账户。";
    return;
  }
  validation.value = "";
  const id = original?.id ?? newID();
  const payload = {
    name: name.value.trim(),
    currency: currency.value,
    account_ids: accountIDs,
    ...(original ? { expected_version: original.version } : { id }),
  };
  const version = original ? (BigInt(original.version) + 1n).toString() : "1";
  send(
    new PendingWrite<Portfolio>(
      original ? `/portfolios/${encodeURIComponent(id)}` : "/portfolios",
      original ? "PUT" : "POST",
      payload,
    ),
    original ? "保存组合" : "创建组合",
    (p) =>
      validPortfolio(p) &&
      p.id === id &&
      p.name === payload.name &&
      p.currency === payload.currency &&
      p.version === version &&
      p.account_ids.join(",") === accountIDs.join(","),
    () => {
      emit("saved", id);
      emit("close");
    },
  );
}

function deletePortfolio() {
  const p = original;
  if (!p || locked.value) return;
  send(
    new PendingWrite<{ portfolio_id: string; deleted: boolean }>(
      `/portfolios/${encodeURIComponent(p.id)}`,
      "DELETE",
      { expected_version: p.version },
    ),
    "删除组合",
    (r) => r?.portfolio_id === p.id && r.deleted === true,
    () => {
      emit("deleted");
      emit("close");
    },
  );
}
</script>

<template>
  <LedgerDialog
    :title="title"
    :dirty="dirty"
    @close="emit('close')"
    v-slot="{ requestClose }"
  >
    <div v-if="remove" class="lp-form">
      <p>删除「{{ original?.name }}」这份组合定义？</p>
      <p class="lp-field-hint">
        删除的是组合名称和成员关系，各账户的原始记录仍由账户管理。
      </p>
      <div class="lp-dialog-footer">
        <button :disabled="locked" @click="requestClose">取消</button>
        <button
          class="lp-danger-button"
          :disabled="locked"
          @click="deletePortfolio"
        >
          删除组合
        </button>
      </div>
    </div>
    <form v-else class="lp-form" @submit.prevent="save">
      <fieldset :disabled="locked">
        <label
          >组合名称<input
            v-model="name"
            maxlength="160"
            required
            placeholder="例如：家庭股票投资"
        /></label>
        <label
          >组合币种<select v-model="currency" data-test="portfolio-currency">
            <option value="CNY">人民币 CNY</option>
            <option value="HKD">港币 HKD</option>
            <option value="USD">美元 USD</option>
          </select></label
        >
        <p class="lp-field-hint">
          可选择不同币种的账户。按最新可用汇率统一折算为组合币种；历史资产和资金流使用同一组汇率，不计历史汇率波动收益。
        </p>
        <label
          >搜索成员账户<input
            v-model="search"
            type="search"
            placeholder="按账户名称搜索"
        /></label>
        <div
          class="lp-portfolio-choices"
          role="group"
          aria-label="选择组合成员"
        >
          <label v-for="a in filtered" :key="a.id" class="lp-portfolio-choice">
            <input v-model="selected" type="checkbox" :value="a.id" />
            <span
              ><strong>{{ a.name }}</strong
              ><small>账户开始于 {{ a.opening_date }}</small></span
            >
            <small>{{ a.currency }}</small>
          </label>
          <p v-if="!filtered.length" class="lp-muted">没有匹配的账户。</p>
          <label v-for="id in missing" :key="id" class="lp-portfolio-choice">
            <input v-model="selected" type="checkbox" :value="id" />
            <span
              >已删除的成员<small>{{ id }}，请取消选择</small></span
            >
          </label>
        </div>
        <p class="lp-field-hint">
          已选 {{ selected.length }} 个账户 · 组合币种
          {{
            currency
          }}。保存后按所选成员重新计算历史，同一账户可以用于多个组合。
        </p>
        <p v-if="validation" class="lp-error" role="alert">{{ validation }}</p>
        <div class="lp-dialog-footer">
          <button type="button" @click="requestClose">取消</button>
          <button type="submit" class="lp-primary">
            {{ original ? "保存更改" : "创建组合" }}
          </button>
        </div>
      </fieldset>
    </form>
    <p
      v-if="
        error.includes('[version_conflict]') ||
        error.includes('[portfolio_member_missing]')
      "
      class="lp-form"
    >
      <button :disabled="locked" @click="emit('reload')">
        关闭并重新读取最新成员
      </button>
    </p>
  </LedgerDialog>
</template>

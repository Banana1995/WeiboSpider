<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import {
  LedgerError,
  newID,
  PendingWrite,
  request,
  type Instrument,
} from "./ledger";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

type SearchItem = Omit<Instrument, "id">;
const props = defineProps<{ instruments: Instrument[] }>();
const emit = defineEmits<{ close: []; registered: [] }>();
const { locked, error, send } = useLedgerWorkspace();
error.value = "";
const empty = (): SearchItem => ({
  name: "",
  market: "SH",
  code: "",
  currency: "CNY",
});
const instrument = ref(empty());
const query = ref("");
const search = reactive(useLedgerRead<SearchItem[]>());
const registrationError = ref("");
const filled = ref(false);
const dirty = computed(
  () =>
    !!query.value ||
    JSON.stringify(instrument.value) !== JSON.stringify(empty()),
);
let generation = 0;
watch(
  query,
  () => {
    generation++;
    search.clear();
    registrationError.value = "";
    if (filled.value) instrument.value = empty();
    filled.value = false;
  },
  { flush: "sync" },
);

function select(item: SearchItem) {
  if (locked.value) return;
  instrument.value = { ...item };
  filled.value = true;
  registrationError.value = "";
}
async function lookup() {
  if (locked.value || search.loading) return;
  const code = query.value.trim().toLowerCase();
  registrationError.value = "";
  search.clear();
  if (!/^(?:\d{5,6}|(?:sh|sz)\d{6}|hk\d{5})$/.test(code)) {
    registrationError.value =
      "请输入完整代码：沪深六位、港股五位，也可带 sh、sz、hk 前缀。";
    return;
  }
  const current = ++generation;
  await search.load(async (signal) => {
    const result = await request<{ items: SearchItem[] }>(
      `/instruments/search?code=${encodeURIComponent(code)}`,
      { signal },
    );
    const market = code.match(/^(sh|sz|hk)/)?.[0]?.toUpperCase();
    const bareCode = code.replace(/^(sh|sz|hk)/, "");
    if (
      !result ||
      !Array.isArray(result.items) ||
      result.items.length > 3 ||
      result.items.some(
        (i) =>
          !i ||
          typeof i.name !== "string" ||
          !i.name.trim() ||
          i.name.length > 160 ||
          !["SH", "SZ", "HK"].includes(i.market) ||
          (market && i.market !== market) ||
          i.code !== bareCode ||
          !(i.market === "HK" ? /^\d{5}$/ : /^\d{6}$/).test(i.code) ||
          !["CNY", "HKD", "USD"].includes(i.currency),
      ) ||
      new Set(result.items.map((i) => `${i.market}/${i.code}`)).size !==
        result.items.length
    )
      throw new LedgerError("invalid_response");
    return result.items;
  });
  if (current === generation && search.data?.length === 1)
    select(search.data[0]!);
}
function register() {
  if (locked.value || search.loading) return;
  const i = instrument.value;
  const payload: Instrument = {
    ...i,
    id: newID(),
    name: i.name.trim(),
    code: i.code.trim().toUpperCase(),
  };
  if (payload.market === "HK" && /^\d{1,5}$/.test(payload.code))
    payload.code = payload.code.padStart(5, "0");
  if (
    !payload.name ||
    !payload.code ||
    (["SH", "SZ"].includes(payload.market) && !/^\d{6}$/.test(payload.code)) ||
    (payload.market === "HK" && !/^\d{5}$/.test(payload.code))
  ) {
    registrationError.value =
      "请填写名称与正确的证券代码：沪深六位、港股五位。";
    return;
  }
  if (
    props.instruments.some(
      (existing) =>
        existing.market === payload.market && existing.code === payload.code,
    )
  ) {
    registrationError.value =
      "该证券已登记，可直接在持仓或交易中选择，无需重复登记。";
    return;
  }
  send(
    new PendingWrite<Instrument>("/instruments", "POST", payload),
    "登记证券",
    (result) =>
      result?.id === payload.id &&
      result.name === payload.name &&
      result.market === payload.market &&
      result.code === payload.code &&
      result.currency === payload.currency,
    () => emit("registered"),
  );
}
</script>

<template>
  <LedgerDialog
    title="登记证券"
    caption="供持仓与交易使用"
    :dirty="dirty"
    @close="emit('close')"
    v-slot="{ requestClose }"
  >
    <form
      class="lp-form"
      data-test="instrument-form"
      @submit.prevent="register"
    >
      <fieldset :disabled="locked">
        <div>
          <label for="security-search">按证券代码查询</label>
          <div class="lp-security-search">
            <input
              id="security-search"
              v-model="query"
              name="security_search"
              placeholder="如 600519、000001、00700"
              maxlength="8"
              autocomplete="off"
              @keydown.enter.prevent="lookup"
            />
            <button
              type="button"
              :disabled="search.loading || !query.trim()"
              @click="lookup"
            >
              {{ search.loading ? "查询中…" : "查询证券" }}
            </button>
          </div>
        </div>
        <p class="lp-field-hint">
          支持沪深港股票代码。查询不登记证券；也可在下方手工填写。
        </p>
        <p v-if="search.loading" role="status">
          正在查询证券并核验名称、市场和币种…
        </p>
        <p v-if="search.error" class="lp-error" role="alert">
          {{ search.error }} 可重试或手工填写。
        </p>
        <p v-if="search.data?.length === 0" role="status">
          未找到支持的股票，请核对代码或手工填写。
        </p>
        <div
          v-if="search.data && search.data.length > 1"
          class="lp-search-results"
        >
          <p>找到多个市场的证券，请选择：</p>
          <button
            v-for="item in search.data"
            :key="`${item.market}/${item.code}`"
            type="button"
            @click="select(item)"
          >
            {{ item.name }} · {{ item.market }} / {{ item.code }} ·
            {{ item.currency }}
          </button>
        </div>
        <p v-if="filled" class="lp-notice" role="status">
          已按腾讯查询结果回填，请核对后登记。手工修改后以表单为准。
        </p>
        <fieldset class="lp-security-fields" :disabled="search.loading">
          <label
            >证券名称<input
              v-model="instrument.name"
              name="security_name"
              required
              maxlength="160"
          /></label>
          <div class="lp-two-fields">
            <label
              >市场<select v-model="instrument.market" name="security_market">
                <option value="SH">上海 SH</option>
                <option value="SZ">深圳 SZ</option>
                <option value="HK">香港 HK</option>
                <option value="US">美国 US（无自动行情）</option>
              </select></label
            >
            <label
              >证券代码<input
                v-model="instrument.code"
                name="security_code"
                required
                maxlength="32"
            /></label>
          </div>
          <label
            >证券原币<select
              v-model="instrument.currency"
              name="security_currency"
            >
              <option>CNY</option>
              <option>HKD</option>
              <option>USD</option>
            </select></label
          >
          <p class="lp-field-hint">
            市场、代码和币种登记后不可修改。币种以查询结果或证券资料为准，不填写模拟价格。查询成功不代表支持自动估值；目前港股非
            HKD 柜台、美股等不支持自动估值。
          </p>
        </fieldset>
        <p v-if="registrationError" class="lp-error" role="alert">
          {{ registrationError }}
        </p>
        <div class="lp-dialog-footer">
          <button type="button" @click="requestClose">取消</button
          ><button type="submit" class="lp-primary" :disabled="search.loading">
            登记证券
          </button>
        </div>
      </fieldset>
    </form>
  </LedgerDialog>
</template>

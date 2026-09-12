<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  reactive,
  ref,
  shallowRef,
  watch,
} from "vue";
import LedgerSecurityDialog from "./LedgerSecurityDialog.vue";
import { money } from "./ledgerView";
import { holdingNumber, type HoldingsView } from "./holdings";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
import {
  decimal,
  errorText,
  failure,
  LedgerError,
  PendingWrite,
  request,
  type Instrument,
} from "./ledger";
import {
  validateCurrentHoldings,
  type CurrentHoldings,
  type CurrentHoldingsInput,
  type CurrentPosition,
} from "./currentHoldings";
import { useLedgerRead } from "./useLedgerRead";

const props = defineProps<{
  accountId: string;
  currency: string;
  instruments: Instrument[];
  disabled: boolean;
  refreshKey: number;
  valuation?: HoldingsView;
  valuationLoading?: boolean;
  valuationError?: string;
}>();
const emit = defineEmits<{
  locked: [boolean];
  configured: [boolean, string];
  saved: [];
  refresh: [];
}>();
const { draftLock } = useLedgerWorkspace();
const read = reactive(useLedgerRead<CurrentHoldings>());
const cash = ref("0.00");
const positions = ref<CurrentPosition[]>([]);
const pending = shallowRef<PendingWrite<CurrentHoldings>>();
const busy = ref(false);
const error = ref("");
const message = ref("");
const editing = ref(false);
const editTarget = ref("cash");
const removing = ref(false);
const discard = ref(false);
const panel = ref<HTMLElement>();
let opener: HTMLElement | null = null;
const securities = ref<Instrument[]>([]);
const selecting = ref<number | null>(null);
const securityDirty = ref(false);
const original = ref("");
const dirty = computed(
  () =>
    securityDirty.value ||
    JSON.stringify([cash.value, positions.value]) !== original.value,
);
const identity = (id: string) =>
  securities.value.find((i) => i.id === id) ??
  props.instruments.find((i) => i.id === id) ??
  props.valuation?.items.find((i) => i.instrument.id === id)?.instrument;
const instrumentName = (id: string) => identity(id)?.name ?? "未知证券";
const quotes = computed(() =>
  !props.valuationLoading &&
  !props.valuationError &&
  props.valuation?.account_id === props.accountId &&
  props.valuation?.manual_version === read.data?.snapshot?.version
    ? props.valuation
    : undefined,
);
const quote = (id: string) =>
  quotes.value?.items.find((i) => i.instrument.id === id);
const blocked = computed(
  () =>
    props.disabled ||
    !!pending.value ||
    read.loading ||
    !!read.error ||
    !read.data,
);
watch(
  editing,
  (value) => {
    draftLock.value = value;
  },
  { flush: "sync" },
);
function closeEdit() {
  if (pending.value || busy.value) return;
  if (dirty.value && !discard.value) {
    discard.value = true;
    void nextTick(() =>
      panel.value
        ?.querySelector<HTMLButtonElement>(".lp-discard button")
        ?.focus(),
    );
    return;
  }
  editing.value = false;
  selecting.value = null;
  discard.value = false;
  error.value = "";
  void nextTick(() =>
    (opener?.isConnected
      ? opener
      : panel.value?.querySelector<HTMLButtonElement>("button")
    )?.focus(),
  );
}
function resume() {
  discard.value = false;
  void nextTick(() =>
    panel.value?.querySelector<HTMLInputElement>("input")?.focus(),
  );
}
watch(error, (value) => {
  if (value)
    void nextTick(() =>
      panel.value?.querySelector<HTMLElement>("[data-holdings-error]")?.focus(),
    );
});
function beforeUnload(event: BeforeUnloadEvent) {
  if (pending.value || (editing.value && dirty.value)) {
    event.preventDefault();
    event.returnValue = "";
  }
}
window.addEventListener("beforeunload", beforeUnload);
onBeforeUnmount(() => {
  draftLock.value = false;
  window.removeEventListener("beforeunload", beforeUnload);
});
function selectSecurity(i: Instrument) {
  const index = selecting.value;
  if (index === null) return;
  if (
    positions.value.some(
      (p, n) =>
        n !== index &&
        identity(p.instrument_id)?.market === i.market &&
        identity(p.instrument_id)?.code === i.code,
    )
  ) {
    error.value = "本账户已持有该市场和代码的证券，请编辑已有持仓。";
    return;
  }
  securities.value.push(i);
  if (index === positions.value.length)
    positions.value.push({ instrument_id: i.id, quantity: "" });
  else positions.value[index]!.instrument_id = i.id;
  selecting.value = null;
  securityDirty.value = false;
  editTarget.value = i.id;
  error.value = "";
  void nextTick(() =>
    panel.value?.querySelector<HTMLInputElement>("input")?.focus(),
  );
}
function openEdit(target = "cash", remove = false) {
  if (blocked.value || editing.value) return;
  opener =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
  cash.value = read.data?.snapshot?.cash ?? "0.00";
  positions.value =
    read.data?.snapshot?.positions.map((p) => ({
      ...p,
      quantity: holdingNumber(p.quantity).replaceAll(",", ""),
    })) ?? [];
  securities.value = [];
  securityDirty.value = false;
  original.value = JSON.stringify([cash.value, positions.value]);
  error.value = "";
  editing.value = true;
  editTarget.value = target;
  removing.value = remove;
  discard.value = false;
  message.value = "";
  void nextTick(() =>
    panel.value?.querySelector<HTMLInputElement>("input")?.focus(),
  );
}
function addHolding() {
  openEdit("add");
  if (!editing.value) return;
  selecting.value = positions.value.length;
}
function reloadDraft() {
  if (pending.value || props.disabled) return;
  if (
    !dirty.value ||
    window.confirm("重新读取将放弃当前持仓草稿，是否继续？")
  ) {
    error.value = "";
    editing.value = false;
    selecting.value = null;
    void load();
  }
}
let generation = 0;
async function load() {
  if (pending.value) return;
  const current = ++generation;
  const id = props.accountId;
  read.clear();
  emit("configured", false, "");
  await read.load(async (signal) =>
    validateCurrentHoldings(
      await request<CurrentHoldings>(`/accounts/${id}/current-holdings`, {
        signal,
      }),
      id,
    ),
  );
  if (
    current !== generation ||
    props.accountId !== id ||
    !read.data ||
    read.error
  )
    return;
  cash.value = read.data.snapshot?.cash ?? "0.00";
  positions.value = read.data.snapshot?.positions.map((p) => ({ ...p })) ?? [];
  original.value = JSON.stringify([cash.value, positions.value]);
  emit("configured", read.data.snapshot !== null, read.data.audit_id);
}
defineExpose({ load });
watch(
  () => [props.accountId, props.refreshKey],
  () => {
    error.value = "";
    message.value = "";
    void load();
  },
  { immediate: true },
);
async function save() {
  if (
    busy.value ||
    (!pending.value && error.value.includes("[version_conflict]")) ||
    (!pending.value &&
      (props.disabled || read.loading || read.error || !read.data))
  )
    return;
  error.value = "";
  if (!pending.value) {
    if (
      !decimal(cash.value, 2) ||
      cash.value.startsWith("-") ||
      positions.value.some(
        (p) =>
          !identity(p.instrument_id) ||
          !decimal(p.quantity, 6) ||
          p.quantity.startsWith("-") ||
          !/[1-9]/.test(p.quantity),
      ) ||
      new Set(
        positions.value.map(
          (p) =>
            `${identity(p.instrument_id)?.market}/${identity(p.instrument_id)?.code}`,
        ),
      ).size !== positions.value.length
    ) {
      error.value =
        "请填写非负现金（最多 2 位小数）、正数量（最多 6 位小数），证券不得重复。";
      return;
    }
    const input: CurrentHoldingsInput = {
      expected_version: read.data!.snapshot?.version ?? "0",
      cash: cash.value,
      positions: positions.value
        .filter((p) => !removing.value || p.instrument_id !== editTarget.value)
        .map((p) => ({ ...p })),
      securities: securities.value.filter((i) =>
        positions.value.some((p) => p.instrument_id === i.id),
      ),
    };
    pending.value = new PendingWrite(
      `/accounts/${props.accountId}/current-holdings`,
      "PUT",
      input,
    );
    emit("locked", true);
  }
  busy.value = true;
  try {
    const result = await pending.value.run();
    try {
      validateCurrentHoldings(result, props.accountId);
      const input = JSON.parse(pending.value.body) as CurrentHoldingsInput;
      const units = (v: string, scale: number) => {
        const [w, f = ""] = v.split(".");
        return BigInt(w! + f.padEnd(scale, "0"));
      };
      const s = result.snapshot;
      if (
        !s ||
        BigInt(s.version) !== BigInt(input.expected_version) + 1n ||
        units(s.cash, 2) !== units(input.cash, 2) ||
        s.positions.length !== input.positions.length ||
        input.positions.some(
          (p) =>
            !s.positions.some(
              (q) =>
                q.instrument_id === p.instrument_id &&
                units(q.quantity, 6) === units(p.quantity, 6),
            ),
        )
      )
        throw new LedgerError("invalid_response");
    } catch (e) {
      pending.value.uncertain = true;
      throw e;
    }
  } catch (e) {
    error.value = failure(e);
    if (!pending.value.uncertain) {
      pending.value = undefined;
      emit("locked", false);
    }
    return;
  } finally {
    busy.value = false;
  }
  pending.value = undefined;
  emit("locked", false);
  editing.value = false;
  emit("saved");
  await load();
  message.value = "持仓已保存，历史记录不变。";
  void nextTick(() =>
    (opener?.isConnected
      ? opener
      : panel.value?.querySelector<HTMLButtonElement>("button")
    )?.focus(),
  );
}
</script>

<template>
  <section ref="panel" data-test="current-holdings">
    <div class="lp-section-title lp-holdings-title">
      <h2>当前持仓</h2>
      <div class="lp-actions">
        <button
          :disabled="disabled || editing || read.loading || valuationLoading"
          @click="emit('refresh')"
        >
          {{ read.loading || valuationLoading ? "刷新中…" : "刷新行情" }}
        </button>
        <button
          class="lp-primary"
          :disabled="
            blocked ||
            editing ||
            (read.data?.snapshot?.positions.length ?? 0) >= 200
          "
          @click="addHolding"
        >
          添加持仓
        </button>
      </div>
    </div>
    <p class="lp-muted">
      当前参考估值，不是账本总资产；调整持仓不改变历史资产和资金记录。
    </p>
    <p v-if="read.loading || valuationLoading" role="status">
      正在读取持仓与参考行情…
    </p>
    <p v-if="read.error || valuationError" class="lp-error" role="alert">
      {{ errorText(read.error || valuationError || "") }}
    </p>
    <p v-if="message" role="status">{{ message }}</p>
    <div v-if="read.data" class="lp-holdings-totals">
      <div>
        <span>当前现金 · {{ currency }}</span>
        <strong v-if="!(editing && editTarget === 'cash')">{{
          money(read.data.snapshot?.cash)
        }}</strong>
        <button
          v-if="!(editing && editTarget === 'cash')"
          class="lp-text-button lp-cash-edit"
          :disabled="blocked || editing"
          @click="openEdit('cash')"
        >
          {{ read.data.snapshot ? "编辑现金" : "设置现金" }}
        </button>
        <form
          v-else
          data-test="current-holdings-form"
          class="lp-inline-editor"
          @submit.prevent="save"
        >
          <label
            >现金金额（{{ currency }}）<input
              v-model="cash"
              name="current_cash"
              inputmode="decimal"
              required
              :disabled="blocked"
          /></label>
          <div class="lp-actions">
            <button
              type="submit"
              class="lp-primary"
              :disabled="blocked || error.includes('[version_conflict]')"
            >
              保存现金</button
            ><button type="button" :disabled="!!pending" @click="closeEdit">
              取消
            </button>
          </div>
        </form>
      </div>
      <div class="lp-reference-total">
        <span>参考总资产 · {{ currency }}</span
        ><strong>{{ money(quotes?.total_assets) }}</strong
        ><small>现金 + 证券折合市值</small>
      </div>
    </div>
    <p v-if="quotes && !quotes.complete" class="lp-reference" role="status">
      部分行情或汇率不可用，无法计算完整参考估值。
    </p>
    <p
      v-else-if="
        read.data?.snapshot &&
        valuation &&
        !quotes &&
        !valuationLoading &&
        !valuationError
      "
      role="status"
      class="lp-reference"
    >
      持仓已变化，请刷新行情后查看参考估值。
    </p>
    <table
      v-if="read.data?.snapshot?.positions.length"
      class="lp-positions-table"
      aria-label="证券持仓"
    >
      <thead>
        <tr>
          <th scope="col">证券 / 原币</th>
          <th scope="col">数量</th>
          <th scope="col">参考价格 / 原币</th>
          <th scope="col">市值 / 原币与折合</th>
          <th scope="col">操作</th>
        </tr>
      </thead>
      <tbody>
        <template
          v-for="p in read.data.snapshot.positions"
          :key="p.instrument_id"
        >
          <tr>
            <td class="lp-security-cell">
              <strong>{{ instrumentName(p.instrument_id) }}</strong
              ><small
                >{{ identity(p.instrument_id)?.market }} /
                {{ identity(p.instrument_id)?.code }} ·
                {{ identity(p.instrument_id)?.currency }}</small
              >
            </td>
            <td data-label="数量">{{ holdingNumber(p.quantity) }}</td>
            <td data-label="参考价格 / 原币">
              <span
                >{{ holdingNumber(quote(p.instrument_id)?.price) }}
                {{ identity(p.instrument_id)?.currency }}</span
              ><small
                v-if="quote(p.instrument_id)?.quote_status === 'prior_date'"
                >较早交易日
                <time class="lp-quote-date">{{
                  quote(p.instrument_id)?.quote?.date
                }}</time></small
              ><small
                v-if="quote(p.instrument_id)?.quote_status === 'unavailable'"
                >行情或汇率暂不可用</small
              >
            </td>
            <td data-label="市值">
              <span
                >原币 {{ money(quote(p.instrument_id)?.market_value) }}
                {{ identity(p.instrument_id)?.currency }}</span
              ><small v-if="identity(p.instrument_id)?.currency !== currency"
                >折合 {{ money(quote(p.instrument_id)?.account_market_value) }}
                {{ currency }}</small
              >
            </td>
            <td class="lp-position-actions">
              <button
                class="lp-text-button"
                :aria-label="`编辑 ${instrumentName(p.instrument_id)}`"
                :disabled="blocked || editing"
                @click="openEdit(p.instrument_id)"
              >
                编辑</button
              ><button
                class="lp-text-button"
                :aria-label="`删除 ${instrumentName(p.instrument_id)}`"
                :disabled="blocked || editing"
                @click="openEdit(p.instrument_id, true)"
              >
                删除
              </button>
            </td>
          </tr>
          <tr
            v-if="editing && editTarget === p.instrument_id"
            class="lp-position-edit-row"
          >
            <td colspan="5">
              <form
                class="lp-inline-editor"
                data-test="current-holdings-form"
                @submit.prevent="save"
              >
                <p v-if="removing">
                  从当前持仓删除
                  {{ instrumentName(p.instrument_id) }}？历史记录不变。
                </p>
                <label v-else
                  >当前数量<input
                    v-model="
                      positions.find(
                        (item) => item.instrument_id === p.instrument_id,
                      )!.quantity
                    "
                    :name="`current_quantity_${positions.findIndex((item) => item.instrument_id === p.instrument_id)}`"
                    inputmode="decimal"
                    required
                    :disabled="blocked"
                /></label>
                <div class="lp-actions">
                  <button
                    type="submit"
                    :class="removing ? 'lp-danger-button' : 'lp-primary'"
                    :disabled="blocked || error.includes('[version_conflict]')"
                  >
                    {{ removing ? "确认删除" : "保存数量" }}</button
                  ><button
                    type="button"
                    :disabled="!!pending"
                    @click="closeEdit"
                  >
                    取消
                  </button>
                </div>
              </form>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
    <p v-else-if="read.data && !editing" class="lp-muted">
      {{
        read.data.snapshot
          ? "暂无证券持仓，可添加股票；现金也可以为零。"
          : "尚未设置当前持仓。设置当前现金或添加第一只股票。"
      }}
    </p>
    <div
      v-if="
        editing &&
        (selecting !== null ||
          editTarget === 'add' ||
          (!read.data?.snapshot?.positions.some(
            (p) => p.instrument_id === editTarget,
          ) &&
            editTarget !== 'cash'))
      "
      class="lp-add-position"
    >
      <h3>添加持仓</h3>
      <LedgerSecurityDialog
        v-if="selecting !== null"
        embedded
        @dirty="securityDirty = $event"
        @close="closeEdit"
        @selected="selectSecurity"
      />
      <form
        v-else
        class="lp-inline-editor"
        data-test="current-holdings-form"
        @submit.prevent="save"
      >
        <template v-if="editTarget !== 'add'"
          ><strong>{{ instrumentName(editTarget) }}</strong
          ><small
            >{{ identity(editTarget)?.market }} /
            {{ identity(editTarget)?.code }} ·
            {{ identity(editTarget)?.currency }}</small
          ></template
        >
        <label v-if="!read.data?.snapshot && editTarget !== 'add'"
          >当前现金（{{ currency }}）<input
            v-model="cash"
            name="current_cash"
            inputmode="decimal"
            required
            :disabled="blocked"
        /></label>
        <label v-if="positions.some((p) => p.instrument_id === editTarget)"
          >当前数量<input
            v-model="
              positions.find((p) => p.instrument_id === editTarget)!.quantity
            "
            :name="`current_quantity_${positions.length - 1}`"
            inputmode="decimal"
            required
            :disabled="blocked"
        /></label>
        <div class="lp-actions">
          <button
            v-if="editTarget !== 'add'"
            type="submit"
            class="lp-primary"
            :disabled="blocked || error.includes('[version_conflict]')"
          >
            保存持仓</button
          ><button v-else type="button" @click="selecting = positions.length">
            重新选择证券</button
          ><button type="button" :disabled="!!pending" @click="closeEdit">
            取消
          </button>
        </div>
      </form>
    </div>
    <p
      v-if="error"
      class="lp-error"
      role="alert"
      tabindex="-1"
      data-holdings-error
    >
      {{ errorText(error) }}
    </p>
    <button
      v-if="error.includes('[version_conflict]') && !pending"
      :disabled="disabled || read.loading"
      @click="reloadDraft"
    >
      重新读取最新持仓
    </button>
    <div v-if="pending" class="ledger-pending" role="status">
      <p>
        {{
          busy
            ? "正在保存持仓…"
            : "保存结果暂未确认，请保留此页并重试，不会重复保存。"
        }}
      </p>
      <button v-if="!busy" data-test="current-retry" @click="save">
        重试保存持仓
      </button>
    </div>
    <div v-if="discard" class="lp-discard" role="alert">
      <strong>放弃尚未保存的修改？</strong>
      <div class="lp-actions">
        <button @click="resume">继续编辑</button
        ><button
          class="lp-danger-button"
          :disabled="!!pending"
          @click="closeEdit"
        >
          放弃修改
        </button>
      </div>
    </div>
  </section>
</template>

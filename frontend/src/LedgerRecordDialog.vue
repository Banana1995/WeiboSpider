<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import LedgerDialog from "./LedgerDialog.vue";
import LedgerChannelEditor from "./LedgerChannelEditor.vue";
import { channelAssetsTotal, validChannelName } from "./ledgerChannels";
import {
  decimal,
  errorText,
  LedgerError,
  newID,
  PendingWrite,
  query,
  request,
  type Account,
  type Page,
} from "./ledger";
import {
  accountRecordOriginLabels,
  type AccountEntry,
  type AccountRecord,
  type AccountRecordRevision,
  type AccountChannels,
  type ChannelAsset,
} from "./accountRecords";
import {
  money,
  recordKind,
  todayShanghai,
  validDay,
  validRecord,
  opaqueID,
} from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{
  account: Account;
  recordId?: string;
  initialMode: "create" | "edit" | "detail";
}>();
const emit = defineEmits<{ close: [] }>();
const { locked, error, send } = useLedgerWorkspace();
const read = reactive(useLedgerRead<AccountRecord>());
const revisions = reactive(useLedgerRead<Page<AccountRecordRevision>>());
const mode = ref<"create" | "edit" | "detail" | "void">(props.initialMode);
const kind = ref("in");
const date = ref(todayShanghai());
const amount = ref("");
const assets = ref("");
const splitAssets = ref(false);
const withAssets = ref(false);
const hasAssets = computed(
  () =>
    kind.value === "asset" ||
    ((kind.value === "in" || kind.value === "out") && withAssets.value),
);
const channelAssets = ref<ChannelAsset[]>([]);
const channelsEdited = ref(false);
const flowChannel = ref("");
const channels = reactive(useLedgerRead<AccountChannels>());
const channelNames = computed(() => [
  ...new Set([
    ...(channels.data?.items.map((item) => item.name) ?? []),
    ...channelAssets.value.map((item) => item.name).filter(Boolean),
    ...(read.data?.flow_channel ? [read.data.flow_channel] : []),
  ]),
]);
const note = ref("");
const reason = ref("");
const validation = ref("");
const original = ref("");
const historyCursors = ref([""]);
const historyPage = ref(0);
const serialize = () =>
  JSON.stringify([
    kind.value,
    date.value,
    amount.value,
    assets.value,
    note.value,
    reason.value,
    splitAssets.value,
    withAssets.value,
    channelAssets.value,
    flowChannel.value,
  ]);
original.value = serialize();
const dirty = computed(
  () => mode.value !== "detail" && serialize() !== original.value,
);
const title = computed(() =>
  mode.value === "detail"
    ? "记录详情"
    : mode.value === "void"
      ? "作废记录"
      : mode.value === "edit"
        ? "编辑记录"
        : "记一笔",
);
const existingNote = computed(() => read.data?.kind === "log");
const quoteManaged = computed(() => !!read.data?.quote_audit_id);
const conflict = computed(() => error.value.includes("[version_conflict]"));
error.value = "";
function populate() {
  const r = read.data;
  if (!r) return;
  kind.value =
    r.kind === "asset"
      ? "asset"
      : r.kind === "log"
        ? "log"
        : r.flow?.startsWith("-")
          ? "out"
          : "in";
  date.value = r.date;
  amount.value = r.flow?.replace(/^-/, "") ?? "";
  assets.value = r.total_assets ?? "";
  channelAssets.value = r.channel_assets?.map((item) => ({ ...item })) ?? [];
  splitAssets.value = channelAssets.value.length > 0;
  withAssets.value = r.total_assets !== null;
  channelsEdited.value = false;
  flowChannel.value = r.flow_channel ?? channelNames.value[0] ?? "";
  note.value = r.note;
  reason.value = "";
  original.value = serialize();
  if (r.voided) mode.value = "detail";
}
async function load() {
  if (locked.value || !props.recordId) return;
  if (dirty.value && !window.confirm("重新读取会放弃当前草稿，是否继续？"))
    return;
  error.value = "";
  read.clear();
  revisions.clear();
  historyCursors.value = [""];
  historyPage.value = 0;
  const id = props.recordId;
  await read.load(async (signal) => {
    const r = await request<AccountRecord>(
      `/accounts/${encodeURIComponent(props.account.id)}/records/${encodeURIComponent(id)}`,
      { signal },
    );
    if (!validRecord(r, props.account.id) || r.id !== id)
      throw new LedgerError("invalid_response");
    return r;
  });
  if (read.data && !read.error) populate();
}
if (props.recordId) void load();
function edit() {
  populate();
  mode.value = "edit";
}
function addFirstChannel() {
  channelAssets.value = [{ name: "", amount: assets.value }];
  channelsEdited.value = true;
  splitAssets.value = true;
}
function updateChannelAssets(items: ChannelAsset[]) {
  channelAssets.value = items;
  channelsEdited.value = true;
}
function selectKind(value: string) {
  kind.value = value;
  if (!props.recordId) splitAssets.value = channelAssets.value.length > 0;
}
async function loadChannels() {
  channels.clear();
  if (!validDay(date.value)) return;
  const to = date.value;
  await channels.load(async (signal) => {
    const result = await request<AccountChannels>(
      `/accounts/${encodeURIComponent(props.account.id)}/channels${query({ to })}`,
      { signal },
    );
    if (
      !result ||
      result.account_id !== props.account.id ||
      result.as_of !== to ||
      !Array.isArray(result.items) ||
      (result.items.length > 0 &&
        (channelAssetsTotal(result.items) === null ||
          !opaqueID(result.source_record_id) ||
          !validDay(result.source_date) ||
          result.source_date > to)) ||
      (result.items.length === 0 &&
        (result.source_record_id !== "" || result.source_date !== ""))
    )
      throw new LedgerError("invalid_response");
    return result;
  });
  if (!channels.data || channels.error || to !== date.value) return;
  const clean = !dirty.value;
  if (!props.recordId && !channelsEdited.value) {
    channelAssets.value = channels.data.items.map((item) => ({
      name: item.name,
      amount: "",
    }));
    splitAssets.value = channelAssets.value.length > 0;
  }
  if (!channelNames.value.includes(flowChannel.value))
    flowChannel.value = channelNames.value[0] ?? "";
  if (clean) original.value = serialize();
}
watch(
  [date, mode],
  () => {
    if (mode.value === "create" || mode.value === "edit") void loadChannels();
    else channels.clear();
  },
  { immediate: true },
);
function save() {
  if (
    locked.value ||
    channels.loading ||
    !!channels.error ||
    conflict.value ||
    read.data?.voided ||
    (props.recordId && (!read.data || read.error || read.loading))
  )
    return;
  validation.value = "";
  if (!validDay(date.value)) {
    validation.value = "请选择有效的记录日期。";
    return;
  }
  const cashFlow = kind.value === "in" || kind.value === "out";
  const channelEntries = channelAssets.value.map((item) => ({
    ...item,
    name: item.name.trim(),
  }));
  const total = hasAssets.value
    ? splitAssets.value
      ? channelAssetsTotal(channelEntries)
      : assets.value
    : "";
  if (hasAssets.value && splitAssets.value && total === null) {
    validation.value =
      "请填写不重复的渠道名称和每个渠道的非负金额（最多两位小数），并检查合计是否超出范围。";
    return;
  }
  const selectedChannel = cashFlow
    ? flowChannel.value || channelNames.value[0] || ""
    : "";
  if (
    selectedChannel &&
    (!validChannelName(selectedChannel) ||
      !channelNames.value.includes(selectedChannel))
  ) {
    validation.value = "请选择账户已有的资金渠道。";
    return;
  }
  if (
    cashFlow &&
    (!decimal(`${kind.value === "out" ? "-" : ""}${amount.value}`, 2) ||
      amount.value.startsWith("-") ||
      !/[1-9]/.test(amount.value))
  ) {
    validation.value = "转入、转出金额请填写大于零的数字，最多两位小数。";
    return;
  }
  if (
    (hasAssets.value && !total) ||
    (total && (!decimal(total, 2) || total.startsWith("-")))
  ) {
    validation.value = "总资产请填写非负金额，最多两位小数；留空与零不同。";
    return;
  }
  if (kind.value === "log" && (!existingNote.value || !note.value.trim())) {
    validation.value = "请填写原备注内容。";
    return;
  }
  if (read.data && !reason.value.trim()) {
    validation.value = "请简单说明修改原因。";
    return;
  }
  if (quoteManaged.value && kind.value !== "asset") return;
  const entry: AccountEntry = {
    kind: cashFlow ? "cash_flow" : kind.value === "log" ? "log" : "asset",
    date: date.value,
    flow: cashFlow ? `${kind.value === "out" ? "-" : ""}${amount.value}` : null,
    total_assets: kind.value === "log" || total === "" ? null : total,
    note: note.value,
    ...(hasAssets.value && splitAssets.value
      ? { channel_assets: channelEntries }
      : {}),
    ...(selectedChannel ? { flow_channel: selectedChannel } : {}),
  };
  const id = read.data?.id ?? `manual-${newID()}`;
  const path = `/accounts/${encodeURIComponent(props.account.id)}/records`;
  const previous = read.data?.version;
  send(
    new PendingWrite<AccountRecord>(
      previous ? `${path}/${encodeURIComponent(id)}` : path,
      previous ? "PUT" : "POST",
      {
        id,
        entry,
        reason: reason.value,
        expected_version: previous,
      },
    ),
    `${previous ? "修改" : "记录"}${recordKind(entry)}`,
    (r) => validRecord(r, props.account.id) && r.id === id,
    () => emit("close"),
  );
}
function voidRecord() {
  const r = read.data;
  if (locked.value || conflict.value || !r || r.voided || !reason.value.trim())
    return;
  send(
    new PendingWrite<AccountRecord>(
      `/accounts/${encodeURIComponent(props.account.id)}/records/${encodeURIComponent(r.id)}`,
      "DELETE",
      {
        expected_version: r.version,
        reason: reason.value,
      },
    ),
    "作废记录",
    (receipt) =>
      validRecord(receipt, props.account.id) &&
      receipt.id === r.id &&
      receipt.voided,
    () => emit("close"),
  );
}
function loadHistory(page = 0, cursor = "") {
  if (!props.recordId || locked.value) return;
  revisions.clear();
  historyPage.value = page;
  const id = props.recordId;
  void revisions.load(async (signal) => {
    const result = await request<Page<AccountRecordRevision>>(
      `/accounts/${encodeURIComponent(props.account.id)}/records/${encodeURIComponent(id)}/revisions${query({ limit: "30", cursor })}`,
      { signal },
    );
    if (
      !result ||
      !Array.isArray(result.items) ||
      result.items.length > 30 ||
      result.items.some(
        (r) =>
          !r ||
          !validRecord(r.record, props.account.id) ||
          r.record.id !== id ||
          typeof r.reason !== "string",
      ) ||
      (result.next_cursor !== undefined &&
        !/^[1-9]\d*$/.test(result.next_cursor))
    )
      throw new LedgerError("invalid_response");
    return result;
  });
}
function nextHistory() {
  const cursor = revisions.data?.next_cursor;
  if (!cursor) return;
  historyCursors.value.splice(historyPage.value + 1, Infinity, cursor);
  loadHistory(historyPage.value + 1, cursor);
}
</script>

<template>
  <LedgerDialog
    :title="title"
    :caption="account.name"
    :dirty="dirty"
    @close="emit('close')"
    v-slot="{ requestClose }"
  >
    <div v-if="read.loading" class="lp-dialog-body" role="status">
      正在读取最新记录…
    </div>
    <div v-else-if="read.error" class="lp-dialog-body">
      <p class="lp-error" role="alert">{{ errorText(read.error) }}</p>
      <button :disabled="locked" @click="load">重新读取记录</button>
    </div>
    <template v-else-if="!recordId || read.data">
      <div v-if="mode === 'detail' && read.data" class="lp-dialog-body">
        <dl class="lp-info">
          <div>
            <dt>日期</dt>
            <dd>{{ read.data.date }}</dd>
          </div>
          <div>
            <dt>类型</dt>
            <dd>
              {{ recordKind(read.data)
              }}{{ read.data.voided ? " · 已作废" : "" }}
            </dd>
          </div>
          <div>
            <dt>转入</dt>
            <dd>
              {{
                read.data.flow !== null && !read.data.flow.startsWith("-")
                  ? money(read.data.flow)
                  : "—"
              }}
            </dd>
          </div>
          <div>
            <dt>转出</dt>
            <dd>
              {{
                read.data.flow?.startsWith("-")
                  ? money(read.data.flow.slice(1))
                  : "—"
              }}
            </dd>
          </div>
          <div>
            <dt>总资产</dt>
            <dd>{{ money(read.data.total_assets) }} {{ account.currency }}</dd>
          </div>
          <div>
            <dt>备注</dt>
            <dd class="lp-note-text">{{ read.data.note || "—" }}</dd>
          </div>
          <div v-if="read.data.flow_channel">
            <dt>资金渠道</dt>
            <dd>{{ read.data.flow_channel }}</dd>
          </div>
          <div v-for="channel in read.data.channel_assets" :key="channel.name">
            <dt>{{ channel.name }}</dt>
            <dd>{{ money(channel.amount) }} {{ account.currency }}</dd>
          </div>
          <div>
            <dt>来源</dt>
            <dd>
              {{ accountRecordOriginLabels[read.data.origin]
              }}{{ read.data.manual_assertion ? " · 已人工确认" : "" }}
            </dd>
          </div>
        </dl>
        <p
          v-if="read.data.original?.detail && !read.data.channel_assets?.length"
          class="lp-note-text"
        >
          原始明细：{{ read.data.original.detail }}
        </p>
        <div class="lp-actions">
          <template v-if="!read.data.voided"
            ><button :disabled="locked" class="lp-primary" @click="edit">
              编辑记录
            </button>
            <button
              :disabled="locked"
              class="lp-danger-button"
              @click="
                reason = '';
                mode = 'void';
              "
            >
              作废记录
            </button></template
          >
          <button :disabled="locked" @click="load">重新读取</button>
        </div>
        <details
          class="lp-history-section"
          @toggle="
            ($event.currentTarget as HTMLDetailsElement).open &&
            !revisions.data &&
            !revisions.error &&
            !revisions.loading &&
            loadHistory()
          "
        >
          <summary>修改历史</summary>
          <p v-if="revisions.loading" role="status">正在读取修改历史…</p>
          <p v-if="revisions.error" class="lp-error" role="alert">
            {{ errorText(revisions.error) }}
          </p>
          <button
            v-if="revisions.error"
            :disabled="locked || revisions.loading"
            @click="loadHistory(historyPage, historyCursors[historyPage]!)"
          >
            重试读取修改历史
          </button>
          <ol class="lp-history">
            <li v-for="r in revisions.data?.items" :key="r.record.version">
              <strong
                >第 {{ r.record.version }} 版 ·
                {{ r.record.voided ? "已作废" : "有效" }}</strong
              >
              <p>{{ r.reason || "首次记录" }}</p>
              <p>
                {{ r.record.date }} · {{ recordKind(r.record)
                }}<template v-if="r.record.flow !== null">
                  · 资金 {{ money(r.record.flow) }}</template
                >
                · 总资产 {{ money(r.record.total_assets) }}
              </p>
              <p class="lp-note-text">{{ r.record.note || "无备注" }}</p>
              <p v-if="r.record.flow_channel">
                资金渠道：{{ r.record.flow_channel }}
              </p>
              <p v-for="channel in r.record.channel_assets" :key="channel.name">
                {{ channel.name }}：{{ money(channel.amount) }}
                {{ account.currency }}
              </p>
            </li>
          </ol>
          <div class="lp-pagination">
            <button
              :disabled="locked || historyPage === 0 || revisions.loading"
              @click="
                loadHistory(historyPage - 1, historyCursors[historyPage - 1]!)
              "
            >
              上一页
            </button>
            <span>第 {{ historyPage + 1 }} 页</span
            ><button
              :disabled="
                locked ||
                !revisions.data?.next_cursor ||
                revisions.loading ||
                !!revisions.error
              "
              @click="nextHistory"
            >
              下一页
            </button>
          </div>
        </details>
      </div>
      <form
        v-else-if="mode === 'void'"
        class="lp-form"
        @submit.prevent="voidRecord"
      >
        <p>
          确认作废 {{ read.data?.date }} 的{{
            read.data ? recordKind(read.data) : ""
          }}记录？作废后不参与收益计算，修改历史仍保留。
        </p>
        <fieldset :disabled="locked">
          <label
            >作废原因<input v-model="reason" required maxlength="160"
          /></label>
        </fieldset>
        <button v-if="conflict" type="button" :disabled="locked" @click="load">
          重新读取最新记录
        </button>
        <div class="lp-dialog-footer">
          <button type="button" :disabled="locked" @click="requestClose">
            取消</button
          ><button
            class="lp-danger-button"
            :disabled="locked || conflict || !reason.trim()"
          >
            确认作废
          </button>
        </div>
      </form>
      <form v-else class="lp-form" @submit.prevent="save">
        <fieldset :disabled="locked">
          <div
            v-if="!existingNote"
            class="lp-segment lp-form-tabs"
            aria-label="记账类型"
          >
            <button
              v-for="tab in [
                ['in', '转入'],
                ['out', '转出'],
                ['asset', '更新总资产'],
              ]"
              :key="tab[0]"
              type="button"
              :aria-pressed="kind === tab[0]"
              :disabled="quoteManaged && tab[0] !== 'asset'"
              @click="selectKind(tab[0]!)"
            >
              {{ tab[1] }}
            </button>
          </div>
          <p v-else class="lp-reference">编辑原有备注，不改变记录类型。</p>
          <label>记录日期<input v-model="date" type="date" required /></label>
          <label v-if="kind === 'in' || kind === 'out'"
            >{{ kind === "in" ? "转入金额" : "转出金额" }}
            <small>{{ account.currency }}</small
            ><input
              v-model="amount"
              inputmode="decimal"
              placeholder="0.00"
              required
          /></label>
          <label
            v-if="(kind === 'in' || kind === 'out') && channelNames.length"
          >
            {{ kind === "in" ? "转入渠道" : "转出渠道" }}
            <select
              v-model="flowChannel"
              aria-label="资金渠道"
              :disabled="channels.loading"
            >
              <option v-for="name in channelNames" :key="name" :value="name">
                {{ name }}
              </option>
            </select>
          </label>
          <label v-if="kind === 'in' || kind === 'out'" class="lp-check">
            <input v-model="withAssets" type="checkbox" />同时更新总资产
          </label>
          <p v-if="channels.loading" class="lp-field-hint" role="status">
            正在读取上次渠道…
          </p>
          <p v-if="channels.error" class="lp-field-hint">
            上次渠道读取失败。
            <button type="button" class="lp-text-button" @click="loadChannels">
              重新读取渠道
            </button>
          </p>
          <LedgerChannelEditor
            v-if="hasAssets && splitAssets"
            :model-value="channelAssets"
            @update:model-value="updateChannelAssets"
            :previous="channels.data?.items ?? []"
            :source-date="channels.data?.source_date ?? ''"
            :currency="account.currency"
          />
          <label v-else-if="hasAssets"
            >{{ kind === "asset" ? "总资产" : "资金变动后的总资产" }}
            <small>{{ account.currency }}</small>
            <input
              v-model="assets"
              inputmode="decimal"
              :disabled="channels.loading"
              required
              placeholder="0.00"
          /></label>
          <button
            v-if="hasAssets && !splitAssets"
            type="button"
            class="lp-text-button"
            :disabled="channels.loading"
            @click="addFirstChannel"
          >
            ＋ 添加渠道
          </button>
          <p v-if="kind !== 'log'" class="lp-field-hint">
            {{
              kind === "asset"
                ? "记录当前资产金额；渠道之间调拨资金时，更新各渠道金额即可。"
                : "金额填写正数；如填写总资产，请填写转入或转出完成后的账户总额。"
            }}
          </p>
          <p v-if="quoteManaged" class="lp-field-hint">
            此笔保留自动估值来源；更正总资产会标记为人工确认。
          </p>
          <label
            >备注 <small>{{ existingNote ? "" : "选填" }}</small
            ><textarea v-model="note" rows="3" :required="existingNote" />
          </label>
          <label v-if="read.data"
            >修改原因<input
              v-model="reason"
              required
              maxlength="160"
              placeholder="例如：金额填写有误"
          /></label>
          <p v-if="validation" class="lp-error" role="alert">
            {{ validation }}
          </p>
          <div class="lp-dialog-footer">
            <button type="button" @click="requestClose">取消</button
            ><button
              type="submit"
              class="lp-primary"
              :disabled="conflict || channels.loading || !!channels.error"
            >
              {{ read.data ? "保存修改" : "保存记录" }}
            </button>
          </div>
        </fieldset>
        <button
          v-if="read.data"
          type="button"
          class="lp-text-button"
          :disabled="locked"
          @click="load"
        >
          记录有变化？重新读取后再编辑
        </button>
      </form>
    </template>
  </LedgerDialog>
</template>

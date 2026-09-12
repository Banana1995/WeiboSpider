<script setup lang="ts">
import { nextTick, onBeforeUnmount, reactive, ref, watch } from "vue";
import { errorText, query, request, type Account, type Page } from "./ledger";
import type { AccountRecord } from "./accountRecords";
import { money, recordKind, recordPage, validDay } from "./ledgerView";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";

const props = defineProps<{ account: Account; refreshKey: number }>();
const emit = defineEmits<{ edit: [id: string]; detail: [id: string] }>();
const { navigationLocked: locked } = useLedgerWorkspace();
const rows = reactive(useLedgerRead<Page<AccountRecord>>());
const from = ref("");
const to = ref("");
const showVoided = ref(false);
const filterMenu = ref<HTMLDetailsElement>();
const filterError = ref("");
const notice = ref("");
const highlighted = ref("");
const page = ref(0);
const cursors = ref([""]);
const table = ref<HTMLDetailsElement>();
const expanded = ref(false);
const applied = reactive({ from: "", to: "", status: "active" });
let generation = 0;
function toggle() {
  const open = table.value?.open ?? false;
  if (open === expanded.value) return;
  expanded.value = open;
  if (open && !rows.data && !rows.loading) load();
}
function load(cursor = "", index = 0) {
  generation++;
  rows.clear();
  page.value = index;
  const id = props.account.id,
    filters = { ...applied };
  void rows.load(async (signal) =>
    recordPage(
      await request<Page<AccountRecord>>(
        `/accounts/${encodeURIComponent(id)}/records${query({ ...filters, limit: "10", cursor })}`,
        { signal },
      ),
      id,
      filters.from,
      filters.to,
      filters.status,
      cursor,
    ),
  );
}
function filter() {
  if (locked.value) return;
  filterError.value = "";
  if (
    (from.value && !validDay(from.value)) ||
    (to.value && !validDay(to.value)) ||
    (from.value && to.value && from.value > to.value)
  ) {
    filterError.value = "请选择有效的记录起止日期。";
    return;
  }
  Object.assign(applied, {
    from: from.value,
    to: to.value,
    status: showVoided.value ? "all" : "active",
  });
  highlighted.value = "";
  notice.value = "";
  cursors.value = [""];
  if (filterMenu.value) filterMenu.value.open = false;
  load();
}
function reset() {
  from.value = "";
  to.value = "";
  showVoided.value = false;
  filter();
}
function next() {
  const cursor = rows.data?.next_cursor;
  if (locked.value || rows.loading || !cursor) return;
  cursors.value.splice(page.value + 1, Infinity, cursor);
  load(cursor, page.value + 1);
}
async function locate(event: { id: string; date: string; accountId: string }) {
  if (
    locked.value ||
    event.accountId !== props.account.id ||
    !validDay(event.date)
  )
    return;
  // Set both before reading so the native toggle cannot replace this lookup.
  expanded.value = true;
  if (table.value) table.value.open = true;
  const current = ++generation;
  from.value = event.date;
  to.value = event.date;
  showVoided.value = false;
  Object.assign(applied, {
    from: event.date,
    to: event.date,
    status: "active",
  });
  rows.clear();
  highlighted.value = "";
  filterError.value = "";
  notice.value = "正在定位这笔记录…";
  if (filterMenu.value) filterMenu.value.open = false;
  const history = [""];
  let foundPage = 0;
  let found = false;
  const id = event.accountId;
  await rows.load(async (signal) => {
    let cursor = "";
    for (let n = 0; n < 100; n++) {
      if (signal.aborted) throw new Error("定位已取消");
      const result = recordPage(
        await request<Page<AccountRecord>>(
          `/accounts/${encodeURIComponent(id)}/records${query({
            from: event.date,
            to: event.date,
            status: "active",
            limit: "10",
            cursor,
          })}`,
          { signal },
        ),
        id,
        event.date,
        event.date,
        "active",
        cursor,
      );
      foundPage = n;
      if (result.items.some((r) => r.id === event.id)) {
        found = true;
        return result;
      }
      if (!result.next_cursor) return result;
      cursor = result.next_cursor;
      history.push(cursor);
    }
    throw new Error(
      "当天记录较多，已停止自动定位。请使用记录分页查找，或刷新收益曲线后再试。",
    );
  });
  if (current !== generation || id !== props.account.id) return;
  cursors.value = history;
  page.value = foundPage;
  highlighted.value = found ? event.id : "";
  notice.value = rows.error
    ? "定位未完成。"
    : found
      ? "已定位资金记录，下方仅显示该日记录。"
      : "未找到这笔有效记录，可能已被更改日期或作废。请刷新收益曲线。";
  if (found) {
    await nextTick();
    if (current !== generation || id !== props.account.id || !expanded.value)
      return;
    const row = Array.from(
      table.value?.querySelectorAll<HTMLElement>("[data-record-id]") ?? [],
    ).find((el) => el.dataset.recordId === event.id);
    row?.scrollIntoView({ block: "center", behavior: "auto" });
    row?.focus({ preventScroll: true });
  }
}
function open() {
  expanded.value = true;
  if (table.value) table.value.open = true;
  if (!rows.data && !rows.loading) load();
}
defineExpose({ locate, open });
watch(
  () => props.account.id,
  () => {
    generation++;
    rows.clear();
    expanded.value = false;
    if (table.value) table.value.open = false;
    from.value = "";
    to.value = "";
    showVoided.value = false;
    Object.assign(applied, { from: "", to: "", status: "active" });
    cursors.value = [""];
    page.value = 0;
    highlighted.value = "";
    notice.value = "";
    filterError.value = "";
    if (filterMenu.value) filterMenu.value.open = false;
  },
  { immediate: true },
);
watch(
  () => props.refreshKey,
  () => {
    generation++;
    rows.clear();
    cursors.value = [""];
    page.value = 0;
    highlighted.value = "";
    notice.value = "";
    if (expanded.value) load();
  },
);
onBeforeUnmount(() => {
  generation++;
});
</script>

<template>
  <details
    ref="table"
    :open="expanded"
    class="lp-records"
    aria-labelledby="records-title"
    data-test="account-records"
    @toggle="toggle"
  >
    <summary class="lp-record-summary">
      <h2 id="records-title">账户记录</h2>
      <span v-if="rows.data">本页 {{ rows.data.items.length }} 笔</span>
    </summary>
    <div class="lp-section-title">
      <div class="lp-record-tools">
        <details ref="filterMenu" class="lp-filter-menu">
          <summary>
            筛选<span
              v-if="applied.from || applied.to || applied.status === 'all'"
              class="lp-filter-dot"
            />
          </summary>
          <form @submit.prevent="filter">
            <fieldset :disabled="locked">
              <strong>仅筛选下方记录</strong>
              <label>记录开始日期<input v-model="from" type="date" /></label
              ><label>记录结束日期<input v-model="to" type="date" /></label>
              <label class="lp-check"
                ><input v-model="showVoided" type="checkbox" />显示已作废</label
              >
              <p v-if="filterError" class="lp-error" role="alert">
                {{ filterError }}
              </p>
              <div class="lp-actions">
                <button type="submit">应用筛选</button
                ><button type="button" @click="reset">清除</button>
              </div>
            </fieldset>
          </form>
        </details>
        <button
          class="lp-text-button"
          :disabled="locked || rows.loading"
          @click="
            cursors = [''];
            load();
          "
        >
          刷新
        </button>
      </div>
    </div>
    <p
      v-if="applied.from || applied.to || applied.status === 'all'"
      class="lp-filter-summary"
    >
      记录筛选：{{ applied.from || "不限开始" }} 至 {{ applied.to || "不限结束"
      }}{{ applied.status === "all" ? "，含已作废" : "" }}
      <button class="lp-text-button" :disabled="locked" @click="reset">
        清除
      </button>
    </p>
    <p v-if="notice" class="lp-filter-summary" role="status">{{ notice }}</p>
    <p v-if="rows.loading" class="lp-empty" role="status">正在读取账户记录…</p>
    <p v-else-if="rows.error" class="lp-error" role="alert">
      {{ errorText(rows.error) }}
    </p>
    <table v-else-if="rows.data?.items.length" class="lp-record-table">
      <thead>
        <tr>
          <th scope="col">日期</th>
          <th scope="col">类型</th>
          <th scope="col">转入</th>
          <th scope="col">转出</th>
          <th scope="col">
            总资产 <small>{{ account.currency }}</small>
          </th>
          <th scope="col">备注</th>
          <th scope="col">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="r in rows.data.items"
          :key="r.id"
          :data-record-id="r.id"
          tabindex="-1"
          :class="{
            'lp-highlighted': highlighted === r.id,
            'lp-voided': r.voided,
          }"
        >
          <td class="lp-record-date">
            {{ r.date }}<small v-if="r.voided">已作废</small>
          </td>
          <td class="lp-record-kind">
            <span
              class="lp-kind"
              :class="
                r.flow !== null
                  ? r.flow.startsWith('-')
                    ? 'lp-out'
                    : 'lp-in'
                  : ''
              "
              >{{ recordKind(r) }}</span
            >
          </td>
          <td data-label="转入" class="lp-money lp-in">
            {{
              r.flow !== null && !r.flow.startsWith("-") ? money(r.flow) : "—"
            }}
          </td>
          <td data-label="转出" class="lp-money lp-out">
            {{ r.flow?.startsWith("-") ? money(r.flow.slice(1)) : "—" }}
          </td>
          <td data-label="总资产" class="lp-money lp-record-assets">
            {{ money(r.total_assets) }}
          </td>
          <td class="lp-record-note">{{ r.note || "—" }}</td>
          <td class="lp-row-actions">
            <button
              v-if="!r.voided"
              class="lp-text-button"
              :disabled="locked"
              @click="emit('edit', r.id)"
            >
              编辑
            </button>
            <button
              class="lp-text-button"
              :disabled="locked"
              @click="emit('detail', r.id)"
            >
              详情
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="lp-empty">
      <h3>没有符合条件的记录</h3>
      <p>从上方“记一笔”开始，或调整记录日期、显示已作废记录。</p>
    </div>
    <footer class="lp-table-footer">
      <span>按日期、同日记录顺序从新到旧 · 未填写的总资产不沿用前值</span>
      <div class="lp-pagination">
        <span>第 {{ page + 1 }} 页</span
        ><button
          :disabled="locked || rows.loading || page === 0"
          @click="load(cursors[page - 1]!, page - 1)"
        >
          上一页
        </button>
        <button
          :disabled="
            locked || rows.loading || !!rows.error || !rows.data?.next_cursor
          "
          @click="next"
        >
          下一页
        </button>
      </div>
    </footer>
  </details>
</template>

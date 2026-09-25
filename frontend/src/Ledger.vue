<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  provide,
  reactive,
  ref,
  watch,
} from "vue";
import LedgerOverview from "./LedgerOverview.vue";
import LedgerPortfolios from "./LedgerPortfolios.vue";
import AccountRecords from "./AccountRecords.vue";
import LedgerRecordDialog from "./LedgerRecordDialog.vue";
import LedgerAccountDialog from "./LedgerAccountDialog.vue";
import LedgerManagement from "./LedgerManagement.vue";
import LedgerHoldings from "./LedgerHoldings.vue";
import {
  all,
  errorText,
  LedgerError,
  type Account,
  type Instrument,
} from "./ledger";
import { opaqueID, validDay, versionString } from "./ledgerView";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerPrefetch } from "./useLedgerPrefetch";
import { applyTabOrder, loadTabOrder, saveTabOrder } from "./ledgerTabOrder";
import type { ImportResult } from "./ledgerImport";
import "./ledger.css";

const emit = defineEmits<{ locked: [value: boolean] }>();
const accounts = reactive(useLedgerRead<Account[]>());
const instruments = reactive(useLedgerRead<Instrument[]>());
const selected = ref("");
const tabOrder = ref<string[]>(loadTabOrder());
const orderedAccounts = computed(() =>
  applyTabOrder(accounts.data ?? [], tabOrder.value),
);
const tablist = ref<HTMLElement>();
const draggingId = ref("");
let suppressTabClick = false;
let tabDrag: {
  id: string;
  pointerId: number;
  startX: number;
  moved: boolean;
} | null = null;
const workspaceView = ref<"accounts" | "portfolios">("accounts");
const portfoliosOpened = ref(false);
const returnPortfolio = ref("");
const account = computed(() =>
  accounts.data?.find((a) => a.id === selected.value),
);
const manage = ref(false);
const managementTab = ref("info");
const refreshKey = ref(0);
const records = ref<InstanceType<typeof AccountRecords>>();
const importedAccount = ref("");
const modal = ref<"" | "create" | "edit" | "detail" | "account">("");
const recordId = ref("");
const workspace = createLedgerWorkspace(() => {
  refreshKey.value++;
});
provide(ledgerWorkspaceKey, workspace);
const { locked, navigationLocked, pending, busy, error, notice, retry, label } =
  workspace;
const readyAccount = ref("");
watch(refreshKey, () => workspace.invalidateReads(), { flush: "sync" });
watch(
  [selected, workspace.cacheEpoch],
  () => {
    readyAccount.value = "";
  },
  { flush: "sync" },
);
useLedgerPrefetch(
  () => ({
    accounts: accounts.data ?? [],
    selected: selected.value,
    ready: readyAccount.value,
    paused:
      navigationLocked.value ||
      manage.value ||
      workspaceView.value !== "accounts",
    epoch: workspace.cacheEpoch.value,
  }),
  workspace.accountCache,
  workspace.benchmarkCache,
);
watch(navigationLocked, (value) => emit("locked", value), {
  immediate: true,
  flush: "sync",
});
let noticeTimer: ReturnType<typeof setTimeout> | undefined;
watch(notice, (value) => {
  clearTimeout(noticeTimer);
  noticeTimer = value
    ? setTimeout(() => {
        notice.value = "";
        noticeTimer = undefined;
      }, 5000)
    : undefined;
});
let accountRequest = 0;
async function loadAccounts(preferred = selected.value) {
  const generation = ++accountRequest;
  selected.value = preferred;
  accounts.clear();
  await accounts.load(async (signal) => {
    const items = await all<Account>("/accounts", signal);
    if (
      items.some(
        (a) =>
          !a ||
          !opaqueID(a.id) ||
          typeof a.name !== "string" ||
          !["CNY", "HKD", "USD"].includes(a.currency) ||
          a.current_holdings_input !== "manual_snapshot" ||
          !validDay(a.opening_date) ||
          !versionString(a.version),
      ) ||
      new Set(items.map((a) => a.id)).size !== items.length
    )
      throw new LedgerError("invalid_response");
    return items;
  });
  if (generation === accountRequest && accounts.data) {
    selected.value = accounts.data.some((a) => a.id === preferred)
      ? preferred
      : (accounts.data[0]?.id ?? "");
    await nextTick();
    if (generation !== accountRequest) return;
    document
      .getElementById(`account-tab-${selected.value}`)
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
    if (
      generation === accountRequest &&
      importedAccount.value === selected.value &&
      records.value
    ) {
      records.value.open();
      importedAccount.value = "";
    }
  }
}
function loadInstruments() {
  instruments.clear();
  void instruments.load(async (signal) => {
    const result = await all<Instrument>("/instruments", signal);
    if (
      result.some(
        (i) =>
          !i ||
          !opaqueID(i.id) ||
          typeof i.name !== "string" ||
          typeof i.market !== "string" ||
          typeof i.code !== "string" ||
          !["CNY", "HKD", "USD"].includes(i.currency),
      ) ||
      new Set(result.map((i) => i.id)).size !== result.length
    )
      throw new LedgerError("invalid_response");
    return result;
  });
}
function refresh() {
  if (navigationLocked.value) return;
  refreshKey.value++;
}
function selectAccount(id: string) {
  if (navigationLocked.value) return;
  if (selected.value !== id) {
    notice.value = "";
    error.value = "";
  }
  selected.value = id;
}
function selectWorkspace(view: "accounts" | "portfolios") {
  if (navigationLocked.value) return;
  workspaceView.value = view;
  if (view === "portfolios") portfoliosOpened.value = true;
  manage.value = false;
}
function tabIndexAt(clientX: number) {
  const element = tablist.value;
  if (!element) return -1;
  let target = -1;
  let closest = Infinity;
  element
    .querySelectorAll<HTMLElement>("[data-account-id]")
    .forEach((node, index) => {
      const rect = node.getBoundingClientRect();
      const distance = Math.abs(rect.left + rect.width / 2 - clientX);
      if (distance < closest) {
        closest = distance;
        target = index;
      }
    });
  return target;
}
function endTabDrag(event: PointerEvent) {
  if (!tabDrag || event.pointerId !== tabDrag.pointerId) return;
  const moved = tabDrag.moved;
  window.removeEventListener("pointermove", moveTabDrag);
  window.removeEventListener("pointerup", endTabDrag);
  window.removeEventListener("pointercancel", endTabDrag);
  tabDrag = null;
  draggingId.value = "";
  document.documentElement.classList.remove("lp-tab-grabbing");
  if (moved) {
    saveTabOrder(orderedAccounts.value.map((a) => a.id));
    window.setTimeout(() => {
      suppressTabClick = false;
    }, 0);
  }
}
function moveTabDrag(event: PointerEvent) {
  if (!tabDrag || event.pointerId !== tabDrag.pointerId) return;
  if (!tabDrag.moved) {
    if (Math.abs(event.clientX - tabDrag.startX) < 6) return;
    tabDrag.moved = true;
    suppressTabClick = true;
    draggingId.value = tabDrag.id;
    document.documentElement.classList.add("lp-tab-grabbing");
  }
  const ids = orderedAccounts.value.map((a) => a.id);
  const from = ids.indexOf(tabDrag.id);
  const to = tabIndexAt(event.clientX);
  if (from < 0 || to < 0 || to === from) return;
  const next = ids.slice();
  next.splice(from, 1);
  next.splice(to, 0, tabDrag.id);
  tabOrder.value = next;
}
function startTabDrag(event: PointerEvent, id: string) {
  if (
    navigationLocked.value ||
    event.pointerType !== "mouse" ||
    event.button !== 0
  )
    return;
  tabDrag = {
    id,
    pointerId: event.pointerId,
    startX: event.clientX,
    moved: false,
  };
  window.addEventListener("pointermove", moveTabDrag);
  window.addEventListener("pointerup", endTabDrag);
  window.addEventListener("pointercancel", endTabDrag);
}
function selectAccountTab(id: string) {
  if (suppressTabClick) return;
  selectAccount(id);
}
function viewPortfolioAccount(event: { id: string; portfolio: string }) {
  if (navigationLocked.value) return;
  returnPortfolio.value = event.portfolio;
  selectAccount(event.id);
  selectWorkspace("accounts");
}
async function locatePortfolioRecord(event: {
  id: string;
  date: string;
  accountId: string;
  portfolio: string;
}) {
  viewPortfolioAccount({ id: event.accountId, portfolio: event.portfolio });
  await locate(event);
}
function accountKey(event: KeyboardEvent) {
  if (
    navigationLocked.value ||
    modal.value ||
    !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)
  )
    return;
  const tabs = Array.from(
    (event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>(
      '[role="tab"]:not(:disabled)',
    ),
  );
  const index = tabs.indexOf(event.target as HTMLButtonElement);
  if (index < 0 || !tabs.length) return;
  event.preventDefault();
  const next =
    event.key === "Home"
      ? 0
      : event.key === "End"
        ? tabs.length - 1
        : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
          tabs.length;
  tabs[next]!.click();
  tabs[next]!.focus();
}
function openRecord(mode: "create" | "edit" | "detail", id = "") {
  if (navigationLocked.value || !account.value) return;
  error.value = "";
  recordId.value = id;
  modal.value = mode;
}
function openAccount() {
  if (navigationLocked.value) return;
  error.value = "";
  if (!instruments.data && !instruments.loading) loadInstruments();
  modal.value = "account";
}
function manageAccount(tab = "info") {
  if (navigationLocked.value) return;
  managementTab.value = tab;
  manage.value = true;
  if (!instruments.data && !instruments.loading) loadInstruments();
}
async function imported(result: ImportResult) {
  managementTab.value = "info";
  manage.value = false;
  notice.value = result.duplicate
    ? `这份文件已导入 ${result.imported_count} 笔记录，未重复添加。`
    : `已导入 ${result.imported_count} 笔记录。`;
  refreshKey.value++;
  importedAccount.value = result.account_id;
  await loadAccounts(result.account_id);
}
function created(id: string) {
  managementTab.value = "info";
  manage.value = false;
  void loadAccounts(id);
}
function deleted() {
  manage.value = false;
  managementTab.value = "info";
  importedAccount.value = "";
  void loadAccounts("");
}
async function locate(event: { id: string; date: string; accountId: string }) {
  if (locked.value) return;
  await nextTick();
  void records.value?.locate(event);
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (locked.value) {
    event.preventDefault();
    event.returnValue = "";
  }
}
function guardLink(event: MouseEvent) {
  if (
    navigationLocked.value &&
    event.target instanceof Element &&
    event.target.closest("a[href]")
  )
    event.preventDefault();
}
onMounted(() => {
  void loadAccounts();
  loadInstruments();
  window.addEventListener("beforeunload", beforeUnload);
  document.addEventListener("click", guardLink, true);
  document.addEventListener("auxclick", guardLink, true);
});
onBeforeUnmount(() => {
  workspace.accountCache.clear();
  workspace.benchmarkCache.clear();
  clearTimeout(noticeTimer);
  window.removeEventListener("beforeunload", beforeUnload);
  document.removeEventListener("click", guardLink, true);
  document.removeEventListener("auxclick", guardLink, true);
  window.removeEventListener("pointermove", moveTabDrag);
  window.removeEventListener("pointerup", endTabDrag);
  window.removeEventListener("pointercancel", endTabDrag);
  document.documentElement.classList.remove("lp-tab-grabbing");
});
</script>

<template>
  <div class="ledger-page">
    <div class="lp-banner">
      <strong>公开共享账本</strong
      ><span>任何人均可查看与修改，请勿上传私人财务信息</span>
    </div>
    <header class="lp-masthead">
      <div class="brand">
        <span class="brand-mark">观</span>观价<span class="brand-divider"
          >/</span
        ><span class="brand-sub">投资账本</span>
      </div>
      <nav aria-label="模块导航">
        <a href="/liquor" :aria-disabled="navigationLocked">白酒行情</a
        ><a href="/ledger" aria-current="page" :aria-disabled="navigationLocked"
          >投资账本</a
        >
      </nav>
    </header>
    <main class="lp-main">
      <div class="lp-account-heading">
        <h1>投资账本</h1>
        <div v-if="workspaceView === 'accounts'" class="lp-actions">
          <template v-if="!manage"
            ><button
              class="lp-text-button"
              :disabled="navigationLocked"
              @click="manageAccount()"
            >
              管理账户
            </button>
            <button
              class="lp-primary"
              data-ledger-focus
              :disabled="navigationLocked || !account"
              @click="openRecord('create')"
            >
              ＋ 记一笔
            </button></template
          >
          <button v-else :disabled="navigationLocked" @click="manage = false">
            返回账户
          </button>
        </div>
      </div>
      <div class="lp-segment lp-workspace-switch" aria-label="账本分析范围">
        <button
          :aria-pressed="workspaceView === 'accounts'"
          :disabled="navigationLocked"
          @click="selectWorkspace('accounts')"
        >
          单账户
        </button>
        <button
          :aria-pressed="workspaceView === 'portfolios'"
          :disabled="navigationLocked"
          @click="selectWorkspace('portfolios')"
        >
          账户组合
        </button>
      </div>
      <p v-if="returnPortfolio && workspaceView === 'accounts'">
        <button
          class="lp-text-button"
          :disabled="navigationLocked"
          @click="selectWorkspace('portfolios')"
        >
          返回「{{ returnPortfolio }}」组合
        </button>
      </p>
      <div
        v-if="workspaceView === 'accounts' && accounts.data?.length"
        ref="tablist"
        class="lp-account-tabs lp-reorderable"
        role="tablist"
        aria-label="选择账户"
        @keydown="accountKey"
      >
        <button
          v-for="a in orderedAccounts"
          :id="`account-tab-${a.id}`"
          :key="a.id"
          role="tab"
          :data-account-id="a.id"
          :aria-selected="selected === a.id"
          :aria-controls="`account-panel-${a.id}`"
          :tabindex="selected === a.id ? 0 : -1"
          :disabled="navigationLocked"
          :title="a.name"
          draggable="false"
          :class="{ 'lp-tab-dragging': draggingId === a.id }"
          @pointerdown="startTabDrag($event, a.id)"
          @click="selectAccountTab(a.id)"
        >
          {{ a.name }}
        </button>
      </div>
      <p v-if="notice" class="lp-notice" role="status">{{ notice }}</p>
      <p v-if="error && !modal" class="lp-error" role="alert">
        {{ errorText(error) }}
      </p>
      <div v-if="pending && !modal" class="lp-reference" role="status">
        <strong>{{ busy ? `正在确认${label}` : `${label}结果待确认` }}</strong>
        <p>原始内容和账户已锁定，请保留此页，不要刷新或重复录入。</p>
        <button :disabled="busy" @click="retry">按原请求重试确认</button>
      </div>
      <p v-if="accounts.loading" class="lp-empty" role="status">
        正在读取账户…
      </p>
      <div v-else-if="accounts.error" class="lp-empty">
        <p class="lp-error" role="alert">{{ errorText(accounts.error) }}</p>
        <button :disabled="locked" @click="loadAccounts()">重新读取账户</button>
      </div>
      <div
        v-else-if="workspaceView === 'accounts' && account"
        :id="`account-panel-${account.id}`"
        role="tabpanel"
        :aria-labelledby="`account-tab-${account.id}`"
      >
        <template v-if="!manage">
          <LedgerOverview
            :key="account.id"
            :account="account"
            :refresh-key="refreshKey"
            @locate="locate"
            @ready="readyAccount = $event"
            @loading="readyAccount = ''"
          />
          <LedgerHoldings
            :key="account.id"
            :account="account"
            :accounts="accounts.data ?? []"
            :instruments="instruments.data ?? []"
            :instruments-error="instruments.error"
            :instruments-loading="instruments.loading"
            :refresh-key="refreshKey"
            @locked="workspace.externalLock.value = $event"
            @create="openAccount"
            @instruments="loadInstruments"
            @changed="refreshKey++"
          />
          <AccountRecords
            :key="account.id"
            ref="records"
            :account="account"
            :refresh-key="refreshKey"
            @edit="openRecord('edit', $event)"
            @detail="openRecord('detail', $event)"
          />
        </template>
        <LedgerManagement
          v-else
          @deleted="deleted"
          :account="account"
          :accounts="accounts.data ?? []"
          :instruments="instruments.data ?? []"
          :instruments-error="instruments.error"
          :instruments-loading="instruments.loading"
          :initial-tab="managementTab"
          :refresh-key="refreshKey"
          @locked="workspace.externalLock.value = $event"
          @create="openAccount"
          @imported="imported"
          @instruments="loadInstruments"
          @changed="refreshKey++"
        />
      </div>
      <LedgerManagement
        v-else-if="workspaceView === 'accounts' && manage"
        :accounts="accounts.data ?? []"
        :instruments="instruments.data ?? []"
        :instruments-error="instruments.error"
        :instruments-loading="instruments.loading"
        :initial-tab="managementTab"
        :refresh-key="refreshKey"
        @locked="workspace.externalLock.value = $event"
        @create="openAccount"
        @imported="imported"
        @instruments="loadInstruments"
        @changed="refreshKey++"
      />
      <section
        v-else-if="workspaceView === 'accounts'"
        class="lp-income lp-empty"
      >
        <h2>从第一份账户开始</h2>
        <p>记录转入、转出和总资产，留下一条清楚的投资轨迹。</p>
        <div class="lp-actions lp-empty-actions">
          <button class="lp-primary" @click="openAccount">新建账户</button
          ><button @click="manageAccount('import')">导入 Excel 账本</button>
        </div>
      </section>
      <LedgerPortfolios
        v-if="portfoliosOpened && accounts.data"
        v-show="workspaceView === 'portfolios'"
        :accounts="accounts.data"
        :refresh-key="refreshKey"
        @account="viewPortfolioAccount"
        @locate="locatePortfolioRecord"
      />
      <template v-for="a in accounts.data" :key="a.id"
        ><div
          v-if="a.id !== selected"
          :id="`account-panel-${a.id}`"
          role="tabpanel"
          :aria-labelledby="`account-tab-${a.id}`"
          hidden
      /></template>
      <footer class="lp-page-footer">
        <span>观价 · 投资账本</span>
        <div class="lp-actions">
          <span>金额按账户币种记录</span
          ><button
            class="lp-text-button"
            :disabled="navigationLocked"
            @click="refresh"
          >
            刷新当前数据
          </button>
        </div>
      </footer>
    </main>
    <LedgerRecordDialog
      v-if="
        account &&
        (modal === 'create' || modal === 'edit' || modal === 'detail')
      "
      :account="account"
      :record-id="recordId || undefined"
      :initial-mode="modal"
      @close="modal = ''"
    />
    <LedgerAccountDialog
      v-if="modal === 'account'"
      :instruments="instruments.data ?? []"
      @close="modal = ''"
      @created="created"
    />
  </div>
</template>

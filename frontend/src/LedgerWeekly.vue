<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import LedgerValuation from "./LedgerValuation.vue";
import {
  query,
  request,
  type Account,
  type Page,
  type ValuationHistory,
} from "./ledger";
import { useLedgerRead } from "./useLedgerRead";
import { accountRecordOriginLabels } from "./accountRecords";
import {
  weeklyStates,
  weeklyReasons,
  weeklySources,
  weeklyTime,
  weeklyRetryLabel,
  validateWeeklyStatus,
  validateWeeklyPage,
  validateWeeklyJob,
  validateWeeklyHistory,
  type WeeklyStatus,
  type WeeklyJob,
} from "./ledgerWeekly";

const props = defineProps<{ accounts: Account[]; disabled?: boolean }>();
const global = reactive(useLedgerRead<WeeklyStatus>());
const list = reactive(useLedgerRead<Page<WeeklyJob>>());
const detail = reactive(useLedgerRead<WeeklyJob>());
const history = reactive(useLedgerRead<ValuationHistory>());
const accountId = ref("");
const selected = computed(() =>
  props.accounts.find((a) => a.id === accountId.value),
);
const filter = ref("");
const cursor = ref("");
const detailId = ref("");
const observedAt = ref<string | null>(null);
const detailAt = ref<string | null>(null);
const panelOpen = ref(false);
function clearDetail() {
  detail.clear();
  history.clear();
  detailId.value = "";
  detailAt.value = null;
}
function loadList() {
  list.clear();
  clearDetail();
  if (!panelOpen.value) return;
  const id = selected.value?.id;
  if (!id) return;
  const status = filter.value;
  const before = cursor.value;
  void list.load(async (signal) =>
    validateWeeklyPage(
      await request<Page<WeeklyJob>>(
        `/accounts/${encodeURIComponent(id)}/weekly-jobs${query({ status, cursor: before, limit: "30" })}`,
        { signal },
      ),
      id,
      status,
      before,
    ),
  );
}
function first() {
  cursor.value = "";
  loadList();
}
function next() {
  if (!list.data?.next_cursor || list.loading || list.error || props.disabled)
    return;
  cursor.value = list.data.next_cursor;
  loadList();
}
function refresh() {
  if (props.disabled || !panelOpen.value) return;
  global.clear();
  observedAt.value = null;
  void global.load(async (signal) => {
    const value = validateWeeklyStatus(
      await request<WeeklyStatus>("/weekly-status", { signal }),
    );
    if (!signal.aborted) observedAt.value = new Date().toISOString();
    return value;
  });
  loadList();
}
function loadDetail(id: string) {
  if (props.disabled || !panelOpen.value || !selected.value) return;
  clearDetail();
  detailId.value = id;
  const account = selected.value;
  const listed = list.data?.items.find((job) => job.id === id);
  void detail.load(async (signal) => {
    const value = validateWeeklyJob(
      await request<WeeklyJob>(
        `/accounts/${encodeURIComponent(account.id)}/weekly-jobs/${id}`,
        { signal },
      ),
      account.id,
      id,
      account.currency,
    );
    if (
      listed &&
      (listed.source !== value.source ||
        listed.scheduled_business_date !== value.scheduled_business_date ||
        listed.created_at !== value.created_at ||
        (listed.history_id !== null && listed.history_id !== value.history_id))
    )
      throw new Error("任务详情的来源、计划日期或已保存记录与任务身份不匹配");
    if (!signal.aborted) detailAt.value = new Date().toISOString();
    return value;
  });
}
function loadHistory() {
  const job = detail.data;
  const account = selected.value;
  if (
    props.disabled ||
    !panelOpen.value ||
    !account ||
    !job?.history_id ||
    job.source !== "holdings_current" ||
    detail.loading ||
    detail.error
  )
    return;
  history.clear();
  void history.load(async (signal) =>
    validateWeeklyHistory(
      await request<ValuationHistory>(
        `/accounts/${encodeURIComponent(account.id)}/valuations/${job.history_id}`,
        { signal },
      ),
      job,
      account,
    ),
  );
}
function toggle(event: Event) {
  const open = (event.currentTarget as HTMLDetailsElement).open;
  if (open === panelOpen.value) return;
  panelOpen.value = open;
  if (open) {
    refresh();
    return;
  }
  global.clear();
  list.clear();
  clearDetail();
  observedAt.value = null;
}
watch(
  () => props.accounts,
  () => {
    if (!selected.value) accountId.value = props.accounts[0]?.id ?? "";
  },
  { immediate: true },
);
watch(accountId, () => {
  cursor.value = "";
  list.clear();
  clearDetail();
  if (filter.value) filter.value = "";
  else if (panelOpen.value) loadList();
});
watch(filter, () => {
  cursor.value = "";
  if (panelOpen.value) loadList();
  else {
    list.clear();
    clearDetail();
  }
});
</script>

<template>
  <details
    class="ledger-panel ledger-weekly"
    data-test="weekly"
    aria-labelledby="weekly-heading"
    @toggle="toggle"
  >
    <summary id="weekly-heading">周六自动更新记录（只读）</summary>
    <div class="weekly-content">
      <div class="weekly-heading">
        <p>
          只读查询，不采集行情、不新增总资产记录。仅在打开本区域和手动操作时读取，无后台轮询。
        </p>
        <button type="button" :disabled="disabled" @click="refresh">
          刷新调度状态
        </button>
      </div>
      <p v-if="disabled" class="ledger-notice">
        账本写入待确认，暂时锁定调度面板操作，不影响后台任务状态。
      </p>
      <p v-if="global.loading" role="status">正在读取全局调度状态…</p>
      <p v-if="global.error" role="alert">{{ global.error }}</p>
      <template v-if="global.data">
        <p>
          <strong class="weekly-badge">{{
            global.data.enabled ? "查询时：全局已启用" : "查询时：全局已关闭"
          }}</strong>
        </p>
        <dl class="ledger-record">
          <dt>时区 / 周计划</dt>
          <dd>{{ global.data.timezone }} · 每周六 {{ global.data.time }}</dd>
          <dt>查询时执行窗口</dt>
          <dd>{{ global.data.window_open ? "窗口开放" : "窗口未开放" }}</dd>
          <dt>下一固定计划时刻</dt>
          <dd>
            {{
              global.data.next_scheduled_at
                ? weeklyTime(global.data.next_scheduled_at)
                : "全局关闭，无下一计划"
            }}
          </dd>
          <dt>状态读取时间</dt>
          <dd>{{ weeklyTime(observedAt) }}（浏览器收到响应时）</dd>
        </dl>
        <p class="ledger-notice">
          已启用不等于 worker
          正在运行；此接口不是心跳证明。窗口开放时，下一固定计划可能已指向下周六，不代表今天不再处理或重试。每个账户每次周任务最多
          {{ global.data.max_attempts }} 次尝试。
        </p>
        <p v-if="!global.data.enabled">
          全局关闭时不新建或执行任务；历史执行中记录及最早重试时间仍可能保留，不代表现在正在执行或将会重试。启用仅由服务端配置控制，本页没有开关。
        </p>
      </template>
      <div class="ledger-grid">
        <label
          >调度账户（独立选择）<select
            v-model="accountId"
            name="weekly_account"
            :disabled="disabled || !accounts.length"
          >
            <option value="" disabled>请选择账户</option>
            <option v-for="a in accounts" :key="a.id" :value="a.id">
              {{ a.name }} · {{ a.currency }}
            </option>
          </select></label
        >
        <label
          >任务状态<select
            v-model="filter"
            name="weekly_status"
            :disabled="disabled || !selected"
          >
            <option value="">全部状态</option>
            <option
              v-for="(label, state) in weeklyStates"
              :key="state"
              :value="state"
            >
              {{ label }}
            </option>
          </select></label
        >
      </div>
      <p v-if="!accounts.length">
        暂无可选账户；全局调度状态仍可独立读取。账户读取失败时请先恢复账户列表。
      </p>
      <p v-if="selected" data-test="weekly-account-context">
        调度账户：{{ selected.name }} ·
        {{ selected.id }}。不会改变上方记账账户或刷新当前估值。
      </p>
      <p v-if="selected" class="ledger-notice">
        有当前现金与持仓时，周六保存完整估值；未设置当前来源时，沿用最近明确资产并保留原始来源，账本展示仍加上后续净转入。没有任何历史总资产时不更新，也不会补零。
      </p>
      <p v-if="list.loading" role="status">正在读取任务列表…</p>
      <p v-if="list.error" role="alert">{{ list.error }}</p>
      <div
        v-if="list.data"
        class="ledger-table"
        tabindex="0"
        role="region"
        aria-label="周六任务列表，可横向滚动"
      >
        <table>
          <caption>
            查询时任务记录，按 ID 倒序，每页最多 30
            条；翻页不是冻结快照，不统计全局成功率
          </caption>
          <thead>
            <tr>
              <th scope="col">任务 / 计划日期</th>
              <th scope="col">查询时状态 / 来源</th>
              <th scope="col">已开始次数</th>
              <th scope="col">原因 / 重试记录</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="job in list.data.items" :key="job.id">
              <td>
                <button
                  type="button"
                  :disabled="disabled"
                  @click="loadDetail(job.id)"
                >
                  任务 #{{ job.id }}</button
                ><small>{{ job.scheduled_business_date }} 周六</small>
              </td>
              <td>
                <strong class="weekly-badge">{{
                  weeklyStates[job.status]
                }}</strong
                ><small>{{ weeklySources[job.source] }}</small>
              </td>
              <td>{{ job.attempts }} / 3</td>
              <td>
                {{ weeklyReasons[job.error_code]
                }}<small>{{ weeklyRetryLabel(job, global.data) }}</small>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="!list.data.items.length">
          当前账户及筛选条件暂无任务记录；不等于零资产，也不表示导入历史为空。
        </p>
      </div>
      <div class="ledger-actions">
        <button
          type="button"
          :disabled="disabled || !cursor || list.loading"
          @click="first"
        >
          任务首页</button
        ><button
          type="button"
          :disabled="
            disabled || !list.data?.next_cursor || list.loading || !!list.error
          "
          @click="next"
        >
          任务下一页
        </button>
      </div>
      <section v-if="detailId" data-test="weekly-detail" aria-live="polite">
        <h3>任务详情 #{{ detailId }}</h3>
        <button
          type="button"
          :disabled="disabled"
          @click="loadDetail(detailId)"
        >
          重新读取任务详情
        </button>
        <p v-if="detail.loading" role="status">正在读取任务详情…</p>
        <p v-if="detail.error" role="alert">{{ detail.error }}</p>
        <template v-if="detail.data">
          <p>
            详情是独立的新查询，可能比列表更新；不是逐次执行日志。详情读取于
            {{ weeklyTime(detailAt) }}。
          </p>
          <dl class="ledger-record">
            <dt>查询时状态</dt>
            <dd>{{ weeklyStates[detail.data.status] }}</dd>
            <dt>计划业务日期</dt>
            <dd>{{ detail.data.scheduled_business_date }}</dd>
            <dt>总资产来源</dt>
            <dd>
              {{ weeklySources[detail.data.source] }}
            </dd>
            <dt>已开始次数</dt>
            <dd>{{ detail.data.attempts }} / 3</dd>
            <dt>任务创建时间</dt>
            <dd>{{ weeklyTime(detail.data.created_at) }}</dd>
            <dt>最近开始时间</dt>
            <dd>{{ weeklyTime(detail.data.started_at) }}</dd>
            <dt>最近结束时间</dt>
            <dd>{{ weeklyTime(detail.data.finished_at) }}</dd>
            <dt>原因</dt>
            <dd>{{ weeklyReasons[detail.data.error_code] }}</dd>
            <dt>重试记录</dt>
            <dd>{{ weeklyRetryLabel(detail.data, global.data) }}</dd>
          </dl>
          <template v-if="detail.data.status === 'succeeded'">
            <p v-if="detail.data.source === 'holdings_current'">
              任务当时已保存总资产记录 #{{
                detail.data.history_id
              }}。这是固定的历史资产记录，不代表当前资产新鲜；后续持仓修改不会使其失效。人工更正资产金额不会改写原始估值审计快照。
            </p>
            <p v-else>
              任务当时已复制总资产记录 #{{
                detail.data.history_id
              }}。本次没有重新采集行情或推断现金 /
              持仓，金额的新鲜程度仍以原始记录日期为准。
            </p>
            <button
              v-if="detail.data.source === 'holdings_current'"
              type="button"
              :disabled="disabled || history.loading"
              @click="loadHistory"
            >
              查看已保存总资产 #{{ detail.data.history_id }}
            </button>
            <dl
              v-if="detail.data.carry"
              class="ledger-record"
              data-test="weekly-carry"
            >
              <dt>本周沿用总资产</dt>
              <dd>
                {{ detail.data.carry.total_assets }}
                {{ detail.data.carry.currency }}
              </dd>
              <dt>原始金额日期</dt>
              <dd>{{ detail.data.carry.source_record.date }}</dd>
              <dt>原始记录</dt>
              <dd>
                {{ detail.data.carry.source_record.id }} · 序号
                {{ detail.data.carry.source_record.sequence }} · 版本
                {{ detail.data.carry.source_record.version }} ·
                {{
                  accountRecordOriginLabels[
                    detail.data.carry.source_record.origin
                  ]
                }}
              </dd>
              <dt>沿用保存时间</dt>
              <dd>{{ weeklyTime(detail.data.carry.saved_at) }}</dd>
            </dl>
          </template>
        </template>
        <p v-if="history.loading" role="status">正在读取冻结总资产记录…</p>
        <p v-if="history.error" role="alert">{{ history.error }}</p>
        <div v-if="history.data" data-test="weekly-history">
          <p>
            周任务 #{{ detailId }} 关联原始记录 #{{
              history.data.id
            }}；保存时账户名：{{ history.data.account_name }}；保存时间：{{
              weeklyTime(history.data.saved_at)
            }}。
          </p>
          <p>
            金额与来源均来自同一冻结记录，时间统一显示为北京时间；报价 /
            汇率实际日期可能早于周六，不是周六成交价。
          </p>
          <LedgerValuation
            :account-id="accountId"
            :value="history.data.valuation"
            :instruments="history.data.instruments"
            :format-time="weeklyTime"
            :loading="false"
            error=""
            historical
          />
        </div>
      </section>
      <details class="weekly-distinction">
        <summary>导入历史与自动记录有什么区别？</summary>
        <p>
          <strong>导入历史 / 手工记录：</strong>直接记录总资产与转入 /
          转出，不反推历史持仓，独立有效，可继续手工更新。
        </p>
        <p>
          <strong>当前持仓生成的总资产：</strong
          >来自任务执行时的现金、股数、报价及汇率。浏览和刷新只预览；周任务保存固定资产记录，后续持仓修改不影响旧记录。只有任务的
          history_id 关联能证明周六来源。
        </p>
        <p>
          导入初始化、人工记录、每周自动估值和每周沿用记录保存在同一账户记录表，共用一条收益曲线，不设切换日期。具备当前持仓来源的账户自动估值；其它账户沿用最近有效总资产并保留原始记录日期，不会把历史金额冒充为持仓、现金或新行情，也不需要历史行情回填。
        </p>
      </details>
    </div>
  </details>
</template>

<style scoped>
.weekly-heading {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.ledger-weekly > summary {
  font-size: 18px;
  font-weight: 600;
}
.weekly-content {
  border-top: 1px solid #dde3db;
  padding-top: 12px;
}
.weekly-badge {
  display: inline-block;
  border: 1px solid #bac9be;
  border-radius: 4px;
  padding: 4px 8px;
  background: #eaf3eb;
  font-size: 13px;
}
.ledger-weekly {
  min-width: 0;
}
.ledger-weekly td {
  white-space: normal;
  min-width: 130px;
  max-width: 360px;
  overflow-wrap: anywhere;
  vertical-align: top;
}
.ledger-weekly td:last-child {
  min-width: 260px;
}
.ledger-weekly caption {
  text-align: left;
  white-space: normal;
  line-height: 1.7;
}
.weekly-distinction {
  border-top: 1px solid #dde3db;
  margin-top: 24px;
  padding-top: 12px;
}
.ledger-weekly :deep(.ledger-table) {
  max-width: 100%;
}
</style>

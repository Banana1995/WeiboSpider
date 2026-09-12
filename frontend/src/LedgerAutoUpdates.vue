<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { errorText, query, request, type Account, type Page } from "./ledger";
import {
  validateWeeklyPage,
  validateWeeklyStatus,
  weeklyReasons,
  weeklySources,
  weeklyStates,
  weeklyTime,
  type WeeklyJob,
  type WeeklyStatus,
} from "./ledgerWeekly";
import { useLedgerRead } from "./useLedgerRead";
import { useLedgerWorkspace } from "./useLedgerWorkspace";
const props = defineProps<{ account?: Account }>();
const { locked } = useLedgerWorkspace();
const status = reactive(useLedgerRead<WeeklyStatus>());
const jobs = reactive(useLedgerRead<Page<WeeklyJob>>());
const cursors = ref([""]);
const page = ref(0);
const observed = ref("");
function loadJobs(cursor = "", index = 0) {
  jobs.clear();
  page.value = index;
  const id = props.account?.id;
  if (!id) return;
  void jobs.load(async (signal) =>
    validateWeeklyPage(
      await request<Page<WeeklyJob>>(
        `/accounts/${encodeURIComponent(id)}/weekly-jobs${query({ cursor, limit: "30" })}`,
        { signal },
      ),
      id,
      "",
      cursor,
    ),
  );
}
function refresh() {
  if (locked.value) return;
  status.clear();
  observed.value = "";
  void status.load(async (signal) => {
    const result = validateWeeklyStatus(
      await request<WeeklyStatus>("/weekly-status", { signal }),
    );
    if (!signal.aborted) observed.value = weeklyTime(new Date().toISOString());
    return result;
  });
  cursors.value = [""];
  loadJobs();
}
function next() {
  const cursor = jobs.data?.next_cursor;
  if (!cursor) return;
  cursors.value.splice(page.value + 1, Infinity, cursor);
  loadJobs(cursor, page.value + 1);
}
watch(() => props.account?.id, refresh, { immediate: true });
</script>
<template>
  <div>
    <div class="lp-section-title">
      <h2>自动更新记录</h2>
      <button
        :disabled="locked || status.loading || jobs.loading"
        @click="refresh"
      >
        刷新状态
      </button>
    </div>
    <p class="lp-muted">
      仅查看服务器已有状态，不启用任务、不获取行情，也不触发保存。
    </p>
    <p v-if="status.loading" role="status">正在读取计划…</p>
    <p v-if="status.error" class="lp-error" role="alert">
      {{ errorText(status.error) }}
    </p>
    <dl v-if="status.data" class="lp-info">
      <div>
        <dt>当前状态</dt>
        <dd>
          {{ status.data.enabled ? "已启用" : "未启用"
          }}<small>配置状态，不代表任务正在运行</small>
        </dd>
      </div>
      <div>
        <dt>固定计划</dt>
        <dd>每周六 {{ status.data.time }}（北京时间）</dd>
      </div>
      <div v-if="status.data.next_scheduled_at">
        <dt>下一计划</dt>
        <dd>{{ weeklyTime(status.data.next_scheduled_at) }}</dd>
      </div>
    </dl>
    <p v-if="observed" class="lp-muted">查询于 {{ observed }}</p>
    <p v-if="jobs.loading" role="status">正在读取更新记录…</p>
    <p v-if="jobs.error" class="lp-error" role="alert">
      {{ errorText(jobs.error) }}
    </p>
    <ul class="lp-business-list">
      <li v-for="job in jobs.data?.items" :key="job.id">
        <div>
          <strong
            >{{ job.scheduled_business_date }} ·
            {{ weeklyStates[job.status] }}</strong
          >
          <p>{{ weeklySources[job.source] }}</p>
          <p v-if="job.error_code">{{ weeklyReasons[job.error_code] }}</p>
          <small v-if="job.finished_at"
            >完成于 {{ weeklyTime(job.finished_at) }}</small
          >
          <small v-if="job.status === 'succeeded'"
            >该次保存已完成，当前金额请在账户记录中查看。</small
          >
        </div>
      </li>
    </ul>
    <p v-if="jobs.data?.items.length === 0" class="lp-empty">
      这个账户还没有自动更新记录。
    </p>
    <div v-if="account" class="lp-pagination">
      <span>第 {{ page + 1 }} 页</span
      ><button
        :disabled="locked || jobs.loading || page === 0"
        @click="loadJobs(cursors[page - 1]!, page - 1)"
      >
        上一页</button
      ><button
        :disabled="
          locked || jobs.loading || !!jobs.error || !jobs.data?.next_cursor
        "
        @click="next"
      >
        下一页
      </button>
    </div>
  </div>
</template>

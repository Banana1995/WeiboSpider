<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { LedgerError, query, request, type Page } from "./ledger";
import { useLedgerRead } from "./useLedgerRead";

interface Audit {
  id: string;
  action: string;
  entity_type: string;
  entity_id: string;
  account_id: string | null;
  version: string;
  recorded_at: string;
  source: string;
  before_json?: string;
  after_json?: string;
  metadata_json?: string;
}
const props = defineProps<{ accountId: string }>();
const list = reactive(useLedgerRead<Page<Audit>>());
const detail = reactive(useLedgerRead<Audit>());
const action = ref("");
const entity = ref("");
const opened = ref(false);
function load(cursor = "") {
  opened.value = true;
  list.clear();
  detail.clear();
  const account = props.accountId;
  void list.load(async (signal) => {
    const result = await request<Page<Audit>>(
      `/audit${query({ account_id: account, action: action.value, entity_type: entity.value, limit: "30", cursor })}`,
      { signal },
    );
    if (
      !result ||
      !Array.isArray(result.items) ||
      result.items.length > 30 ||
      result.items.some(
        (a) =>
          typeof a.id !== "string" || (account && a.account_id !== account),
      )
    )
      throw new LedgerError("invalid_response");
    return result;
  });
}
function inspect(id: string) {
  detail.clear();
  const account = props.accountId;
  void detail.load(async (signal) => {
    const result = await request<Audit>(`/audit/${id}`, { signal });
    if (
      !result ||
      result.id !== id ||
      (account && result.account_id !== account) ||
      typeof result.after_json !== "string"
    )
      throw new LedgerError("invalid_response");
    return result;
  });
}
watch(
  () => props.accountId,
  () => {
    list.clear();
    detail.clear();
    opened.value = false;
  },
);
</script>

<template>
  <section class="ledger-panel" data-test="ledger-audit">
    <h3>操作审计（只读）</h3>
    <p>
      已提交的操作、修订和系统任务事件。只读查询不新增审计；原始 JSON
      按文本展示，保留数字精度。
    </p>
    <form class="ledger-grid" @submit.prevent="load()">
      <label
        >对象类型<input
          v-model="entity"
          name="audit_entity"
          placeholder="account_record / operation / weekly_job"
      /></label>
      <label
        >动作<input
          v-model="action"
          name="audit_action"
          placeholder="create / replace / void"
      /></label>
      <button :disabled="list.loading">查看 / 刷新操作审计</button>
    </form>
    <p v-if="list.loading || detail.loading" role="status">正在读取审计…</p>
    <p v-if="list.error || detail.error" role="alert">
      {{ list.error || detail.error }}
    </p>
    <p v-if="opened && list.data?.items.length === 0">没有匹配的已提交操作</p>
    <ul v-if="list.data">
      <li v-for="entry in list.data.items" :key="entry.id">
        <button @click="inspect(entry.id)">
          #{{ entry.id }} {{ entry.action }} · {{ entry.entity_type }} /
          {{ entry.entity_id }} · v{{ entry.version }}
        </button>
        {{ entry.recorded_at || "旧数据迁移捕获，原操作时间未知" }} ·
        {{ entry.source }}
      </li>
    </ul>
    <button v-if="list.data?.next_cursor" @click="load(list.data.next_cursor)">
      下一页审计
    </button>
    <div v-if="detail.data">
      <h4>审计 #{{ detail.data.id }}</h4>
      <p>修改前</p>
      <pre>{{ detail.data.before_json ?? "无前一版本" }}</pre>
      <p>修改后 / 原始证据</p>
      <pre>{{ detail.data.after_json }}</pre>
      <p>元数据</p>
      <pre>{{ detail.data.metadata_json }}</pre>
    </div>
  </section>
</template>

<style scoped>
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 24rem;
  overflow: auto;
}
li {
  overflow-wrap: anywhere;
  margin-block: 0.5rem;
}
</style>

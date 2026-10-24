<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from "vue";
import {
  taxRequest,
  taxDay,
  taxNumber,
  taxExact,
  taxMoney,
  taxPrice,
  securityIdentity,
  pivotRates,
  summaryColumns,
  type TaxState,
  type TaxReport,
  type TaxPreview,
  type TaxRecord,
} from "./tax";
import "./tax.css";
import TaxSecurity from "./TaxSecurity.vue";
import TaxDetailDialog from "./TaxDetailDialog.vue";

const state = ref<TaxState>({ imports: [], records: [], rates: [] });
const report = ref<TaxReport>({
  rows: [],
  matches: [],
  entries: [],
  missing: [],
  method: "",
});
const tab = ref("profit");
const busy = ref(false),
  loading = ref(false),
  error = ref(""),
  notice = ref("");
const from = ref(""),
  to = ref(""),
  account = ref("");
const kind = ref(""),
  search = ref(""),
  page = ref(1),
  detailYear = ref<number>();
const importsOpen = ref(false),
  fileInput = ref<HTMLInputElement>();
const names = ref<Record<string, string>>({});
const namesLoading = ref(false);
const rateRows = computed(() => pivotRates(state.value.rates));
const pending = ref<
  { file: File; preview?: TaxPreview; error: string; imported: boolean }[]
>([]);
const weiboURL = `${location.protocol}//${location.hostname}:5050/`;
const accounts = computed(() => [
  ...new Map(
    state.value.records.map((r) => [
      r.account_id,
      { id: r.account_id, name: r.account },
    ]),
  ).values(),
]);
const filtered = computed(() =>
  state.value.records.filter((r) => {
    const d = taxDay(r.date);
    return (
      (!from.value || d >= from.value) &&
      (!to.value || d <= to.value) &&
      (!account.value || r.account_id === account.value)
    );
  }),
);
const statements = computed(() =>
  filtered.value
    .filter(
      (r) =>
        (!kind.value || r.kind === kind.value) &&
        (!search.value ||
          `${r.code} ${securityIdentity(r).code} ${names.value[securityIdentity(r).key] ?? ""} ${r.note} ${r.reason} ${r.account}`
            .toLowerCase()
            .includes(search.value.toLowerCase())),
    )
    .sort(
      (a, b) =>
        taxDay(b.date).localeCompare(taxDay(a.date)) ||
        b.date.localeCompare(a.date) ||
        b.line - a.line,
    ),
);
const shown = computed(() =>
  statements.value.slice((page.value - 1) * 50, page.value * 50),
);
const pageCount = computed(() =>
  Math.max(1, Math.ceil(statements.value.length / 50)),
);
const details = computed(() =>
  report.value.entries.filter(
    (entry) => Number(entry.record.date.slice(0, 4)) === detailYear.value,
  ),
);
const readyFiles = computed(() =>
  pending.value.filter((p) => p.preview && !p.imported && !p.error),
);
const fail = (e: unknown) =>
  e instanceof Error ? e.message : "请求失败，请重试";
let reportController: AbortController | undefined;
let reportGeneration = 0;
let namesController: AbortController | undefined;
async function loadNames() {
  namesController?.abort();
  const controller = new AbortController();
  namesController = controller;
  if (!state.value.records.length) {
    names.value = {};
    namesLoading.value = false;
    return;
  }
  namesLoading.value = true;
  try {
    const result = await taxRequest<{ names: Record<string, string> }>(
      "names",
      { signal: controller.signal },
    );
    if (!controller.signal.aborted) names.value = result.names;
  } catch {
    /* Names are optional; imported transactions remain usable. */
  } finally {
    if (!controller.signal.aborted) namesLoading.value = false;
  }
}

async function loadReport() {
  reportController?.abort();
  const controller = new AbortController();
  reportController = controller;
  const generation = ++reportGeneration;
  loading.value = true;
  error.value = "";
  const params = new URLSearchParams();
  if (from.value) params.set("from", from.value);
  if (to.value) params.set("to", to.value);
  if (account.value) params.set("account", account.value);
  try {
    const result = await taxRequest<TaxReport>(`report?${params}`, {
      signal: controller.signal,
    });
    if (generation === reportGeneration && !controller.signal.aborted)
      report.value = result;
  } catch (e) {
    if (generation === reportGeneration && !controller.signal.aborted) {
      error.value = fail(e);
      report.value = {
        rows: [],
        matches: [],
        entries: [],
        missing: [],
        method: "",
      };
    }
  } finally {
    if (generation === reportGeneration) loading.value = false;
  }
}
async function reload() {
  error.value = "";
  try {
    state.value = await taxRequest<TaxState>("state");
    void loadNames();
    if (account.value && !accounts.value.some((a) => a.id === account.value))
      account.value = "";
    await loadReport();
  } catch (e) {
    error.value = fail(e);
  }
}
watch([from, to, account], () => {
  page.value = 1;
  detailYear.value = undefined;
  void loadReport();
});
watch([kind, search], () => {
  page.value = 1;
});
onMounted(() => void reload());
onBeforeUnmount(() => {
  reportController?.abort();
  namesController?.abort();
});

async function previewFiles(event: Event) {
  const input = event.target as HTMLInputElement;
  if (!input.files?.length) return;
  pending.value = Array.from(input.files).map((file) => ({
    file,
    error: "",
    imported: false,
  }));
  input.value = "";
  busy.value = true;
  notice.value = "";
  for (const item of pending.value) {
    try {
      const form = new FormData();
      form.append("file", item.file);
      item.preview = await taxRequest<TaxPreview>("preview", {
        method: "POST",
        body: form,
      });
    } catch (e) {
      item.error = fail(e);
    }
  }
  busy.value = false;
}
async function confirmImports() {
  busy.value = true;
  notice.value = "";
  error.value = "";
  let added = 0;
  let importError = "";
  for (const item of readyFiles.value) {
    try {
      const form = new FormData();
      form.append("file", item.file);
      form.append("digest", item.preview!.digest);
      const result = await taxRequest<{ added: number }>("imports", {
        method: "POST",
        body: form,
      });
      item.imported = true;
      added += result.added;
    } catch (e) {
      importError = `${item.file.name}：${fail(e)}。可重试导入，已成功的文件不会重复添加。`;
      break;
    }
  }
  await reload();
  if (importError) error.value = importError;
  notice.value = `本次新增 ${added} 条记录，已有记录自动去重。`;
  busy.value = false;
  if (pending.value.every((p) => p.imported)) pending.value = [];
}
async function removeBatch(id: number) {
  busy.value = true;
  error.value = "";
  try {
    await taxRequest(`imports/${id}`, { method: "DELETE" });
    await reload();
    page.value = 1;
    notice.value = "已移除导入批次，其他来源的重叠记录仍保留。";
  } catch (e) {
    error.value = fail(e);
  } finally {
    busy.value = false;
  }
}
const textAmount = (r: TaxRecord, key: keyof TaxRecord) => {
  const value = r[key];
  return typeof value === "string" && value !== ""
    ? taxNumber(taxExact(value))
    : "—";
};
</script>

<template>
  <div class="tax-page">
    <header class="tax-header">
      <a class="tax-brand" href="/tax"
        ><span>观</span>观价 <b>/</b> <small>税务计算</small></a
      >
      <nav aria-label="模块导航">
        <a :href="weiboURL">微博管理</a><a href="/liquor">白酒行情</a
        ><a href="/ledger">投资账本</a
        ><a href="/tax" aria-current="page">税务计算</a>
      </nav>
    </header>
    <main class="tax-main">
      <div class="tax-heading">
        <div>
          <h1>税务计算</h1>
          <p>从每笔成交，到每年的已实现收益。</p>
        </div>
        <div class="tax-actions">
          <button :disabled="busy || loading" @click="reload">刷新</button
          ><button :disabled="busy" @click="importsOpen = !importsOpen">
            导入记录 <span>{{ state.imports.length }}</span></button
          ><button
            class="tax-primary"
            :disabled="busy"
            @click="fileInput?.click()"
          >
            导入 CSV</button
          ><input
            ref="fileInput"
            class="tax-file"
            type="file"
            accept=".csv,text/csv"
            multiple
            aria-label="上传税务 CSV"
            @change="previewFiles"
          />
        </div>
      </div>
      <p v-if="error" role="alert" class="tax-message tax-error">{{ error }}</p>
      <p v-if="notice" role="status" class="tax-message">{{ notice }}</p>
      <section v-if="pending.length" class="tax-panel" aria-label="导入预览">
        <h2>导入预览</h2>
        <ul class="tax-import-list">
          <li v-for="(item, i) in pending" :key="i">
            <strong>{{ item.file.name }}</strong
            ><span v-if="item.imported">已导入</span
            ><span v-else-if="item.error" class="tax-error-text">{{
              item.error
            }}</span
            ><span v-else-if="item.preview"
              >{{ item.preview.count }} 条，预计新增
              {{ item.preview.added }} 条</span
            ><span v-else>正在校验…</span>
          </li>
        </ul>
        <p class="tax-muted">
          文件之间的重叠记录会在确认导入时再次去重；每份文件原子导入。
        </p>
        <div class="tax-actions">
          <button
            class="tax-primary"
            :disabled="busy || !readyFiles.length"
            @click="confirmImports"
          >
            {{ busy ? "处理中…" : "确认导入" }}</button
          ><button :disabled="busy" @click="pending = []">关闭预览</button>
        </div>
      </section>
      <section v-if="importsOpen" class="tax-panel">
        <h2>导入记录</h2>
        <p v-if="!state.imports.length">还没有导入文件。</p>
        <ul class="tax-import-list">
          <li v-for="batch in state.imports" :key="batch.id">
            <div>
              <strong>{{ batch.name }}</strong
              ><small
                >{{ batch.count }} 条 · {{ batch.created.slice(0, 10) }}</small
              >
            </div>
            <button :disabled="busy" @click="removeBatch(batch.id)">
              移除批次
            </button>
          </li>
        </ul>
        <p class="tax-muted">
          移除批次会重新计算。需要恢复时，重新上传原 CSV。
        </p>
      </section>
      <div v-show="tab !== 'rates'" class="tax-filters">
        <label
          >资产账户<select v-model="account" :disabled="busy">
            <option value="">全部账户</option>
            <option v-for="a in accounts" :key="a.id" :value="a.id">
              {{ a.name }} · {{ a.id.slice(-4) }}
            </option>
          </select></label
        ><label
          >开始日期<input v-model="from" type="date" :disabled="busy" /></label
        ><label
          >结束日期<input v-model="to" type="date" :disabled="busy" /></label
        ><button
          :disabled="busy"
          @click="
            from = '';
            to = '';
            account = '';
          "
        >
          全部时间
        </button>
      </div>
      <div class="tax-tabs" role="tablist" aria-label="税务视图">
        <button
          v-for="t in [
            { id: 'records', name: '对账单' },
            { id: 'profit', name: '净利润' },
            { id: 'rates', name: '汇率' },
          ]"
          :id="`tax-tab-${t.id}`"
          :key="t.id"
          role="tab"
          :aria-selected="tab === t.id"
          :aria-controls="`tax-view-${t.id}`"
          @click="tab = t.id"
        >
          {{ t.name }}
        </button>
      </div>
      <div v-if="loading" class="tax-loading" role="status">正在核算…</div>

      <section
        v-show="tab === 'records'"
        id="tax-view-records"
        role="tabpanel"
        aria-labelledby="tax-tab-records"
      >
        <div class="tax-tools">
          <label
            >流水品类<select v-model="kind">
              <option value="">全部品类</option>
              <option>证券</option>
              <option>基金</option>
              <option>债券</option>
              <option>资金进出</option>
            </select></label
          ><input
            v-model="search"
            type="search"
            placeholder="搜索代码、名称、备注"
            aria-label="搜索流水"
          />
        </div>
        <div class="tax-table-wrap">
          <table class="tax-records">
            <thead>
              <tr>
                <th class="tax-index">#</th>
                <th>日期</th>
                <th>账户</th>
                <th>品类</th>
                <th>证券</th>
                <th>方向</th>
                <th>币种</th>
                <th class="tax-num">数量</th>
                <th class="tax-num">价格</th>
                <th class="tax-num">成交金额</th>
                <th class="tax-num">费用</th>
                <th class="tax-num">变动金额</th>
                <th>变动原因</th>
                <th>备注</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(r, i) in shown" :key="`${r.source}-${r.line}-${i}`">
                <td class="tax-index">{{ (page - 1) * 50 + i + 1 }}</td>
                <td>
                  {{ r.date.length === 8 ? taxDay(r.date) : r.date }}
                </td>
                <td>
                  {{ r.account }}<small>{{ r.account_id }}</small>
                </td>
                <td>{{ r.kind }}</td>
                <td>
                  <TaxSecurity
                    :record="r"
                    :names="names"
                    :loading="namesLoading"
                  />
                </td>
                <td>{{ r.direction }}</td>
                <td>{{ r.currency }}</td>
                <td class="tax-num">{{ textAmount(r, "quantity") }}</td>
                <td class="tax-num">{{ taxPrice(r.price, r.currency) }}</td>
                <td class="tax-num" :title="taxPrice(r.amount, r.currency)">
                  {{ taxMoney(r.amount, r.currency) }}
                </td>
                <td class="tax-num">{{ taxMoney(r.fee, r.currency) }}</td>
                <td class="tax-num" :title="taxPrice(r.change, r.currency)">
                  {{ taxMoney(r.change, r.currency) }}
                </td>
                <td>{{ r.reason || "—" }}</td>
                <td class="tax-note">
                  {{ r.note }}
                </td>
              </tr>
              <tr v-if="!shown.length">
                <td colspan="14" class="tax-empty">
                  {{
                    state.records.length
                      ? "筛选范围内没有流水。"
                      : "上传理杏仁格式 CSV，开始核对交易与资金流水。"
                  }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="tax-pagination">
          <div>
            <button :disabled="page <= 1" @click="page--">上一页</button
            ><span>{{ page }} / {{ pageCount }}</span
            ><button :disabled="page >= pageCount" @click="page++">
              下一页
            </button>
          </div>
        </div>
      </section>

      <section
        v-show="tab === 'profit'"
        id="tax-view-profit"
        role="tabpanel"
        aria-labelledby="tax-tab-profit"
      >
        <div class="tax-tools">
          <p>净利润＝已配对平仓盈亏＋赎回盈亏−当年全部费用</p>
        </div>
        <div class="tax-table-wrap">
          <table class="tax-summary">
            <thead>
              <tr>
                <th>年度</th>
                <th>币种</th>
                <th v-for="c in summaryColumns" :key="c[0]" class="tax-num">
                  {{ c[1] }}
                </th>
                <th class="tax-num">净利润</th>
                <th class="tax-num">股息收入</th>
                <th class="tax-num">估算税额（元）</th>
                <th>明细</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="r in report.rows"
                :key="`${r.year}-${r.currency}`"
                :class="{ 'tax-total': r.currency === 'CNY' }"
              >
                <td>
                  {{ r.year
                  }}<small v-if="!r.complete" class="tax-error-text"
                    >缺成本</small
                  >
                </td>
                <td>{{ r.currency === "CNY" ? "人民币合计" : r.currency }}</td>
                <td v-for="c in summaryColumns" :key="c[0]" class="tax-num">
                  {{ taxMoney(String(r[c[0]]), r.currency) }}
                </td>
                <td class="tax-num tax-emphasis">
                  {{ taxMoney(r.net, r.currency)
                  }}<small>税额 {{ taxMoney(r.profit_tax, "CNY") }}</small>
                </td>
                <td class="tax-num">
                  {{ taxMoney(r.dividends, r.currency)
                  }}<small>税额 {{ taxMoney(r.dividend_tax, "CNY") }}</small>
                </td>
                <td class="tax-num tax-emphasis">
                  {{ taxMoney(r.tax, "CNY")
                  }}<small v-if="r.missing_rate" class="tax-error-text"
                    >缺少年度汇率</small
                  >
                </td>
                <td>
                  <button
                    v-if="r.currency === 'CNY'"
                    class="tax-link"
                    :disabled="loading || busy"
                    @click="detailYear = r.year"
                  >
                    详情
                  </button>
                </td>
              </tr>
              <tr v-if="!report.rows.length">
                <td colspan="15" class="tax-empty">
                  <h2>
                    {{
                      state.records.length
                        ? "当前范围没有可汇总的交易、股息或费用"
                        : "让每一笔收益都有依据"
                    }}
                  </h2>
                  <p>
                    {{
                      state.records.length
                        ? "可调整日期或账户筛选。"
                        : "一次导入多个年度，自动衔接历史成本，按年汇总港元与美元收益。"
                    }}
                  </p>
                  <button
                    v-if="!state.records.length"
                    class="tax-primary"
                    :disabled="busy"
                    @click="fileInput?.click()"
                  >
                    选择年度 CSV
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <section v-if="report.missing.length" class="tax-panel">
          <h2>缺少前置开仓</h2>
          <p>以下未匹配部分未计为零成本盈利。补齐历史流水后会自动重新配对。</p>
          <div class="tax-table-wrap">
            <table>
              <thead>
                <tr>
                  <th>平仓日期</th>
                  <th>代码</th>
                  <th>币种</th>
                  <th class="tax-num">缺口数量</th>
                  <th class="tax-num">未匹配金额</th>
                  <th>账户</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(m, i) in report.missing" :key="i">
                  <td>{{ m.record.date }}</td>
                  <td>
                    <TaxSecurity
                      :record="m.record"
                      :names="names"
                      :loading="namesLoading"
                    />
                  </td>
                  <td>{{ m.record.currency }}</td>
                  <td class="tax-num">{{ taxExact(m.quantity) }}</td>
                  <td class="tax-num">
                    {{ taxMoney(m.amount, m.record.currency) }}
                  </td>
                  <td>
                    {{ m.record.account
                    }}<small>{{ m.record.account_id }}</small>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <details class="tax-method">
          <summary>计算口径与支持范围</summary>
          <p>
            证券、基金、债券及资金进出，支持
            HKD／USD。股票采用先进先出，交易费用按发生年度全部扣除；“股息收入”单列，“银证转账”不计收益，其他非空变动原因按费用处理。空原因资金只保留流水。
          </p>
          <p>
            交易利润按每币种正收益的 20% 估算；港元股息 20%，美元股息按已扣约
            10% 后再补 10% 的简化口径。股息保留 CSV
            原金额，不自动调整税前税后。原币合计后折人民币，金额最终保留两位。期权不属于此格式的支持范围。
          </p>
          <p>
            结果用于复现理杏仁测算，申报和缴款由官方税务渠道完成。<a
              href="https://www.lixinger.com/marketing/oversea-tax-import"
              target="_blank"
              rel="noopener"
              >查看理杏仁导入指南</a
            >
          </p>
        </details>
      </section>

      <section
        v-show="tab === 'rates'"
        id="tax-view-rates"
        role="tabpanel"
        aria-labelledby="tax-tab-rates"
      >
        <h2 class="tax-rates-heading">兑换人民币汇率</h2>
        <div class="tax-table-wrap">
          <table class="tax-rates">
            <thead>
              <tr>
                <th>年度</th>
                <th>港币（HKD）</th>
                <th>美元（USD）</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in rateRows" :key="r.year">
                <td>{{ r.year }}年</td>
                <td>{{ r.HKD === null ? "—" : taxExact(r.HKD) }}</td>
                <td>{{ r.USD === null ? "—" : taxExact(r.USD) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </main>
    <TaxDetailDialog
      v-if="detailYear"
      :year="detailYear"
      :entries="details"
      :names="names"
      :names-loading="namesLoading"
      @close="detailYear = undefined"
    />
  </div>
</template>

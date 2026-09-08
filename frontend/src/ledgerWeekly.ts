import type { Account, Page, ValuationHistory } from "./ledger";
import type { AccountRecordSource } from "./accountRecords";

export const weeklyStates = {
  pending: "待处理",
  running: "执行中记录",
  succeeded: "已保存",
  failed: "失败",
  skipped: "已跳过",
} as const;
export const weeklyReasons = {
  "": "无",
  no_source: "没有可沿用的历史总资产，本周未更新（不是失败或零资产）",
  expired: "已错过周六窗口，不用今日价格补写历史",
  interrupted: "上次尝试中断",
  canceled: "本次尝试已取消",
  timeout: "本次尝试超时",
  basis_changed: "采样期间持仓依据改变，未保存",
  incomplete_valuation: "必要报价或汇率不完整，未保存总额",
  invalid_valuation: "金额、来源或时间校验未通过",
  storage_error: "保存失败",
} as const;
export const weeklySources = {
  holdings_current: "当前持仓 / 现金",
  account_record_carry: "沿用最近总资产",
} as const;
export interface WeeklyStatus {
  enabled: boolean;
  timezone: "Asia/Shanghai";
  weekday: "Saturday";
  time: string;
  next_scheduled_at: string | null;
  window_open: boolean;
  max_attempts: 3;
}
export interface WeeklyJob {
  id: string;
  account_id: string;
  scheduled_business_date: string;
  source: keyof typeof weeklySources;
  status: keyof typeof weeklyStates;
  attempts: number;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  next_attempt_at: string | null;
  error_code: keyof typeof weeklyReasons;
  history_id: string | null;
  carry?: WeeklyCarryHistory;
}
export interface WeeklyCarryHistory {
  id: string;
  saved_at: string;
  schema_version: 1;
  account_name: string;
  record_id: string;
  account_id: string;
  currency: Account["currency"];
  as_of: string;
  total_assets: string;
  source_record: AccountRecordSource;
}
function requireValid(value: unknown): asserts value {
  if (!value) throw new Error("调度响应无效或与所选账户 / 任务不匹配");
}
const id = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[1-9]\d{0,18}$/.test(value) &&
  BigInt(value) <= 9223372036854775807n;
const recordId = (value: unknown): value is string =>
  typeof value === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(value);
const date = (value: unknown): value is string =>
  typeof value === "string" &&
  /^\d{4}-\d{2}-\d{2}$/.test(value) &&
  Number.isFinite(Date.parse(value)) &&
  new Date(value).toISOString().slice(0, 10) === value;
const stamp = (value: unknown): value is string =>
  typeof value === "string" &&
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(
    value,
  ) &&
  date(value.slice(0, 10)) &&
  Number.isFinite(Date.parse(value));
const nullableStamp = (value: unknown) => value === null || stamp(value);
const money = (value: unknown) =>
  typeof value === "string" && /^-?\d+\.\d{2}$/.test(value);
const numeric = (value: unknown) =>
  typeof value === "string" && /^\d+(?:\.\d+)?$/.test(value);
const before = (a: string, b: string) => Date.parse(a) <= Date.parse(b);
const owns = (object: object, key: unknown) =>
  typeof key === "string" && Object.hasOwn(object, key);

const beijing = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hourCycle: "h23",
});
export function weeklyTime(value: string | null) {
  return value === null
    ? "未提供"
    : `${beijing.format(new Date(value))} 北京时间`;
}
export function validateWeeklyStatus(value: WeeklyStatus): WeeklyStatus {
  requireValid(
    value &&
      typeof value.enabled === "boolean" &&
      value.timezone === "Asia/Shanghai" &&
      value.weekday === "Saturday" &&
      typeof value.time === "string" &&
      /^(?:[01]\d|2[0-3]):[0-5]\d$/.test(value.time) &&
      typeof value.window_open === "boolean" &&
      value.max_attempts === 3 &&
      (value.enabled
        ? stamp(value.next_scheduled_at)
        : value.next_scheduled_at === null && value.window_open === false),
  );
  return value;
}
export function validateWeeklyJob(
  value: WeeklyJob,
  accountId: string,
  jobId?: string,
  currency?: Account["currency"],
): WeeklyJob {
  requireValid(
    value &&
      id(value.id) &&
      (!jobId || value.id === jobId) &&
      value.account_id === accountId &&
      date(value.scheduled_business_date) &&
      new Date(value.scheduled_business_date).getUTCDay() === 6 &&
      owns(weeklySources, value.source) &&
      owns(weeklyStates, value.status) &&
      owns(weeklyReasons, value.error_code) &&
      Number.isInteger(value.attempts) &&
      value.attempts >= 0 &&
      value.attempts <= 3 &&
      stamp(value.created_at) &&
      nullableStamp(value.started_at) &&
      nullableStamp(value.finished_at) &&
      nullableStamp(value.next_attempt_at) &&
      (value.history_id === null || id(value.history_id)),
  );
  requireValid(
    (!value.started_at || before(value.created_at, value.started_at)) &&
      (!value.finished_at ||
        before(value.started_at ?? value.created_at, value.finished_at)) &&
      (!value.next_attempt_at ||
        (value.status === "failed" &&
          value.attempts < 3 &&
          value.finished_at &&
          before(value.finished_at, value.next_attempt_at))) &&
      (value.status === "succeeded"
        ? value.history_id !== null &&
          value.attempts > 0 &&
          value.started_at &&
          value.finished_at &&
          value.error_code === ""
        : value.history_id === null) &&
      (value.error_code !== "no_source" ||
        (value.source === "account_record_carry" &&
          value.status === "skipped")) &&
      (value.status !== "failed" ||
        (value.attempts > 0 &&
          value.started_at &&
          value.finished_at &&
          !["", "no_source", "expired"].includes(value.error_code))) &&
      (value.status !== "skipped" ||
        (["no_source", "expired"].includes(value.error_code) &&
          value.finished_at)) &&
      (value.status !== "running" ||
        (value.attempts > 0 &&
          value.started_at &&
          !value.finished_at &&
          value.error_code === "")) &&
      (value.status !== "pending" ||
        (value.attempts === 0 &&
          !value.started_at &&
          !value.finished_at &&
          value.error_code === "")),
  );
  if (
    jobId &&
    value.source === "account_record_carry" &&
    value.status === "succeeded"
  )
    validateWeeklyCarry(value.carry, value, accountId, currency);
  else requireValid(value.carry === undefined);
  return value;
}

function validateWeeklyCarry(
  value: WeeklyCarryHistory | undefined,
  job: WeeklyJob,
  accountId: string,
  currency?: Account["currency"],
) {
  const source = value?.source_record;
  requireValid(
    value &&
      value.id === job.history_id &&
      value.schema_version === 1 &&
      typeof value.account_name === "string" &&
      value.account_name.trim().length > 0 &&
      value.account_name.length <= 512 &&
      value.record_id === `weekly-carry-${job.id}` &&
      recordId(value.record_id) &&
      value.account_id === accountId &&
      ["CNY", "HKD", "USD"].includes(value.currency) &&
      (!currency || value.currency === currency) &&
      value.as_of === job.scheduled_business_date &&
      money(value.total_assets) &&
      !value.total_assets.startsWith("-") &&
      stamp(value.saved_at) &&
      job.started_at &&
      job.finished_at &&
      before(job.started_at, value.saved_at) &&
      before(value.saved_at, job.finished_at) &&
      source &&
      recordId(source.id) &&
      source.account_id === accountId &&
      id(source.sequence) &&
      id(source.version) &&
      date(source.date) &&
      source.date <= value.as_of &&
      [
        "import",
        "manual",
        "currentrefresh",
        "weekly",
        "weekly_carry",
        "operation",
      ].includes(source.origin) &&
      money(source.total_assets) &&
      !source.total_assets.startsWith("-") &&
      source.total_assets === value.total_assets &&
      (source.manual_assertion === undefined ||
        typeof source.manual_assertion === "boolean") &&
      (source.origin !== "weekly_carry" || source.manual_assertion === true),
  );
}
export function validateWeeklyPage(
  value: Page<WeeklyJob>,
  accountId: string,
  status: string,
  cursor: string,
): Page<WeeklyJob> {
  requireValid(
    value &&
      Array.isArray(value.items) &&
      value.items.length <= 30 &&
      (value.next_cursor === undefined || id(value.next_cursor)),
  );
  let previous = cursor;
  for (const item of value.items) {
    validateWeeklyJob(item, accountId);
    requireValid(
      (!status || item.status === status) &&
        (!previous || BigInt(item.id) < BigInt(previous)),
    );
    previous = item.id;
  }
  requireValid(
    value.next_cursor === undefined ||
      (value.items.length > 0 && value.next_cursor === value.items.at(-1)?.id),
  );
  return value;
}
export function weeklyRetryLabel(job: WeeklyJob, status?: WeeklyStatus) {
  if (!job.next_attempt_at)
    return job.status === "failed" && job.attempts === 3
      ? "已达 3 次上限，不再重试"
      : "未记录后续重试";
  const time = weeklyTime(job.next_attempt_at);
  if (!status?.enabled)
    return `记录的最早重试时间：${time}；${status ? "全局已关闭，不会执行重试" : "全局状态未确认，不代表将执行"}`;
  return `记录的最早重试时间：${time}；仍受周六窗口及后续轮次约束，不保证执行`;
}
export function validateWeeklyHistory(
  value: ValuationHistory,
  job: WeeklyJob,
  account: Account,
): ValuationHistory {
  const v = value?.valuation;
  requireValid(
    job.status === "succeeded" &&
      job.source === "holdings_current" &&
      account.id === job.account_id &&
      value?.id === job.history_id &&
      value.schema_version === 1 &&
      typeof value.account_name === "string" &&
      stamp(value.saved_at) &&
      Array.isArray(value.instruments) &&
      v &&
      v.account_id === account.id &&
      v.currency === account.currency &&
      v.as_of === job.scheduled_business_date &&
      v.complete === true &&
      money(v.cash) &&
      money(v.positions_value) &&
      money(v.known_positions_value) &&
      money(v.total_assets) &&
      typeof v.ledger_revision === "string" &&
      /^[a-f0-9]{64}$/.test(v.ledger_revision) &&
      stamp(v.ledger_at) &&
      stamp(v.calculated_at) &&
      Array.isArray(v.items) &&
      job.started_at &&
      job.finished_at &&
      before(job.started_at, v.ledger_at) &&
      before(v.ledger_at, v.calculated_at) &&
      before(v.calculated_at, value.saved_at) &&
      before(value.saved_at, job.finished_at),
  );
  const instruments = new Map(
    value.instruments.map((i) => {
      requireValid(
        i &&
          typeof i.id === "string" &&
          typeof i.name === "string" &&
          typeof i.market === "string" &&
          typeof i.code === "string" &&
          ["CNY", "HKD", "USD"].includes(i.currency),
      );
      return [i.id, i];
    }),
  );
  requireValid(
    instruments.size === value.instruments.length &&
      instruments.size === v.items.length,
  );
  const seen = new Set<string>();
  for (const item of v.items) {
    requireValid(
      item &&
        instruments.has(item.instrument_id) &&
        !seen.has(item.instrument_id) &&
        numeric(item.quantity) &&
        money(item.market_value) &&
        ["current", "prior_date", "closed"].includes(item.status),
    );
    seen.add(item.instrument_id);
    if (item.status === "closed") {
      requireValid(item.quote === null && item.fx === null);
      continue;
    }
    const q = item.quote;
    requireValid(
      q &&
        typeof q.symbol === "string" &&
        q.source === "Tencent" &&
        numeric(q.price) &&
        q.currency === instruments.get(item.instrument_id)!.currency &&
        date(q.date) &&
        q.date <= v.as_of &&
        stamp(q.quoted_at) &&
        stamp(q.fetched_at) &&
        before(q.quoted_at, q.fetched_at) &&
        before(v.ledger_at, q.fetched_at) &&
        before(q.fetched_at, v.calculated_at),
    );
    if (q.currency === v.currency) requireValid(item.fx === null);
    else {
      const fx = item.fx;
      const pair = `${q.currency}${v.currency}`;
      const direct = ["USDCNY", "USDHKD", "HKDCNY"].includes(pair);
      const source = direct ? pair : `${v.currency}${q.currency}/inverse`;
      requireValid(
        fx &&
          fx.base === q.currency &&
          fx.quote === v.currency &&
          fx.mode === "latest" &&
          numeric(fx.rate) &&
          fx.source === `Tencent/spot/${source}` &&
          fx.requested_date === v.as_of &&
          date(fx.date) &&
          fx.date <= v.as_of &&
          stamp(fx.quoted_at) &&
          stamp(fx.fetched_at) &&
          before(fx.quoted_at, fx.fetched_at) &&
          before(v.ledger_at, fx.fetched_at) &&
          before(fx.fetched_at, v.calculated_at),
      );
    }
  }
  return value;
}

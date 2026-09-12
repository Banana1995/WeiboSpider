import { describe, expect, it } from "vitest";
import type { Account, ValuationHistory } from "./ledger";
import {
  validateWeeklyHistory,
  validateWeeklyJob,
  validateWeeklyPage,
  validateWeeklyStatus,
  weeklyRetryLabel,
  weeklyTime,
  type WeeklyCarryHistory,
  type WeeklyJob,
  type WeeklyStatus,
} from "./ledgerWeekly";

const account: Account = {
  current_holdings_input: "manual_snapshot",
  id: "a",
  name: "合成持仓账户",
  currency: "CNY",
  opening_date: "2020-01-01",
  opening_cash: "0.00",
  version: "1",
};
const disabledStatus: WeeklyStatus = {
  enabled: false,
  timezone: "Asia/Shanghai",
  weekday: "Saturday",
  time: "08:00",
  next_scheduled_at: null,
  window_open: false,
  max_attempts: 3,
};
const job: WeeklyJob = {
  id: "9007199254740993",
  account_id: "a",
  scheduled_business_date: "2026-09-05",
  source: "holdings_current",
  status: "succeeded",
  attempts: 1,
  created_at: "2026-09-05T00:00:00.000000000Z",
  started_at: "2026-09-05T00:00:01.000000001Z",
  finished_at: "2026-09-05T00:00:04.000000001Z",
  next_attempt_at: null,
  error_code: "",
  history_id: "9007199254740994",
};
const carriedJob: WeeklyJob = {
  ...job,
  id: "3",
  account_id: "b",
  source: "account_record_carry",
  history_id: "4",
};
const carried: WeeklyCarryHistory = {
  id: "4",
  saved_at: "2026-09-05T00:00:03.500000001Z",
  schema_version: 1,
  account_name: "合成手工账户",
  record_id: "weekly-carry-3",
  account_id: "b",
  currency: "CNY",
  as_of: "2026-09-05",
  total_assets: "900.00",
  source_record: {
    id: "manual-old",
    account_id: "b",
    sequence: "1",
    version: "1",
    date: "2026-09-01",
    origin: "manual",
    total_assets: "900.00",
  },
};
const frozen: ValuationHistory = {
  id: job.history_id!,
  schema_version: 1,
  account_name: "合成冻结旧名称 <b>不是 HTML</b>",
  saved_at: "2026-09-05T00:00:03.500000001Z",
  valuation: {
    source: "manual_snapshot",
    account_id: "a",
    currency: "CNY",
    as_of: "2026-09-05",
    ledger_revision: "a".repeat(64),
    ledger_at: "2026-09-05T00:00:01.500000001Z",
    calculated_at: "2026-09-05T00:00:03.000000001Z",
    cash: "90071992547409.01",
    positions_value: "70.00",
    known_positions_value: "70.00",
    total_assets: "90071992547479.01",
    complete: true,
    items: [
      {
        instrument_id: "stock",
        quantity: "1.000000",
        market_value: "70.00",
        status: "prior_date",
        quote: {
          symbol: "sh900001",
          price: "10.000000",
          currency: "USD",
          source: "Tencent",
          date: "2026-09-04",
          quoted_at: "2026-09-04T07:00:00Z",
          fetched_at: "2026-09-05T00:00:02Z",
        },
        fx: {
          base: "USD",
          quote: "CNY",
          mode: "latest",
          requested_date: "2026-09-05",
          rate: "7.00000000",
          source: "Tencent/spot/USDCNY",
          date: "2026-09-04",
          quoted_at: "2026-09-04T08:00:00Z",
          fetched_at: "2026-09-05T00:00:02Z",
        },
      },
    ],
  },
  instruments: [
    {
      id: "stock",
      name: "合成冻结证券",
      market: "SH",
      code: "900001",
      currency: "USD",
    },
  ],
};

describe("weekly read contract", () => {
  it("accepts disabled and enabled open window with next Saturday, formats nanosecond UTC explicitly in Beijing", () => {
    expect(validateWeeklyStatus(disabledStatus)).toBe(disabledStatus);
    expect(
      validateWeeklyStatus({
        ...disabledStatus,
        enabled: true,
        window_open: true,
        next_scheduled_at: "2026-09-12T08:00:00+08:00",
      }).window_open,
    ).toBe(true);
    expect(weeklyTime("2026-09-04T16:00:00.123456789Z")).toBe(
      "2026/09/05 00:00:00 北京时间",
    );
    expect(weeklyTime("2026-09-05T00:00:00+08:00")).toBe(
      "2026/09/05 00:00:00 北京时间",
    );
    expect(weeklyTime(null)).toBe("未提供");
  });
  it.each([
    { enabled: "false" },
    { timezone: "UTC" },
    { weekday: "Friday" },
    { time: "8:00" },
    { window_open: true },
    { max_attempts: 4 },
    { next_scheduled_at: "2026-09-12" },
  ])("rejects malformed global status %j", (patch) => {
    expect(() =>
      validateWeeklyStatus({ ...disabledStatus, ...patch } as WeeklyStatus),
    ).toThrow("响应无效");
  });
  it("retains exact string IDs and money, accepts zero-success and prior-date provenance", () => {
    expect(validateWeeklyJob(job, "a", job.id).id).toBe(job.id);
    expect(
      validateWeeklyHistory(frozen, job, account).valuation.total_assets,
    ).toBe("90071992547479.01");
    const zero = structuredClone(frozen);
    zero.instruments = [];
    zero.valuation = {
      ...zero.valuation,
      cash: "0.00",
      total_assets: "0.00",
      positions_value: "0.00",
      known_positions_value: "0.00",
      items: [],
    };
    expect(
      validateWeeklyHistory(zero, job, account).valuation.total_assets,
    ).toBe("0.00");
  });
  it("accepts carried totals only as frozen detail evidence and rejects missing or mismatched evidence", () => {
    expect(validateWeeklyJob(carriedJob, "b")).toBe(carriedJob);
    const detail = { ...carriedJob, carry: carried };
    expect(validateWeeklyJob(detail, "b", "3", "CNY").carry).toBe(carried);
    expect(() => validateWeeklyJob(carriedJob, "b", "3", "CNY")).toThrow(
      "响应无效",
    );
    expect(() => validateWeeklyJob(detail, "b")).toThrow("响应无效");
    expect(() =>
      validateWeeklyJob(
        {
          ...detail,
          carry: {
            ...carried,
            source_record: {
              ...carried.source_record,
              total_assets: "901.00",
            },
          },
        },
        "b",
        "3",
        "CNY",
      ),
    ).toThrow("响应无效");
    expect(
      validateWeeklyJob(
        {
          ...carriedJob,
          status: "skipped",
          error_code: "no_source",
          history_id: null,
          carry: undefined,
        },
        "b",
        "3",
        "CNY",
      ).status,
    ).toBe("skipped");
  });
  it.each([
    { id: 1 },
    { id: "01" },
    { id: "9223372036854775808" },
    { account_id: "b" },
    { status: "constructor" },
    { source: "manual" },
    { attempts: 4 },
    { attempts: -1 },
    { error_code: "<img src=x onerror=alert(1)>" },
    { started_at: "2026-09-05T08:00:00" },
    { history_id: "../../valuation" },
    { history_id: null },
  ])("rejects malformed or cross-scope job %j", (patch) => {
    expect(() =>
      validateWeeklyJob({ ...job, ...patch } as WeeklyJob, "a"),
    ).toThrow("响应无效");
  });
  it("validates descending page/cursor strings, account identity, and filter without Number coercion", () => {
    expect(
      validateWeeklyPage(
        { items: [job], next_cursor: job.id },
        "a",
        "succeeded",
        "",
      ).next_cursor,
    ).toBe(job.id);
    for (const page of [
      { items: [job], next_cursor: 1 },
      { items: [job], next_cursor: "" },
      { items: [], next_cursor: "1" },
      { items: [job], next_cursor: "2" },
      { items: [job, job] },
      { items: [{ ...job, account_id: "b" }] },
    ])
      expect(() => validateWeeklyPage(page as never, "a", "", "")).toThrow();
    expect(() =>
      validateWeeklyPage({ items: [job] }, "a", "failed", ""),
    ).toThrow();
    expect(() =>
      validateWeeklyPage({ items: [job] }, "a", "", job.id),
    ).toThrow();
  });
  it("does not promise retry while disabled or unknown, distinguishes exhausted three attempts", () => {
    const failed: WeeklyJob = {
      ...job,
      status: "failed",
      error_code: "timeout",
      history_id: null,
      next_attempt_at: "2026-09-05T00:05:04Z",
    };
    expect(validateWeeklyJob(failed, "a")).toBe(failed);
    expect(weeklyRetryLabel(failed, disabledStatus)).toContain(
      "全局已关闭，不会执行重试",
    );
    expect(weeklyRetryLabel(failed)).toContain("全局状态未确认");
    expect(
      weeklyRetryLabel(failed, { ...disabledStatus, enabled: true }),
    ).toContain("不保证执行");
    expect(
      weeklyRetryLabel({ ...failed, attempts: 3, next_attempt_at: null }),
    ).toContain("已达 3 次上限");
    expect(() => validateWeeklyJob({ ...failed, attempts: 3 }, "a")).toThrow();
  });
  it.each([
    "id",
    "account",
    "currency",
    "date",
    "source",
    "chronology",
    "instruments",
    "amount",
  ])(
    "rejects wrong frozen history %s without using current valuation",
    (kind) => {
      const value = structuredClone(frozen);
      if (kind === "id") value.id = "1";
      if (kind === "account") value.valuation.account_id = "b";
      if (kind === "currency") value.valuation.currency = "HKD";
      if (kind === "date") value.valuation.as_of = "2026-09-04";
      if (kind === "source")
        value.valuation.items[0]!.quote!.source = "unknown";
      if (kind === "chronology") value.saved_at = "2026-09-05T00:00:00Z";
      if (kind === "instruments") value.instruments = [];
      if (kind === "amount") value.valuation.total_assets = 0 as never;
      expect(() => validateWeeklyHistory(value, job, account)).toThrow(
        "响应无效",
      );
    },
  );
});

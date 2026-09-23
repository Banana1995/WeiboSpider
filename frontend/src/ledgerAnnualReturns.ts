import type { Account } from "./ledger";
import { benchmarkDefinition, type BenchmarkCode } from "./ledgerBenchmark";
import { returnReasons, type ReturnMetric } from "./ledgerReturns";
import { validDay } from "./ledgerView";

export interface AnnualReturnRow {
  year: number;
  from: string;
  to: string;
  money_weighted: ReturnMetric;
  time_weighted: ReturnMetric;
  benchmark: ReturnMetric;
  benchmark_from: string;
  benchmark_to: string;
}

export interface AnnualReturns {
  account_id: string;
  currency: string;
  as_of: string;
  revision: string;
  benchmark_code: BenchmarkCode;
  benchmark_name: string;
  benchmark_currency: string;
  benchmark_source: string;
  benchmark_error: "" | "benchmark_unavailable" | "benchmark_timeout";
  annualized: AnnualReturnRow;
  since: AnnualReturnRow;
  years: AnnualReturnRow[];
}

const benchmarkReasons = [
  "missing_benchmark",
  "benchmark_unavailable",
  "benchmark_timeout",
  "out_of_solver_range",
  "precision_unresolved",
  "not_converged",
  "twr_product_limit",
];

export function validateAnnualReturns(
  value: AnnualReturns,
  account: Account,
  code: BenchmarkCode,
): AnnualReturns {
  const definition = benchmarkDefinition(code);
  const day = (s: string) => s === "" || validDay(s);
  const metric = (m: ReturnMetric, index = false) =>
    m &&
    ["available", "reference", "unavailable"].includes(m.status) &&
    (m.status === "unavailable"
      ? m.value === null &&
        m.percentage === null &&
        (index
          ? benchmarkReasons.includes(m.reason)
          : Object.hasOwn(returnReasons, m.reason))
      : typeof m.value === "string" &&
        /^-?\d{1,100}\.\d{12}$/.test(m.value) &&
        typeof m.percentage === "string" &&
        /^-?\d{1,100}\.\d{2}$/.test(m.percentage) &&
        m.reason === "");
  const row = (r: AnnualReturnRow) =>
    r &&
    Number.isInteger(r.year) &&
    r.year >= 0 &&
    r.year <= 9999 &&
    day(r.from) &&
    day(r.to) &&
    (!r.from || r.from <= value.as_of) &&
    (!r.to || r.to <= value.as_of) &&
    day(r.benchmark_from) &&
    day(r.benchmark_to) &&
    (!r.benchmark_from || r.benchmark_from <= r.from) &&
    (!r.benchmark_to || r.benchmark_to <= r.to) &&
    metric(r.money_weighted) &&
    metric(r.time_weighted) &&
    metric(r.benchmark, true) &&
    (r.benchmark.status === "unavailable" ||
      (!!r.benchmark_from && !!r.benchmark_to));
  if (
    !value ||
    value.account_id !== account.id ||
    value.currency !== account.currency ||
    !validDay(value.as_of) ||
    !/^[a-f0-9]{64}$/.test(value.revision) ||
    !definition ||
    value.benchmark_code !== code ||
    value.benchmark_name !== definition.name ||
    value.benchmark_currency !== definition.currency ||
    value.benchmark_source !== definition.source ||
    !["", "benchmark_unavailable", "benchmark_timeout"].includes(
      value.benchmark_error,
    ) ||
    !row(value.annualized) ||
    !row(value.since) ||
    value.annualized.year !== 0 ||
    value.since.year !== 0 ||
    value.annualized.from !== value.since.from ||
    value.annualized.to !== value.since.to ||
    !Array.isArray(value.years) ||
    value.years.length > 100 ||
    value.years.some(
      (item, i) =>
        !row(item) ||
        item.year === 0 ||
        item.year > Number(value.as_of.slice(0, 4)) ||
        (i > 0 && item.year !== value.years[i - 1]!.year + 1) ||
        (item.from !== "" && item.from > `${item.year}-12-31`) ||
        (item.to !== "" && item.to > `${item.year}-12-31`) ||
        (item.money_weighted.value !== null &&
          (!item.to || Number(item.to.slice(0, 4)) !== item.year)),
    )
  )
    throw new Error("年度收益响应校验失败，请刷新重试");
  return value;
}

export function yearDescription(row: AnnualReturnRow): string {
  if (!row.to || !row.from || row.to <= row.from) return "暂无有效区间";
  const year = String(row.year);
  const first = row.from.startsWith(year) ? row.from.slice(5) : "年初";
  const last = row.to === `${year}-12-31` ? "年末" : row.to.slice(5);
  return first === "年初" && last === "年末" ? "全年" : `${first} 至 ${last}`;
}

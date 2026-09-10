import type { BasisPoint } from "./ledgerChart";

export interface ReturnMetric {
  value: string | null;
  percentage: string | null;
  status: "available" | "reference" | "unavailable";
  reason: string;
}
export interface LedgerReturns {
  revision: string;
  requested_from: string;
  requested_to: string;
  start_mode: "baseline" | "custom";
  effective_from: string;
  effective_to: string;
  days: number;
  period_days: number;
  opening: BasisPoint | null;
  closing: BasisPoint | null;
  net_flow: string;
  denominator: string | null;
  profit: ReturnMetric;
  modified_dietz: ReturnMetric;
  xirr: ReturnMetric;
  twr: ReturnMetric;
  twr_annualized: ReturnMetric;
  curve: ReturnPoint[];
  warnings: string[];
  flows: {
    date: string;
    record_id: string;
    version: string;
    flow: string;
    weight_days: number;
    period_days: number;
  }[];
  investor_flows: { date: string; amount: string }[];
}

export interface ReturnPoint {
  date: string;
  record_id: string;
  baseline: boolean;
  profit: ReturnMetric;
  modified_dietz: ReturnMetric;
  twr: ReturnMetric;
}

export const returnReasons: Record<string, string> = {
  missing_opening: "缺少期初资产，不推断初始财富",
  missing_closing: "缺少期末资产",
  no_interval: "没有正日数的计算区间",
  stale_endpoint: "端点估值受后续持仓修改影响，需要核实或修正",
  untracked_endpoint: "端点历史估值未追踪版本，不能确认有效性",
  closing_before_flow: "最后估值之后仍有资金流，缺少覆盖这些资金流的期末资产",
  nonpositive_denominator: "加权资金分母小于或等于零",
  indeterminate_all_zero: "同日抵消后全部为零，年化没有信息量",
  no_solution: "净额现金流没有正负两种符号，XIRR 无解",
  possible_multiple_roots: "现金流多次变号，可能多解；未证明唯一，不选择任意根",
  out_of_solver_range: "超出数值求解范围，或边界精度不足，暂无法给出可靠年化",
  not_converged: "求解未达到残差与精度要求",
  precision_unresolved: "数值不确定性跨越舍入边界，不能确认展示精度",
  missing_flow_boundary: "外部资金流边界缺少总资产，不能计算真实 TWR",
  zero_twr_base: "资金边界资产为零，不能作为下一段 TWR 分母",
  negative_twr_factor: "扣除当日资金流后资产为负，TWR 因子无效",
  twr_product_limit: "精确复利乘积超过计算或展示范围，不用近似值替代",
};
export const returnWarnings: Record<string, string> = {
  carried_assets_unchanged:
    "仅供参考：端点或必需资金边界沿用较早资产原额，未增加资金流。入金但未更新总资产时可能显示亏损，不代表已核实的市场损失。",
  sampled_valuation_not_daily_close:
    "参与计算的持仓估值是已保存的请求时参考估值，不保证当天收盘价；声明日期不是所有报价或汇率的实际日期。完整采样来源见估值历史。",
  short_period_extrapolation:
    "短区间年化外推风险：不足一年仍展示年化，短期结果可能被显著放大，不是未来收益预测。未采用参考产品的半年隐藏门槛。",
};

// Exact string -> integer rounding. Derived totals may exceed Money/int64.
export function returnPercent(
  value: string | null,
  percentage?: string | null,
): string {
  if (value === null) return "不可用";
  // Server rates carry a display rounded from the unrounded calculation, avoiding
  // a second rounding of the 12-decimal value near a half-percent-cent boundary.
  if (percentage != null) return `${percentage}%`;
  const negative = value.startsWith("-");
  const [whole, fraction = ""] = value.replace(/^-/, "").split(".");
  const scale = 10n ** BigInt(fraction.length);
  const raw = BigInt(whole! + fraction) * 10000n;
  let rounded = raw / scale;
  if ((raw % scale) * 2n >= scale) rounded++;
  return `${negative && rounded !== 0n ? "-" : ""}${rounded / 100n}.${String(rounded % 100n).padStart(2, "0")}%`;
}

export function validReturns(
  r: LedgerReturns,
  revision: string,
  from: string,
  to: string,
): boolean {
  const number = (v: unknown) =>
    typeof v === "string" && /^-?\d{1,100}(\.\d{1,12})?$/.test(v);
  const date = (v: unknown) =>
    typeof v === "string" &&
    (v === "" ||
      (/^\d{4}-\d{2}-\d{2}$/.test(v) &&
        !v.startsWith("0000-") &&
        Number.isFinite(Date.parse(`${v}T00:00:00Z`)) &&
        new Date(`${v}T00:00:00Z`).toISOString().slice(0, 10) === v));
  const same = (a: ReturnMetric, b: ReturnMetric) =>
    a &&
    b &&
    a.value === b.value &&
    a.percentage === b.percentage &&
    a.status === b.status &&
    a.reason === b.reason;
  const metric = (m: ReturnMetric, rate = false) =>
    m &&
    ["available", "reference", "unavailable"].includes(m.status) &&
    (m.status === "unavailable"
      ? m.value === null &&
        m.percentage === null &&
        Object.hasOwn(returnReasons, m.reason)
      : number(m.value) &&
        (rate ? /^-?\d{1,100}\.\d{12}$/ : /^-?\d{1,100}\.\d{2}$/).test(
          m.value!,
        ) &&
        m.reason === "" &&
        (rate
          ? typeof m.percentage === "string" &&
            /^-?\d{1,100}\.\d{2}$/.test(m.percentage)
          : m.percentage === null));
  return !!(
    r &&
    r.revision === revision &&
    r.requested_from === from &&
    r.requested_to === to &&
    r.start_mode === (from ? "custom" : "baseline") &&
    date(r.effective_from) &&
    date(r.effective_to) &&
    Number.isInteger(r.days) &&
    Math.abs(r.days) <= 3652059 &&
    Number.isInteger(r.period_days) &&
    r.period_days >= 0 &&
    r.period_days <= 3652060 &&
    (r.opening?.assets != null && r.closing?.assets != null
      ? !!r.effective_from &&
        !!r.effective_to &&
        r.period_days === (r.days >= 0 ? r.days + 1 : 0)
      : r.period_days === 0) &&
    number(r.net_flow) &&
    (r.denominator === null ||
      (typeof r.denominator === "string" &&
        /^-?\d{1,110}(\/\d{1,110})?$/.test(r.denominator))) &&
    [r.opening, r.closing].every(
      (p) =>
        p === null ||
        (p &&
          date(p.date) &&
          date(p.source_date) &&
          typeof p.source_id === "string" &&
          typeof p.source_version === "string" &&
          (p.assets === null || number(p.assets))),
    ) &&
    metric(r.profit) &&
    metric(r.modified_dietz, true) &&
    metric(r.xirr, true) &&
    metric(r.twr, true) &&
    metric(r.twr_annualized, true) &&
    ((r.profit.status === "unavailable" &&
      r.modified_dietz.status === "unavailable") ||
      r.period_days === r.days + 1) &&
    Array.isArray(r.curve) &&
    r.curve.length <= 10001 &&
    (!(r.opening?.assets != null && r.effective_from) || r.curve.length > 0) &&
    r.curve.every(
      (p, i) =>
        p &&
        date(p.date) &&
        p.date !== "" &&
        p.date >= r.effective_from &&
        (p.baseline || p.date <= r.effective_to) &&
        (i === 0
          ? p.baseline && p.date === r.effective_from
          : !p.baseline && p.date > r.curve[i - 1]!.date) &&
        typeof p.record_id === "string" &&
        typeof p.baseline === "boolean" &&
        metric(p.profit) &&
        metric(p.modified_dietz, true) &&
        metric(p.twr, true),
    ) &&
    (r.curve.length <= 1 ||
      (r.curve.at(-1)!.date === r.effective_to &&
        same(r.curve.at(-1)!.profit, r.profit) &&
        same(r.curve.at(-1)!.modified_dietz, r.modified_dietz) &&
        same(r.curve.at(-1)!.twr, r.twr))) &&
    Array.isArray(r.warnings) &&
    r.warnings.length <= 3 &&
    r.warnings.every((w) => Object.hasOwn(returnWarnings, w)) &&
    Array.isArray(r.flows) &&
    r.flows.length <= 10000 &&
    r.flows.every(
      (f) =>
        f &&
        date(f.date) &&
        f.date > r.effective_from &&
        f.date <= r.effective_to &&
        number(f.flow) &&
        typeof f.record_id === "string" &&
        typeof f.version === "string" &&
        Number.isInteger(f.weight_days) &&
        f.weight_days >= 0 &&
        f.weight_days <= r.days &&
        f.period_days === r.period_days,
    ) &&
    Array.isArray(r.investor_flows) &&
    r.investor_flows.length <= 10002 &&
    r.investor_flows.every((f) => f && date(f.date) && number(f.amount))
  );
}

export interface BenchmarkItem {
  date: string;
  close: string;
  return: string;
}
export interface Benchmark {
  code: string;
  name: string;
  currency: string;
  source: string;
  from: string;
  to: string;
  items: BenchmarkItem[];
}

export type BenchmarkCode = "H00300" | "H00922" | "usINX";

export interface BenchmarkDefinition {
  code: BenchmarkCode;
  name: string;
  currency: "CNY" | "USD";
  source: string;
}

export const benchmarkDefinitions: BenchmarkDefinition[] = [
  {
    code: "H00300",
    name: "沪深300全收益",
    currency: "CNY",
    source: "中证指数",
  },
  {
    code: "H00922",
    name: "中证红利全收益",
    currency: "CNY",
    source: "中证指数",
  },
  { code: "usINX", name: "标普500", currency: "USD", source: "腾讯" },
];

// Presentation is shared with the parent toggles so the dash pattern is visible
// on the control as well as on the curve; never color-only.
export interface BenchmarkEncoding {
  color: string;
  dash: number[];
}
export const accountEncoding: BenchmarkEncoding = {
  color: "#1f6f5c",
  dash: [],
};
export const benchmarkEncodings: Record<BenchmarkCode, BenchmarkEncoding> = {
  H00300: { color: "#2f6fb0", dash: [6, 3] },
  H00922: { color: "#a8721f", dash: [1.5, 3] },
  usINX: { color: "#6f5b8f", dash: [8, 3, 2, 3] },
};
export const benchmarkDash = (code: string) =>
  (benchmarkEncodings[code as BenchmarkCode]?.dash ?? []).join(" ");

export function benchmarkDefinition(
  code: string,
): BenchmarkDefinition | undefined {
  return benchmarkDefinitions.find((definition) => definition.code === code);
}

const DATE = /^\d{4}-\d{2}-\d{2}$/;
const CLOSE = /^-?\d+(?:\.\d+)?$/;
const RETURN = /^-?\d+\.\d{8}$/;

function validDate(value: unknown): value is string {
  return (
    typeof value === "string" &&
    DATE.test(value) &&
    !value.startsWith("0000-") &&
    Number.isFinite(Date.parse(`${value}T00:00:00Z`)) &&
    new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value
  );
}

// Exact string -> scaled integer. Never routes a financial value through Number.
function scaled(value: string, digits: number): bigint | null {
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(value);
  if (!match) return null;
  const fraction = (match[3] ?? "").slice(0, digits).padEnd(digits, "0");
  const magnitude = BigInt(match[2]! + fraction);
  return match[1] ? -magnitude : magnitude;
}

// Same contract as the Go provider: (close-base)/base at 8dp, half away from zero.
function cumulativeReturn(close: bigint, base: bigint): bigint {
  const delta = (close - base) * 100000000n;
  let quotient = delta / base;
  const remainder = delta % base;
  const twice = (remainder < 0n ? -remainder : remainder) * 2n;
  if (twice >= base) quotient += delta > 0n ? 1n : delta < 0n ? -1n : 0n;
  return quotient;
}

export function validBenchmark(
  value: unknown,
  code: string,
  from: string,
  to: string,
): value is Benchmark {
  const definition = benchmarkDefinition(code);
  if (!definition) return false;
  if (typeof value !== "object" || value === null || Array.isArray(value))
    return false;
  const benchmark = value as Record<string, unknown>;
  if (
    benchmark.code !== code ||
    benchmark.name !== definition.name ||
    benchmark.currency !== definition.currency ||
    benchmark.source !== definition.source ||
    !validDate(benchmark.from) ||
    !validDate(benchmark.to) ||
    benchmark.from > benchmark.to ||
    benchmark.from < from ||
    benchmark.to > to ||
    !Array.isArray(benchmark.items) ||
    benchmark.items.length > 8000
  )
    return false;
  let previous = "";
  let base: bigint | undefined;
  for (const raw of benchmark.items) {
    if (typeof raw !== "object" || raw === null || Array.isArray(raw))
      return false;
    const item = raw as Record<string, unknown>;
    if (
      !validDate(item.date) ||
      item.date <= previous ||
      typeof item.close !== "string" ||
      item.close.length > 32 ||
      !CLOSE.test(item.close) ||
      typeof item.return !== "string" ||
      !RETURN.test(item.return)
    )
      return false;
    const close = scaled(item.close, 8);
    const declared = scaled(item.return, 8);
    if (close === null || close <= 0n || declared === null) return false;
    base ??= close;
    const difference = declared - cumulativeReturn(close, base);
    if (difference > 1n || difference < -1n) return false;
    previous = item.date;
  }
  return true;
}

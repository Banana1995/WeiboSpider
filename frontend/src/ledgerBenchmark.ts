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
  if (typeof value !== "object" || value === null || Array.isArray(value))
    return false;
  const benchmark = value as Record<string, unknown>;
  if (
    benchmark.code !== code ||
    typeof benchmark.name !== "string" ||
    !benchmark.name.trim() ||
    benchmark.currency !== "CNY" ||
    typeof benchmark.source !== "string" ||
    !benchmark.source.trim() ||
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

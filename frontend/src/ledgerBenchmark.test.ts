import { expect, it } from "vitest";
import { validBenchmark, type Benchmark } from "./ledgerBenchmark";

const from = "2026-01-01";
const to = "2026-01-31";

function benchmark(overrides: Partial<Benchmark> = {}): Benchmark {
  return {
    code: "H00300",
    name: "沪深300全收益",
    currency: "CNY",
    source: "中证指数",
    from: "2026-01-02",
    to: "2026-01-06",
    items: [
      { date: "2026-01-02", close: "100.00", return: "0.00000000" },
      { date: "2026-01-05", close: "110.00", return: "0.10000000" },
      { date: "2026-01-06", close: "90.00", return: "-0.10000000" },
    ],
    ...overrides,
  };
}

it("accepts the exact Go contract including half-away rounding", () => {
  expect(validBenchmark(benchmark(), "H00300", from, to)).toBe(true);
  const half = benchmark({
    items: [
      { date: "2026-01-02", close: "200000000", return: "0.00000000" },
      { date: "2026-01-05", close: "200000001", return: "0.00000001" },
      { date: "2026-01-06", close: "199999999", return: "-0.00000001" },
    ],
  });
  expect(validBenchmark(half, "H00300", from, to)).toBe(true);
  const third = benchmark({
    items: [
      { date: "2026-01-02", close: "3", return: "0.00000000" },
      { date: "2026-01-05", close: "4", return: "0.33333333" },
    ],
  });
  expect(validBenchmark(third, "H00300", from, to)).toBe(true);
  // One unit of rounding slack is the mirror of the backend's half-away rule.
  expect(
    validBenchmark(
      benchmark({
        items: [
          { date: "2026-01-02", close: "3", return: "0.00000000" },
          { date: "2026-01-05", close: "4", return: "0.33333334" },
        ],
      }),
      "H00300",
      from,
      to,
    ),
  ).toBe(true);
});

it("rejects identity, currency, envelope and range drift", () => {
  for (const value of [
    benchmark({ code: "H00301" }),
    benchmark({ name: " " }),
    benchmark({ currency: "USD" }),
    benchmark({ source: "" }),
    benchmark({ from: "2025-12-31" }),
    benchmark({ to: "2026-02-01" }),
    benchmark({ from: "2026-02-01", to: "2026-01-01" }),
    { ...benchmark(), items: null },
    { ...benchmark(), items: "x" },
    null,
    [],
    "H00300",
  ])
    expect(validBenchmark(value, "H00300", from, to)).toBe(false);
  expect(validBenchmark(benchmark(), "H00301", from, to)).toBe(false);
});

it("accepts each whitelisted definition with its own name, currency and source", () => {
  for (const definition of [
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
  ]) {
    expect(
      validBenchmark(
        benchmark({
          code: definition.code,
          name: definition.name,
          currency: definition.currency,
          source: definition.source,
        }),
        definition.code,
        from,
        to,
      ),
    ).toBe(true);
  }
  // Presentation drift or an unknown code is rejected even with valid items.
  for (const value of [
    benchmark({ code: "H00922", name: "沪深300全收益" }),
    benchmark({
      code: "usINX",
      name: "标普500",
      currency: "CNY",
      source: "腾讯",
    }),
    benchmark({
      code: "usINX",
      name: "标普500",
      currency: "USD",
      source: "中证指数",
    }),
  ])
    expect(validBenchmark(value, value.code, from, to)).toBe(false);
  expect(validBenchmark(benchmark(), "SPX", from, to)).toBe(false);
});

it("rejects malformed items, dates, closes and inconsistent returns", () => {
  const item = (overrides: Record<string, unknown>) => ({
    ...benchmark().items[1]!,
    ...overrides,
  });
  const withItems = (items: unknown[]) => benchmark({ items: items as never });
  for (const value of [
    withItems([item({ date: "2026-02-30" })]),
    withItems([item({ date: "2026-01-05" }), item({ date: "2026-01-05" })]),
    withItems([benchmark().items[1]!, benchmark().items[0]!]),
    withItems([item({ close: "0" })]),
    withItems([item({ close: "-1" })]),
    withItems([item({ close: "1e3" })]),
    withItems([item({ close: "abc" })]),
    withItems([item({ close: "1.".repeat(20) })]),
    withItems([item({ return: "0.1000000" })]),
    withItems([item({ return: "0.20000000" })]),
    withItems([item({ return: "0.33333335" })]),
    withItems([{ date: "2026-01-05", close: "110.00" }]),
  ])
    expect(validBenchmark(value, "H00300", from, to)).toBe(false);
  expect(
    validBenchmark(
      withItems(new Array(8001).fill(item({}))),
      "H00300",
      from,
      to,
    ),
  ).toBe(false);
});

import { describe, expect, it, vi } from "vitest";
import { LedgerReadCache } from "./ledgerReadCache";
import {
  accountBasisRequest,
  peekBenchmark,
  readBenchmark,
} from "./ledgerCachedRequests";
import type { Benchmark } from "./ledgerBenchmark";

const signal = () => new AbortController().signal;
const pending = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

describe("page-lifetime read cache", () => {
  it("shares a pending read without letting one consumer cancel another", async () => {
    const cache = new LedgerReadCache();
    const value = pending<{ revision: string }>();
    let upstream!: AbortSignal;
    const reader = vi.fn((signal: AbortSignal) => {
      upstream = signal;
      return value.promise;
    });
    const first = new AbortController();
    const a = cache.fetch("a", reader, first.signal);
    const b = cache.fetch("a", reader, signal());
    await Promise.resolve();
    const rejected = expect(a).rejects.toMatchObject({ name: "AbortError" });
    first.abort();
    await rejected;
    expect(upstream.aborted).toBe(false);
    value.resolve({ revision: "1" });
    await expect(b).resolves.toEqual({ revision: "1" });
    expect(reader).toHaveBeenCalledOnce();
    expect(cache.peek("a")).toEqual({ revision: "1" });
  });

  it("fences an old in-flight response after invalidation or last-consumer cancellation", async () => {
    const cache = new LedgerReadCache();
    const old = pending<number>();
    const controller = new AbortController();
    const request = cache.fetch("a", () => old.promise, controller.signal);
    await Promise.resolve();
    cache.clear();
    await cache.fetch("a", async () => 2, signal());
    old.resolve(1);
    await expect(request).rejects.toMatchObject({ name: "AbortError" });
    expect(cache.peek("a")).toBe(2);
    const late = pending<number>();
    const next = cache.fetch("b", () => late.promise, controller.signal);
    await Promise.resolve();
    const rejected = expect(next).rejects.toMatchObject({ name: "AbortError" });
    controller.abort();
    await rejected;
    late.resolve(3);
    await Promise.resolve();
    expect(cache.peek("b")).toBeUndefined();
  });

  it("bounds memory with TTL, LRU and a per-cache byte limit", async () => {
    let now = 0;
    const cache = new LedgerReadCache(100, 2, 20, () => now);
    await cache.fetch("a", async () => "aaa", signal());
    await cache.fetch("b", async () => "bbb", signal());
    expect(cache.peek("a")).toBe("aaa");
    await cache.fetch("c", async () => "ccc", signal());
    expect(cache.peek("b")).toBeUndefined();
    await cache.fetch("huge", async () => "x".repeat(30), signal());
    expect(cache.peek("huge")).toBeUndefined();
    now = 100;
    expect(cache.peek("a")).toBeUndefined();
    expect(cache.peek("c")).toBeUndefined();
  });

  it("revalidates by default, and never caches failed or unvalidated reads", async () => {
    const cache = new LedgerReadCache();
    const reader = vi
      .fn()
      .mockResolvedValueOnce(1)
      .mockRejectedValueOnce(new Error("invalid response"));
    await cache.fetch("a", reader, signal());
    await expect(cache.fetch("a", reader, signal(), false)).resolves.toBe(1);
    await expect(cache.fetch("a", reader, signal())).rejects.toThrow(
      "invalid response",
    );
    expect(cache.peek("a")).toBeUndefined();
    expect(reader).toHaveBeenCalledTimes(2);
  });
});

it("shares covered benchmark windows across accounts and rebases a smaller interval exactly", async () => {
  const cache = new LedgerReadCache();
  const data: Benchmark = {
    code: "H00300",
    name: "沪深300全收益",
    currency: "CNY",
    source: "中证指数",
    from: "2025-01-02",
    to: "2025-01-10",
    items: [
      { date: "2025-01-02", close: "2", return: "0.00000000" },
      { date: "2025-01-06", close: "3", return: "0.50000000" },
      { date: "2025-01-10", close: "4", return: "1.00000000" },
    ],
  };
  await cache.fetch("H00300|2025-01-01|2025-01-12", async () => data, signal());
  const subset = await readBenchmark(
    cache,
    "H00300",
    "2025-01-05",
    "2025-01-12",
    signal(),
  );
  expect(subset.items.map((item) => item.return)).toEqual([
    "0.00000000",
    "0.33333333",
  ]);
  expect(data.items).toHaveLength(3);
  expect(data.items[1]!.return).toBe("0.50000000");
  expect(
    peekBenchmark(cache, "usINX", "2025-01-05", "2025-01-12"),
  ).toBeUndefined();
  expect(
    peekBenchmark(cache, "H00300", "2024-12-31", "2025-01-12"),
  ).toBeUndefined();
  expect(
    peekBenchmark(cache, "H00300", "2025-01-01", "2025-01-13"),
  ).toBeUndefined();
});

it("expands overlapping benchmark coverage once and keeps subsequent subsets local", async () => {
  const cache = new LedgerReadCache();
  const value: Benchmark = {
    code: "H00300",
    name: "沪深300全收益",
    currency: "CNY",
    source: "中证指数",
    from: "2025-01-02",
    to: "2025-01-10",
    items: [
      { date: "2025-01-02", close: "2", return: "0.00000000" },
      { date: "2025-01-06", close: "3", return: "0.50000000" },
      { date: "2025-01-10", close: "4", return: "1.00000000" },
    ],
  };
  await cache.fetch(
    "H00300|2025-01-01|2025-01-06",
    async () => ({
      ...value,
      to: "2025-01-06",
      items: value.items.slice(0, 2),
    }),
    signal(),
  );
  const fetch = vi.fn(
    async (_input: string, _options?: RequestInit) =>
      new Response(JSON.stringify(value)),
  );
  vi.stubGlobal("fetch", fetch);
  try {
    const data = await readBenchmark(
      cache,
      "H00300",
      "2025-01-05",
      "2025-01-12",
      signal(),
    );
    expect(fetch.mock.calls[0]?.[0]).toBe(
      "/api/platform/ledger/benchmark?code=H00300&from=2025-01-01&to=2025-01-12",
    );
    expect(data.items.map((item) => item.return)).toEqual([
      "0.00000000",
      "0.33333333",
    ]);
    await readBenchmark(cache, "H00300", "2025-01-05", "2025-01-09", signal());
    expect(fetch).toHaveBeenCalledOnce();
  } finally {
    vi.unstubAllGlobals();
  }
});

it("isolates cached account snapshots by account, currency and date interval", () => {
  const key = (id: string, currency: "CNY" | "USD", from: string, to: string) =>
    accountBasisRequest({ id, currency }, from, to).key;
  const keys = [
    key("a", "CNY", "", "2026-09-24"),
    key("b", "CNY", "", "2026-09-24"),
    key("a", "USD", "", "2026-09-24"),
    key("a", "CNY", "2026-01-01", "2026-09-24"),
    key("a", "CNY", "", "2026-09-25"),
  ];
  expect(new Set(keys).size).toBe(keys.length);
});

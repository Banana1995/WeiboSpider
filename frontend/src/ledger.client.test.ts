import { afterEach, expect, it, vi } from "vitest";
import {
  decimal,
  LedgerError,
  newID,
  PendingWrite,
  query,
  request,
  type AccountInput,
} from "./ledger";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });

it("uses secure random bytes when randomUUID is unavailable on HTTP origins", () => {
  const getRandomValues = vi.fn((bytes: Uint8Array) => {
    bytes.set(Array.from({ length: 16 }, (_, index) => index));
    return bytes;
  });
  vi.stubGlobal("crypto", { getRandomValues });
  expect(newID()).toBe("000102030405060708090a0b0c0d0e0f");
  expect(getRandomValues).toHaveBeenCalledOnce();
  expect(getRandomValues.mock.calls[0]![0]).toBeInstanceOf(Uint8Array);
});

it("preserves decimal precision and omits empty query values", () => {
  expect(decimal("9007199254740991.01", 2)).toBe(true);
  expect(decimal("1.000001", 6)).toBe(true);
  expect(decimal("0.00000001", 8)).toBe(true);
  for (const value of [
    "1e3",
    "NaN",
    "0.001",
    "92233720368547758.08",
    " 1",
    "1.",
  ])
    expect(decimal(value, 2)).toBe(false);
  expect(
    query({
      from: "2026-01-01",
      to: "",
      cursor: "2026-01-02:9",
      status: "voided",
    }),
  ).toBe("?from=2026-01-01&cursor=2026-01-02%3A9&status=voided");
});

it("preserves immutable write bytes, IDs, FX and key across network and server timeout failures", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new TypeError("lost response"))
    .mockResolvedValueOnce(json({ code: "request_timeout" }, 504))
    .mockResolvedValueOnce(json({ version: "1" }, 201));
  vi.stubGlobal("fetch", fetcher);
  const payload = {
    operation: {
      id: "op",
      amount: "9007199254740991.01",
      fx: {
        rate: "7.00000001",
        date: "2026-01-02",
        source: "confirmed",
        fetched_at: "2026-01-02T12:00:00Z",
      },
    },
    reason: "首次录入",
  };
  const write = new PendingWrite("/operations", "POST", payload);
  await expect(write.run()).rejects.toThrow("network_error");
  payload.operation.amount = "2.00";
  await expect(write.run()).rejects.toThrow("request_timeout");
  expect(write.uncertain).toBe(true);
  await expect(write.run()).resolves.toEqual({ version: "1" });
  const calls = fetcher.mock.calls.map((call) => call[1]);
  expect(new Set(calls.map((call) => call.body)).size).toBe(1);
  expect(
    new Set(calls.map((call) => call.headers["Idempotency-Key"])).size,
  ).toBe(1);
  expect(JSON.parse(calls[2].body).operation.amount).toBe(
    "9007199254740991.01",
  );
});

it("aborts a timed out transport then retries with the same key", async () => {
  vi.useFakeTimers();
  const fetcher = vi
    .fn()
    .mockImplementationOnce(
      (_url, options) =>
        new Promise((_resolve, reject) =>
          options.signal.addEventListener("abort", () =>
            reject(new Error("aborted")),
          ),
        ),
    )
    .mockResolvedValueOnce(json({ version: "1" }));
  vi.stubGlobal("fetch", fetcher);
  const write = new PendingWrite("/operations", "POST", {
    operation: { id: "stable" },
  });
  const result = expect(write.run()).rejects.toThrow("request_timeout");
  await vi.advanceTimersByTimeAsync(15000);
  await result;
  await write.run();
  expect(fetcher.mock.calls[0]![1].headers).toEqual(
    fetcher.mock.calls[1]![1].headers,
  );
});

const account: AccountInput = {
  id: "stable-account",
  name: "合成账户",
  currency: "CNY",
  opening_date: "2026-01-01",
  opening_cash: "1000",
  positions: [
    { instrument_id: "stock", quantity: "1", cost: null, diluted_basis: "0" },
  ],
};

it("reconciles an ambiguous account by fixed ID before any retry POST", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("lost response"))
    .mockResolvedValueOnce(
      json({
        ...account,
        opening_cash: "1000.00",
        version: "1",
        cash: "999.00",
      }),
    );
  vi.stubGlobal("fetch", fetcher);
  const write = new PendingWrite("/accounts", "POST", account, account);
  await expect(write.run()).rejects.toThrow();
  await expect(write.run()).resolves.toMatchObject({ id: account.id });
  expect(fetcher.mock.calls.map((c) => [c[0], c[1].method ?? "GET"])).toEqual([
    ["/api/platform/ledger/accounts", "POST"],
    ["/api/platform/ledger/accounts/stable-account", "GET"],
  ]);
});

it("only retries account POST after GET confirms absence and never regenerates IDs", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("network"))
    .mockResolvedValueOnce(json({ code: "not_found" }, 404))
    .mockResolvedValueOnce(json(account, 201));
  vi.stubGlobal("fetch", fetcher);
  const write = new PendingWrite("/accounts", "POST", account, account);
  await expect(write.run()).rejects.toThrow();
  await write.run();
  expect(fetcher.mock.calls[0]![1].body).toBe(fetcher.mock.calls[2]![1].body);
  expect(fetcher.mock.calls[1]![0]).toContain(account.id);
});

it("keeps an ambiguous account locked on reconciliation conflict or failure", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("network"))
    .mockResolvedValueOnce(json({ ...account, name: "另一个账户" }))
    .mockResolvedValueOnce(json({ code: "internal_error" }, 500));
  vi.stubGlobal("fetch", fetcher);
  const write = new PendingWrite("/accounts", "POST", account, account);
  await expect(write.run()).rejects.toThrow();
  await expect(write.run()).rejects.toThrow("conflict");
  expect(write.uncertain).toBe(true);
  await expect(write.run()).rejects.toThrow("internal_error");
  expect(fetcher.mock.calls.filter((c) => c[1].method === "POST")).toHaveLength(
    1,
  );
});

it("surfaces Chinese errors with backend codes and historical operation/date", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      json(
        {
          code: "insufficient_cash",
          operation_id: "later-op",
          date: "2026-01-05",
        },
        422,
      ),
    ),
  );
  await expect(request("/operations")).rejects.toThrow(
    "历史重放后现金不足 [insufficient_cash]；操作 later-op；日期 2026-01-05",
  );
  expect(new LedgerError("version_conflict", 409).uncertain).toBe(false);
});

it("retries instrument registration with the exact identity and body", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("network"))
    .mockResolvedValueOnce(json({ id: "stock" }));
  vi.stubGlobal("fetch", fetcher);
  const write = new PendingWrite("/instruments", "POST", {
    id: "stock",
    code: "001",
    currency: "CNY",
  });
  await expect(write.run()).rejects.toThrow();
  await write.run();
  expect(fetcher.mock.calls[0]![1].body).toBe(fetcher.mock.calls[1]![1].body);
});

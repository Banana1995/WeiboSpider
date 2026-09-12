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

it("preserves immutable write bytes, identity, quantities and key across network and server timeout failures", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new TypeError("lost response"))
    .mockResolvedValueOnce(json({ code: "request_timeout" }, 504))
    .mockResolvedValueOnce(json({ version: "1" }, 201));
  vi.stubGlobal("fetch", fetcher);
  const payload = {
    expected_version: "1",
    cash: "9007199254740991.01",
    positions: [{ instrument_id: "security", quantity: "1.000001" }],
    securities: [
      {
        id: "security",
        market: "SH",
        code: "600000",
        name: "Confirmed",
        currency: "CNY",
      },
    ],
  };
  const write = new PendingWrite(
    "/accounts/a/current-holdings",
    "PUT",
    payload,
  );
  await expect(write.run()).rejects.toThrow("network_error");
  payload.cash = "2.00";
  payload.securities[0]!.name = "Changed";
  payload.positions[0]!.quantity = "9";
  await expect(write.run()).rejects.toThrow("request_timeout");
  expect(write.uncertain).toBe(true);
  await expect(write.run()).resolves.toEqual({ version: "1" });
  const calls = fetcher.mock.calls.map((call) => call[1]);
  expect(new Set(calls.map((call) => call.body)).size).toBe(1);
  expect(
    new Set(calls.map((call) => call.headers["Idempotency-Key"])).size,
  ).toBe(1);
  expect(JSON.parse(calls[2].body).cash).toBe("9007199254740991.01");
  expect(JSON.parse(calls[2].body).securities[0].name).toBe("Confirmed");
  expect(JSON.parse(calls[2].body).positions[0].quantity).toBe("1.000001");
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
  const write = new PendingWrite("/accounts/stable/current-holdings", "PUT", {
    expected_version: "0",
    cash: "0.00",
    positions: [],
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
};

it("retries ambiguous account creation with its original idempotency key, never inferring success from a GET", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("lost response"))
    .mockResolvedValueOnce(
      json({
        ...account,
        opening_cash: null,
        version: "1",
      }),
    );
  vi.stubGlobal("fetch", fetcher);
  const write = new PendingWrite("/accounts", "POST", account);
  await expect(write.run()).rejects.toThrow();
  await expect(write.run()).resolves.toMatchObject({ id: account.id });
  expect(fetcher.mock.calls.map((c) => [c[0], c[1].method ?? "GET"])).toEqual([
    ["/api/platform/ledger/accounts", "POST"],
    ["/api/platform/ledger/accounts", "POST"],
  ]);
  expect(fetcher.mock.calls[0]![1].headers).toEqual(
    fetcher.mock.calls[1]![1].headers,
  );
  expect(
    new Headers(fetcher.mock.calls[0]![1].headers).get("Idempotency-Key"),
  ).toBeTruthy();
});

it("freezes account identity and body across retries even when the caller changes its draft", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("network"))
    .mockResolvedValueOnce(json(account, 201));
  vi.stubGlobal("fetch", fetcher);
  const draft = { ...account };
  const write = new PendingWrite("/accounts", "POST", draft);
  await expect(write.run()).rejects.toThrow();
  draft.id = "changed";
  draft.name = "changed";
  await write.run();
  expect(fetcher.mock.calls[0]![1].body).toBe(fetcher.mock.calls[1]![1].body);
  expect(JSON.parse(fetcher.mock.calls[1]![1].body)).toEqual(account);
});

it("keeps an ambiguous account locked after conflicting or failed receipt retries", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error("network"))
    .mockResolvedValueOnce(json({ code: "idempotency_conflict" }, 409))
    .mockResolvedValueOnce(json({ code: "internal_error" }, 500));
  vi.stubGlobal("fetch", fetcher);
  const write = new PendingWrite("/accounts", "POST", account);
  await expect(write.run()).rejects.toThrow();
  await expect(write.run()).rejects.toThrow("conflict");
  expect(write.uncertain).toBe(true);
  await expect(write.run()).rejects.toThrow("internal_error");
  expect(fetcher.mock.calls.filter((c) => c[1].method === "POST")).toHaveLength(
    3,
  );
  expect(fetcher.mock.calls.every((c) => c[1].body === write.body)).toBe(true);
});

it("surfaces current backend codes without exposing arbitrary error-body or retired operation metadata", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      json(
        {
          code: "version_conflict",
          message: "private error body",
          operation_id: "private retired operation",
          date: "2026-01-05",
        },
        409,
      ),
    ),
  );
  await expect(request("/accounts/a/current-holdings")).rejects.toMatchObject({
    message: "记录已被修改，请重新读取详情后更正 [version_conflict]",
  });
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

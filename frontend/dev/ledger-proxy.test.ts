import { describe, expect, it, vi } from "vitest";
import type { IncomingMessage, ServerResponse } from "node:http";
import { ledgerBoundary, ledgerDevelopment } from "./ledger-proxy";

function invoke(enabled: boolean, overrides: Partial<IncomingMessage> = {}) {
  const res = { writeHead: vi.fn(), end: vi.fn() };
  const next = vi.fn();
  ledgerBoundary(enabled)(
    {
      url: "/api/platform/ledger/accounts",
      headers: { host: "127.0.0.1:5173" },
      socket: { remoteAddress: "127.0.0.1" },
      ...overrides,
    } as IncomingMessage,
    res as unknown as ServerResponse,
    next,
  );
  return { res, next };
}

describe("local ledger proxy", () => {
  it("is opt-in, without changing the public proxy", () => {
    expect(ledgerDevelopment({}).proxy).toEqual({});
    expect(invoke(false).res.writeHead).toHaveBeenCalledWith(
      404,
      expect.anything(),
    );
    expect(
      invoke(false, { url: "/api/platform/liquor/latest" }).next,
    ).toHaveBeenCalled();
  });
  it("accepts only loopback backend origins when enabled", () => {
    for (const target of [
      "http://example.com",
      "http://127.0.0.1/path",
      "https://localhost",
      "http://user:secret@localhost",
    ]) {
      expect(() =>
        ledgerDevelopment({
          LEDGER_DEV_PROXY: "true",
          BACKEND_PROXY_URL: target,
        }),
      ).toThrow(/loopback/);
    }
    const { proxy } = ledgerDevelopment({
      LEDGER_DEV_PROXY: "true",
      BACKEND_API_TOKEN: "synthetic-token",
    });
    const [pattern, options] = Object.entries(proxy)[0]!;
    expect(new RegExp(pattern).test("/api/platform/ledger/accounts")).toBe(
      true,
    );
    expect(new RegExp(pattern).test("/api/platform/ledger-private")).toBe(
      false,
    );
    expect(options.headers?.Authorization).toBe("Bearer synthetic-token");
    expect(options.changeOrigin).toBe(false);
  });
  it("accepts same-origin loopback requests", () => {
    expect(
      invoke(true, {
        headers: {
          host: "127.0.0.1:5173",
          origin: "http://127.0.0.1:5173",
          "sec-fetch-site": "same-origin",
        },
      }).next,
    ).toHaveBeenCalled();
  });
  it("rejects nonlocal socket peers even with spoofed forwarding headers", () => {
    const { res, next } = invoke(true, {
      socket: { remoteAddress: "192.0.2.1" } as IncomingMessage["socket"],
      headers: { host: "127.0.0.1:5173", "x-forwarded-for": "127.0.0.1" },
    });
    expect(next).not.toHaveBeenCalled();
    expect(res.end).toHaveBeenCalledWith(expect.stringContaining("local_only"));
  });
  it("rejects rebinding, foreign origins and cross-site requests", () => {
    for (const headers of [
      { host: "evil.example" },
      { host: "127.0.0.1:5173", origin: "http://evil.example" },
      { host: "127.0.0.1:5173", "sec-fetch-site": "cross-site" },
      { host: "127.0.0.1:5173", origin: "null" },
    ]) {
      const { res, next } = invoke(true, { headers });
      expect(next).not.toHaveBeenCalled();
      expect(res.writeHead).toHaveBeenCalledWith(403, expect.anything());
    }
  });
});

import { describe, expect, it, vi, afterEach } from "vitest";
import {
  calendarSeries,
  shiftDate,
  money,
  change,
  request,
  type Quote,
} from "./market";
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
describe("quote presentation", () => {
  it("uses cents and signed changes", () => {
    expect(money(179600)).toBe("1,796.00");
    expect(change(-200)).toBe("-2.00");
    expect(change(300)).toBe("+3.00");
    expect(change(0)).toBe("0.00");
  });
  it("uses inclusive UTC calendar ranges across leap days", () => {
    expect(shiftDate("2024-03-01", -1)).toBe("2024-02-29");
    expect(shiftDate("2026-09-05", -29)).toBe("2026-08-07");
  });
  it("orders descending API observations and preserves missing dates", () => {
    const rows = [
      { price_date: "2026-09-05", price_cents: 200 },
      { price_date: "2026-09-03", price_cents: 100 },
    ] as Quote[];
    expect(calendarSeries(rows, "2026-09-03", "2026-09-05")).toEqual([
      ["2026-09-03", 1],
      ["2026-09-04", null],
      ["2026-09-05", 2],
    ]);
    expect(rows[0]!.price_date).toBe("2026-09-05");
  });
  it("retains single points and empty periods", () => {
    expect(calendarSeries([], "2026-09-05", "2026-09-05")).toEqual([
      ["2026-09-05", null],
    ]);
  });
});
describe("API failures", () => {
  it("uses a GET and forwards cancellation", async () => {
    const source = new AbortController();
    vi.stubGlobal(
      "fetch",
      vi.fn(
        (_url, options) =>
          new Promise((_resolve, reject) => {
            options.signal.addEventListener("abort", () =>
              reject(new DOMException("Aborted", "AbortError")),
            );
          }),
      ),
    );
    const pending = request("latest", source.signal);
    const assertion = expect(pending).rejects.toMatchObject({
      name: "AbortError",
    });
    source.abort();
    await assertion;
    expect(vi.mocked(fetch).mock.calls[0]![1]).not.toHaveProperty("method");
  });
  it("times out a hanging request and clears its timer", async () => {
    vi.useFakeTimers();
    vi.stubGlobal(
      "fetch",
      vi.fn(
        (_url, options) =>
          new Promise((_resolve, reject) => {
            options.signal.addEventListener("abort", () =>
              reject(new DOMException("Aborted", "AbortError")),
            );
          }),
      ),
    );
    const assertion = expect(request("latest")).rejects.toThrow("请求超时");
    await vi.advanceTimersByTimeAsync(15000);
    await assertion;
    expect(vi.getTimerCount()).toBe(0);
  });
  it("rejects HTML fallback", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response("<html/>", { headers: { "content-type": "text/html" } }),
        ),
    );
    await expect(request("latest")).rejects.toThrow("JSON");
  });
  it("reports authorization failures", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("", { status: 401 })),
    );
    await expect(request("latest")).rejects.toThrow("访问被拒绝");
  });
});

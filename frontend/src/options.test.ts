import { afterEach, expect, it, vi } from "vitest";
import { requestID } from "./options";

afterEach(() => vi.unstubAllGlobals());

it("uses secure random bytes when randomUUID is unavailable on HTTP origins", () => {
  const getRandomValues = vi.fn((bytes: Uint8Array) => {
    bytes.fill(0xab);
    return bytes;
  });
  vi.stubGlobal("crypto", { getRandomValues });
  expect(requestID()).toBe("ab".repeat(16));
  expect(getRandomValues).toHaveBeenCalledOnce();
});

it("prefers the platform UUID when the page is a secure context", () => {
  const randomUUID = vi.fn(() => "11111111-2222-3333-4444-555555555555");
  vi.stubGlobal("crypto", { randomUUID });
  expect(requestID()).toBe("11111111-2222-3333-4444-555555555555");
  expect(randomUUID).toHaveBeenCalledOnce();
});

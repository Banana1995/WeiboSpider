import { expect, it } from "vitest";
import { applyTabOrder, loadTabOrder, saveTabOrder } from "./ledgerTabOrder";

const account = (id: string) => ({ id });

it("reorders known ids and keeps unknown ids in server order after them", () => {
  const items = ["a", "b", "c", "d"].map(account);
  expect(applyTabOrder(items, ["c", "a"]).map((i) => i.id)).toEqual([
    "c",
    "a",
    "b",
    "d",
  ]);
  expect(applyTabOrder(items, [])).toBe(items);
  expect(applyTabOrder(items, ["missing"]).map((i) => i.id)).toEqual([
    "a",
    "b",
    "c",
    "d",
  ]);
});

it("loads only a stored array of opaque ids", () => {
  const storage = (value: string | null) => ({ getItem: () => value });
  expect(loadTabOrder(storage('["a","b"]'))).toEqual(["a", "b"]);
  expect(loadTabOrder(storage(null))).toEqual([]);
  expect(loadTabOrder(storage("not json"))).toEqual([]);
  expect(loadTabOrder(storage('{"a":1}'))).toEqual([]);
  expect(loadTabOrder(storage("[1,2]"))).toEqual([]);
  expect(loadTabOrder(undefined)).toEqual([]);
});

it("saves the order and tolerates a blocked storage", () => {
  const written: string[] = [];
  saveTabOrder(["b", "a"], { setItem: (_key, value) => written.push(value) });
  expect(written).toEqual(['["b","a"]']);
  expect(() =>
    saveTabOrder(["a"], {
      setItem: () => {
        throw new Error("quota");
      },
    }),
  ).not.toThrow();
  expect(() => saveTabOrder(["a"], undefined)).not.toThrow();
});

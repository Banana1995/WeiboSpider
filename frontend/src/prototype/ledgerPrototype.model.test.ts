import { describe, expect, it } from "vitest";
import {
  activeRecords,
  analyze,
  cents,
  createAccounts,
  createRecord,
  DEMO_TODAY,
  importExample,
} from "./ledgerPrototype";

describe("local prototype calculations", () => {
  it("uses cents, rejects invalid amounts and retains unfilled assets", () => {
    expect(cents("12.30")).toBe(1230);
    expect(cents("0")).toBe(0);
    for (const value of ["-1", "1.001", "1e3", "NaN", "", "10000000001"])
      expect(cents(value)).toBeNull();
    expect(createAccounts()[2]!.records[1]!.assets).toBeNull();
  });
  it("excludes first-day flows and interprets same-day assets as post-flow", () => {
    const rows = [
      createRecord(1, "2026-01-01", "in", 10000, 10000, ""),
      createRecord(2, "2026-01-11", "in", 5000, 16000, ""),
    ];
    const result = analyze(rows, "2026-01-01", DEMO_TODAY, "personal");
    expect(result.profit).toBe(1000);
    expect(result.rate).toBeCloseTo(0.1);
    expect(analyze(rows, "2026-01-01", DEMO_TODAY, "manager").rate).toBeCloseTo(
      0.1,
    );
  });
  it("derives different coherent periods and return methods, including loss and empty cases", () => {
    const accounts = createAccounts();
    const all = analyze(
      accounts[0]!.records,
      "2025-01-01",
      DEMO_TODAY,
      "personal",
    );
    const year = analyze(
      accounts[0]!.records,
      "2026-01-01",
      DEMO_TODAY,
      "personal",
    );
    const manager = analyze(
      accounts[0]!.records,
      "2026-01-01",
      DEMO_TODAY,
      "manager",
    );
    expect(all.profit).toBe(3284000);
    expect(year.profit).toBe(2604000);
    expect(manager.rate).not.toBeCloseTo(year.rate!, 5);
    const loss = analyze(
      accounts[1]!.records,
      "2026-01-01",
      DEMO_TODAY,
      "personal",
    );
    expect(loss.profit).toBe(-80000);
    expect(loss.rate).toBeLessThan(0);
    const empty = analyze([], "2026-01-01", DEMO_TODAY, "personal");
    expect(empty.profit).toBeNull();
    expect(empty.rate).toBeNull();
    expect(empty.annual).toBeNull();
    const single = analyze(
      accounts[0]!.records,
      DEMO_TODAY,
      DEMO_TODAY,
      "personal",
    );
    expect(single.profit).toBeNull();
    expect(single.rate).toBeNull();
  });
  it("does not invent asset observations or a manager return at missing flow boundaries", () => {
    const records = createAccounts()[2]!.records;
    const result = analyze(records, "2026-01-01", DEMO_TODAY, "personal");
    expect(result.points).toHaveLength(2);
    expect(result.missingBoundary).toBe(true);
    expect(result.profit).toBe(250000);
    expect(
      analyze(records, "2026-01-01", DEMO_TODAY, "manager").rate,
    ).toBeNull();
    records.push(createRecord(43, "2026-09-11", "in", 100000, null, ""));
    expect(
      analyze(records, "2026-01-01", "2026-09-11", "personal").trailingFlow,
    ).toBe(true);
  });
  it("edits and voids recalculate without affecting another account or fresh demo instance", () => {
    const accounts = createAccounts();
    const rows = accounts[0]!.records;
    const base = analyze(rows, "2025-01-01", DEMO_TODAY, "personal").profit!;
    rows[6]!.amount! += 100000;
    expect(analyze(rows, "2025-01-01", DEMO_TODAY, "personal").profit).toBe(
      base - 100000,
    );
    rows[6]!.voided = true;
    expect(analyze(rows, "2025-01-01", DEMO_TODAY, "personal").profit).toBe(
      base + 1500000,
    );
    expect(accounts[1]).toEqual(createAccounts()[1]);
    expect(createAccounts()[0]!.records[6]!.voided).toBe(false);
  });
  it("uses the final asset observation per day and deterministic record order", () => {
    const records = [
      createRecord(2, "2026-01-01", "asset", null, 12000, ""),
      createRecord(1, "2026-01-01", "in", 10000, 10000, ""),
      createRecord(3, "2026-02-01", "asset", null, 13000, ""),
    ];
    expect(activeRecords(records).map((r) => r.id)).toEqual([1, 2, 3]);
    expect(analyze(records, "2026-01-01", DEMO_TODAY, "personal").profit).toBe(
      1000,
    );
    expect(importExample()).toEqual(importExample());
    expect(importExample()).not.toBe(importExample());
  });
});

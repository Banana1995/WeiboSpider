import { expect, it } from "vitest";
import { channelAssetsTotal, validRecordChannels } from "./ledgerChannels";

it("uses exact cents for channel totals larger than safe floating point integers", () => {
  const items = [
    { name: "Synthetic A", amount: "90071992547409.01" },
    { name: "Synthetic B", amount: "0.02" },
  ];
  expect(channelAssetsTotal(items)).toBe("90071992547409.03");
  expect(validRecordChannels(items, "90071992547409.03", undefined, null)).toBe(
    true,
  );
  expect(validRecordChannels(items, "90071992547409.04", undefined, null)).toBe(
    false,
  );
});
it("distinguishes blank balances, duplicates, invalid precision and overflow from real zero", () => {
  for (const amount of ["", "-1", "1.001", "92233720368547758.08"])
    expect(channelAssetsTotal([{ name: "Synthetic", amount }])).toBeNull();
  expect(channelAssetsTotal([{ name: "Synthetic", amount: "0" }])).toBe("0.00");
  expect(
    channelAssetsTotal([
      { name: "Synthetic", amount: "1" },
      { name: "Synthetic", amount: "1" },
    ]),
  ).toBeNull();
  expect(
    channelAssetsTotal([
      { name: "A", amount: "92233720368547758.07" },
      { name: "B", amount: "0.01" },
    ]),
  ).toBeNull();
  expect(validRecordChannels(undefined, null, "Synthetic", null)).toBe(false);
});

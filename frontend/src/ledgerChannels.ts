import { decimal } from "./ledger";
import type { ChannelAsset } from "./accountRecords";

export const maxAccountChannels = 100;
export function validChannelName(name: unknown): name is string {
  return (
    typeof name === "string" &&
    name !== "" &&
    name === name.trim() &&
    new TextEncoder().encode(name).length <= 128 &&
    !/[\u0000-\u001f\u007f-\u009f]/.test(name)
  );
}

function cents(value: string) {
  const [whole, fraction = ""] = value.split(".");
  return BigInt(whole!) * 100n + BigInt(fraction.padEnd(2, "0"));
}

// A missing amount is not zero. Keep totals exact even above Number.MAX_SAFE_INTEGER.
export function channelAssetsTotal(items: ChannelAsset[]): string | null {
  if (
    !Array.isArray(items) ||
    !items.length ||
    items.length > maxAccountChannels
  )
    return null;
  let total = 0n;
  const names = new Set<string>();
  for (const item of items) {
    if (
      !item ||
      !validChannelName(item.name) ||
      names.has(item.name) ||
      typeof item.amount !== "string" ||
      !decimal(item.amount, 2) ||
      item.amount.startsWith("-")
    )
      return null;
    names.add(item.name);
    total += cents(item.amount);
  }
  if (total > 9223372036854775807n) return null;
  return `${total / 100n}.${String(total % 100n).padStart(2, "0")}`;
}

export function validRecordChannels(
  items: ChannelAsset[] | undefined,
  total: string | null,
  flowChannel: string | undefined,
  flow: string | null,
) {
  if (
    flowChannel !== undefined &&
    (!validChannelName(flowChannel) || flow === null)
  )
    return false;
  if (items === undefined) return true;
  const sum = channelAssetsTotal(items);
  return (
    sum !== null &&
    typeof total === "string" &&
    decimal(total, 2) &&
    !total.startsWith("-") &&
    cents(sum) === cents(total)
  );
}

// Reorder UI tabs locally. Only opaque account ids are stored, never names or
// amounts, and a blocked/absent storage simply keeps the server order.
const storageKey = "ledger.account-tab-order.v1";

export function loadTabOrder(
  storage: Pick<Storage, "getItem"> | undefined = safeStorage(),
): string[] {
  if (!storage) return [];
  try {
    const raw = storage.getItem(storageKey);
    if (!raw) return [];
    const value: unknown = JSON.parse(raw);
    return Array.isArray(value) && value.every((id) => typeof id === "string")
      ? (value as string[])
      : [];
  } catch {
    return [];
  }
}

export function saveTabOrder(
  ids: string[],
  storage: Pick<Storage, "setItem"> | undefined = safeStorage(),
): void {
  if (!storage) return;
  try {
    storage.setItem(storageKey, JSON.stringify(ids));
  } catch {
    // Ignore quota or private-mode failures; ordering is a convenience only.
  }
}

// Known ids keep their stored rank; unknown ids stay in server order after them.
export function applyTabOrder<T extends { id: string }>(
  items: T[],
  order: string[],
): T[] {
  if (!order.length) return items;
  const rank = new Map(order.map((id, index) => [id, index]));
  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => {
      const rankA = rank.get(a.item.id) ?? Number.MAX_SAFE_INTEGER;
      const rankB = rank.get(b.item.id) ?? Number.MAX_SAFE_INTEGER;
      return rankA === rankB ? a.index - b.index : rankA - rankB;
    })
    .map(({ item }) => item);
}

function safeStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

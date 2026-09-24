// A bounded, page-lifetime cache. Values are validated before being stored and
// treated as immutable by consumers. No localStorage, disk or service worker.
export class LedgerReadCache {
  private entries = new Map<
    string,
    { value: unknown; until: number; bytes: number }
  >();
  private flights = new Map<
    string,
    {
      controller: AbortController;
      promise: Promise<unknown>;
      users: number;
      settled: boolean;
    }
  >();
  private bytes = 0;

  constructor(
    private ttl = 5 * 60_000,
    private maxEntries = 24,
    private maxBytes = 8 * 1024 * 1024,
    private now = Date.now,
  ) {}

  peek<T>(key: string): T | undefined {
    const entry = this.entries.get(key);
    if (!entry) return;
    if (entry.until <= this.now()) {
      this.drop(key);
      return;
    }
    this.entries.delete(key);
    this.entries.set(key, entry);
    return entry.value as T;
  }

  find<T>(
    matches: (key: string) => boolean,
  ): { key: string; value: T } | undefined {
    for (const key of [...this.entries.keys()].reverse()) {
      if (!matches(key)) continue;
      const value = this.peek<T>(key);
      if (value !== undefined) return { key, value };
    }
  }

  private drop(key: string) {
    const previous = this.entries.get(key);
    if (previous) this.bytes -= previous.bytes;
    this.entries.delete(key);
  }

  private put<T>(key: string, value: T) {
    this.drop(key);
    const bytes = new TextEncoder().encode(JSON.stringify(value)).length;
    if (bytes > this.maxBytes) return;
    while (
      this.entries.size >= this.maxEntries ||
      this.bytes + bytes > this.maxBytes
    )
      this.drop(this.entries.keys().next().value!);
    this.entries.set(key, { value, bytes, until: this.now() + this.ttl });
    this.bytes += bytes;
  }

  clear() {
    this.entries.clear();
    this.bytes = 0;
    for (const flight of this.flights.values()) flight.controller.abort();
    this.flights.clear();
  }

  async fetch<T>(
    key: string,
    reader: (signal: AbortSignal) => Promise<T>,
    signal: AbortSignal,
    revalidate = true,
  ): Promise<T> {
    if (signal.aborted) throw new DOMException("Read canceled", "AbortError");
    if (!revalidate) {
      const value = this.peek<T>(key);
      if (value !== undefined) return value;
    }
    let flight = this.flights.get(key);
    if (!flight) {
      const current = {
        controller: new AbortController(),
        promise: Promise.resolve<unknown>(undefined),
        users: 0,
        settled: false,
      };
      flight = current;
      this.flights.set(key, current);
      current.promise = Promise.resolve()
        .then(() => {
          if (current.controller.signal.aborted)
            throw new DOMException("Read canceled", "AbortError");
          return reader(current.controller.signal);
        })
        .then((value) => {
          // A canceled prefetch or pre-write request cannot repopulate the cache.
          if (
            current.controller.signal.aborted ||
            this.flights.get(key) !== current
          )
            throw new DOMException("Read superseded", "AbortError");
          this.put(key, value);
          return value;
        })
        .catch((error) => {
          // A failed refresh must not make an old snapshot look current later.
          if (this.flights.get(key) === current) this.drop(key);
          throw error;
        })
        .finally(() => {
          current.settled = true;
          if (this.flights.get(key) === current) this.flights.delete(key);
        });
    }
    const shared = flight;
    shared.users++;
    return new Promise<T>((resolve, reject) => {
      let finished = false;
      const finish = () => {
        if (finished) return false;
        finished = true;
        signal.removeEventListener("abort", abort);
        shared.users--;
        if (!shared.users && !shared.settled) {
          shared.controller.abort();
          if (this.flights.get(key) === shared) this.flights.delete(key);
        }
        return true;
      };
      const abort = () => {
        if (finish()) reject(new DOMException("Read canceled", "AbortError"));
      };
      signal.addEventListener("abort", abort, { once: true });
      shared.promise.then(
        (value) => {
          if (finish()) resolve(value as T);
        },
        (error) => {
          if (finish()) reject(error);
        },
      );
      if (signal.aborted) abort();
    });
  }
}

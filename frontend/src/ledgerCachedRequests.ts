import { LedgerError, query, request, type Account } from "./ledger";
import type { EffectiveSummary } from "./accountRecords";
import {
  todayShanghai,
  validateBasis,
  validSummary,
  type AnalysisBasis,
} from "./ledgerView";
import {
  validateAnnualReturns,
  type AnnualReturns,
} from "./ledgerAnnualReturns";
import {
  benchmarkLookbackFrom,
  benchmarkRate,
  validBenchmark,
  type Benchmark,
  type BenchmarkCode,
} from "./ledgerBenchmark";
import { LedgerReadCache } from "./ledgerReadCache";
import type { CachedLedgerRequest } from "./useLedgerCachedRead";

type AccountIdentity = Pick<Account, "id" | "currency">;

export const defaultBenchmarks: BenchmarkCode[] = ["H00300", "usINX"];

export function accountBasisRequest(
  a: AccountIdentity,
  from: string,
  to: string,
): CachedLedgerRequest<AnalysisBasis> {
  const path = `/accounts/${encodeURIComponent(a.id)}/analysis-basis${query({ from, to })}`;
  return {
    key: `${a.currency}|${path}`,
    read: async (signal) =>
      validateBasis(
        await request<AnalysisBasis>(path, { signal }),
        a,
        from,
        to,
      ),
  };
}

export function accountSummaryRequest(
  a: AccountIdentity,
): CachedLedgerRequest<EffectiveSummary> {
  const path = `/accounts/${encodeURIComponent(a.id)}/effective-summary`;
  return {
    key: `${todayShanghai()}|${a.currency}|${path}`,
    read: async (signal) => {
      const data = await request<EffectiveSummary>(path, { signal });
      if (!validSummary(data)) throw new LedgerError("invalid_response");
      return data;
    },
  };
}

export function annualReturnsRequest(
  a: AccountIdentity,
  code: BenchmarkCode,
): CachedLedgerRequest<AnnualReturns> {
  const path = `/accounts/${encodeURIComponent(a.id)}/annual-returns${query({ benchmark: code })}`;
  return {
    key: `${todayShanghai()}|${a.currency}|${path}`,
    read: async (signal) => {
      try {
        return validateAnnualReturns(
          await request<AnnualReturns>(path, { signal }),
          a,
          code,
        );
      } catch (error) {
        if (error instanceof LedgerError) throw error;
        throw new LedgerError("invalid_response");
      }
    },
  };
}

// Use the requested coverage, not the first/last trading date (weekends differ).
const benchmarkKey = (code: BenchmarkCode, from: string, to: string) =>
  `${code}|${from}|${to}`;

export function peekBenchmark(
  cache: LedgerReadCache,
  code: BenchmarkCode,
  from: string,
  to: string,
): Benchmark | undefined {
  const covering = cache.find<Benchmark>((key) => {
    const [c, first, last] = key.split("|");
    return c === code && !!first && !!last && first <= from && last >= to;
  });
  if (!covering) return;
  return sliceBenchmark(covering.value, from, to);
}

function sliceBenchmark(value: Benchmark, from: string, to: string): Benchmark {
  const selected = value.items.filter(
    (item) => item.date >= from && item.date <= to,
  );
  return {
    ...value,
    from: selected[0]?.date ?? from,
    to: selected.at(-1)?.date ?? to,
    items: selected.map((item) => ({
      ...item,
      return: benchmarkRate(item.close, selected[0]!.close),
    })),
  };
}

export async function readBenchmark(
  cache: LedgerReadCache,
  code: BenchmarkCode,
  from: string,
  to: string,
  signal: AbortSignal,
  force = false,
): Promise<Benchmark> {
  if (signal.aborted) throw new DOMException("Read canceled", "AbortError");
  if (!force) {
    const cached = peekBenchmark(cache, code, from, to);
    if (cached) return cached;
  }
  let first = from,
    last = to;
  if (!force) {
    const overlapping = cache.find<Benchmark>((key) => {
      const [c, start, end] = key.split("|");
      return c === code && !!start && !!end && start <= to && end >= from;
    });
    if (overlapping) {
      const [, start, end] = overlapping.key.split("|");
      const unionFrom = start! < from ? start! : from;
      const unionTo = end! > to ? end! : to;
      const limit = new Date(`${unionFrom}T00:00:00Z`);
      limit.setUTCFullYear(limit.getUTCFullYear() + 15);
      if (unionTo <= limit.toISOString().slice(0, 10)) {
        first = unionFrom;
        last = unionTo;
      }
    }
  }
  const value = await cache.fetch(
    benchmarkKey(code, first, last),
    async (sharedSignal) => {
      const data = await request<unknown>(
        `/benchmark${query({ code, from: first, to: last })}`,
        { signal: sharedSignal },
      );
      if (
        !validBenchmark(data, code, first, last) ||
        data.items.some((item) => item.date < first || item.date > last)
      )
        throw new LedgerError("invalid_response");
      return data;
    },
    signal,
    force,
  );
  return sliceBenchmark(value, from, to);
}

// One idle account at a time, with requests serialized. Only existing read APIs
// are used; importing/saving data or triggering source collection is impossible.
export async function prefetchAccount(
  account: AccountIdentity,
  accounts: LedgerReadCache,
  benchmarks: LedgerReadCache,
  signal: AbortSignal,
) {
  const basisRequest = accountBasisRequest(account, "", todayShanghai());
  const basis = await accounts.fetch(
    basisRequest.key,
    basisRequest.read,
    signal,
    false,
  );
  const summary = accountSummaryRequest(account);
  await accounts.fetch(summary.key, summary.read, signal, false);
  const annual = annualReturnsRequest(account, "H00300");
  await accounts.fetch(annual.key, annual.read, signal, false);
  const { effective_from: from, effective_to: to } = basis.returns;
  if (!from || !to || signal.aborted) return;
  for (const code of defaultBenchmarks) {
    if (signal.aborted) return;
    await readBenchmark(
      benchmarks,
      code,
      benchmarkLookbackFrom(from),
      to,
      signal,
    );
  }
}

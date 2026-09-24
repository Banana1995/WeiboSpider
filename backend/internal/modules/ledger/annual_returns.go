package ledger

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

// Annual results use the same projected account-record snapshot as analysis-basis.
// Rates remain decimal strings; reference status is calculation provenance, not a
// separate badge in the annual comparison.
type AnnualReturnRow struct {
	Year          int          `json:"year"` // zero for the lifetime/annualized rows
	From          string       `json:"from"`
	To            string       `json:"to"`
	MoneyWeighted ReturnMetric `json:"money_weighted"`
	TimeWeighted  ReturnMetric `json:"time_weighted"`
	Benchmark     ReturnMetric `json:"benchmark"`
	BenchmarkFrom string       `json:"benchmark_from"`
	BenchmarkTo   string       `json:"benchmark_to"`
}

type AnnualReturns struct {
	AccountID         string            `json:"account_id"`
	Currency          Currency          `json:"currency"`
	AsOf              string            `json:"as_of"`
	Revision          string            `json:"revision"`
	BenchmarkCode     string            `json:"benchmark_code"`
	BenchmarkName     string            `json:"benchmark_name"`
	BenchmarkCurrency Currency          `json:"benchmark_currency"`
	BenchmarkSource   string            `json:"benchmark_source"`
	BenchmarkError    string            `json:"benchmark_error"`
	Annualized        AnnualReturnRow   `json:"annualized"`
	Since             AnnualReturnRow   `json:"since"`
	Years             []AnnualReturnRow `json:"years"`
}

func (s *Store) annualAccountReturns(ctx context.Context, id, code string) (AnnualReturns, error) {
	b, err := s.AnalysisBasis(ctx, id, "", "", 0)
	if err != nil {
		return AnnualReturns{}, err
	}
	return annualBasisReturns(ctx, b, code)
}

func annualBasisReturns(ctx context.Context, b AnalysisBasis, code string) (AnnualReturns, error) {
	definition, err := benchmarkDefinition(code)
	if err != nil {
		return AnnualReturns{}, err
	}
	out := AnnualReturns{AccountID: b.AccountID, Currency: b.Currency, AsOf: b.To, Revision: b.Revision,
		BenchmarkCode: code, BenchmarkName: definition.Name, BenchmarkCurrency: definition.Currency,
		BenchmarkSource: definition.Source, Years: []AnnualReturnRow{}}
	out.Since = AnnualReturnRow{From: b.Returns.EffectiveFrom, To: b.Returns.EffectiveTo,
		MoneyWeighted: b.Returns.Dietz, TimeWeighted: b.Returns.TWR}
	out.Annualized = AnnualReturnRow{From: b.Returns.EffectiveFrom, To: b.Returns.EffectiveTo,
		MoneyWeighted: b.Returns.XIRR, TimeWeighted: b.Returns.TWRAnnualized}
	first := ""
	last := ""
	for _, p := range b.Points {
		if p.Selected && p.Assets != nil && first == "" {
			first = p.Date
		}
		if p.Selected && p.Date >= first && first != "" {
			last = p.Date
		}
	}
	if first == "" {
		return out, nil
	}
	firstYear, _ := strconv.Atoi(first[:4])
	lastYear, _ := strconv.Atoi(last[:4])
	// Limit repeated financial solvers and upstream history work on malformed
	// millennia-spanning accounts rather than returning a partial year table.
	if lastYear-firstYear >= 100 {
		return AnnualReturns{}, ErrQuery
	}
	for year := firstYear; year <= lastYear; year++ {
		if err := ctx.Err(); err != nil {
			return AnnualReturns{}, err
		}
		start := fmt.Sprintf("%04d-01-01", year)
		end := start[:4] + "-12-31"
		if end > b.To {
			end = b.To
		}
		period := AnalysisBasis{Revision: b.Revision, Points: []BasisPoint{}}
		for i := range b.Points {
			p := b.Points[i]
			if p.Date < start {
				period.twrHistory = append(period.twrHistory, p)
				if p.Selected {
					copy := p
					period.Opening = &copy
				}
			} else if p.Date <= end {
				period.Points = append(period.Points, p)
				if p.Selected {
					copy := p
					period.Closing = &copy
				}
			}
		}
		if period.Closing == nil {
			period.Closing = period.Opening
		}
		requestedFrom := start
		if year == firstYear {
			requestedFrom = "" // baseline day includes its flows; exclude them
		}
		r, err := calculateReturns(ctx, period, requestedFrom, end)
		if err != nil {
			return AnnualReturns{}, err
		}
		out.Years = append(out.Years, AnnualReturnRow{Year: year, From: r.EffectiveFrom, To: r.EffectiveTo,
			MoneyWeighted: r.Dietz, TimeWeighted: r.TWR})
	}
	return out, nil
}

// Fetch index closes outside the DB transaction. A missing provider cannot hide
// valid account returns; cancellation of the client request still stops work.
func (h Handler) annualReturns(w http.ResponseWriter, r *http.Request) {
	h.writeAnnualReturns(w, r, false)
}

func (h Handler) portfolioAnnualReturns(w http.ResponseWriter, r *http.Request) {
	h.writeAnnualReturns(w, r, true)
}

func (h Handler) writeAnnualReturns(w http.ResponseWriter, r *http.Request, portfolio bool) {
	if !method(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	q, err := query(r, "benchmark")
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	code := q.Get("benchmark")
	if code == "" {
		code = "H00300"
	}
	if _, err := benchmarkDefinition(code); err != nil {
		h.fail(w, r, err)
		return
	}
	var out AnnualReturns
	if portfolio {
		var basis PortfolioBasis
		basis, err = h.Store.PortfolioAnalysis(r.Context(), r.PathValue("id"), "", "")
		if err == nil {
			out, err = annualBasisReturns(r.Context(), basis.AnalysisBasis, code)
		}
	} else {
		out, err = h.Store.annualAccountReturns(r.Context(), r.PathValue("id"), code)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if out.Since.From != "" && out.Since.To > out.Since.From {
		if h.Benchmark == nil {
			out.BenchmarkError = "benchmark_unavailable"
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), benchmarkTimeout)
			items, fetchErr := h.annualBenchmarkHistory(ctx, code, out.Since.From, out.Since.To)
			cancel()
			if r.Context().Err() != nil {
				h.fail(w, r, r.Context().Err())
				return
			}
			if fetchErr != nil {
				out.BenchmarkError = "benchmark_unavailable"
				if errors.Is(benchmarkError(fetchErr), ErrBenchmarkTimeout) {
					out.BenchmarkError = "benchmark_timeout"
				}
			} else {
				out.Since.Benchmark, out.Since.BenchmarkFrom, out.Since.BenchmarkTo = periodBenchmark(items, out.Since.From, out.Since.To)
				out.Annualized.Benchmark = out.Since.Benchmark
				out.Annualized.BenchmarkFrom, out.Annualized.BenchmarkTo = out.Since.BenchmarkFrom, out.Since.BenchmarkTo
				if out.Since.Benchmark.Value != nil {
					base, _ := benchmarkClose(indexAsOf(items, out.Since.From).Close)
					close, _ := benchmarkClose(indexAsOf(items, out.Since.To).Close)
					out.Annualized.Benchmark, err = annualizeTWR(r.Context(), new(big.Rat).Quo(close, base), out.Since.From, out.Since.To, "available")
					if err != nil {
						h.fail(w, r, err)
						return
					}
				}
				for i := range out.Years {
					row := &out.Years[i]
					row.Benchmark, row.BenchmarkFrom, row.BenchmarkTo = periodBenchmark(items, row.From, row.To)
				}
			}
		}
	}
	if out.BenchmarkError != "" || out.Since.From == "" || out.Since.To <= out.Since.From {
		reason := out.BenchmarkError
		if reason == "" {
			reason = "missing_benchmark"
		}
		out.Since.Benchmark = returnUnavailable(reason)
		out.Annualized.Benchmark = returnUnavailable(reason)
		for i := range out.Years {
			out.Years[i].Benchmark = returnUnavailable(reason)
		}
	}
	httpapi.Write(w, 200, out)
}

const benchmarkLookbackDays = 30

func indexAsOf(items []BenchmarkItem, date string) *BenchmarkItem {
	var found *BenchmarkItem
	for i := range items {
		if items[i].Date > date {
			break
		}
		found = &items[i]
	}
	if found == nil || civilDay(date)-civilDay(found.Date) > benchmarkLookbackDays {
		return nil
	}
	return found
}

func periodBenchmark(items []BenchmarkItem, from, to string) (ReturnMetric, string, string) {
	if from == "" || to <= from {
		return returnUnavailable("missing_benchmark"), "", ""
	}
	opening, closing := indexAsOf(items, from), indexAsOf(items, to)
	if opening == nil || closing == nil {
		return returnUnavailable("missing_benchmark"), "", ""
	}
	base, ok := benchmarkClose(opening.Close)
	close, valid := benchmarkClose(closing.Close)
	if !ok || !valid {
		return returnUnavailable("missing_benchmark"), "", ""
	}
	rate := new(big.Rat).Sub(new(big.Rat).Quo(close, base), big.NewRat(1, 1))
	return returnValue(rate, 12, "available"), opening.Date, closing.Date
}

func (h Handler) annualBenchmarkHistory(ctx context.Context, code, from, to string) ([]BenchmarkItem, error) {
	start, _ := time.Parse(time.DateOnly, from)
	if start.Year() > 1 || start.YearDay() > benchmarkLookbackDays {
		start = start.AddDate(0, 0, -benchmarkLookbackDays)
	}
	items := []BenchmarkItem{}
	finish, _ := time.Parse(time.DateOnly, to)
	chunkYears := 10
	if code == "usINX" {
		// Tencent's longer windows return weekly/monthly bars whose dates need not
		// be the actual closing dates. Keep daily bars for as-of comparisons.
		chunkYears = 3
	}
	for !start.After(finish) {
		end := start.AddDate(chunkYears, 0, 0).AddDate(0, 0, -1)
		if end.After(finish) {
			end = finish
		}
		fromDate, toDate := start.Format(time.DateOnly), end.Format(time.DateOnly)
		b, err := h.Benchmark.Fetch(ctx, code, fromDate, toDate)
		if err != nil {
			return nil, err
		}
		definition, _ := benchmarkDefinition(code)
		if b.Code != code || b.Currency != definition.Currency || b.Name != definition.Name || b.Source != definition.Source || len(b.Items) > benchmarkMaxItems {
			return nil, ErrBenchmarkUnavailable
		}
		for _, item := range b.Items {
			if item.Date < fromDate || item.Date > toDate || !validDate(item.Date) || len(items) > 0 && item.Date <= items[len(items)-1].Date {
				return nil, ErrBenchmarkUnavailable
			}
			if _, ok := benchmarkClose(item.Close); !ok {
				return nil, ErrBenchmarkUnavailable
			}
			items = append(items, item)
		}
		start = end.AddDate(0, 0, 1)
	}
	return items, nil
}

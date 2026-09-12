package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	benchmarkTimeout  = 12 * time.Second
	maxBenchmarkBody  = 2 << 20
	benchmarkCacheTTL = 30 * time.Minute
	benchmarkCacheMax = 64
	benchmarkMaxYears = 15
	benchmarkMaxItems = 8000
)

var (
	ErrBenchmarkUnavailable = errors.New("benchmark unavailable")
	ErrBenchmarkTimeout     = errors.New("benchmark timed out")
)

// BenchmarkItem carries exact decimal strings only. close is preserved verbatim
// from the upstream token; return is the exact cumulative return from the first
// item of the returned range, rounded half-away-from-zero to eight decimals.
type BenchmarkItem struct {
	Date   string `json:"date"`
	Close  string `json:"close"`
	Return string `json:"return"`
}

type Benchmark struct {
	Code     string          `json:"code"`
	Name     string          `json:"name"`
	Currency Currency        `json:"currency"`
	Source   string          `json:"source"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Items    []BenchmarkItem `json:"items"`
}

// Implementations must honor cancellation and never invent a value for a gap.
type BenchmarkProvider interface {
	Fetch(ctx context.Context, code, from, to string) (Benchmark, error)
}

type benchmarkIndex struct {
	Name     string
	Currency Currency
	Source   string
}

// Deliberately one entry: an unknown code must not silently proxy upstream.
var benchmarkIndexes = map[string]benchmarkIndex{
	"H00300": {Name: "沪深300全收益", Currency: CNY, Source: "中证指数"},
}

func benchmarkDefinition(code string) (benchmarkIndex, error) {
	definition, ok := benchmarkIndexes[code]
	if !ok {
		return benchmarkIndex{}, ErrQuery
	}
	return definition, nil
}

func validateBenchmarkRange(code, from, to string) error {
	if _, err := benchmarkDefinition(code); err != nil {
		return err
	}
	if !validDate(from) || !validDate(to) || from > to {
		return ErrQuery
	}
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	if end.After(start.AddDate(benchmarkMaxYears, 0, 0)) {
		return ErrQuery
	}
	return nil
}

func benchmarkError(err error) error {
	var timeout net.Error
	switch {
	case err == nil, errors.Is(err, ErrQuery), errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, ErrBenchmarkTimeout), errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return ErrBenchmarkTimeout
	default:
		return ErrBenchmarkUnavailable
	}
}

type benchmarkCacheEntry struct {
	value   Benchmark
	expires time.Time
}

type CSIndexBenchmark struct {
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]benchmarkCacheEntry
}

// No startup I/O and no user-configurable URL or redirect destination.
func NewCSIndexBenchmark() *CSIndexBenchmark {
	return newCSIndexBenchmark(http.DefaultTransport, time.Now)
}

func newCSIndexBenchmark(transport http.RoundTripper, now func() time.Time) *CSIndexBenchmark {
	return &CSIndexBenchmark{
		client: &http.Client{Transport: transport, Timeout: benchmarkTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:   now,
		cache: make(map[string]benchmarkCacheEntry),
	}
}

func (p *CSIndexBenchmark) Fetch(ctx context.Context, code, from, to string) (Benchmark, error) {
	if err := validateBenchmarkRange(code, from, to); err != nil {
		return Benchmark{}, err
	}
	definition, _ := benchmarkDefinition(code)
	key := code + "|" + from + "|" + to
	if cached, ok := p.cacheGet(key); ok {
		return cached, nil
	}
	ctx, cancel := context.WithTimeout(ctx, benchmarkTimeout)
	defer cancel()
	body, err := p.get(ctx, code, from, to)
	if err != nil {
		return Benchmark{}, benchmarkError(err)
	}
	result, err := parseBenchmark(body, code, from, to, definition)
	if err != nil {
		return Benchmark{}, benchmarkError(err)
	}
	p.cachePut(key, result)
	return result, nil
}

func (p *CSIndexBenchmark) get(ctx context.Context, code, from, to string) ([]byte, error) {
	select {
	case stockQuoteSlots <- struct{}{}:
		defer func() { <-stockQuoteSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	params := url.Values{
		"indexCode": {code},
		"startDate": {strings.ReplaceAll(from, "-", "")},
		"endDate":   {strings.ReplaceAll(to, "-", "")},
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.csindex.com.cn/csindex-home/perf/index-perf?"+params.Encode(), nil)
	if err != nil {
		return nil, ErrBenchmarkUnavailable
	}
	r.Header.Set("User-Agent", "Mozilla/5.0")
	r.Header.Set("Referer", "https://www.csindex.com.cn/")
	response, err := p.client.Do(r)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrBenchmarkUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBenchmarkBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBenchmarkBody {
		return nil, ErrBenchmarkUnavailable
	}
	return body, ctx.Err()
}

func parseBenchmark(body []byte, code, from, to string, definition benchmarkIndex) (Benchmark, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !utf8.Valid(body) || uniqueFXJSON(decoder, 0) != nil {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	var payload struct {
		Code string `json:"code"`
		Data *[]struct {
			TradeDate string      `json:"tradeDate"`
			Close     json.Number `json:"close"`
		} `json:"data"`
	}
	strict := json.NewDecoder(bytes.NewReader(body))
	strict.UseNumber()
	if strict.Decode(&payload) != nil || payload.Code != "200" || payload.Data == nil {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	result := Benchmark{Code: code, Name: definition.Name, Currency: definition.Currency,
		Source: definition.Source, From: from, To: to, Items: []BenchmarkItem{}}
	if len(*payload.Data) > benchmarkMaxItems {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	var base *big.Rat
	previous := ""
	for _, row := range *payload.Data {
		stamp, err := time.Parse("20060102", row.TradeDate)
		if err != nil || stamp.Format("20060102") != row.TradeDate {
			return Benchmark{}, ErrBenchmarkUnavailable
		}
		date := stamp.Format(time.DateOnly)
		if previous != "" && date <= previous {
			return Benchmark{}, ErrBenchmarkUnavailable
		}
		previous = date
		close, ok := benchmarkClose(row.Close.String())
		if !ok {
			return Benchmark{}, ErrBenchmarkUnavailable
		}
		if base == nil {
			base = close
		}
		value, err := benchmarkReturn(close, base)
		if err != nil {
			return Benchmark{}, err
		}
		result.Items = append(result.Items, BenchmarkItem{Date: date, Close: row.Close.String(), Return: value})
	}
	if len(result.Items) > 0 {
		result.From = result.Items[0].Date
		result.To = result.Items[len(result.Items)-1].Date
	}
	return result, nil
}

// benchmarkClose accepts a plain decimal JSON number and requires it to be positive.
func benchmarkClose(s string) (*big.Rat, bool) {
	if len(s) == 0 || len(s) > maxDecimalLength {
		return nil, false
	}
	start, point := 0, false
	if s[0] == '-' {
		start = 1
	}
	if start == len(s) {
		return nil, false
	}
	for i := start; i < len(s); i++ {
		if s[i] == '.' {
			if point || i == start || i == len(s)-1 {
				return nil, false
			}
			point = true
		} else if s[i] < '0' || s[i] > '9' {
			return nil, false
		}
	}
	value, ok := new(big.Rat).SetString(s)
	if !ok || value.Sign() <= 0 {
		return nil, false
	}
	return value, true
}

// benchmarkReturn is exact rational math rounded half-away-from-zero at 8dp.
func benchmarkReturn(close, base *big.Rat) (string, error) {
	ratio := new(big.Rat).Quo(new(big.Rat).Sub(close, base), base)
	scaled := new(big.Rat).Mul(ratio, big.NewRat(100_000_000, 1))
	numerator, denominator := scaled.Num(), scaled.Denom()
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	remainder.Abs(remainder).Lsh(remainder, 1)
	if remainder.Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(int64(numerator.Sign())))
	}
	if !quotient.IsInt64() {
		return "", ErrBenchmarkUnavailable
	}
	return formatFixed(quotient.Int64(), 8), nil
}

func cloneBenchmark(value Benchmark) Benchmark {
	value.Items = append([]BenchmarkItem{}, value.Items...)
	return value
}

func (p *CSIndexBenchmark) cacheGet(key string) (Benchmark, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.cache[key]
	if !ok {
		return Benchmark{}, false
	}
	if !p.now().Before(entry.expires) {
		delete(p.cache, key)
		return Benchmark{}, false
	}
	return cloneBenchmark(entry.value), true
}

func (p *CSIndexBenchmark) cachePut(key string, value Benchmark) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if len(p.cache) >= benchmarkCacheMax {
		for existing, entry := range p.cache {
			if !now.Before(entry.expires) {
				delete(p.cache, existing)
			}
		}
		for existing := range p.cache {
			if len(p.cache) < benchmarkCacheMax {
				break
			}
			delete(p.cache, existing)
		}
	}
	p.cache[key] = benchmarkCacheEntry{value: cloneBenchmark(value), expires: now.Add(benchmarkCacheTTL)}
}

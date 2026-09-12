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
	"strconv"
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

	tencentDayMaxCount   = 1000
	tencentWeekMaxCount  = 1000
	tencentMonthMaxCount = 400
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

type benchmarkUpstream string

const (
	upstreamCSIndex   benchmarkUpstream = "csindex"
	upstreamTencentUS benchmarkUpstream = "tencent_us"
)

type benchmarkIndex struct {
	Name     string
	Currency Currency
	Source   string
	Upstream benchmarkUpstream
}

// Deliberately a closed list: an unknown code must not silently proxy upstream.
var benchmarkIndexes = map[string]benchmarkIndex{
	"H00300": {Name: "沪深300全收益", Currency: CNY, Source: "中证指数", Upstream: upstreamCSIndex},
	"H00922": {Name: "中证红利全收益", Currency: CNY, Source: "中证指数", Upstream: upstreamCSIndex},
	"usINX":  {Name: "标普500", Currency: USD, Source: "腾讯", Upstream: upstreamTencentUS},
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

// BenchmarkService routes each whitelisted code to its fixed read-only upstream.
type BenchmarkService struct {
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]benchmarkCacheEntry
}

// No startup I/O and no user-configurable URL or redirect destination.
func NewBenchmarkService() *BenchmarkService {
	return newBenchmarkService(http.DefaultTransport, time.Now)
}

func newBenchmarkService(transport http.RoundTripper, now func() time.Time) *BenchmarkService {
	return &BenchmarkService{
		client: &http.Client{Transport: transport, Timeout: benchmarkTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:   now,
		cache: make(map[string]benchmarkCacheEntry),
	}
}

func (p *BenchmarkService) Fetch(ctx context.Context, code, from, to string) (Benchmark, error) {
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
	var result Benchmark
	var err error
	switch definition.Upstream {
	case upstreamCSIndex:
		result, err = p.fetchCSIndex(ctx, code, from, to, definition)
	case upstreamTencentUS:
		result, err = p.fetchTencentUS(ctx, code, from, to, definition)
	default:
		err = ErrBenchmarkUnavailable
	}
	if err != nil {
		return Benchmark{}, benchmarkError(err)
	}
	p.cachePut(key, result)
	return result, nil
}

func (p *BenchmarkService) fetchCSIndex(ctx context.Context, code, from, to string, definition benchmarkIndex) (Benchmark, error) {
	params := url.Values{
		"indexCode": {code},
		"startDate": {strings.ReplaceAll(from, "-", "")},
		"endDate":   {strings.ReplaceAll(to, "-", "")},
	}
	body, err := p.get(ctx, "https://www.csindex.com.cn/csindex-home/perf/index-perf?"+params.Encode(), map[string]string{
		"User-Agent": "Mozilla/5.0", "Referer": "https://www.csindex.com.cn/",
	})
	if err != nil {
		return Benchmark{}, err
	}
	return parseCSIndexBenchmark(body, code, from, to, definition)
}

func (p *BenchmarkService) fetchTencentUS(ctx context.Context, code, from, to string, definition benchmarkIndex) (Benchmark, error) {
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	period, count := tencentGranularity(int(end.Sub(start).Hours() / 24))
	params := url.Values{"param": {code + "," + period + ",,," + strconv.Itoa(count) + ",qfq"}}
	body, err := p.get(ctx, "https://web.ifzq.gtimg.cn/appstock/app/usfqkline/get?"+params.Encode(), map[string]string{
		"User-Agent": "Mozilla/5.0",
	})
	if err != nil {
		return Benchmark{}, err
	}
	return parseTencentUSBenchmark(body, code, from, to, period, definition)
}

// tencentGranularity picks the coarsest period whose row cap still covers the
// span with a safety margin. Rows are requested as the latest N, so an
// undersized window fails the coverage check rather than silently truncating.
func tencentGranularity(spanDays int) (string, int) {
	if spanDays < 0 {
		spanDays = 0
	}
	if count := spanDays*72/100 + 40; count <= tencentDayMaxCount {
		return "day", count
	}
	weekCount := spanDays/7 + 2
	weekCount += weekCount/10 + 10
	if weekCount <= tencentWeekMaxCount {
		return "week", weekCount
	}
	monthCount := spanDays/28 + 2
	monthCount += monthCount/5 + 6
	if monthCount > tencentMonthMaxCount {
		monthCount = tencentMonthMaxCount
	}
	return "month", monthCount
}

func (p *BenchmarkService) get(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	select {
	case stockQuoteSlots <- struct{}{}:
		defer func() { <-stockQuoteSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ErrBenchmarkUnavailable
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
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

func parseCSIndexBenchmark(body []byte, code, from, to string, definition benchmarkIndex) (Benchmark, error) {
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

type benchmarkRowValue struct {
	date  string
	text  string
	close *big.Rat
}

func parseTencentUSBenchmark(body []byte, code, from, to, period string, definition benchmarkIndex) (Benchmark, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !utf8.Valid(body) || uniqueFXJSON(decoder, 0) != nil {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	var envelope struct {
		Code *int                       `json:"code"`
		Data map[string]json.RawMessage `json:"data"`
	}
	strict := json.NewDecoder(bytes.NewReader(body))
	strict.UseNumber()
	if strict.Decode(&envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || envelope.Data == nil {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	raw, ok := envelope.Data[code]
	if !ok {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	var sections map[string]json.RawMessage
	if json.Unmarshal(raw, &sections) != nil {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	rowsRaw, ok := sections[period]
	if !ok {
		rowsRaw, ok = sections["qfq"+period]
	}
	if !ok {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	var rows [][]any
	rowsDecoder := json.NewDecoder(bytes.NewReader(rowsRaw))
	rowsDecoder.UseNumber()
	if rowsDecoder.Decode(&rows) != nil || len(rows) > benchmarkMaxItems {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	parsed := make([]benchmarkRowValue, 0, len(rows))
	previous := ""
	for _, row := range rows {
		if len(row) < 3 {
			return Benchmark{}, ErrBenchmarkUnavailable
		}
		date, ok := row[0].(string)
		if !ok || !validDate(date) || date <= previous {
			return Benchmark{}, ErrBenchmarkUnavailable
		}
		previous = date
		text := benchmarkNumberText(row[2])
		close, ok := benchmarkClose(text)
		if !ok {
			return Benchmark{}, ErrBenchmarkUnavailable
		}
		parsed = append(parsed, benchmarkRowValue{date: date, text: text, close: close})
	}
	if !tencentCovered(parsed, from, to, period) {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	result := Benchmark{Code: code, Name: definition.Name, Currency: definition.Currency,
		Source: definition.Source, From: from, To: to, Items: []BenchmarkItem{}}
	var base *big.Rat
	for _, row := range parsed {
		if row.date < from || row.date > to {
			continue
		}
		if base == nil {
			base = row.close
		}
		value, err := benchmarkReturn(row.close, base)
		if err != nil {
			return Benchmark{}, err
		}
		result.Items = append(result.Items, BenchmarkItem{Date: row.date, Close: row.text, Return: value})
	}
	if len(result.Items) == 0 {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	result.From = result.Items[0].Date
	result.To = result.Items[len(result.Items)-1].Date
	return result, nil
}

func benchmarkNumberText(value any) string {
	switch number := value.(type) {
	case json.Number:
		return number.String()
	case string:
		return number
	}
	return ""
}

// tencentCovered rejects a window that starts after `from` or ends before `to`
// by more than the granularity's tolerance, which signals a truncated count.
func tencentCovered(rows []benchmarkRowValue, from, to, period string) bool {
	if len(rows) == 0 {
		return false
	}
	tolerance := map[string]int{"day": 14, "week": 21, "month": 62}[period]
	first, last := rows[0].date, rows[len(rows)-1].date
	if first > from && calendarDays(from, first) > tolerance {
		return false
	}
	if last < to && calendarDays(last, to) > tolerance {
		return false
	}
	return true
}

func calendarDays(from, to string) int {
	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return 0
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return 0
	}
	return int(end.Sub(start).Hours() / 24)
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

func (p *BenchmarkService) cacheGet(key string) (Benchmark, bool) {
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

func (p *BenchmarkService) cachePut(key string, value Benchmark) {
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

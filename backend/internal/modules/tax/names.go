package tax

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var hkCode = regexp.MustCompile(`^[0-9]{1,5}$`)
var usCode = regexp.MustCompile(`^[A-Z][A-Z0-9.-]{0,15}$`)
var hkMemo = regexp.MustCompile(`<SEHK\s+([0-9]{1,5})\s`)
var usMemo = regexp.MustCompile(`^([A-Z][A-Z0-9.-]{0,15})\s+[0-9.]+\s+SHARES\s`)
var quoteLine = regexp.MustCompile(`^v_([A-Za-z0-9.-]+)="([^"\r\n]*)";?$`)

func quoteSymbol(r Record) string {
	market, code := r.Market, strings.ToUpper(r.Code)
	if r.Kind == "资金进出" {
		if m := hkMemo.FindStringSubmatch(r.Note); len(m) > 0 {
			market, code = "SEHK", m[1]
		} else if m := usMemo.FindStringSubmatch(r.Note); len(m) > 0 {
			market, code = "US", m[1]
		}
	} else if r.Kind != "证券" {
		return ""
	}
	if (market == "SEHK" || market == "HK") && hkCode.MatchString(code) {
		return "hk" + strings.Repeat("0", 5-len(code)) + code
	}
	if market == "US" && usCode.MatchString(code) {
		return "us" + code
	}
	return ""
}

type nameCache struct {
	name    string
	expires time.Time
}
type NamesClient struct {
	client   *http.Client
	endpoint string
	mu       sync.Mutex
	cache    map[string]nameCache
	slots    chan struct{}
}

func NewNamesClient() *NamesClient {
	return &NamesClient{client: &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint: "https://qt.gtimg.cn/q=", cache: map[string]nameCache{}, slots: make(chan struct{}, 2)}
}

// Only public symbols derived from this workspace are sent to the provider.
// Lookup is optional enrichment; quote failures never block financial reports.
func (p *NamesClient) Lookup(ctx context.Context, records []Record) map[string]string {
	result := map[string]string{}
	if p == nil {
		return result
	}
	unique := map[string]bool{}
	for _, r := range records {
		if symbol := quoteSymbol(r); symbol != "" {
			unique[symbol] = true
		}
	}
	symbols := make([]string, 0, len(unique))
	for symbol := range unique {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	if len(symbols) > 500 {
		symbols = symbols[:500]
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	missing := []string{}
	now := time.Now()
	p.mu.Lock()
	for _, symbol := range symbols {
		cached, exists := p.cache[symbol]
		if cached.name != "" {
			result[symbol] = cached.name
		}
		if !exists || !now.Before(cached.expires) {
			missing = append(missing, symbol)
		}
	}
	p.mu.Unlock()
	for start := 0; start < len(missing); start += 50 {
		batch := missing[start:min(start+50, len(missing))]
		select {
		case p.slots <- struct{}{}:
		case <-ctx.Done():
			return result
		}
		names := p.fetch(ctx, batch)
		<-p.slots
		p.mu.Lock()
		if len(p.cache) > 1024 {
			clear(p.cache)
		}
		for _, symbol := range batch {
			name := names[symbol]
			ttl := time.Minute
			if name != "" {
				result[symbol] = name
				ttl = 6 * time.Hour
			} else {
				name = result[symbol]
			}
			p.cache[symbol] = nameCache{name, time.Now().Add(ttl)}
		}
		p.mu.Unlock()
		if ctx.Err() != nil {
			break
		}
	}
	return result
}
func (p *NamesClient) fetch(ctx context.Context, symbols []string) map[string]string {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+strings.Join(symbols, ","), nil)
	if err != nil {
		return nil
	}
	r.Header.Set("User-Agent", "Mozilla/5.0")
	response, err := p.client.Do(r)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(body)
	if err != nil {
		return nil
	}
	return parseNames(string(decoded), symbols)
}
func parseNames(text string, symbols []string) map[string]string {
	wanted := map[string]bool{}
	for _, symbol := range symbols {
		wanted[symbol] = true
	}
	result := map[string]string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		parts := quoteLine.FindStringSubmatch(strings.TrimSpace(line))
		if len(parts) != 3 || !wanted[parts[1]] {
			continue
		}
		symbol := parts[1]
		if seen[symbol] {
			delete(result, symbol)
			continue
		}
		seen[symbol] = true
		fields := strings.Split(parts[2], "~")
		if len(fields) < 3 {
			continue
		}
		if strings.HasPrefix(symbol, "hk") && fields[0] != "100" || strings.HasPrefix(symbol, "us") && fields[0] != "200" {
			continue
		}
		name, code := strings.TrimSpace(fields[1]), fields[2]
		expected := symbol[2:]
		if code != expected && !(strings.HasPrefix(symbol, "us") && (code == expected+".N" || code == expected+".OQ" || code == expected+".O" || code == expected+".A")) {
			continue
		}
		if name == "" || len(name) > 256 || strings.IndexFunc(name, func(r rune) bool {
			return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError || strings.ContainsRune("<>\"", r)
		}) >= 0 {
			continue
		}
		result[symbol] = name
	}
	return result
}

package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

type DividendEvent struct {
	ID         string   `json:"id"`
	Market     string   `json:"market"`
	Code       string   `json:"code"`
	Currency   Currency `json:"currency"`
	PerShare   Price    `json:"per_share"`
	RecordDate string   `json:"record_date"`
	ExDate     string   `json:"ex_date"`
	PayDate    string   `json:"pay_date"`
	Source     string   `json:"source"`
	Content    string   `json:"content"`
}

func (e DividendEvent) valid() bool {
	return validID(e.ID) && e.ID == dividendID(e.Market, e.Code, e.ExDate) && e.Currency.valid() && e.PerShare > 0 && validDate(e.ExDate) && validDate(e.PayDate) && e.PayDate >= e.ExDate && (e.Market == "HK" || validDate(e.RecordDate) && e.RecordDate <= e.ExDate) && (e.Source == "Tencent" || e.Source == "Eastmoney")
}
func dividendID(market, code, date string) string {
	return "div-" + receiptDigest(market + "/" + code + "/" + date)[:32]
}

type DividendCache struct {
	FetchedAt string             `json:"fetched_at"`
	Events    []DividendEvent    `json:"events"`
	FX        map[string]FXQuote `json:"fx"`
	Message   string             `json:"message"`
	Blocks    []string           `json:"blocks"`
}
type DividendProvider interface {
	FetchDividends(context.Context, Instrument) (DividendCache, error)
}
type PublicDividends struct{ client *http.Client }

func NewPublicDividends() *PublicDividends {
	return &PublicDividends{&http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (p *PublicDividends) get(ctx context.Context, u string, out any) error {
	r, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	r.Header.Set("User-Agent", "Mozilla/5.0")
	r.Header.Set("Referer", "https://gu.qq.com/")
	res, err := p.client.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("dividend upstream status %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return ErrOperation
	}
	return json.Unmarshal(data, out)
}

var aCashPlan = regexp.MustCompile(`^10派([0-9]+(?:\.[0-9]+)?)元(?:[（(]含税[^）)]*[）)])?$`)
var hkCashPlan = regexp.MustCompile(`^(?:每股派|末期息|中期息|特别息|特別息|季度股息)(?:港币|港幣|港元|美元)?([0-9]+(?:\.[0-9]+)?)(港元|美元|元)?[;；]?$`)

func dividendPrice(text string, hk bool, currency Currency) (Price, bool) {
	re := aCashPlan
	if hk {
		re = hkCashPlan
	}
	m := re.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, false
	}
	// Currency must be explicit for HK cash; never assume an amount is HKD.
	if hk {
		if currency == HKD && !strings.Contains(text, "港") || currency == USD && !strings.Contains(text, "美元") {
			return 0, false
		}
	}
	v, err := ParsePrice(m[1])
	if err != nil || v <= 0 {
		return 0, false
	}
	if !hk {
		if v%10 != 0 {
			return 0, false
		}
		v /= 10
	}
	return v, true
}
func dividendDate(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	if len(s) >= 10 {
		s = s[:10]
	}
	if !validDate(s) {
		return ""
	}
	return s
}
func (p *PublicDividends) FetchDividends(ctx context.Context, i Instrument) (DividendCache, error) {
	c := DividendCache{Events: []DividendEvent{}, FX: map[string]FXQuote{}, Blocks: []string{}, Message: "自动现金分红按公告金额计算，可编辑为实际到账金额。较早历史及送转、拆合股请核对。"}
	if _, code := quoteSymbol(i); code != "" {
		return c, ErrUnsupported
	}
	if i.Market == "HK" {
		var response struct {
			Success bool `json:"success"`
			Result  *struct {
				Pages int `json:"pages"`
				Data  []struct {
					Code    string `json:"SECURITY_CODE"`
					ExDate  string `json:"EX_DIVIDEND_DATE"`
					PayDate string `json:"DIVIDEND_DATE"`
					Content string `json:"PLAN_EXPLAIN"`
				} `json:"data"`
			} `json:"result"`
		}
		params := url.Values{"reportName": {"RPT_HKF10_MAIN_DIVBASIC"}, "columns": {"ALL"}, "filter": {`(SECURITY_CODE="` + i.Code + `")(IS_BFP="0")`}, "pageNumber": {"1"}, "pageSize": {"200"}, "sortTypes": {"-1,-1"}, "sortColumns": {"NOTICE_DATE,EX_DIVIDEND_DATE"}, "source": {"F10"}, "client": {"PC"}}
		err := p.get(ctx, "https://datacenter.eastmoney.com/securities/api/data/v1/get?"+params.Encode(), &response)
		if err == nil && response.Success && response.Result != nil && response.Result.Pages <= 1 {
			for _, r := range response.Result.Data {
				if r.Code != i.Code {
					return c, ErrOperation
				}
				ex := dividendDate(r.ExDate)
				price, ok := dividendPrice(r.Content, true, i.Currency)
				if !ok {
					if strings.Contains(r.Content, "拆") || strings.Contains(r.Content, "合股") || strings.Contains(r.Content, "送股") || strings.Contains(r.Content, "转增") {
						c.Blocks = append(c.Blocks, ex)
					}
					continue
				}
				e := DividendEvent{dividendID(i.Market, i.Code, ex), i.Market, i.Code, i.Currency, price, "", ex, dividendDate(r.PayDate), "Eastmoney", r.Content}
				if e.valid() {
					c.Events = append(c.Events, e)
				}
			}
		} else {
			var t struct {
				Code *int `json:"code"`
				Data struct {
					Rows []struct {
						Ex      string `json:"cqr"`
						Pay     string `json:"real_pay_date"`
						Content string `json:"CONTENT"`
					} `json:"fhpx"`
				} `json:"data"`
			}
			if err := p.get(ctx, "https://proxy.finance.qq.com/ifzqgtimg/appstock/app/hkStockinfo/jiankuang?_appver=6.5&app=official_website&code=hk"+i.Code, &t); err != nil {
				return c, err
			}
			if t.Code == nil || *t.Code != 0 || t.Data.Rows == nil {
				return c, ErrOperation
			}
			c.Message = "腾讯仅返回近期港股分红，历史分红可能不完整；按公告金额记账。"
			for _, r := range t.Data.Rows {
				price, ok := dividendPrice(r.Content, true, i.Currency)
				if !ok {
					continue
				}
				e := DividendEvent{dividendID(i.Market, i.Code, r.Ex), i.Market, i.Code, i.Currency, price, "", r.Ex, r.Pay, "Tencent", r.Content}
				if e.valid() {
					c.Events = append(c.Events, e)
				}
			}
		}
	} else {
		if i.Currency != CNY {
			return c, ErrUnsupported
		}
		var response struct {
			Rows []struct {
				Code       string `json:"SECURITY_CODE"`
				Content    string `json:"IMPL_PLAN_PROFILE"`
				Progress   string `json:"ASSIGN_PROGRESS"`
				RecordDate string `json:"EQUITY_RECORD_DATE"`
				ExDate     string `json:"EX_DIVIDEND_DATE"`
				PayDate    string `json:"PAY_CASH_DATE"`
			} `json:"fhyx"`
		}
		if err := p.get(ctx, "https://emweb.securities.eastmoney.com/PC_HSF10/BonusFinancing/PageAjax?code="+i.Market+i.Code, &response); err != nil {
			return c, err
		}
		if response.Rows == nil {
			return c, ErrOperation
		}
		c.Message = "沪深自动分红覆盖来源返回的近期实施方案；更早分红可手工补录。金额为公告口径。"
		for _, r := range response.Rows {
			if r.Code != i.Code {
				return c, ErrOperation
			}
			if r.Progress != "实施方案" && r.Progress != "实施分配" {
				continue
			}
			ex := dividendDate(r.ExDate)
			price, ok := dividendPrice(r.Content, false, i.Currency)
			if !ok {
				if strings.Contains(r.Content, "送") || strings.Contains(r.Content, "转") {
					c.Blocks = append(c.Blocks, ex)
				}
				continue
			}
			e := DividendEvent{dividendID(i.Market, i.Code, ex), i.Market, i.Code, i.Currency, price, dividendDate(r.RecordDate), ex, dividendDate(r.PayDate), "Eastmoney", r.Content}
			if e.valid() {
				c.Events = append(c.Events, e)
			}
		}
	}
	seen := map[string]DividendEvent{}
	for _, e := range c.Events {
		if old, ok := seen[e.ID]; ok && !reflect.DeepEqual(old, e) {
			return c, ErrConflict
		}
		seen[e.ID] = e
	}
	c.Events = c.Events[:0]
	for _, e := range seen {
		c.Events = append(c.Events, e)
	}
	slices.SortFunc(c.Events, func(a, b DividendEvent) int { return strings.Compare(a.ExDate, b.ExDate) })
	return c, nil
}

func readDividendCache(ctx context.Context, tx *sql.Tx, i Instrument) (DividendCache, error) {
	var raw string
	c := DividendCache{Events: []DividendEvent{}, FX: map[string]FXQuote{}}
	err := tx.QueryRowContext(ctx, `SELECT payload FROM stock_dividend_cache WHERE market=? AND code=?`, i.Market, i.Code).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if decodeReceipt(raw, &c) != nil || c.FX == nil || c.Events == nil {
		return c, ErrCorrupt
	}
	for _, e := range c.Events {
		if !e.valid() || e.Market != i.Market || e.Code != i.Code || e.Currency != i.Currency {
			return c, ErrCorrupt
		}
	}
	return c, nil
}
func reconcileStockDividends(ctx context.Context, tx *sql.Tx, st *stockState, today, stamp, key string) error {
	ids := []string{}
	for id := range st.instruments {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		i := st.instruments[id]
		cache, err := readDividendCache(ctx, tx, i)
		if err != nil {
			return err
		}
		for _, event := range cache.Events {
			if event.ExDate > today {
				continue
			}
			cutoff := event.RecordDate
			if event.Market == "HK" {
				cutoff = event.ExDate
			}
			trades := []StockEntry{}
			first := ""
			for _, e := range st.entries {
				if e.InstrumentID == id && e.Kind != "dividend" && !e.Voided && (e.Date < cutoff || event.Market != "HK" && e.Date == cutoff) {
					trades = append(trades, e)
					if first == "" || e.Date < first {
						first = e.Date
					}
				}
			}
			blocked := false
			for _, date := range cache.Blocks {
				if date != "" && first != "" && date >= first && date <= cutoff {
					blocked = true
				}
			}
			if blocked {
				continue
			}
			positions, err := replayStocks(st.journal, trades, today)
			if err != nil {
				return err
			}
			position := positions[id]
			entryID := event.ID + "-" + receiptDigest(id)[:12]
			n := slices.IndexFunc(st.entries, func(e StockEntry) bool { return e.ID == entryID })
			if n >= 0 && st.entries[n].Override {
				continue
			}
			if position == nil || !position.known || position.quantity == 0 {
				if n >= 0 && !st.entries[n].Voided {
					old := st.entries[n]
					next := old
					next.Voided = true
					if err := putStockEntry(ctx, tx, st.account.ID, key, "system", stamp, &next, &old); err != nil {
						return err
					}
					st.entries[n] = next
				}
				continue
			}
			// Match manual payments instead of crediting an already-entered dividend again.
			manual := slices.ContainsFunc(st.entries, func(e StockEntry) bool {
				return !e.Voided && e.Event == nil && e.Kind == "dividend" && e.InstrumentID == id && (e.Date == event.ExDate || e.Date == event.PayDate)
			})
			if manual {
				continue
			}
			amount, err := TradeAmount(position.quantity, event.PerShare)
			if err != nil {
				return err
			}
			rate := Rate(100000000)
			var fx *FXQuote
			if i.Currency != st.account.Currency {
				rate = 0
				if q, ok := cache.FX[event.ID+"/"+string(st.account.Currency)]; ok {
					if q.Base != i.Currency || q.Quote != st.account.Currency || q.Rate <= 0 || q.RequestedDate != event.PayDate || q.Date > event.PayDate {
						return ErrCorrupt
					}
					rate = q.Rate
					fx = &q
				}
			}
			next := StockEntry{ID: entryID, InstrumentID: id, Kind: "dividend", Date: event.ExDate, Quantity: position.quantity, Price: event.PerShare, Amount: amount, FX: rate, Event: &event, Cycle: position.cycle, SettlementFX: fx, Note: "自动分红 · 公告金额"}
			var old *StockEntry
			if n >= 0 {
				previous := st.entries[n]
				old = &previous
				next.Sequence = previous.Sequence
				next.Version = previous.Version
				next.CreatedAt = previous.CreatedAt
				next.UpdatedAt = previous.UpdatedAt
				if previous.SettlementFX != nil && previous.SettlementFX.RequestedDate == event.PayDate {
					next.FX = previous.FX
					next.SettlementFX = previous.SettlementFX
				}
				if reflect.DeepEqual(previous, next) {
					continue
				}
			}
			if len(st.entries) >= maxStockEntries && old == nil {
				return ErrOperation
			}
			if err := putStockEntry(ctx, tx, st.account.ID, key, "system", stamp, &next, old); err != nil {
				return err
			}
			if n >= 0 {
				st.entries[n] = next
			} else {
				st.entries = append(st.entries, next)
			}
		}
	}
	return nil
}

// A single owned worker shares requests across accounts. GET handlers never write.
type DividendWorker struct {
	store    *Store
	provider DividendProvider
	fx       FXProvider
	logger   *slog.Logger
	wake     chan struct{}
	mu       sync.Mutex
}

func NewDividendWorker(s *Store, p DividendProvider, fx FXProvider, l *slog.Logger) *DividendWorker {
	return &DividendWorker{s, p, fx, l, make(chan struct{}, 1), sync.Mutex{}}
}
func (w *DividendWorker) Trigger() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *DividendWorker) Run(ctx context.Context) error {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.wake:
		case <-timer.C:
		}
		budget, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err := w.Tick(budget)
		cancel()
		if err != nil && ctx.Err() == nil && w.logger != nil {
			w.logger.WarnContext(ctx, "ledger.dividends.failed", "code", "sync_failed")
		}
		timer.Reset(time.Minute)
	}
}
func (w *DividendWorker) Tick(ctx context.Context) error {
	if !w.mu.TryLock() {
		return nil
	}
	defer w.mu.Unlock()
	rows, err := w.store.db.QueryContext(ctx, `SELECT account_id FROM stock_journals ORDER BY account_id`)
	if err != nil {
		return err
	}
	accounts := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		accounts = append(accounts, id)
	}
	re := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if re != nil {
		return re
	}
	for _, id := range accounts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := w.syncAccount(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		_, stamp, e := w.store.cutoff()
		if e != nil {
			return e
		}
		message := "分红已检查；公告金额自动记账。沪深及备用来源仅覆盖近期记录，较早历史请核对。"
		if err != nil {
			message = "分红同步未完成，将自动重试；请核对现金、历史股数及汇率。"
		}
		if _, e = w.store.db.ExecContext(ctx, `INSERT INTO stock_dividend_status(account_id,checked_at,message) SELECT id,?,? FROM accounts WHERE id=? ON CONFLICT(account_id) DO UPDATE SET checked_at=excluded.checked_at,message=excluded.message`, stamp, message, id); e != nil {
			return e
		}
	}
	return nil
}
func (w *DividendWorker) syncAccount(ctx context.Context, id string) error {
	var st stockState
	var err error
	var sourceErrors error
	err = w.store.db.WithTx(ctx, func(tx *sql.Tx) error { st, err = readStockState(ctx, tx, id); return err })
	if err != nil {
		return err
	}
	today, stamp, err := w.store.cutoff()
	if err != nil {
		return err
	}
	for _, i := range st.instruments {
		var cache DividendCache
		err = w.store.db.WithTx(ctx, func(tx *sql.Tx) error { cache, err = readDividendCache(ctx, tx, i); return err })
		if err != nil {
			return err
		}
		fetched, _ := time.Parse(time.RFC3339Nano, cache.FetchedAt)
		if cache.FetchedAt == "" || w.store.now().Sub(fetched) >= 6*time.Hour {
			next, e := w.provider.FetchDividends(ctx, i)
			if e != nil {
				sourceErrors = errors.Join(sourceErrors, e)
			} else {
				// Preserve older observations when the upstream only returns its recent window.
				for _, old := range cache.Events {
					if !slices.ContainsFunc(next.Events, func(e DividendEvent) bool { return e.ID == old.ID }) {
						next.Events = append(next.Events, old)
					}
				}
				next.FX = cache.FX
				for _, date := range cache.Blocks {
					if !slices.Contains(next.Blocks, date) {
						next.Blocks = append(next.Blocks, date)
					}
				}
				next.FetchedAt = stamp
				cache = next
			}
		}
		for _, event := range cache.Events {
			if !event.valid() {
				return ErrOperation
			}
			if event.PayDate > today || i.Currency == st.account.Currency || st.journal.Cash == nil || event.PayDate <= st.journal.Cash.Date {
				continue
			}
			key := event.ID + "/" + string(st.account.Currency)
			if _, ok := cache.FX[key]; ok {
				continue
			}
			// No old FX is needed for events predating all recorded buys.
			if !slices.ContainsFunc(st.entries, func(e StockEntry) bool {
				return !e.Voided && e.InstrumentID == i.ID && e.Kind == "buy" && e.Date <= event.ExDate
			}) {
				continue
			}
			if w.fx == nil {
				continue
			}
			q, e := w.fx.Fetch(ctx, FXRequest{Base: i.Currency, Quote: st.account.Currency, Mode: "historical", Date: event.PayDate})
			if e == nil && q.Base == i.Currency && q.Quote == st.account.Currency && q.Rate > 0 && q.RequestedDate == event.PayDate && validDate(q.Date) && q.Date <= event.PayDate {
				cache.FX[key] = q
			}
		}
		raw, err := json.Marshal(cache)
		if err != nil {
			return err
		}
		if _, err = w.store.db.ExecContext(ctx, `INSERT INTO stock_dividend_cache(market,code,payload) VALUES(?,?,?) ON CONFLICT(market,code) DO UPDATE SET payload=excluded.payload`, i.Market, i.Code, string(raw)); err != nil {
			return err
		}
	}
	err = w.store.db.WithTx(ctx, func(tx *sql.Tx) error {
		// Re-read under the write lock: a user's simultaneous edit must win or be replayed.
		st, err = readStockState(ctx, tx, id)
		if err != nil {
			return err
		}
		today, stamp, err = w.store.cutoff()
		if err != nil {
			return err
		}
		before := slices.Clone(st.entries)
		if err := reconcileStockDividends(ctx, tx, &st, today, stamp, "dividends-"+receiptDigest(id + stamp)[:32]); err != nil {
			return err
		}
		if !stockStateChanged(st, before, today) {
			return nil
		}
		_, err = saveStockProjection(ctx, tx, &st, "dividends-"+receiptDigest(id + stamp)[:32], "system", today, stamp)
		return err
	})
	return errors.Join(err, sourceErrors)
}

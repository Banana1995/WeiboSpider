package options

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Scanner struct {
	store    *Store
	provider Provider
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	wg       sync.WaitGroup
	active   *Run
	state    ProviderState
	closed   bool
}

func NewScanner(ctx context.Context, s *Store, p Provider) *Scanner {
	ctx, cancel := context.WithCancel(ctx)
	return &Scanner{store: s, provider: p, ctx: ctx, cancel: cancel, state: ProviderState{USOptions: "unknown"}}
}
func (w *Scanner) Close() { w.mu.Lock(); w.closed = true; w.cancel(); w.mu.Unlock(); w.wg.Wait() }
func (w *Scanner) Status(ctx context.Context) (ProviderState, *Run, error) {
	w.mu.Lock()
	state := w.state
	w.mu.Unlock()
	runs, e := listJSON[Run](ctx, w.store.db, "SELECT data FROM scan_runs ORDER BY created_at DESC LIMIT 1")
	if len(runs) == 0 {
		return state, nil, e
	}
	return state, &runs[0], e
}
func (s *Store) saveRun(ctx context.Context, r Run) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO scan_runs(id,created_at,state,data) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,data=excluded.data", r.ID, r.CreatedAt, r.State, encode(r))
	return e
}
func (s *Store) recover(ctx context.Context) error {
	runs, e := listJSON[Run](ctx, s.db, "SELECT data FROM scan_runs WHERE state IN ('running','queued')")
	if e != nil {
		return e
	}
	for _, r := range runs {
		r.State = "interrupted"
		r.FinishedAt = stamp(s.now())
		for i := range r.Targets {
			if r.Targets[i].State == "running" || r.Targets[i].State == "queued" {
				r.Targets[i].State = "interrupted"
				r.Targets[i].Error = "服务重启，等待手动更新"
			}
		}
		if e = s.saveRun(ctx, r); e != nil {
			return e
		}
	}
	return nil
}
func (w *Scanner) Submit(ctx context.Context, requestID string, symbols []string) (Run, error) {
	return w.SubmitType(ctx, requestID, symbols, "PUT")
}
func (w *Scanner) SubmitType(ctx context.Context, requestID string, symbols []string, side string) (Run, error) {
	var empty Run
	if !idPattern.MatchString(requestID) || !validSide(side) {
		return empty, ErrInput
	}
	side = optionSide(side)
	requested := []string{}
	seenRequests := map[string]bool{}
	for _, symbol := range symbols {
		if !symbolPattern.MatchString(symbol) {
			return empty, ErrInput
		}
		if !seenRequests[symbol] {
			requested = append(requested, symbol)
			seenRequests[symbol] = true
		}
	}
	sort.Strings(requested)
	digest := fingerprint(requested)
	// Keep old PUT request receipts replayable across the schema upgrade.
	if side == "CALL" {
		digest = fingerprint([]any{side, requested})
	}
	if old, found, err := w.store.replayScan(ctx, requestID, digest); found || err != nil {
		return old, err
	}
	watch, e := w.store.Watchlist(ctx)
	if e != nil {
		return empty, e
	}
	selected := map[string]bool{}
	if len(symbols) == 0 {
		for _, v := range watch {
			if v.Enabled {
				selected[v.Code] = true
			}
		}
	} else {
		for _, code := range symbols {
			found := false
			for _, v := range watch {
				if v.Code == code && v.Enabled {
					found = true
				}
			}
			if !found {
				return empty, ErrInput
			}
			selected[code] = true
		}
	}
	symbols = nil
	for code := range selected {
		symbols = append(symbols, code)
	}
	sort.Strings(symbols)
	if len(symbols) == 0 {
		return empty, fmt.Errorf("%w：请先添加并启用股票", ErrInput)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return empty, context.Canceled
	}
	if old, found, err := w.store.replayScan(ctx, requestID, digest); found || err != nil {
		return old, err
	}
	old, e := readJSON[Run](ctx, w.store.db, "SELECT data FROM scan_runs WHERE id=?", requestID)
	if e == nil {
		keys := []string{}
		for _, t := range old.Targets {
			keys = append(keys, t.Symbol)
		}
		if optionSide(old.OptionType) != side || strings.Join(keys, ",") != strings.Join(symbols, ",") {
			return empty, ErrReplay
		}
		return old, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return empty, e
	}
	if w.active != nil {
		covered := optionSide(w.active.OptionType) == side
		for _, code := range symbols {
			found := false
			for _, t := range w.active.Targets {
				if t.Symbol == code {
					found = true
				}
			}
			covered = covered && found
		}
		if covered {
			if _, e = w.store.db.ExecContext(ctx, "INSERT INTO scan_receipts(request_id,fingerprint,run_id) VALUES(?,?,?)", requestID, digest, w.active.ID); e != nil {
				return empty, e
			}
			return *w.active, nil
		}
		return empty, ErrBusy
	}
	r := Run{ID: requestID, OptionType: side, State: "queued", CreatedAt: stamp(w.store.now()), Targets: []Target{}}
	for _, code := range symbols {
		r.Targets = append(r.Targets, Target{Symbol: code, State: "queued", Progress: "等待更新"})
	}
	if e = w.store.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO scan_runs(id,created_at,state,data) VALUES(?,?,?,?)", r.ID, r.CreatedAt, r.State, encode(r)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO scan_receipts(request_id,fingerprint,run_id) VALUES(?,?,?)", requestID, digest, r.ID)
		return err
	}); e != nil {
		return empty, e
	}
	copy := r
	copy.Targets = append([]Target{}, r.Targets...)
	w.active = &copy
	w.wg.Add(1)
	go w.run(r)
	return r, nil
}

func (s *Store) replayScan(ctx context.Context, id, digest string) (Run, bool, error) {
	var saved, runID string
	err := s.db.QueryRowContext(ctx, "SELECT fingerprint,run_id FROM scan_receipts WHERE request_id=?", id).Scan(&saved, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	if saved != digest {
		return Run{}, true, ErrReplay
	}
	r, err := readJSON[Run](ctx, s.db, "SELECT data FROM scan_runs WHERE id=?", runID)
	return r, true, err
}
func (w *Scanner) run(r Run) {
	defer w.wg.Done()
	defer func() { w.mu.Lock(); w.active = nil; w.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(w.ctx, w.store.limits.Timeout)
	defer cancel()
	r.State = "running"
	update := func() error { return w.store.saveRun(context.WithoutCancel(w.ctx), r) }
	if update() != nil {
		return
	}
	state, e := w.provider.State(ctx)
	state.CheckedAt = stamp(w.store.now())
	if e != nil {
		state.Error = e.Error()
	}
	w.mu.Lock()
	w.state = state
	w.mu.Unlock()
	settings, se := w.store.Settings(ctx, 0)
	if e == nil {
		e = se
	}
	success := 0
	limited := false
	remaining := w.store.limits.MaxContracts
	for i := range r.Targets {
		t := &r.Targets[i]
		t.State = "running"
		t.Progress = "读取合约目录"
		if update() != nil {
			return
		}
		var snapshot Snapshot
		err := e
		if err == nil && remaining < 1 {
			err = &ProviderError{Code: "contract_limit", Message: "本轮合约数量已达上限，请单独刷新该股", Retryable: false}
		}
		if err == nil {
			snapshot, err = w.scan(ctx, t.Symbol, optionSide(r.OptionType), settings.HorizonDays, remaining, func(message string) error { t.Progress = message; return update() })
		}
		if err == nil {
			err = w.store.Publish(ctx, snapshot)
		}
		if err != nil {
			t.State = "failed"
			t.Error = publicScanError(err)
			t.Progress = "保留上次行情"
		} else {
			t.State = "succeeded"
			t.SnapshotID = snapshot.ID
			t.Progress = fmt.Sprintf("已更新 %d 份 %s", len(snapshot.Quotes), sideLabel(r.OptionType))
			remaining -= len(snapshot.Quotes)
			if !snapshot.Complete {
				limited = true
				t.Progress += "（覆盖不完整）"
			}
			success++
		}
		if update() != nil {
			return
		}
	}
	r.State = "succeeded"
	if success == 0 {
		r.State = "failed"
	} else if success < len(r.Targets) || limited {
		r.State = "partial"
	}
	if w.ctx.Err() != nil {
		r.State = "interrupted"
	}
	r.FinishedAt = stamp(w.store.now())
	if success > 0 {
		w.mu.Lock()
		w.state.USOptions = "verified"
		w.state.CheckedAt = r.FinishedAt
		w.mu.Unlock()
	}
	_ = update()
	// Local retention only; this never initiates an upstream request.
	_ = w.store.Prune(context.WithoutCancel(w.ctx))
}
func publicScanError(e error) string {
	var pe *ProviderError
	if errors.As(e, &pe) {
		return pe.Message
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return "本次更新超时，请缩小范围后手动重试"
	}
	if errors.Is(e, context.Canceled) {
		return "本次更新已中断"
	}
	return "本次行情校验或保存失败，已保留原快照"
}

type directory struct {
	From      string     `json:"from"`
	To        string     `json:"to"`
	At        string     `json:"at"`
	Contracts []Contract `json:"contracts"`
}

func (w *Scanner) directory(ctx context.Context, symbol, from, to, side string, progress func(string) error) ([]Contract, error) {
	old, e := readJSON[directory](ctx, w.store.db, "SELECT data FROM directories WHERE symbol=? AND option_type=?", symbol, side)
	if e == nil {
		at, err := time.Parse(time.RFC3339Nano, old.At)
		if err == nil && w.store.now().Sub(at) < w.store.limits.DirectoryTTL && old.From <= from && old.To >= to {
			out := []Contract{}
			for _, c := range old.Contracts {
				if c.Type != side {
					return nil, ErrInput
				}
				if c.Expiry >= from && c.Expiry <= to {
					out = append(out, c)
				}
			}
			return out, nil
		}
	} else if !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	dates, e := w.provider.Expirations(ctx, symbol)
	if e != nil {
		return nil, e
	}
	sort.Strings(dates)
	all := []Contract{}
	seen := map[string]bool{}
	end := ""
	for _, day := range dates {
		if _, e = time.Parse(time.DateOnly, day); e != nil {
			return nil, ErrInput
		}
		if day < from || day > to || day <= end {
			continue
		}
		start, _ := time.Parse(time.DateOnly, day)
		end = start.AddDate(0, 0, 29).Format(time.DateOnly)
		if end > to {
			end = to
		}
		if e = progress("读取到期日 " + day + " 至 " + end); e != nil {
			return nil, e
		}
		contracts, err := w.provider.Chain(ctx, symbol, day, end, side)
		if err != nil {
			return nil, err
		}
		for _, c := range contracts {
			if c.Symbol != symbol || c.Expiry < day || c.Expiry > end || c.Type != side || !idPattern.MatchString(c.Code) {
				return nil, ErrInput
			}
			if _, err := positive(c.Strike, false); err != nil {
				return nil, err
			}
			if !seen[c.Code] {
				seen[c.Code] = true
				all = append(all, c)
			}
		}
		if len(all) > 50000 {
			return nil, ErrLimit
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Expiry != b.Expiry {
			return a.Expiry < b.Expiry
		}
		x, _ := number(a.Strike)
		y, _ := number(b.Strike)
		if c := x.Cmp(y); c != 0 {
			return c < 0
		}
		return a.Code < b.Code
	})
	d := directory{from, to, stamp(w.store.now()), all}
	_, e = w.store.db.ExecContext(ctx, "INSERT INTO directories(symbol,option_type,from_date,to_date,captured_at,data) VALUES(?,?,?,?,?,?) ON CONFLICT(symbol,option_type) DO UPDATE SET from_date=excluded.from_date,to_date=excluded.to_date,captured_at=excluded.captured_at,data=excluded.data", symbol, side, from, to, d.At, encode(d))
	return all, e
}
func (w *Scanner) scan(ctx context.Context, symbol, side string, horizon, maxContracts int, progress func(string) error) (Snapshot, error) {
	start := w.store.now()
	from := dateET(start)
	date, _ := time.Parse(time.DateOnly, from)
	to := date.AddDate(0, 0, horizon).Format(time.DateOnly)
	v := Snapshot{ID: uid(), Symbol: symbol, OptionType: side, Source: "Futu OpenAPI", StartedAt: stamp(start), From: from, To: to, Complete: true, Quotes: []Quote{}}
	instrument, e := readJSON[Instrument](ctx, w.store.db, "SELECT data FROM instruments WHERE code=?", symbol)
	if e != nil {
		return v, e
	}
	v.Name = instrument.Name
	contracts, e := w.directory(ctx, symbol, from, to, side, progress)
	if e != nil {
		return v, e
	}
	v.TotalContracts = len(contracts)
	if len(contracts) > maxContracts {
		contracts = contracts[:maxContracts]
		v.Complete = false
		v.Warning = fmt.Sprintf("共 %d 份 %s，本次仅更新前 %d 份；请缩短采集期限", v.TotalContracts, sideLabel(side), len(contracts))
	}
	for offset := 0; offset < len(contracts); offset += 399 {
		batch := contracts[offset:min(offset+399, len(contracts))]
		if e = progress(fmt.Sprintf("已完成 %d / %d 份报价", offset, len(contracts))); e != nil {
			return v, e
		}
		var data QuoteBatch
		for attempt := 0; attempt < 2; attempt++ {
			data, e = w.provider.Quotes(ctx, batch, symbol)
			if e == nil {
				break
			}
			var pe *ProviderError
			if !errors.As(e, &pe) || !pe.Retryable {
				break
			}
			if attempt == 0 {
				timer := time.NewTimer(2 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return v, ctx.Err()
				case <-timer.C:
				}
			}
		}
		if e != nil {
			return v, e
		}
		if len(data.Quotes) != len(batch) {
			return v, ErrInput
		}
		expected := map[string]Contract{}
		for _, c := range batch {
			expected[c.Code] = c
		}
		for _, q := range data.Quotes {
			c, ok := expected[q.Contract.Code]
			if !ok || c != q.Contract {
				return v, ErrInput
			}
			delete(expected, c.Code)
			if e = validateQuote(q); e != nil {
				return v, e
			}
			v.Quotes = append(v.Quotes, q)
		}
		if data.Underlying != nil {
			if _, e = positive(*data.Underlying, false); e != nil {
				return v, e
			}
		}
		v.Underlying = data.Underlying
		v.MarketState = data.MarketState
	}
	v.CapturedAt = stamp(w.store.now())
	v.ValuationDate = dateET(w.store.now())
	return v, nil
}
func validateQuote(q Quote) error {
	for _, v := range []*string{q.Bid, q.Ask, q.BidSize, q.AskSize, q.Volume, q.OI, q.IV, q.LastPrice} {
		if v != nil {
			if _, e := positive(*v, true); e != nil {
				return e
			}
		}
	}
	if q.Delta != nil {
		if _, e := number(*q.Delta); e != nil {
			return e
		}
	}
	for _, v := range []*string{q.LastTradeAt, q.QuoteAt} {
		if v != nil {
			if _, e := time.Parse(time.RFC3339Nano, *v); e != nil {
				return ErrInput
			}
		}
	}
	return nil
}
func (s *Store) Prune(ctx context.Context) error {
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		cutoff := stamp(s.now().AddDate(0, 0, -7))
		dailyCutoff := dateET(s.now().AddDate(0, 0, -180))
		if _, e := tx.ExecContext(ctx, "DELETE FROM daily_history WHERE day<?", dailyCutoff); e != nil {
			return e
		}
		var size int64
		if e := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(length(data)),0) FROM daily_history").Scan(&size); e != nil {
			return e
		}
		if size < s.limits.HistoryBytes {
			values, e := listJSON[Snapshot](ctx, tx, "SELECT s.data FROM snapshots s JOIN latest l ON s.id=l.snapshot_id")
			if e != nil {
				return e
			}
			for _, v := range values {
				v.Quotes = nil
				raw := encode(v)
				if size+int64(len(raw)) > s.limits.HistoryBytes {
					break
				}
				if _, e = tx.ExecContext(ctx, "INSERT INTO daily_history(symbol,option_type,day,captured_at,data) VALUES(?,?,?,?,?) ON CONFLICT(symbol,option_type,day) DO UPDATE SET captured_at=excluded.captured_at,data=excluded.data", v.Symbol, optionSide(v.OptionType), v.ValuationDate, v.CapturedAt, raw); e != nil {
					return e
				}
				size += int64(len(raw))
			}
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM snapshots WHERE captured_at<? AND id NOT IN (SELECT snapshot_id FROM latest)", cutoff); e != nil {
			return e
		}
		// The quota applies to ordinary historical payloads, never favorites/latest.
		historySize := func() (int64, error) {
			var n int64
			e := tx.QueryRowContext(ctx, `SELECT
				(SELECT COALESCE(SUM(length(data)),0) FROM snapshots WHERE id NOT IN (SELECT snapshot_id FROM latest)) +
				(SELECT COALESCE(SUM(length(data)),0) FROM daily_history)`).Scan(&n)
			return n, e
		}
		used, e := historySize()
		if e != nil {
			return e
		}
		limited := used > s.limits.HistoryBytes
		for used > s.limits.HistoryBytes {
			result, e := tx.ExecContext(ctx, `DELETE FROM snapshots WHERE id IN
				(SELECT id FROM snapshots WHERE id NOT IN (SELECT snapshot_id FROM latest) ORDER BY captured_at LIMIT 1)`)
			if e != nil {
				return e
			}
			removed, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if removed == 0 {
				result, e = tx.ExecContext(ctx, `DELETE FROM daily_history WHERE rowid IN (SELECT rowid FROM daily_history ORDER BY day LIMIT 1)`)
				if e != nil {
					return e
				}
				removed, e = result.RowsAffected()
				if e != nil {
					return e
				}
				if removed == 0 {
					break
				}
			}
			used, e = historySize()
			if e != nil {
				return e
			}
		}
		if limited {
			if _, e := tx.ExecContext(ctx, `INSERT INTO module_state(key,value) VALUES('history_limited','true') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM scan_runs WHERE created_at<? AND state NOT IN ('running','queued')", stamp(s.now().AddDate(0, 0, -30)))
		return e
	})
}

func (s *Store) HistoryLimited(ctx context.Context) bool {
	var value string
	return s.db.QueryRowContext(ctx, "SELECT value FROM module_state WHERE key='history_limited'").Scan(&value) == nil && value == "true"
}

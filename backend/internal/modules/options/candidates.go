package options

import (
	"context"
	"math/big"
	"sort"
	"strconv"
)

func validateFilter(f Filter) error {
	for _, v := range []string{f.MinDays, f.MaxDays, f.MinStrike, f.MaxStrike, f.MaxCost, f.MinSalePrice, f.MinAPR, f.MinOI, f.MinVolume, f.MaxSpread, f.MaxDelta, f.Budget} {
		if v != "" {
			if _, e := positive(v, true); e != nil {
				return e
			}
		}
	}
	for _, v := range []string{f.MinDays, f.MaxDays, f.MinOI, f.MinVolume} {
		if v != "" {
			r, _ := number(v)
			if !r.IsInt() {
				return ErrInput
			}
		}
	}
	if f.MinDays != "" && f.MaxDays != "" {
		a, _ := number(f.MinDays)
		b, _ := number(f.MaxDays)
		if a.Cmp(b) > 0 {
			return ErrInput
		}
	}
	if f.MinStrike != "" && f.MaxStrike != "" {
		a, _ := number(f.MinStrike)
		b, _ := number(f.MaxStrike)
		if a.Cmp(b) > 0 {
			return ErrInput
		}
	}
	if f.Moneyness != "" && f.Moneyness != "in" && f.Moneyness != "out" {
		return ErrInput
	}
	return nil
}
func filterReason(f Filter, c Candidate, exact map[string]*big.Rat) string {
	delta := optional(c.Quote.Delta)
	if delta != nil {
		delta.Abs(delta)
	}
	for _, v := range []struct {
		input   string
		value   *big.Rat
		minimum bool
		label   string
	}{
		{f.MinDays, integer(c.Result.DTE), true, "期限"}, {f.MaxDays, integer(c.Result.DTE), false, "期限"},
		{f.MaxStrike, exact["strike"], false, "行权价"}, {f.MinStrike, exact["strike"], true, "行权价"}, {f.MaxCost, exact["cost"], false, "参考成本"}, {f.MinSalePrice, exact["sale_price"], true, "含权利金卖出价"},
		{f.MinAPR, exact["yield"], true, "年化"}, {f.MinOI, optional(c.Quote.OI), true, "未平仓量"},
		{f.MinVolume, optional(c.Quote.Volume), true, "成交量"}, {f.MaxSpread, exact["spread"], false, "价差"},
		{f.MaxDelta, delta, false, "Delta"}, {f.Budget, exact["capital"], false, "预算"},
	} {
		if v.input != "" {
			target, _ := number(v.input)
			if v.value == nil {
				return v.label + "缺失"
			}
			cmp := v.value.Cmp(target)
			if (v.minimum && cmp < 0) || (!v.minimum && cmp > 0) {
				return v.label
			}
		}
	}
	if f.Moneyness != "" {
		stock := optional(c.Snapshot.Underlying)
		strike := exact["strike"]
		if stock == nil || strike == nil {
			return "正股价格缺失"
		}
		cmp := strike.Cmp(stock)
		if c.Quote.Contract.Type == "CALL" {
			cmp = -cmp
		}
		if (f.Moneyness == "in" && cmp <= 0) || (f.Moneyness == "out" && cmp >= 0) {
			return "价内外"
		}
	}
	return ""
}

type CandidateQuery struct {
	OptionType string
	Symbol     string
	Mode       string
	FeeVersion int64
	Filter     Filter
	Sort       string
	Descending bool
	Offset     int
	Limit      int
	View       string
}
type CandidatePage struct {
	Items      []Candidate    `json:"items"`
	Total      int            `json:"total"`
	Available  int            `json:"available"`
	Excluded   map[string]int `json:"excluded"`
	Snapshots  []Snapshot     `json:"snapshots"`
	NextCursor string         `json:"next_cursor,omitempty"`
	View       string         `json:"view"`
}

func (s *Store) Candidates(ctx context.Context, q CandidateQuery) (CandidatePage, error) {
	out := CandidatePage{Items: []Candidate{}, Snapshots: []Snapshot{}, Excluded: map[string]int{}}
	if !validSide(q.OptionType) || validateFilter(q.Filter) != nil || q.Offset < 0 || q.Limit < 1 || q.Limit > 200 || (q.Mode != "full" && q.Mode != "net") {
		return out, ErrInput
	}
	q.OptionType = optionSide(q.OptionType)
	switch q.Sort {
	case "date", "strike", "premium", "yield", "capital":
	default:
		return out, ErrInput
	}
	watch, e := s.Watchlist(ctx)
	if e != nil {
		return out, e
	}
	snapshots, e := s.latestForSide(ctx, q.OptionType)
	if e != nil {
		return out, e
	}
	favorites, e := listJSON[Favorite](ctx, s.db, "SELECT data FROM favorites ORDER BY created_at DESC,id DESC")
	if e != nil {
		return out, e
	}
	saved := map[string]string{}
	for _, f := range favorites {
		k := f.Candidate.Snapshot.ID + ":" + f.Candidate.Quote.Contract.Code
		if saved[k] == "" {
			saved[k] = f.ID
		}
	}
	in := Input{Quantity: 1, Basis: "bid", Mode: q.Mode, FeeVersion: q.FeeVersion}
	fees, e := s.fees(ctx, in)
	if e != nil {
		return out, e
	}
	type ranked struct {
		c     Candidate
		exact map[string]*big.Rat
	}
	list := []ranked{}
	today := dateET(s.now())
	versions := []any{q.OptionType, today, q.Mode, q.FeeVersion, q.Filter, q.Sort, q.Descending, q.Symbol}
	for _, w := range watch {
		if !w.Enabled || (q.Symbol != "" && q.Symbol != "all" && w.Code != q.Symbol) {
			continue
		}
		versions = append(versions, w)
		for _, v := range snapshots {
			if v.Symbol != w.Code || optionSide(v.OptionType) != q.OptionType {
				continue
			}
			versions = append(versions, v.ID)
			meta := v
			meta.Quotes = nil
			out.Snapshots = append(out.Snapshots, meta)
			for _, quote := range v.Quotes {
				if quote.Contract.Type != q.OptionType {
					return out, ErrInput
				}
				result, exact, e := calculate(quote, v.ValuationDate, in, fees, v.Underlying)
				if e != nil {
					return out, e
				}
				// Expired and unquotable contracts cannot be sold at a Bid, so they
				// never enter the candidate list; the count stays auditable.
				currentDays, err := Days(today, quote.Contract.Expiry)
				if err != nil {
					return out, err
				}
				if result.DTE <= 0 || currentDays <= 0 {
					out.Excluded["已到期"]++
					continue
				}
				if result.Status == "quote_unavailable" {
					out.Excluded["Bid 不可用"]++
					continue
				}
				if result.Status == "unsupported_contract" {
					out.Excluded["合约属性未确认"]++
					continue
				}
				out.Available++
				c := Candidate{Snapshot: meta, Quote: quote, Input: in, Result: result, FavoriteID: saved[v.ID+":"+quote.Contract.Code], Stale: s.isStale(v)}
				stockFilter := w.Filter
				if q.OptionType == "CALL" {
					stockFilter = w.CallFilter
				}
				reason := filterReason(stockFilter, c, exact)
				if reason == "" {
					reason = filterReason(q.Filter, c, exact)
				}
				if reason != "" {
					out.Excluded[reason]++
					continue
				}
				budget := q.Filter.Budget
				if stockFilter.Budget != "" {
					if budget == "" {
						budget = stockFilter.Budget
					} else {
						a, _ := number(budget)
						b, _ := number(stockFilter.Budget)
						if b.Cmp(a) < 0 {
							budget = stockFilter.Budget
						}
					}
				}
				if budget != "" && exact["capital"] != nil {
					b, _ := number(budget)
					r := div(b, exact["capital"])
					c.BudgetQuantity = ptr(new(big.Int).Quo(r.Num(), r.Denom()).String())
				}
				list = append(list, ranked{c, exact})
			}
		}
	}
	out.View = fingerprint(versions)
	if q.Offset > 0 && (q.View == "" || q.View != out.View) {
		return out, ErrVersion
	}
	cmpNumber := func(a, b *big.Rat) int {
		if a == nil && b == nil {
			return 0
		}
		if a == nil {
			return 1
		}
		if b == nil {
			return -1
		}
		return a.Cmp(b)
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		cmp := 0
		if q.Sort == "date" {
			cmp = compareText(a.c.Quote.Contract.Expiry, b.c.Quote.Contract.Expiry)
		} else {
			x, y := a.exact[q.Sort], b.exact[q.Sort]
			if x == nil || y == nil {
				if x == nil && y != nil {
					return false
				}
				if x != nil && y == nil {
					return true
				}
			}
			cmp = cmpNumber(x, y)
		}
		if cmp != 0 {
			if q.Descending {
				return cmp > 0
			}
			return cmp < 0
		}
		if q.Sort == "yield" {
			if c := cmpNumber(a.exact["spread"], b.exact["spread"]); c != 0 {
				return c < 0
			}
		}
		if c := compareText(a.c.Quote.Contract.Expiry, b.c.Quote.Contract.Expiry); c != 0 {
			return c < 0
		}
		if c := cmpNumber(a.exact["strike"], b.exact["strike"]); c != 0 {
			return c < 0
		}
		return a.c.Quote.Contract.Code < b.c.Quote.Contract.Code
	})
	out.Total = len(list)
	end := min(q.Offset+q.Limit, len(list))
	for i := min(q.Offset, len(list)); i < end; i++ {
		out.Items = append(out.Items, list[i].c)
	}
	if end < len(list) {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}
func compareText(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

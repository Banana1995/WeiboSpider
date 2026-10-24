package tax

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

type Rate struct {
	Year     int    `json:"year"`
	Currency string `json:"currency"`
	Value    string `json:"value"`
	Date     string `json:"date"`
	Source   string `json:"source"`
}

func DefaultRates() []Rate {
	return []Rate{
		{2020, "HKD", "0.84164", "2020-12-31", "https://www.zmdnews.cn/2021/0104/626863.shtml"},
		{2020, "USD", "6.5249", "2020-12-31", "https://www.zmdnews.cn/2021/0104/626863.shtml"},
		{2021, "HKD", "0.81760", "2021-12-31", "https://www.news.cn/2021-12/31/c_1128219921.htm"},
		{2021, "USD", "6.3757", "2021-12-31", "https://www.news.cn/2021-12/31/c_1128219921.htm"},
		{2022, "HKD", "0.89327", "2022-12-30", "https://www.chinamoney.com.cn/chinese/ccprnoticecontent/index.html?searchDate=2022-12-30"},
		{2022, "USD", "6.9646", "2022-12-30", "https://www.chinamoney.com.cn/chinese/ccprnoticecontent/index.html?searchDate=2022-12-30"},
		{2023, "HKD", "0.90622", "2023-12-29", "https://xining.pbc.gov.cn/zhengcehuobisi/125207/125217/125925/5184576/index.html"},
		{2023, "USD", "7.0827", "2023-12-29", "https://xining.pbc.gov.cn/zhengcehuobisi/125207/125217/125925/5184576/index.html"},
		{2024, "HKD", "0.92604", "2024-12-31", "https://www.xinhuanet.com/fortune/20241231/c45faecb827645aebc9b2ac0dc3f82c4/c.html"},
		{2024, "USD", "7.1884", "2024-12-31", "https://www.xinhuanet.com/fortune/20241231/c45faecb827645aebc9b2ac0dc3f82c4/c.html"},
		{2025, "HKD", "0.90322", "2025-12-31", "https://nanjing.pbc.gov.cn/zhengcehuobisi/125207/125217/125925/2025123109021714424/index.html"},
		{2025, "USD", "7.0288", "2025-12-31", "https://nanjing.pbc.gov.cn/zhengcehuobisi/125207/125217/125925/2025123109021714424/index.html"},
	}
}

type Filter struct{ From, To, Account string }

func (f Filter) includes(r Record) bool {
	d := r.timestamp()[:10]
	return (f.From == "" || d >= f.From) && (f.To == "" || d <= f.To) && (f.Account == "" || f.Account == r.AccountID)
}

type Match struct {
	Open        Record `json:"open"`
	Close       Record `json:"close"`
	Quantity    string `json:"quantity"`
	OpenAmount  string `json:"open_amount"`
	CloseAmount string `json:"close_amount"`
	Profit      string `json:"profit"`
}
type Missing struct {
	Record   Record `json:"record"`
	Quantity string `json:"quantity"`
	Amount   string `json:"amount"`
}
type Summary struct {
	Year         int     `json:"year"`
	Currency     string  `json:"currency"`
	SellClose    string  `json:"sell_close"`
	BuyOpen      string  `json:"buy_open"`
	BuyClose     string  `json:"buy_close"`
	SellOpen     string  `json:"sell_open"`
	Redemption   string  `json:"redemption"`
	Subscription string  `json:"subscription"`
	TradeFees    string  `json:"trade_fees"`
	OtherFees    string  `json:"other_fees"`
	Fees         string  `json:"fees"`
	Net          string  `json:"net"`
	Dividends    string  `json:"dividends"`
	ProfitTax    *string `json:"profit_tax"`
	DividendTax  *string `json:"dividend_tax"`
	Tax          *string `json:"tax"`
	Rate         *string `json:"rate"`
	Complete     bool    `json:"complete"`
	MissingRate  bool    `json:"missing_rate"`
}
type Report struct {
	Rows    []Summary `json:"rows"`
	Matches []Match   `json:"matches"`
	Missing []Missing `json:"missing"`
	Entries []Detail  `json:"entries"`
	Method  string    `json:"method"`
}

// Detail preserves the original event and its cost lots. Fees charged to the
// year remain on the event that incurred them; opening fee allocations are
// informational and must not be subtracted from the closing profit again.
type Opening struct {
	Record   Record `json:"record"`
	Quantity string `json:"quantity"`
	Amount   string `json:"amount"`
	Fee      string `json:"fee"`
}
type Detail struct {
	Record    Record    `json:"record"`
	Openings  []Opening `json:"openings"`
	HasClose  bool      `json:"has_close"`
	Fees      string    `json:"fees"`
	Profit    string    `json:"profit"`
	Dividends string    `json:"dividends"`
	Complete  bool      `json:"complete"`
}

func newDetail(r Record) Detail {
	return Detail{Record: r, Openings: []Opening{}, Fees: r.Fee, Profit: fixed(sub(zero(), number(r.Fee)), 12), Dividends: "0", Complete: true}
}

type amounts struct {
	year                   int
	currency               string
	v                      [11]*big.Rat
	profitTax, dividendTax *big.Rat
	missing                bool
	rate                   *big.Rat
	rateMissing            bool
}

const (
	sellClose = iota
	buyOpen
	buyClose
	sellOpen
	redemption
	subscription
	tradeFees
	otherFees
	net
	dividends
	fees
)

func newAmounts(year int, currency string) *amounts {
	a := &amounts{year: year, currency: currency, profitTax: zero(), dividendTax: zero()}
	for i := range a.v {
		a.v[i] = zero()
	}
	return a
}

type lot struct {
	record           Record
	quantity, amount *big.Rat
}

func poolKey(r Record) string {
	code := r.Code
	if r.Market == "SEHK" {
		if v, err := strconv.ParseUint(code, 10, 64); err == nil {
			code = strconv.FormatUint(v, 10)
		}
	}
	return strings.Join([]string{r.Kind, r.Market, code, r.Currency}, "\x00")
}

func Calculate(records []Record, rates []Rate, filter Filter) Report {
	result := Report{Rows: []Summary{}, Matches: []Match{}, Missing: []Missing{}, Entries: []Detail{}, Method: "lixinger-compatible-v1"}
	ordered := append([]Record{}, records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].timestamp() != ordered[j].timestamp() {
			return ordered[i].timestamp() < ordered[j].timestamp()
		}
		if ordered[i].Source != ordered[j].Source {
			return ordered[i].Source < ordered[j].Source
		}
		return ordered[i].Line < ordered[j].Line
	})
	annual := map[string]*amounts{}
	get := func(r Record) *amounts {
		y, _ := strconv.Atoi(r.Date[:4])
		key := fmt.Sprintf("%d/%s", y, r.Currency)
		if annual[key] == nil {
			annual[key] = newAmounts(y, r.Currency)
		}
		return annual[key]
	}
	longs, shorts := map[string][]*lot{}, map[string][]*lot{}
	for _, r := range ordered {
		selected := filter.includes(r)
		var a *amounts
		if selected {
			a = get(r)
		}
		if r.Kind == "资金进出" {
			if !selected {
				continue
			}
			entry := newDetail(r)
			switch r.Reason {
			case "股息收入":
				a.v[dividends].Add(a.v[dividends], number(r.Change))
				entry.Dividends = r.Change
			case "", "银证转账":
				continue
			default:
				a.v[otherFees].Sub(a.v[otherFees], number(r.Change))
				entry.Fees = fixed(sub(zero(), number(r.Change)), 12)
				entry.Profit = r.Change
			}
			result.Entries = append(result.Entries, entry)
			continue
		}
		if selected {
			a.v[tradeFees].Add(a.v[tradeFees], number(r.Fee))
		}
		quantity, amount := abs(number(r.Quantity)), abs(number(r.Amount))
		entry := newDetail(r)
		key := poolKey(r)
		if r.Direction == "买入开仓" || r.Direction == "申购" {
			longs[key] = append(longs[key], &lot{r, quantity, amount})
			if selected {
				entry.Openings = append(entry.Openings, Opening{r, fixed(quantity, 12), fixed(amount, 12), r.Fee})
				result.Entries = append(result.Entries, entry)
			}
			continue
		}
		if r.Direction == "卖出开仓" {
			shorts[key] = append(shorts[key], &lot{r, quantity, amount})
			if selected {
				entry.Openings = append(entry.Openings, Opening{r, fixed(quantity, 12), fixed(amount, 12), r.Fee})
				result.Entries = append(result.Entries, entry)
			}
			continue
		}
		entry.HasClose = true
		entryProfit := sub(zero(), number(r.Fee))
		pool := longs
		short := r.Direction == "买入平仓"
		if short {
			pool = shorts
		}
		remaining := new(big.Rat).Set(quantity)
		for remaining.Sign() > 0 && len(pool[key]) > 0 {
			l := pool[key][0]
			matched := new(big.Rat).Set(remaining)
			if matched.Cmp(l.quantity) > 0 {
				matched.Set(l.quantity)
			}
			cost := portion(l.amount, matched, l.quantity)
			proceeds := portion(amount, matched, quantity)
			profit := sub(proceeds, cost)
			if short {
				profit = sub(cost, proceeds)
			}
			if selected {
				entry.Openings = append(entry.Openings, Opening{l.record, fixed(matched, 12), fixed(cost, 12), fixed(portion(number(l.record.Fee), matched, abs(number(l.record.Quantity))), 12)})
				entryProfit.Add(entryProfit, profit)
				op, cl := buyOpen, sellClose
				if short {
					op, cl = sellOpen, buyClose
				} else if r.Kind != "证券" {
					op, cl = subscription, redemption
				}
				a.v[op].Add(a.v[op], cost)
				a.v[cl].Add(a.v[cl], proceeds)
				result.Matches = append(result.Matches, Match{l.record, r, fixed(matched, 12), fixed(cost, 12), fixed(proceeds, 12), fixed(profit, 12)})
			}
			remaining.Sub(remaining, matched)
			l.quantity.Sub(l.quantity, matched)
			l.amount.Sub(l.amount, cost)
			if l.quantity.Sign() == 0 {
				pool[key] = pool[key][1:]
			}
		}
		if selected && remaining.Sign() > 0 {
			a.missing = true
			entry.Complete = false
			result.Missing = append(result.Missing, Missing{r, fixed(remaining, 12), fixed(portion(amount, remaining, quantity), 12)})
		}
		if selected {
			entry.Profit = fixed(entryProfit, 12)
			result.Entries = append(result.Entries, entry)
		}
	}
	rateMap := map[string]*big.Rat{}
	for _, r := range rates {
		rateMap[fmt.Sprintf("%d/%s", r.Year, r.Currency)] = number(r.Value)
	}
	totals := map[int]*amounts{}
	for key, a := range annual {
		// Pure cash transfers do not create an otherwise empty currency summary.
		nonzero := false
		for _, v := range a.v {
			if v.Sign() != 0 {
				nonzero = true
			}
		}
		if !nonzero && !a.missing {
			continue
		}
		a.v[fees] = add(a.v[tradeFees], a.v[otherFees])
		a.v[net] = sub(add(add(sub(a.v[sellClose], a.v[buyOpen]), sub(a.v[sellOpen], a.v[buyClose])), sub(a.v[redemption], a.v[subscription])), a.v[fees])
		a.rate = rateMap[key]
		a.rateMissing = a.rate == nil
		if totals[a.year] == nil {
			totals[a.year] = newAmounts(a.year, "CNY")
		}
		total := totals[a.year]
		total.missing = total.missing || a.missing
		if a.rate != nil {
			a.profitTax = mul(mul(positive(a.v[net]), number("0.2")), a.rate)
			dr := number("0.2")
			if a.currency == "USD" {
				dr = number("0.1")
			}
			a.dividendTax = mul(mul(positive(a.v[dividends]), dr), a.rate)
			for i := range a.v {
				total.v[i].Add(total.v[i], mul(a.v[i], a.rate))
			}
			total.profitTax.Add(total.profitTax, a.profitTax)
			total.dividendTax.Add(total.dividendTax, a.dividendTax)
		} else {
			total.rateMissing = true
		}
		result.Rows = append(result.Rows, a.summary())
	}
	for _, total := range totals {
		result.Rows = append(result.Rows, total.summary())
	}
	rank := map[string]int{"HKD": 0, "USD": 1, "CNY": 2}
	sort.Slice(result.Rows, func(i, j int) bool {
		a, b := result.Rows[i], result.Rows[j]
		if a.Year != b.Year {
			return a.Year > b.Year
		}
		return rank[a.Currency] < rank[b.Currency]
	})
	return result
}

func (a *amounts) summary() Summary {
	s := Summary{Year: a.year, Currency: a.currency, Complete: !a.missing, MissingRate: a.rateMissing}
	fields := []*string{&s.SellClose, &s.BuyOpen, &s.BuyClose, &s.SellOpen, &s.Redemption, &s.Subscription, &s.TradeFees, &s.OtherFees, &s.Net, &s.Dividends, &s.Fees}
	for i, f := range fields {
		if a.currency != "CNY" || !a.rateMissing {
			*f = fixed(a.v[i], 2)
		}
	}
	if a.rate != nil {
		rate := fixed(a.rate, 8)
		s.Rate = &rate
	}
	if !a.rateMissing {
		pt, dt, total := fixed(a.profitTax, 2), fixed(a.dividendTax, 2), fixed(add(a.profitTax, a.dividendTax), 2)
		s.ProfitTax, s.DividendTax, s.Tax = &pt, &dt, &total
	}
	return s
}

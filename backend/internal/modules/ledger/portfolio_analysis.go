package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"time"
)

type PortfolioEntry struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	RecordID  string `json:"record_id"`
	Date      string `json:"date"`
	Kind      string `json:"kind"` // cash_flow or opening; opening is an analytical adjustment only
	Amount    Money  `json:"amount"`
}

type PortfolioContribution struct {
	AccountID     string       `json:"account_id"`
	Name          string       `json:"name"`
	State         string       `json:"state"` // active, not_started, empty
	FirstDate     string       `json:"first_date"`
	InitialAssets *Money       `json:"initial_assets"`
	SourceDate    string       `json:"source_date"`
	From          string       `json:"from"`
	To            string       `json:"to"`
	Assets        Money        `json:"assets"`
	AssetShare    ReturnMetric `json:"asset_share"`
	Profit        ReturnMetric `json:"profit"`
	Dietz         ReturnMetric `json:"modified_dietz"`
	XIRR          ReturnMetric `json:"xirr"`
	TWR           ReturnMetric `json:"twr"`
	TWRAnnualized ReturnMetric `json:"twr_annualized"`
	Carried       bool         `json:"carried"`
}

type PortfolioBasis struct {
	AnalysisBasis
	Portfolio Portfolio               `json:"portfolio"`
	Members   []PortfolioContribution `json:"members"`
	Entries   []PortfolioEntry        `json:"entries"`
	Carried   bool                    `json:"carried"`
}

type portfolioDay struct {
	date       string
	assets     Money
	flow       Money
	sourceDate string
	carried    bool
}

type portfolioMember struct {
	info  AccountInfo
	basis AnalysisBasis
	days  []portfolioDay
}

func portfolioEntryID(account, record string) string {
	hash := sha256.Sum256([]byte(account + "/" + record))
	return "p-" + hex.EncodeToString(hash[:16])
}

func checkedPortfolioMoney(value *big.Int) (Money, error) {
	if !value.IsInt64() {
		return 0, ErrPrecision
	}
	return Money(value.Int64()), nil
}

// An explicit total is after ALL flows that day. The first such total brings in
// only the capital not already represented by that day's cash flows. A first
// flow-only day starts from zero and is explicitly marked as an estimate.
func normalizePortfolioMember(ctx context.Context, member *portfolioMember) ([]PortfolioEntry, error) {
	entries := []PortfolioEntry{}
	var balance Money
	sourceDate := ""
	for i := 0; i < len(member.basis.Points); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		date := member.basis.Points[i].Date
		flow := new(big.Int)
		var explicit *Money
		hasFlow, hasCarry := false, false
		for ; i < len(member.basis.Points) && member.basis.Points[i].Date == date; i++ {
			p := member.basis.Points[i]
			if p.Flow != nil {
				hasFlow = true
				flow.Add(flow, big.NewInt(int64(*p.Flow)))
				entries = append(entries, PortfolioEntry{ID: portfolioEntryID(member.info.ID, p.RecordID),
					AccountID: member.info.ID, RecordID: p.RecordID, Date: date, Kind: "cash_flow", Amount: *p.Flow})
			}
			if p.Selected && p.Record != nil && p.Record.TotalAssets != nil {
				if p.Record.CarriedFrom == nil || p.Record.ManualAssertion {
					explicit = p.Record.TotalAssets
				} else {
					hasCarry = true
				}
			}
		}
		if explicit == nil && !hasFlow && (!hasCarry || len(member.days) == 0) {
			continue
		}
		amount := new(big.Int).Add(big.NewInt(int64(balance)), flow)
		if explicit != nil {
			amount.SetInt64(int64(*explicit))
			sourceDate = date
			if len(member.days) == 0 {
				opening := new(big.Int).Sub(amount, flow)
				// A deposit larger than the first closing amount is a first-day
				// loss, not negative opening capital that erases that loss.
				if opening.Sign() < 0 {
					opening.SetInt64(0)
				}
				value, err := checkedPortfolioMoney(opening)
				if err != nil {
					return nil, err
				}
				if value != 0 {
					entries = append(entries, PortfolioEntry{ID: portfolioEntryID(member.info.ID, "opening"),
						AccountID: member.info.ID, Date: date, Kind: "opening", Amount: value})
				}
				flow.Add(flow, opening)
			}
		}
		if amount.Sign() < 0 {
			return nil, ErrPortfolioBasis
		}
		var err error
		balance, err = checkedPortfolioMoney(amount)
		if err != nil {
			return nil, err
		}
		net, err := checkedPortfolioMoney(flow)
		if err != nil {
			return nil, err
		}
		member.days = append(member.days, portfolioDay{date: date, assets: balance, flow: net,
			sourceDate: sourceDate, carried: explicit == nil})
	}
	return entries, nil
}

func portfolioAsOf(days []portfolioDay, date string) *portfolioDay {
	i := sort.Search(len(days), func(i int) bool { return days[i].date > date }) - 1
	if i < 0 {
		return nil
	}
	return &days[i]
}

// These are calculated daily portfolio observations, never persisted records.
// Their estimation provenance is carried separately and propagated to metrics.
func portfolioPoint(date string, sequence int, assets, flow Money) BasisPoint {
	id := "portfolio-" + date
	p := BasisPoint{Date: date, Sequence: strconv.Itoa(sequence + 1), RecordID: id, Version: "1",
		Assets: copyMoney(&assets), Status: "reported", SourceID: id, SourceDate: date, SourceVersion: "1", Selected: true}
	if flow != 0 {
		p.Flow = copyMoney(&flow)
	}
	return p
}

func portfolioSlice(id string, currency Currency, revision string, points []BasisPoint, from, to string) AnalysisBasis {
	b := AnalysisBasis{AccountID: id, Currency: currency, From: from, To: to, Timezone: "Asia/Shanghai", Revision: revision,
		ChangeRevision: "0", Status: "current", Points: []BasisPoint{}, Changes: []BasisChange{}}
	if b.From == "" {
		b.From = "0001-01-01"
	}
	net := new(big.Int)
	for _, p := range points {
		if p.Date > to {
			break
		}
		if p.Date < b.From {
			copy := p
			b.Opening = &copy
		} else {
			b.Points = append(b.Points, p)
			if p.Flow != nil {
				net.Add(net, big.NewInt(int64(*p.Flow)))
			}
		}
		copy := p
		b.Closing = &copy
	}
	b.NetFlow = centsString(net)
	if b.Closing == nil {
		b.Status = "unavailable"
	}
	return b
}

func portfolioReference(r *Returns) {
	mark := func(m *ReturnMetric) {
		if m.Value != nil {
			m.Status = "reference"
		}
	}
	for _, metric := range []*ReturnMetric{&r.Profit, &r.Dietz, &r.XIRR, &r.TWR, &r.TWRAnnualized} {
		mark(metric)
	}
	for i := range r.Curve {
		mark(&r.Curve[i].Profit)
		mark(&r.Curve[i].Dietz)
		mark(&r.Curve[i].TWR)
	}
	r.Warnings = append(r.Warnings, "portfolio_carried_assets")
}

func (s *Store) PortfolioAnalysis(ctx context.Context, id, from, to string) (PortfolioBasis, error) {
	out := PortfolioBasis{Members: []PortfolioContribution{}, Entries: []PortfolioEntry{}}
	today, _, err := s.cutoff()
	if err != nil {
		return out, err
	}
	requestedTo := to
	if to == "" {
		to = today
	}
	if !validID(id) || !validDate(to) || to > today || from != "" && (!validDate(from) || from > to || from == "0001-01-01") {
		return out, ErrQuery
	}
	var members []portfolioMember
	latest := ""
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		p, err := scanPortfolio(tx.QueryRowContext(ctx, portfolioSelect+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		out.Portfolio = p
		count := 0
		for _, accountID := range p.AccountIDs {
			info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, accountID))
			if errors.Is(err, ErrNotFound) {
				return ErrPortfolioMember
			}
			if err != nil {
				return err
			}
			if info.Currency != p.Currency {
				return ErrPortfolioCurrency
			}
			b, err := s.analysisBasis(ctx, tx, accountID, "", today, 0)
			if err != nil {
				return err
			}
			count += len(b.Points)
			if count > 10000 {
				return ErrQuery
			}
			// Determine the actual overall endpoint before clipping a historical
			// query. A custom Dec 31 and its annual row must use the same carry.
			cut := len(b.Points)
			for i, point := range b.Points {
				if point.Selected && (point.Assets != nil || point.Flow != nil) && point.Date > latest {
					latest = point.Date
				}
				if point.Date > to && i < cut {
					cut = i
				}
			}
			b.Points = b.Points[:cut]
			members = append(members, portfolioMember{info: info, basis: b})
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	revisions := make([]string, 0, len(members))
	dateSet := map[string]bool{}
	first, last := "", ""
	for i := range members {
		member := &members[i]
		revisions = append(revisions, member.basis.Revision)
		entries, err := normalizePortfolioMember(ctx, member)
		if err != nil {
			return out, err
		}
		for _, entry := range entries {
			if entry.Date >= from {
				out.Entries = append(out.Entries, entry)
			}
		}
		for _, day := range member.days {
			dateSet[day.date] = true
			if first == "" || day.date < first {
				first = day.date
			}
			if day.date > last {
				last = day.date
			}
		}
	}
	// Calendar boundaries use the agreed carry rule; annual rows can therefore
	// measure complete calendar years even when members update on different days.
	if first != "" {
		last = min(to, latest)
		dateSet[last] = true
		firstYear, _ := strconv.Atoi(first[:4])
		lastYear, _ := strconv.Atoi(last[:4])
		if lastYear-firstYear >= 100 {
			return out, ErrQuery
		}
		for year := firstYear; year < lastYear; year++ {
			dateSet[fmt.Sprintf("%04d-12-31", year)] = true
		}
		if from != "" {
			start, _ := time.Parse(time.DateOnly, from)
			boundary := start.AddDate(0, 0, -1).Format(time.DateOnly)
			if boundary >= first && boundary <= last {
				dateSet[boundary] = true
			}
		}
	}
	dates := make([]string, 0, len(dateSet))
	for date := range dateSet {
		dates = append(dates, date)
	}
	slices.Sort(dates)
	if len(dates) > 10000 {
		return out, ErrQuery
	}
	points := make([]BasisPoint, 0, len(dates))
	for i, date := range dates {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		assets, flow := new(big.Int), new(big.Int)
		for _, member := range members {
			day := portfolioAsOf(member.days, date)
			if day == nil {
				continue // before this member's records: it has not joined the analysis
			}
			assets.Add(assets, big.NewInt(int64(day.assets)))
			if day.date == date {
				flow.Add(flow, big.NewInt(int64(day.flow)))
			}
			if (day.carried || day.date != date) && (from == "" || date >= from) {
				out.Carried = true
			}
		}
		total, err := checkedPortfolioMoney(assets)
		if err != nil {
			return out, err
		}
		net, err := checkedPortfolioMoney(flow)
		if err != nil {
			return out, err
		}
		points = append(points, portfolioPoint(date, i, total, net))
	}
	payload, err := json.Marshal(struct {
		Portfolio Portfolio
		Revisions []string
		From, To  string
		Version   int
	}{out.Portfolio, revisions, from, to, 1})
	if err != nil {
		return out, err
	}
	hash := sha256.Sum256(payload)
	out.AnalysisBasis = portfolioSlice(id, out.Portfolio.Currency, hex.EncodeToString(hash[:]), points, from, to)
	out.Returns, err = calculateReturns(ctx, out.AnalysisBasis, from, requestedTo)
	if err != nil {
		return out, err
	}
	if out.Carried {
		portfolioReference(&out.Returns)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.ID < b.ID
	})
	for _, member := range members {
		contribution, err := portfolioContribution(ctx, member, out)
		if err != nil {
			return out, err
		}
		out.Members = append(out.Members, contribution)
	}
	return out, nil
}

func portfolioContribution(ctx context.Context, member portfolioMember, b PortfolioBasis) (PortfolioContribution, error) {
	unavailable := returnUnavailable("no_interval")
	c := PortfolioContribution{AccountID: member.info.ID, Name: member.info.Name, State: "empty",
		Profit: returnValue(new(big.Rat), 2, "available"), Dietz: unavailable, XIRR: unavailable, TWR: unavailable, TWRAnnualized: unavailable,
		AssetShare: returnUnavailable("nonpositive_denominator")}
	end := b.Returns.EffectiveTo
	if len(member.days) > 0 {
		c.FirstDate, c.InitialAssets = member.days[0].date, copyMoney(&member.days[0].assets)
		c.State = "not_started"
	}
	if day := portfolioAsOf(member.days, end); day != nil {
		c.State, c.Assets, c.SourceDate = "active", day.assets, day.sourceDate
		c.Carried = day.carried || day.date < end
		points := []BasisPoint{}
		from := b.Returns.RequestedFrom
		for _, day := range member.days {
			if day.date <= end {
				points = append(points, portfolioPoint(day.date, len(points), day.assets, day.flow))
			}
		}
		if from != "" && c.FirstDate >= from {
			from = "" // later member's first balance is its own baseline, not profit
		}
		boundaries := []string{end}
		if from != "" {
			start, _ := time.Parse(time.DateOnly, from)
			boundaries = append(boundaries, start.AddDate(0, 0, -1).Format(time.DateOnly))
		}
		for _, date := range boundaries {
			if day := portfolioAsOf(member.days, date); day != nil && day.date != date {
				points = append(points, portfolioPoint(date, len(points), day.assets, 0))
				c.Carried = true
			}
		}
		sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })
		for i := range points {
			points[i].Sequence = strconv.Itoa(i + 1)
		}
		period := portfolioSlice(member.info.ID, b.Currency, b.Revision, points, from, end)
		r, err := calculateReturns(ctx, period, from, end)
		if err != nil {
			return c, err
		}
		if c.Carried {
			portfolioReference(&r)
		}
		c.From, c.To = r.EffectiveFrom, r.EffectiveTo
		if r.Days > 0 {
			c.Profit = r.Profit
		}
		// Dollar contribution uses exactly the portfolio's boundary and cash
		// flows, including a later member's first-day loss. Its own rate still
		// starts at its own first asset basis, as in single-account analysis.
		if b.Returns.EffectiveFrom != "" && end > b.Returns.EffectiveFrom {
			profit := big.NewInt(int64(c.Assets))
			if opening := portfolioAsOf(member.days, b.Returns.EffectiveFrom); opening != nil {
				profit.Sub(profit, big.NewInt(int64(opening.assets)))
			}
			for _, day := range member.days {
				if day.date > b.Returns.EffectiveFrom && day.date <= end {
					profit.Sub(profit, big.NewInt(int64(day.flow)))
				}
			}
			status := "available"
			if c.Carried {
				status = "reference"
			}
			c.Profit = returnValue(new(big.Rat).SetFrac(profit, big.NewInt(100)), 2, status)
		}
		c.Dietz, c.XIRR, c.TWR, c.TWRAnnualized = r.Dietz, r.XIRR, r.TWR, r.TWRAnnualized
	}
	if b.Closing != nil && b.Closing.Assets != nil && *b.Closing.Assets > 0 {
		c.AssetShare = returnValue(new(big.Rat).SetFrac(big.NewInt(int64(c.Assets)), big.NewInt(int64(*b.Closing.Assets))), 12, "available")
	}
	return c, nil
}

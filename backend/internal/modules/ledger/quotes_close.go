package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var errClosingQuote = errors.New("no verified last closing quote")

// This shares the quote provider's deadline, response-size limit and global
// network slots. An empty adjustment parameter requests native, unadjusted bars.
func (p *TencentQuotes) lastClosingQuote(ctx context.Context, i Instrument, previous Price) (*Quote, error) {
	symbol, code := quoteSymbol(i)
	if code != "" || previous <= 0 {
		return nil, errClosingQuote
	}
	body, err := p.get(ctx, "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param="+symbol+",day,,,30,", 256<<10)
	if err != nil {
		return nil, err
	}
	return parseClosingQuote(body, i, previous, p.now())
}

func parseClosingQuote(body []byte, i Instrument, previous Price, now time.Time) (*Quote, error) {
	var response struct {
		Code *int `json:"code"`
		Data map[string]struct {
			Day [][]string          `json:"day"`
			QT  map[string][]string `json:"qt"`
		} `json:"data"`
	}
	symbol, code := quoteSymbol(i)
	if code != "" || previous <= 0 || json.Unmarshal(body, &response) != nil || response.Code == nil || *response.Code != 0 {
		return nil, errClosingQuote
	}
	data, ok := response.Data[symbol]
	identity := data.QT[symbol]
	currencyIndex := 82
	if i.Market == "HK" {
		currencyIndex = 75
	}
	if !ok || len(data.Day) == 0 || len(data.Day) > 30 || len(identity) <= currencyIndex || identity[2] != i.Code || Currency(identity[currencyIndex]) != i.Currency {
		return nil, errClosingQuote
	}
	today := now.In(fxBeijing).Format(time.DateOnly)
	var latest time.Time
	var close Price
	lastDate := ""
	for _, row := range data.Day {
		if len(row) < 6 || !validDate(row[0]) || row[0] > today || row[0] <= lastDate {
			return nil, errClosingQuote
		}
		lastDate = row[0]
		volume, err := ParseQuantity(row[5])
		if err != nil || volume < 0 {
			return nil, errClosingQuote
		}
		// Today's bar may still be trading; zero-volume bars are placeholders.
		if row[0] == today || volume == 0 {
			continue
		}
		open, e1 := ParsePrice(row[1])
		price, e2 := ParsePrice(row[2])
		high, e3 := ParsePrice(row[3])
		low, e4 := ParsePrice(row[4])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || low <= 0 || low > open || low > price || high < open || high < price {
			return nil, errClosingQuote
		}
		latest, err = time.ParseInLocation(time.DateOnly, row[0], fxBeijing)
		if err != nil {
			return nil, errClosingQuote
		}
		close = price
	}
	// Do not silently use an older matching bar, an adjusted price or a
	// corporate-action discontinuity when the latest real close disagrees.
	if latest.IsZero() || close != previous {
		return nil, errClosingQuote
	}
	return &Quote{
		Symbol: symbol, Price: close, Currency: i.Currency, Source: "Tencent",
		Date: latest.Format(time.DateOnly), FetchedAt: now.UTC().Format(time.RFC3339Nano),
		// Daily history has civil-day precision. Midnight encodes that date,
		// not a claimed intraday trade time; consumers display the actual Date.
		QuotedAt: latest.Format(time.RFC3339),
	}, nil
}

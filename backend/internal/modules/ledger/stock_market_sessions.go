package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const stockIntervalMinutes = 20

var stockMarkets = [...]string{"SH", "SZ", "HK"}

func stockWeekday(day time.Time) bool {
	return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
}

// Calendar times only nominate slots. Actual trading is confirmed separately
// for each held exchange. Never replay missed slots over lunch or after close.
func stockQuoteSchedule(now time.Time) (slot time.Time, markets []string, next time.Time) {
	now = now.In(fxBeijing)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, fxBeijing)
	for next = day.AddDate(0, 0, 1); !stockWeekday(next); next = next.AddDate(0, 0, 1) {
	}
	const morning, afternoon = 9*60 + 30, 13 * 60
	next = next.Add((morning + stockIntervalMinutes) * time.Minute)
	if !stockWeekday(day) {
		return
	}
	for _, market := range stockMarkets {
		lunch, closeMinute := 11*60+30, 15*60
		if market == "HK" {
			lunch, closeMinute = 12*60, 16*60
		}
		for _, window := range [][2]int{{morning, lunch}, {afternoon, closeMinute}} {
			if !now.Before(day.Add(time.Duration(window[1]+1) * time.Minute)) {
				continue
			}
			// End of each 20-minute interval, plus the close of a short interval.
			for m := window[0]; m < window[1]; {
				m = min(m+stockIntervalMinutes, window[1])
				at := day.Add(time.Duration(m) * time.Minute)
				if at.After(now) {
					if at.Before(next) {
						next = at
					}
					break
				}
				if at.After(slot) {
					slot, markets = at, nil
				}
				if at.Equal(slot) {
					markets = append(markets, market)
				}
			}
		}
	}
	return
}

var errStockSession = errors.New("market session evidence unavailable")

// Check the exchange's own index minute tape, NOT an individual holding
// (which could be suspended), a weekday alone, or a quote-server timestamp
// (which may advance on a holiday). No annual holiday table to expire.
func (p *TencentQuotes) stockMarketOpen(ctx context.Context, market string, now time.Time) (bool, error) {
	symbol := map[string]string{"SH": "sh000001", "SZ": "sz399001", "HK": "hkHSI"}[market]
	if symbol == "" {
		return false, errStockSession
	}
	body, err := p.get(ctx, "https://web.ifzq.gtimg.cn/appstock/app/minute/query?code="+symbol, 256<<10)
	if err != nil {
		return false, err
	}
	var tape struct {
		Code *int `json:"code"`
		Data map[string]struct {
			Data struct {
				Date string   `json:"date"`
				Bars []string `json:"data"`
			} `json:"data"`
			QT map[string]json.RawMessage `json:"qt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &tape); err != nil || tape.Code == nil || *tape.Code != 0 {
		return false, errStockSession
	}
	row, ok := tape.Data[symbol]
	var identity []string
	if !ok || json.Unmarshal(row.QT[symbol], &identity) != nil || len(identity) < 3 || identity[2] != symbol[2:] {
		return false, errStockSession
	}
	now = now.In(fxBeijing)
	if row.Data.Date != now.Format("20060102") || len(row.Data.Bars) == 0 {
		return false, nil
	}
	fields := strings.Fields(row.Data.Bars[len(row.Data.Bars)-1])
	if len(fields) < 3 {
		return false, errStockSession
	}
	bar, err := time.ParseInLocation("200601021504", row.Data.Date+fields[0], fxBeijing)
	price, pErr := ParsePrice(fields[1])
	volume, vErr := ParseQuantity(fields[2])
	if err != nil || pErr != nil || vErr != nil || price <= 0 || volume <= 0 {
		return false, errStockSession
	}
	// At 13:00 the tape may simply repeat the morning close. A half-day
	// (or a halt) cannot authorize an afternoon refresh from that marker.
	if now.Hour() >= 13 && (bar.Hour() < 13 || bar.Hour() == 13 && bar.Minute() == 0) {
		return false, nil
	}
	age := now.Truncate(time.Minute).Sub(bar)
	return age >= 0 && age <= stockIntervalMinutes*time.Minute, nil
}

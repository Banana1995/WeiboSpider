package ledger

import "strconv"

type accountJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Currency    Currency `json:"currency"`
	OpeningDate string   `json:"opening_date"`
	OpeningCash *Money   `json:"opening_cash"`
	Version     string   `json:"version"`
}

func publicAccount(a AccountInfo) accountJSON {
	return accountJSON{ID: a.ID, Name: a.Name, Currency: a.Currency, OpeningDate: a.OpeningDate, Version: strconv.FormatInt(a.Version, 10)}
}

type instrumentJSON struct {
	ID       string   `json:"id"`
	Market   string   `json:"market"`
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Currency Currency `json:"currency"`
}
type accountView struct {
	accountJSON
	CurrentHoldingsInput string `json:"current_holdings_input"`
}

func viewAccount(a AccountInfo) accountView { return accountView{publicAccount(a), "manual_snapshot"} }

type listJSON[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func positiveInteger(s string) (int64, error) {
	if len(s) == 0 || len(s) > 19 {
		return 0, ErrQuery
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrQuery
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, ErrQuery
	}
	return n, nil
}

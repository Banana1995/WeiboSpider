package ledger

import "strconv"

// HTTP types are deliberately separate from persisted Record/Operation JSON.
// Changing the latter would invalidate existing revision and receipt snapshots.
type operationInput struct {
	ID           string   `json:"id"`
	Date         string   `json:"date"`
	Sequence     string   `json:"sequence"`
	Kind         Kind     `json:"kind"`
	AccountID    string   `json:"account_id"`
	ToAccountID  string   `json:"to_account_id,omitempty"`
	InstrumentID string   `json:"instrument_id,omitempty"`
	Amount       Money    `json:"amount"`
	Quantity     Quantity `json:"quantity"`
	Price        Price    `json:"price"`
	Fee          *Money   `json:"fee"`
	FX           *fxJSON  `json:"fx"`
	CycleID      string   `json:"cycle_id,omitempty"`
}

type fxJSON struct {
	Rate      Rate   `json:"rate"`
	Date      string `json:"date"`
	Source    string `json:"source"`
	FetchedAt string `json:"fetched_at"`
}

func (input operationInput) operation() (Operation, error) {
	sequence, err := positiveInteger(input.Sequence)
	if err != nil || !validID(input.ID) || !validID(input.AccountID) {
		return Operation{}, ErrOperation
	}
	o := Operation{ID: input.ID, Date: input.Date, Sequence: sequence, Kind: input.Kind, AccountID: input.AccountID,
		ToAccountID: input.ToAccountID, InstrumentID: input.InstrumentID, Amount: input.Amount, Quantity: input.Quantity,
		Price: input.Price, Fee: input.Fee, CycleID: input.CycleID}
	if input.FX != nil {
		o.FX = &FXSnapshot{Rate: input.FX.Rate, Date: input.FX.Date, Source: input.FX.Source, FetchedAt: input.FX.FetchedAt}
	}
	return o, nil
}

type operationJSON struct {
	operationInput
	Voided bool `json:"voided"`
}

type recordJSON struct {
	Operation operationJSON `json:"operation"`
	Note      string        `json:"note"`
	Version   string        `json:"version"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
}

func publicRecord(r Record) recordJSON {
	o := r.Operation
	input := operationInput{ID: o.ID, Date: o.Date, Sequence: strconv.FormatInt(o.Sequence, 10), Kind: o.Kind,
		AccountID: o.AccountID, ToAccountID: o.ToAccountID, InstrumentID: o.InstrumentID,
		Amount: o.Amount, Quantity: o.Quantity, Price: o.Price, Fee: o.Fee, CycleID: o.CycleID}
	if o.FX != nil {
		input.FX = &fxJSON{Rate: o.FX.Rate, Date: o.FX.Date, Source: o.FX.Source, FetchedAt: o.FX.FetchedAt}
	}
	return recordJSON{Operation: operationJSON{operationInput: input, Voided: o.Voided}, Note: r.Note,
		Version: strconv.FormatInt(r.Version, 10), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

type openingPositionJSON struct {
	InstrumentID string   `json:"instrument_id"`
	Quantity     Quantity `json:"quantity"`
	Cost         *Money   `json:"cost"`
	DilutedBasis *Money   `json:"diluted_basis"`
}

type accountJSON struct {
	AccountingMode string   `json:"accounting_mode"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Currency       Currency `json:"currency"`
	OpeningDate    string   `json:"opening_date"`
	OpeningCash    *Money   `json:"opening_cash"`
	Version        string   `json:"version"`
}

func publicAccount(a AccountInfo) accountJSON {
	result := accountJSON{ID: a.ID, Name: a.Name, Currency: a.Currency, OpeningDate: a.OpeningDate,
		AccountingMode: a.AccountingMode, OpeningCash: &a.OpeningCash, Version: strconv.FormatInt(a.Version, 10)}
	if a.AccountingMode == "reported" {
		result.OpeningCash = nil
	}
	return result
}

type instrumentJSON struct {
	ID       string   `json:"id"`
	Market   string   `json:"market"`
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Currency Currency `json:"currency"`
}

// Read capabilities are separate from immutable accounting metadata and receipts.
type accountView struct {
	accountJSON
	CurrentHoldingsInput string `json:"current_holdings_input"`
}

func viewAccount(a AccountInfo) accountView {
	input := "transaction_replay"
	if a.AccountingMode == "reported" {
		input = "manual_snapshot"
	}
	return accountView{publicAccount(a), input}
}

type positionJSON struct {
	InstrumentID   string   `json:"instrument_id"`
	CycleID        string   `json:"cycle_id"`
	Quantity       Quantity `json:"quantity"`
	RemainingCost  *Money   `json:"remaining_cost"`
	MovingAverage  *Price   `json:"moving_average"`
	DilutedBasis   *Money   `json:"diluted_basis"`
	DilutedCost    *Price   `json:"diluted_cost"`
	RealizedProfit *Money   `json:"realized_profit"`
	Dividends      Money    `json:"dividends"`
}

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

package ledger

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Currency string

const (
	CNY Currency = "CNY"
	HKD Currency = "HKD"
	USD Currency = "USD"
)

func (c Currency) valid() bool { return c == CNY || c == HKD || c == USD }

type Instrument struct {
	ID       string
	Market   string
	Code     string
	Name     string
	Currency Currency
}

type OpeningPosition struct {
	InstrumentID string
	Quantity     Quantity
	Cost         *Money // Remaining book cost in the account currency; nil means unknown.
	DilutedBasis *Money // Independent of remaining cost for a migrated position.
}

type Opening struct {
	AccountID string
	Currency  Currency
	Date      string
	Cash      Money
	Positions []OpeningPosition
}

type Kind string

const (
	Deposit      Kind = "deposit"
	Withdrawal   Kind = "withdrawal"
	Buy          Kind = "buy"
	Sell         Kind = "sell"
	DepositBuy   Kind = "deposit_buy"
	SellWithdraw Kind = "sell_withdraw"
	Dividend     Kind = "dividend"
	Transfer     Kind = "transfer"
)

type FXSnapshot struct {
	Rate      Rate
	Date      string
	Source    string
	FetchedAt string
}

// Operation is a business fact, not an HTTP request or a persistence envelope.
// Amount is account-currency cash, except Dividend which uses instrument currency.
// Fee uses instrument currency; nil is distinct from an explicitly entered zero.
type Operation struct {
	ID           string
	Date         string
	Sequence     int64
	Kind         Kind
	AccountID    string
	ToAccountID  string
	InstrumentID string
	Amount       Money
	Quantity     Quantity
	Price        Price
	Fee          *Money
	FX           *FXSnapshot
	CycleID      string
	Voided       bool
}

type Cycle struct {
	ID             string
	InstrumentID   string
	Quantity       Quantity
	RemainingCost  *Money
	DilutedBasis   *Money
	RealizedProfit *Money // Price gains after allocated cost and sell fees; excludes dividends.
	Dividends      Money
}

func (c Cycle) MovingAverage() (*Price, error) { return c.unitCost(c.RemainingCost) }
func (c Cycle) DilutedCost() (*Price, error)   { return c.unitCost(c.DilutedBasis) }

func (c Cycle) unitCost(basis *Money) (*Price, error) {
	if c.Quantity == 0 || basis == nil {
		return nil, nil
	}
	price, err := UnitCost(*basis, c.Quantity)
	if err != nil {
		return nil, err
	}
	return &price, nil
}

type AccountState struct {
	Currency  Currency
	Cash      Money
	Positions map[string]string // Instrument ID -> latest cycle ID, including closed positions.
	Cycles    map[string]*Cycle // Retains closed cycles for explicitly attributed late dividends.
}

// Movement separates cash balance effects from account-boundary capital flows.
// Transfer legs share OperationID; group calculations must consider both members.
type Movement struct {
	OperationID  string
	Date         string
	AccountID    string
	Kind         Kind
	CashDelta    Money
	CapitalFlow  Money
	FeeProvided  bool
	Counterparty string
}

type Book struct {
	Accounts  map[string]*AccountState
	Movements []Movement
}

var (
	ErrOperation         = errors.New("invalid ledger operation")
	ErrInsufficientCash  = errors.New("insufficient cash")
	ErrInsufficientStock = errors.New("insufficient position")
	ErrUnsupported       = errors.New("unsupported ledger operation")
)

type OperationError struct {
	ID   string
	Date string
	Err  error
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("operation %s on %s: %v", e.ID, e.Date, e.Err)
}
func (e *OperationError) Unwrap() error { return e.Err }

func validDate(s string) bool {
	d, err := time.Parse(time.DateOnly, s)
	return err == nil && len(s) == 10 && d.Year() > 0
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validText(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && len(s) <= 512
}

func OpeningCycleID(accountID, instrumentID string) string {
	return "opening:" + accountID + ":" + instrumentID
}

func copyMoney(v *Money) *Money {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

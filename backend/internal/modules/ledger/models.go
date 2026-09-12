package ledger

import (
	"errors"
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

type AccountState struct {
	Currency Currency
	Cash     Money
}

var (
	ErrOperation   = errors.New("invalid ledger input")
	ErrUnsupported = errors.New("unsupported ledger input")
)

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
func copyMoney(v *Money) *Money {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

package liquor

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	SourceID   = "sina_jiujia"
	PriceBasis = "terminal_retail_weighted_mean"
	Unit       = "\u5143/\u74f6"
)

var (
	ErrSourceData = errors.New("invalid source data")
	ErrQuery      = errors.New("invalid query")
	ErrNotFound   = errors.New("product not found")
	ErrRunning    = errors.New("sync already running")
)

type ProductID int64
type Cents int64

type Product struct {
	ID             ProductID `json:"id"`
	Name           string    `json:"name"`
	Specifications string    `json:"specifications"`
	Unit           string    `json:"unit"`
	Sort           int       `json:"sort"`
}

type Point struct {
	Date      string `json:"price_date"`
	Price     Cents  `json:"price_cents"`
	Change    Cents  `json:"change_cents"`
	FetchedAt string `json:"fetched_at"`
}

type Series struct {
	Product Product
	Prices  []Point
}

type Snapshot struct {
	Date   string
	Series []Series
}

type Quote struct {
	Product
	Point
}

type Latest struct {
	Source string  `json:"source"`
	Basis  string  `json:"price_basis"`
	Date   string  `json:"price_date"`
	Items  []Quote `json:"items"`
}

type History struct {
	Source  string  `json:"source"`
	Basis   string  `json:"price_basis"`
	Product Product `json:"product"`
	Items   []Point `json:"items"`
}

type HistoryQuery struct {
	ID    ProductID
	From  string
	To    string
	Limit int
}

func ParseHistoryQuery(id string, values map[string][]string) (HistoryQuery, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return HistoryQuery{}, fmt.Errorf("%w: product id", ErrQuery)
	}
	q := HistoryQuery{ID: ProductID(n), From: "0001-01-01", To: "9999-12-31", Limit: 30}
	for key, v := range values {
		if len(v) != 1 {
			return HistoryQuery{}, ErrQuery
		}
		switch key {
		case "from", "to":
			if !validDate(v[0]) {
				return HistoryQuery{}, fmt.Errorf("%w: date", ErrQuery)
			}
			if key == "from" {
				q.From = v[0]
			} else {
				q.To = v[0]
			}
		case "limit":
			limit, err := strconv.Atoi(v[0])
			if err != nil || limit < 1 || limit > 366 {
				return HistoryQuery{}, fmt.Errorf("%w: limit must be 1..366", ErrQuery)
			}
			q.Limit = limit
		default:
			return HistoryQuery{}, fmt.Errorf("%w: unknown parameter", ErrQuery)
		}
	}
	if q.From > q.To {
		return HistoryQuery{}, fmt.Errorf("%w: reversed date range", ErrQuery)
	}
	return q, nil
}

func validDate(value string) bool {
	date, err := time.Parse(time.DateOnly, value)
	return err == nil && len(value) == 10 && date.Year() > 0
}

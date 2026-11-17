// Package options implements manual cash-secured put and covered call screening.
package options

import (
	"errors"
	"regexp"
	"time"
)

const CalculationVersion = "option-premium-act365-v2"

// Existing snapshots, settings and requests without a side retain PUT semantics.
func optionSide(side string) string {
	if side == "" {
		return "PUT"
	}
	return side
}
func validSide(side string) bool { return optionSide(side) == "PUT" || side == "CALL" }
func sideLabel(side string) string {
	if side == "CALL" {
		return "Call"
	}
	return "Put"
}

var (
	ErrInput      = errors.New("字段格式或数值不正确")
	ErrNotFound   = errors.New("记录不存在，或行情快照已清理，请重新读取")
	ErrVersion    = errors.New("设置已被修改，请重新读取后保存")
	ErrBusy       = errors.New("已有行情任务正在运行，请等待完成")
	ErrLimit      = errors.New("已达到数量上限")
	ErrReplay     = errors.New("请求编号已用于不同的内容")
	symbolPattern = regexp.MustCompile(`^US\.[A-Z][A-Z0-9.-]{0,14}$`)
	idPattern     = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,100}$`)
)

type Instrument struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	EnglishName string `json:"english_name"`
	Aliases     string `json:"aliases"`
	Kind        string `json:"kind"`
	Currency    string `json:"currency"`
}

// Empty filter strings mean no constraint. Values are decimal strings, never floats.
type Filter struct {
	MinDays      string `json:"min_days"`
	MaxDays      string `json:"max_days"`
	MaxStrike    string `json:"max_strike"`
	MinStrike    string `json:"min_strike"`
	MinSalePrice string `json:"min_sale_price"`
	MaxCost      string `json:"max_cost"`
	MinAPR       string `json:"min_apr"`
	MinOI        string `json:"min_oi"`
	MinVolume    string `json:"min_volume"`
	MaxSpread    string `json:"max_spread"`
	MaxDelta     string `json:"max_delta"`
	Budget       string `json:"budget"`
	Moneyness    string `json:"moneyness"`
}

type Watch struct {
	Instrument
	Enabled    bool   `json:"enabled"`
	Version    int64  `json:"version"`
	Filter     Filter `json:"filter"`
	CallFilter Filter `json:"call_filter"`
}

// Each explicitly supplied component is known; a nil profile means fees unknown.
type FeeRule struct {
	PerContract string `json:"per_contract"`
	Minimum     string `json:"minimum"`
	Fixed       string `json:"fixed"`
	Regulatory  string `json:"regulatory_per_contract"`
}
type FeeProfile struct {
	Name       string   `json:"name"`
	Source     string   `json:"source"`
	Open       FeeRule  `json:"open"`
	Close      *FeeRule `json:"close"`
	Assignment *FeeRule `json:"assignment"`
}
type Settings struct {
	Version     int64       `json:"version"`
	Mode        string      `json:"mode"`
	Fees        *FeeProfile `json:"fees"`
	Filter      Filter      `json:"filter"`
	CallFilter  Filter      `json:"call_filter"`
	HorizonDays int         `json:"horizon_days"`
}

func DefaultSettings() Settings { return Settings{Version: 1, Mode: "full", HorizonDays: 365} }

type Contract struct {
	Code       string `json:"code"`
	Symbol     string `json:"symbol"`
	Name       string `json:"name"`
	Expiry     string `json:"expiry"`
	Strike     string `json:"strike"`
	Multiplier int    `json:"multiplier"`
	Type       string `json:"type"`
	Standard   string `json:"standard"`
	Settlement string `json:"settlement"`
	Currency   string `json:"currency"`
}
type Quote struct {
	Contract    Contract `json:"contract"`
	Bid         *string  `json:"bid"`
	Ask         *string  `json:"ask"`
	BidSize     *string  `json:"bid_size"`
	AskSize     *string  `json:"ask_size"`
	Volume      *string  `json:"volume"`
	OI          *string  `json:"oi"`
	IV          *string  `json:"iv"`
	Delta       *string  `json:"delta"`
	LastPrice   *string  `json:"last_price"`
	LastTradeAt *string  `json:"last_trade_at"`
	QuoteAt     *string  `json:"quote_at"`
	Status      string   `json:"status"`
}
type Snapshot struct {
	ID             string  `json:"id"`
	OptionType     string  `json:"option_type,omitempty"`
	Symbol         string  `json:"symbol"`
	Name           string  `json:"name"`
	Source         string  `json:"source"`
	StartedAt      string  `json:"started_at"`
	CapturedAt     string  `json:"captured_at"`
	ValuationDate  string  `json:"valuation_date_et"`
	MarketState    string  `json:"market_state"`
	Underlying     *string `json:"underlying"`
	From           string  `json:"from"`
	To             string  `json:"to"`
	Complete       bool    `json:"complete"`
	TotalContracts int     `json:"total_contracts"`
	Warning        string  `json:"warning"`
	Quotes         []Quote `json:"quotes,omitempty"`
}
type InterestInput struct {
	Principal  string `json:"principal"`
	AnnualRate string `json:"annual_rate"`
	Days       int    `json:"days"`
}
type ScenarioInput struct {
	Kind      string  `json:"kind"` // expiry or close
	Price     string  `json:"price"`
	Fee       *string `json:"fee"`
	Interest  *string `json:"interest"`
	StockCost *string `json:"stock_cost,omitempty"` // explicit stock cost assumption, covered expiry only
}
type Input struct {
	Quantity    int            `json:"quantity"`
	Basis       string         `json:"basis"`
	Price       *string        `json:"price"`
	Mode        string         `json:"mode"`
	FeeVersion  int64          `json:"fee_version"` // 0 means unknown
	FeeOverride *string        `json:"fee_override"`
	Interest    *InterestInput `json:"interest"`
	Scenario    *ScenarioInput `json:"scenario"`
}
type Calculation struct {
	Version               string  `json:"version"`
	Status                string  `json:"status"`
	DTE                   int     `json:"dte"`
	Price                 *string `json:"price"`
	Gross                 *string `json:"gross"`
	Intrinsic             *string `json:"intrinsic"`
	TimeValue             *string `json:"time_value"`
	Fee                   *string `json:"fee"`
	Net                   *string `json:"net"`
	Income                *string `json:"income"`
	Capital               *string `json:"capital"`
	Base                  *string `json:"base"`
	Rate                  *string `json:"rate"`
	APR                   *string `json:"apr"`
	Cost                  *string `json:"cost"`
	Interest              *string `json:"interest"`
	CombinedAPR           *string `json:"combined_apr"`
	ScenarioPnL           *string `json:"scenario_pnl"`
	ScenarioFeesKnown     bool    `json:"scenario_fees_known"`
	ScenarioInterestKnown bool    `json:"scenario_interest_known"`
	CapitalBasis          string  `json:"capital_basis,omitempty"`
	CoveredShares         *string `json:"covered_shares,omitempty"`
	StrikeProceeds        *string `json:"strike_proceeds,omitempty"`
	NetSalePrice          *string `json:"net_sale_price,omitempty"`
	CoveredPnL            *string `json:"covered_pnl,omitempty"`
}
type Candidate struct {
	Snapshot       Snapshot    `json:"snapshot"`
	Quote          Quote       `json:"quote"`
	Input          Input       `json:"input"`
	Result         Calculation `json:"result"`
	FavoriteID     string      `json:"favorite_id,omitempty"`
	BudgetQuantity *string     `json:"budget_quantity"`
	Stale          bool        `json:"stale"`
}
type Favorite struct {
	ID            string      `json:"id"`
	CreatedAt     string      `json:"created_at"`
	FormatVersion int         `json:"format_version"`
	Candidate     Candidate   `json:"candidate"`
	FeeProfile    *FeeProfile `json:"fee_profile"`
}
type Target struct {
	Symbol     string `json:"symbol"`
	State      string `json:"state"`
	Progress   string `json:"progress"`
	Error      string `json:"error,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
}
type Run struct {
	ID         string   `json:"id"`
	OptionType string   `json:"option_type,omitempty"`
	State      string   `json:"state"`
	CreatedAt  string   `json:"created_at"`
	FinishedAt string   `json:"finished_at,omitempty"`
	Targets    []Target `json:"targets"`
}
type ProviderState struct {
	Connected   bool   `json:"connected"`
	Version     string `json:"version"`
	MarketState string `json:"market_state"`
	USOptions   string `json:"us_options"`
	CheckedAt   string `json:"checked_at"`
	Error       string `json:"error,omitempty"`
}
type Limits struct {
	MaxWatch     int
	MaxContracts int
	Timeout      time.Duration
	DirectoryTTL time.Duration
	HistoryBytes int64
}

func DefaultLimits() Limits { return Limits{20, 5000, 20 * time.Minute, 24 * time.Hour, 256 << 20} }

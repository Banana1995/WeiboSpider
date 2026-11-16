package tax

import (
	"math/big"
	"strings"
)

type cashClass int

const (
	cashTransfer cashClass = iota
	cashDividend
	cashInterest
	cashWithholding
	cashFee
)

// A blank reason does not erase the explicit income/tax facts in a broker memo.
// In particular, "Interest In Jan." is a financing charge when it is an OUT.
func classifyCash(r Record) cashClass {
	reason, note := strings.TrimSpace(r.Reason), strings.ToUpper(strings.TrimSpace(r.Note))
	for _, marker := range []string{"WITHHOLDING TAX", "W/TAX", "DIVIDEND TAX", "股息税", "股息稅", "红利税", "紅利稅", "预扣税", "預扣稅"} {
		if strings.Contains(strings.ToUpper(reason), marker) || strings.Contains(note, marker) {
			return cashWithholding
		}
	}
	switch reason {
	case "股息收入", "股息紅利", "股息红利":
		return cashDividend
	case "利息收入", "利息收益", "存款利息", "债券利息", "債券利息":
		return cashInterest
	case "银证转账", "銀證轉賬":
		return cashTransfer
	case "":
		if strings.Contains(note, "SHARES DIVIDENDS") {
			return cashDividend
		}
		if strings.Contains(note, "INTEREST INCOME") || strings.Contains(note, "CREDIT INTEREST") || strings.Contains(note, "利息收入") ||
			(strings.HasPrefix(note, "INTEREST IN ") && number(r.Change).Sign() > 0) {
			return cashInterest
		}
		return cashTransfer
	default:
		return cashFee
	}
}

type incomeKey struct{ account, year, symbol string }
type incomeGroup struct {
	annual        *amounts
	dividend, tax *big.Rat
	taxRows       int
}
type incomeGroups map[incomeKey]*incomeGroup

func (groups incomeGroups) add(r Record, annual *amounts, withholding bool) {
	// HKD dividends use the imported statement amount at 20%, as agreed. An
	// embedded (-10%)/(NET) memo is neither grossed up nor credited a second time.
	if r.Currency != "USD" {
		return
	}
	r.Note = strings.ToUpper(strings.TrimSpace(r.Note))
	key := incomeKey{r.AccountID, r.Date[:4], quoteSymbol(r)}
	g := groups[key]
	if g == nil {
		g = &incomeGroup{annual: annual, dividend: zero(), tax: zero()}
		groups[key] = g
	}
	if withholding {
		g.tax.Sub(g.tax, number(r.Change))
		g.taxRows++
	} else {
		g.dividend.Add(g.dividend, number(r.Change))
	}
}

func (groups incomeGroups) applyCredits() {
	for key, g := range groups {
		if g.taxRows == 0 || g.tax.Sign() == 0 {
			continue
		}
		if !strings.HasPrefix(key.symbol, "us") || g.dividend.Sign() <= 0 || g.tax.Sign() < 0 {
			g.annual.flagIncomeTax("存在未匹配同账户、同年度股息的美元扣税或退税，请核对完整流水。")
			continue
		}
		// This tool handles ordinary US dividend withholding, not treaty excess
		// (e.g. 30%) or ADR/special-distribution tax eligibility. Allow the broker's
		// per-row cent rounding without replacing the recorded payment by 10%.
		limit := add(mul(g.dividend, number("0.1")), mul(new(big.Rat).SetInt64(int64(g.taxRows)), number("0.005")))
		if g.tax.Cmp(limit) > 0 {
			g.annual.flagIncomeTax("美元预扣税超出普通美股10%口径，需核对所得来源及抵免资格。")
			continue
		}
		credit := new(big.Rat).Set(g.tax)
		ceiling := mul(g.dividend, number("0.2"))
		if credit.Cmp(ceiling) > 0 {
			credit.Set(ceiling)
		}
		g.annual.v[dividendCredit].Add(g.annual.v[dividendCredit], credit)
	}
}

func (a *amounts) flagIncomeTax(issue string) {
	if issue != "" && (a.incomeTaxIssue == "" || issue < a.incomeTaxIssue) {
		a.incomeTaxIssue = issue
	}
}

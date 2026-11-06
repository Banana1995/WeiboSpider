package ledger

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s MCPService) resolveInstrument(ctx context.Context, account string, selector MCPInstrumentSelector, allowNew bool) (*Instrument, error) {
	var found *Instrument
	err := s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		st, err := readStockState(ctx, tx, account)
		if err != nil {
			return err
		}
		for _, i := range st.instruments {
			if i.Market == selector.Market && i.Code == selector.Code {
				if found != nil {
					return ErrCorrupt
				}
				copy := i
				found = &copy
			}
		}
		if found != nil {
			return nil
		}
		if !allowNew {
			return mcpIssue("entry_not_found", "请先选择该账户已有持仓或历史证券", "select_existing", "instrument")
		}
		var i Instrument
		err = tx.QueryRowContext(ctx, `SELECT id,market,code,name,currency FROM instruments WHERE market=? AND code=? ORDER BY id LIMIT 1`, selector.Market, selector.Code).Scan(&i.ID, &i.Market, &i.Code, &i.Name, &i.Currency)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, code := quoteSymbol(i); code != "" || !validID(i.ID) || !validText(i.Name) {
			return ErrCorrupt
		}
		found = &i
		return nil
	})
	if err != nil || found != nil {
		return found, err
	}
	if s.Instruments == nil {
		return nil, mcpIssue("instrument_unavailable", "暂时无法验证新证券身份", "retry_same_request")
	}
	items, err := s.Instruments.Search(ctx, strings.ToLower(selector.Market)+selector.Code)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, mcpIssue("instrument_unavailable", "暂时无法验证新证券身份", "retry_same_request")
	}
	for _, item := range items {
		if item.Market != selector.Market || item.Code != selector.Code {
			continue
		}
		i := Instrument{ID: "mcp-instrument-" + receiptDigest(item.Market+"/"+item.Code), Market: item.Market, Code: item.Code, Name: item.Name, Currency: item.Currency}
		if _, code := quoteSymbol(i); code != "" || !validInstrumentSearchName(i.Name) {
			return nil, mcpIssue("instrument_unavailable", "证券身份响应不合法", "retry_same_request")
		}
		if found != nil {
			return nil, mcpIssue("instrument_ambiguous", "证券身份不能唯一确定", "clarify_identity", "instrument")
		}
		found = &i
	}
	if found == nil {
		return nil, mcpIssue("instrument_unavailable", "未找到该市场和代码的受支持证券", "retry_same_request")
	}
	return found, nil
}

type MCPInstrumentQuery struct {
	Query  string `json:"query"`
	Market string `json:"market,omitempty"`
}

type MCPInstrumentResult struct {
	Items  []InstrumentSearchItem `json:"items"`
	Source string                 `json:"source"`
}

func (s MCPService) SearchInstruments(ctx context.Context, q MCPInstrumentQuery) (MCPInstrumentResult, error) {
	out := MCPInstrumentResult{Items: []InstrumentSearchItem{}, Source: "public_market_data"}
	if !validInstrumentSearchQuery(q.Query) || q.Market != "" && q.Market != "SH" && q.Market != "SZ" && q.Market != "HK" {
		return out, mcpInvalid("query", "market")
	}
	// Exact code queries can use existing immutable identities while offline.
	market, code, codeErr := instrumentSearchCode(q.Query)
	if codeErr == nil {
		if market != "" && q.Market != "" && market != q.Market {
			return out, mcpInvalid("query", "market")
		}
		if market == "" {
			market = q.Market
		}
		rows, err := s.Store.db.QueryContext(ctx, `SELECT i.market,i.code,i.name,i.currency FROM instruments i JOIN
			(SELECT min(id) AS id FROM instruments WHERE code=? AND (?='' OR market=?) GROUP BY market,code) chosen ON chosen.id=i.id ORDER BY i.market LIMIT 8`, code, market, market)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var i InstrumentSearchItem
			if err = rows.Scan(&i.Market, &i.Code, &i.Name, &i.Currency); err != nil {
				break
			}
			if _, invalid := quoteSymbol(Instrument{Market: i.Market, Code: i.Code, Currency: i.Currency}); invalid == "" {
				out.Items = append(out.Items, i)
			}
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return out, err
		}
		if len(out.Items) > 0 {
			out.Source = "ledger_catalog"
			return out, nil
		}
	}
	if s.Instruments == nil {
		return out, mcpIssue("instrument_unavailable", "证券搜索暂不可用", "retry_same_request")
	}
	items, err := s.Instruments.Search(ctx, q.Query)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, mcpIssue("instrument_unavailable", "证券搜索暂不可用", "retry_same_request")
	}
	for _, i := range items {
		if q.Market != "" && q.Market != i.Market {
			continue
		}
		if codeErr == nil && (i.Code != code || market != "" && i.Market != market) {
			continue
		}
		if _, invalid := quoteSymbol(Instrument{Market: i.Market, Code: i.Code, Currency: i.Currency}); invalid == "" && validInstrumentSearchName(i.Name) {
			out.Items = append(out.Items, i)
		}
		if len(out.Items) == maxInstrumentSearchResults {
			break
		}
	}
	return out, nil
}

package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

type AccountEntry struct {
	Kind        string `json:"kind"`
	Date        string `json:"date"`
	Flow        *Money `json:"flow"`
	TotalAssets *Money `json:"total_assets"`
	Note        string `json:"note"`
}
type AccountRecord struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	AccountEntry
	Sequence        string               `json:"sequence,omitempty"`
	Origin          string               `json:"origin"`
	Original        *ImportedRow         `json:"original"`
	Version         string               `json:"version"`
	Voided          bool                 `json:"voided"`
	CreatedAt       string               `json:"created_at"`
	UpdatedAt       string               `json:"updated_at"`
	QuoteAuditID    string               `json:"quote_audit_id,omitempty"`
	ManualAssertion bool                 `json:"manual_assertion,omitempty"`
	CarriedFrom     *AccountRecordSource `json:"carried_from,omitempty"`
}
type AccountRecordSource struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	Sequence        string `json:"sequence"`
	Version         string `json:"version"`
	Date            string `json:"date"`
	Origin          string `json:"origin"`
	TotalAssets     Money  `json:"total_assets"`
	ManualAssertion bool   `json:"manual_assertion,omitempty"`
}
type AccountRecordRevision struct {
	Record AccountRecord `json:"record"`
	Reason string        `json:"reason"`
}
type AccountRecordCommand struct {
	Action          WriteAction   `json:"action"`
	AccountID       string        `json:"account_id"`
	ID              string        `json:"id"`
	Entry           *AccountEntry `json:"entry"`
	ExpectedVersion string        `json:"expected_version"`
	Reason          string        `json:"reason"`
}
type ReportedAccountInput struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Currency    Currency `json:"currency"`
	OpeningDate string   `json:"opening_date"`
}

func (e AccountEntry) valid() bool {
	if !validDate(e.Date) || !utf8.ValidString(e.Note) || len(e.Note) > 16384 || e.TotalAssets != nil && *e.TotalAssets < 0 {
		return false
	}
	switch e.Kind {
	case "asset":
		return e.TotalAssets != nil && e.Flow == nil
	case "cash_flow":
		return e.Flow != nil
	case "log":
		return e.TotalAssets == nil && e.Flow == nil && strings.TrimSpace(e.Note) != ""
	}
	return false
}

func validAccountRecordOrigin(origin string) bool {
	switch origin {
	case "import", "manual", "currentrefresh", "weekly", "weekly_carry":
		return true
	}
	return false
}

func (s AccountRecordSource) valid(accountID, date string) bool {
	if !validID(s.ID) || s.AccountID != accountID || !validDate(s.Date) || s.Date > date || s.TotalAssets < 0 || !validAccountRecordOrigin(s.Origin) {
		return false
	}
	if _, err := positiveInteger(s.Sequence); err != nil {
		return false
	}
	if _, err := positiveInteger(s.Version); err != nil {
		return false
	}
	return s.Origin != "weekly_carry" || s.ManualAssertion
}

func (r AccountRecord) validProvenance() bool {
	if !validAccountRecordOrigin(r.Origin) {
		return false
	}
	if r.CarriedFrom == nil {
		return r.Origin != "weekly_carry"
	}
	return r.Origin == "weekly_carry" && r.Kind == "asset" && r.TotalAssets != nil && r.QuoteAuditID != "" &&
		r.CarriedFrom.valid(r.AccountID, r.Date) && (r.ManualAssertion || *r.TotalAssets == r.CarriedFrom.TotalAssets)
}

// The receipt and mutation share one transaction. A replay is checked against its
// immutable audit event and the still-present current entity before it is returned.
func (s *Store) accountReceipt(ctx context.Context, key, kind string, intent any, write func(*sql.Tx) (any, int64, error)) (json.RawMessage, error) {
	if !validID(key) {
		return nil, ErrOperation
	}
	request, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	var response string
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		receipt, found, err := loadReceipt(ctx, tx, key, kind, string(request))
		if err != nil {
			return err
		}
		if found {
			response = receipt.Response
			return validateAccountReceipt(ctx, tx, intent, response, key, receipt.AuditID)
		}
		result, auditID, err := write(tx)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		response = string(encoded)
		return saveReceipt(ctx, tx, key, kind, string(request), response, auditID)
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(response), nil
}

func (s *Store) CreateReportedAccount(ctx context.Context, key string, input ReportedAccountInput) (json.RawMessage, error) {
	if !validID(input.ID) || !validText(input.Name) || strings.TrimSpace(input.Name) == "" || !input.Currency.valid() || !validDate(input.OpeningDate) {
		return nil, ErrOperation
	}
	return s.accountReceipt(ctx, key, "reported_account", reportedAccountIntent{"reported-account", input}, func(tx *sql.Tx) (any, int64, error) {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, input.ID).Scan(&exists); err != nil {
			return nil, 0, err
		}
		if exists {
			return nil, 0, ErrConflict
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return nil, 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO accounts(id,name,currency,opening_date,opening_cash_minor,version) VALUES(?,?,?,?,0,1)`, input.ID, input.Name, input.Currency, input.OpeningDate); err != nil {
			return nil, 0, constraintError(err)
		}
		result := accountJSON{ID: input.ID, Name: input.Name, Currency: input.Currency, OpeningDate: input.OpeningDate, Version: "1"}
		auditID, err := appendAudit(ctx, tx, key, "create", "account", input.ID, input.ID, 1, stamp, "human", nil, result, nil)
		return result, auditID, err
	})
}

func scanAccountRecord(row interface{ Scan(...any) error }) (AccountRecord, error) {
	var r AccountRecord
	var payload string
	var date, stamp, sequence string
	var kind, note, origin, updated string
	var flow, assets, version sql.NullInt64
	var quote sql.NullString
	var voided, assertion, audited bool
	if err := row.Scan(&r.AccountID, &r.ID, &date, &payload, &stamp, &sequence, &kind, &flow, &assets, &note, &origin, &voided, &version, &updated, &quote, &assertion, &audited); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	id, account := r.ID, r.AccountID
	if decodeReceipt(payload, &r) != nil || r.ID != id || r.AccountID != account || r.Date != date || r.Sequence != sequence {
		return r, ErrCorrupt
	}
	if !audited || !r.AccountEntry.valid() || !r.validProvenance() || r.Kind != kind || r.Note != note || r.Origin != origin || r.Voided != voided || r.Version != strconv.FormatInt(version.Int64, 10) || r.CreatedAt != stamp || r.UpdatedAt != updated || r.QuoteAuditID != quote.String || r.ManualAssertion != assertion ||
		(r.Flow != nil) != flow.Valid || (r.TotalAssets != nil) != assets.Valid || r.Flow != nil && int64(*r.Flow) != flow.Int64 || r.TotalAssets != nil && int64(*r.TotalAssets) != assets.Int64 {
		return r, ErrCorrupt
	}
	return r, nil
}

const accountRecordSelect = `SELECT account_id,id,business_date,payload,created_at,CAST(sequence AS TEXT) AS stable_sequence,kind,flow_minor,total_assets_minor,note,origin,voided,version,updated_at,CAST(quote_audit_id AS TEXT),manual_assertion,
 EXISTS(SELECT 1 FROM audit_log a WHERE a.entity_type='account_record' AND a.account_id=account_records.account_id AND a.entity_id=account_records.id AND a.version=account_records.version AND a.after_json=account_records.payload) FROM account_records`

func (s *Store) WriteAccountRecord(ctx context.Context, key string, c AccountRecordCommand) (json.RawMessage, error) {
	if c.Entry != nil {
		entry := *c.Entry
		entry.Flow, entry.TotalAssets = copyMoney(entry.Flow), copyMoney(entry.TotalAssets)
		c.Entry = &entry
	}
	if !validID(c.AccountID) || !validID(c.ID) || !utf8.ValidString(c.Reason) || len(c.Reason) > 512 {
		return nil, ErrOperation
	}
	if c.Action == CreateOperation {
		if c.ExpectedVersion != "" || !strings.HasPrefix(c.ID, "manual-") {
			return nil, ErrOperation
		}
	} else {
		if _, err := positiveInteger(c.ExpectedVersion); err != nil || strings.TrimSpace(c.Reason) == "" {
			return nil, ErrOperation
		}
	}
	if c.Action == VoidOperation {
		if c.Entry != nil {
			return nil, ErrOperation
		}
	} else if c.Action != CreateOperation && c.Action != ReplaceOperation || c.Entry == nil || !c.Entry.valid() {
		return nil, ErrOperation
	}
	return s.accountReceipt(ctx, key, "account_record", c, func(tx *sql.Tx) (any, int64, error) {
		if _, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, c.AccountID)); err != nil {
			return nil, 0, err
		}
		current, err := scanAccountRecord(tx.QueryRowContext(ctx, accountRecordSelect+` WHERE account_id=? AND id=?`, c.AccountID, c.ID))
		if c.Action == CreateOperation {
			if err == nil {
				return nil, 0, ErrConflict
			}
			if !errors.Is(err, ErrNotFound) {
				return nil, 0, err
			}
		} else {
			if err != nil {
				return nil, 0, err
			}
			if current.QuoteAuditID != "" && c.Entry != nil && c.Entry.Kind != "asset" {
				return nil, 0, ErrUnsupported
			}
			if current.Version != c.ExpectedVersion {
				return nil, 0, ErrVersion
			}
			if current.Voided {
				return nil, 0, ErrVoided
			}
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return nil, 0, err
		}
		next := current
		version := int64(1)
		if c.Action == CreateOperation {
			next = AccountRecord{ID: c.ID, AccountID: c.AccountID, Origin: "manual", CreatedAt: stamp}
		} else {
			version, err = positiveInteger(current.Version)
			if err != nil || version == 9223372036854775807 {
				return nil, 0, ErrCorrupt
			}
			version++
		}
		if c.Entry != nil {
			next.AccountEntry = *c.Entry
			if next.QuoteAuditID != "" {
				next.ManualAssertion = true
			}
		}
		if !next.validProvenance() {
			return nil, 0, ErrOperation
		}
		next.Voided = c.Action == VoidOperation
		next.Version = strconv.FormatInt(version, 10)
		next.UpdatedAt = stamp
		var previous *AccountRecord
		if c.Action != CreateOperation {
			previous = &current
		}
		auditID, err := putAccountRecord(ctx, tx, &next, previous, key, c.Reason, "human")
		return next, auditID, err
	})
}

type EffectiveSummary struct {
	RowCount         int     `json:"row_count"`
	AssetCount       int     `json:"asset_count"`
	FlowCount        int     `json:"flow_count"`
	LogCount         int     `json:"log_count"`
	VoidedCount      int     `json:"voided_count"`
	From             *string `json:"from"`
	To               *string `json:"to"`
	TotalIn          string  `json:"total_in"`
	TotalOut         string  `json:"total_out"`
	LatestAssets     *Money  `json:"latest_assets"`
	LatestAssetDate  *string `json:"latest_asset_date"`
	LatestAssetCount int     `json:"latest_asset_count"`
}

func (s *Store) EffectiveSummary(ctx context.Context, id string) (EffectiveSummary, error) {
	var summary EffectiveSummary
	today, _, err := s.cutoff()
	if err != nil {
		return summary, err
	}
	flows := map[string]*big.Int{}
	incoming, outgoing := new(big.Int), new(big.Int)
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id)); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, accountRecordSelect+` WHERE account_id=? ORDER BY business_date DESC,CAST(stable_sequence AS INTEGER) DESC`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanAccountRecord(rows)
			if err != nil {
				return err
			}
			if r.Voided {
				summary.VoidedCount++
				continue
			}
			summary.RowCount++
			date := r.Date
			if summary.To == nil {
				summary.To = &date
			}
			summary.From = &date
			switch r.Kind {
			case "asset":
				summary.AssetCount++
			case "cash_flow":
				summary.FlowCount++
			case "log":
				summary.LogCount++
			}
			if r.Flow != nil {
				n := big.NewInt(int64(*r.Flow))
				if r.Date <= today {
					if flows[r.Date] == nil {
						flows[r.Date] = new(big.Int)
					}
					flows[r.Date].Add(flows[r.Date], n)
				}
				if n.Sign() >= 0 {
					incoming.Add(incoming, n)
				} else {
					outgoing.Sub(outgoing, n)
				}
			}
			if r.Date <= today && r.TotalAssets != nil && (r.CarriedFrom == nil || r.ManualAssertion) && (summary.LatestAssetDate == nil || *summary.LatestAssetDate == r.Date) {
				summary.LatestAssetDate = &date
				summary.LatestAssetCount++
				if summary.LatestAssetCount == 1 {
					summary.LatestAssets = r.TotalAssets
				}
			}
		}
		return rows.Err()
	})
	if err == nil && summary.LatestAssets != nil {
		assets := big.NewInt(int64(*summary.LatestAssets))
		for date, net := range flows {
			if date > *summary.LatestAssetDate {
				assets.Add(assets, net)
			}
		}
		var projected Money
		raw, _ := json.Marshal(centsString(assets))
		if e := json.Unmarshal(raw, &projected); e != nil {
			return summary, e
		}
		summary.LatestAssets = &projected
	}
	format := func(n *big.Int) string {
		whole, rem := new(big.Int), new(big.Int)
		whole.QuoRem(n, big.NewInt(100), rem)
		return whole.String() + "." + fmtTwo(rem.Int64())
	}
	summary.TotalIn = format(incoming)
	summary.TotalOut = format(outgoing)
	return summary, err
}
func fmtTwo(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

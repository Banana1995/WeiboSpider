package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"sort"
	"strconv"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type BasisPoint struct {
	Date          string            `json:"date"`
	Sequence      string            `json:"sequence"`
	RecordID      string            `json:"record_id"`
	Version       string            `json:"version"`
	Flow          *Money            `json:"flow"`
	Assets        *Money            `json:"assets"`
	Status        string            `json:"status"`
	SourceID      string            `json:"source_id"`
	SourceVersion string            `json:"source_version"`
	SourceDate    string            `json:"source_date"`
	Selected      bool              `json:"selected"`
	Record        *AccountRecord    `json:"record,omitempty"`
	Valuation     *ValuationSummary `json:"valuation,omitempty"`
}

type BasisChange struct {
	Revision      string  `json:"revision"`
	SourceID      string  `json:"source_id"`
	SourceVersion string  `json:"source_version"`
	From          string  `json:"from"`
	To            *string `json:"to"` // nil: downstream dependencies are potentially unbounded.
	Reason        string  `json:"reason"`
}

type AnalysisBasis struct {
	AccountID             string        `json:"account_id"`
	Currency              Currency      `json:"currency"`
	From                  string        `json:"from"`
	To                    string        `json:"to"`
	Timezone              string        `json:"timezone"`
	Revision              string        `json:"revision"`
	ChangeRevision        string        `json:"change_revision"`
	Points                []BasisPoint  `json:"points"`
	Opening               *BasisPoint   `json:"opening"`
	Closing               *BasisPoint   `json:"closing"`
	NetFlow               string        `json:"net_flow"`
	Changes               []BasisChange `json:"changes"`
	PreviousBasisAffected bool          `json:"previous_basis_affected"`
	Status                string        `json:"status"`
	Returns               Returns       `json:"returns"`
	twrHistory            []BasisPoint  // Pre-range facts needed only for the temporary TWR opening estimate.
}

// AnalysisBasis is a fresh, consistent projection, never a historical-price replay.
// since is the account's last seen account-record audit ID.
func (s *Store) AnalysisBasis(ctx context.Context, id, from, to string, since int64) (AnalysisBasis, error) {
	requestedFrom, requestedTo := from, to
	out := AnalysisBasis{AccountID: id, From: from, To: to, Timezone: "Asia/Shanghai", Points: []BasisPoint{}, Changes: []BasisChange{}, Status: "current"}
	today, _, err := s.cutoff()
	if err != nil {
		return out, err
	}
	if to == "" {
		to = today
	}
	if from == "" {
		from = "0001-01-01"
	}
	out.From, out.To = from, to
	if !validID(id) || !validDate(from) || !validDate(to) || from > to || to > today || since < 0 {
		return out, ErrQuery
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		out.Currency = info.Currency
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM audit_log
			WHERE account_id=? AND entity_type='account_record'`, id).Scan(&revision); err != nil {
			return err
		}
		if since > revision {
			return ErrQuery
		}
		out.ChangeRevision = strconv.FormatInt(revision, 10)
		rows, err := tx.QueryContext(ctx, `SELECT CAST(id AS TEXT),entity_id,CAST(version AS TEXT),
			json_extract(metadata_json,'$.from_date'),coalesce(json_extract(metadata_json,'$.reason'),action),entity_type
			FROM audit_log WHERE account_id=? AND entity_type='account_record' AND id>?
			ORDER BY id LIMIT 10001`, id, since)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c BasisChange
			var entity string
			if err := rows.Scan(&c.Revision, &c.SourceID, &c.SourceVersion, &c.From, &c.Reason, &entity); err != nil {
				rows.Close()
				return err
			}
			out.Changes = append(out.Changes, c)
			if c.From <= to && entity == "account_record" {
				out.PreviousBasisAffected = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(out.Changes) > 10000 {
			return ErrQuery
		}
		all := []BasisPoint{}
		{
			rows, err := tx.QueryContext(ctx, accountRecordSelect+` WHERE account_id=? AND business_date<=? ORDER BY business_date,CAST(stable_sequence AS INTEGER) LIMIT 10001`, id, to)
			if err != nil {
				return err
			}
			var source *BasisPoint
			count := 0
			for rows.Next() {
				count++
				r, err := scanAccountRecord(rows)
				if err != nil {
					rows.Close()
					return err
				}
				if r.Voided {
					continue
				}
				p := BasisPoint{Date: r.Date, Sequence: r.Sequence, RecordID: r.ID, Version: r.Version, Flow: r.Flow, Record: &r, Status: "unavailable"}
				if r.TotalAssets != nil {
					p.Assets = r.TotalAssets
					if r.CarriedFrom != nil && !r.ManualAssertion {
						p.Status, p.SourceID, p.SourceVersion, p.SourceDate = "carried", r.CarriedFrom.ID, r.CarriedFrom.Version, r.CarriedFrom.Date
					} else {
						p.Status, p.SourceID, p.SourceVersion, p.SourceDate = "reported", r.ID, r.Version, r.Date
					}
					copy := p
					source = &copy
				} else if r.Kind == "cash_flow" && source != nil {
					p.Assets = source.Assets
					p.Status, p.SourceID, p.SourceVersion, p.SourceDate = "carried", source.SourceID, source.SourceVersion, source.SourceDate
				} else if r.Kind == "log" {
					p.Status = "log"
				}
				all = append(all, p)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			if count > 10000 {
				return ErrQuery
			}
		}
		// Saved amounts are fixed facts. Mutable holdings and transaction history
		// are not dependencies of income or historical valuation validity.
		for i := range all {
			p := &all[i]
			r := p.Record
			if r.QuoteAuditID == "" || r.ManualAssertion || r.TotalAssets == nil || r.CarriedFrom != nil {
				continue
			}
			p.Status = "observed"
		}
		// Prefer the last explicit asset on a day, not a later flow-only carry.
		// Weekly carries are not new observations unless manually asserted.
		lastByDate := map[string]int{}
		explicitByDate := map[string]int{}
		for i := range all {
			if all[i].Status != "log" {
				lastByDate[all[i].Date] = i
			}
			r := all[i].Record
			if r.TotalAssets != nil && (r.CarriedFrom == nil || r.ManualAssertion) {
				explicitByDate[all[i].Date] = i
			}
		}
		for date, i := range explicitByDate {
			lastByDate[date] = i
		}
		for _, i := range lastByDate {
			all[i].Selected = true
		}
		// Use the same whole-day projection for display, endpoints and every
		// return metric. Never fill the raw account record's null total_assets.
		estimates, err := projectTWREstimates(ctx, AnalysisBasis{Points: all})
		if err != nil {
			return err
		}
		versions := make(map[string]string, len(all))
		for _, p := range all {
			versions[p.RecordID] = p.Version
		}
		for i := range all {
			if estimate := estimates[all[i].Date]; estimate != nil && all[i].Status == "carried" {
				var amount Money
				raw, _ := json.Marshal(estimate.Assets)
				if err := json.Unmarshal(raw, &amount); err != nil {
					return err
				}
				all[i].Assets = &amount
				all[i].SourceID, all[i].SourceDate = estimate.SourceRecordID, estimate.SourceDate
				all[i].SourceVersion = versions[estimate.SourceRecordID]
			} else if all[i].Status == "carried" {
				if index, ok := explicitByDate[all[i].Date]; ok {
					source := all[index]
					all[i].Assets = copyMoney(source.Assets)
					all[i].SourceID, all[i].SourceDate, all[i].SourceVersion = source.RecordID, source.Date, source.Version
				} else {
					// A frozen carry is evidence of its old source, not a new
					// observation that can resurrect a now-voided asset anchor.
					all[i].Assets = nil
					all[i].Status = "unavailable"
					all[i].SourceID, all[i].SourceDate, all[i].SourceVersion = "", "", ""
				}
			}
		}
		net := new(big.Int)
		for _, p := range all {
			if p.Date < from {
				out.twrHistory = append(out.twrHistory, p)
				if p.Selected && p.Status != "log" {
					copy := p
					out.Opening = &copy
				}
				continue
			}
			out.Points = append(out.Points, p)
		}
		out.Closing = out.Opening
		sort.SliceStable(out.Points, func(i, j int) bool {
			a, b := out.Points[i], out.Points[j]
			if a.Date != b.Date {
				return a.Date < b.Date
			}
			ai, _ := strconv.ParseInt(a.Sequence, 10, 64)
			bi, _ := strconv.ParseInt(b.Sequence, 10, 64)
			return ai < bi
		})
		if len(out.Points) > 10000 {
			return ErrQuery
		}
		for _, p := range out.Points {
			if p.Flow != nil {
				net.Add(net, big.NewInt(int64(*p.Flow)))
			}
			if p.Selected {
				copy := p
				out.Closing = &copy
			}
		}
		if out.Status == "current" && (out.Closing == nil || out.Closing.Assets == nil) {
			out.Status = "unavailable"
		}
		sign := ""
		if net.Sign() < 0 {
			sign = "-"
			net.Abs(net)
		}
		whole, rem := new(big.Int), new(big.Int)
		whole.QuoRem(net, big.NewInt(100), rem)
		out.NetFlow = sign + whole.String() + "." + fmtTwo(rem.Int64())
		// Exclude caller's invalidation cursor from the deterministic basis identity.
		payload, err := json.Marshal(struct {
			From, To     string
			DefaultStart bool
			Points       []BasisPoint
			Opening      *BasisPoint
			TWRHistory   []BasisPoint
		}{from, to, requestedFrom == "", out.Points, out.Opening, out.twrHistory})
		if err != nil {
			return err
		}
		hash := sha256.Sum256(payload)
		out.Revision = hex.EncodeToString(hash[:])
		return ctx.Err()
	})
	if err == nil {
		out.Returns, err = calculateReturns(ctx, out, requestedFrom, requestedTo)
	}
	return out, err
}

func (h Handler) analysisBasis(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	q, err := query(r, "from", "to", "since_revision")
	var since int64
	if err == nil && q.Get("since_revision") != "" {
		since, err = strconv.ParseInt(q.Get("since_revision"), 10, 64)
		if err == nil && strconv.FormatInt(since, 10) != q.Get("since_revision") {
			err = ErrQuery
		}
	}
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	result, err := h.Store.AnalysisBasis(r.Context(), r.PathValue("id"), q.Get("from"), q.Get("to"), since)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// Without an earlier basis token, don't claim that an existing result is stale.
	if q.Get("since_revision") == "" {
		result.PreviousBasisAffected = false
	}
	httpapi.Write(w, 200, result)
}

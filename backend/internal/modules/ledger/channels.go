package ledger

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

const maxAccountChannels = 100

// Channels describe an account's assets, not independent accounts or positions.
// A snapshot is complete: its amounts must sum exactly to the recorded total.
type ChannelAsset struct {
	Name   string `json:"name"`
	Amount *Money `json:"amount"`
}

func validChannelName(name string) bool {
	return utf8.ValidString(name) && name != "" && len(name) <= 128 && strings.TrimSpace(name) == name &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

func channelTotal(items []ChannelAsset) (Money, bool) {
	if len(items) == 0 || len(items) > maxAccountChannels {
		return 0, false
	}
	seen := make(map[string]bool, len(items))
	var total Money
	for _, item := range items {
		if !validChannelName(item.Name) || seen[item.Name] || item.Amount == nil || *item.Amount < 0 {
			return 0, false
		}
		seen[item.Name] = true
		var err error
		total, err = AddMoney(total, *item.Amount)
		if err != nil {
			return 0, false
		}
	}
	return total, true
}

func (e AccountEntry) validChannels() bool {
	if e.FlowChannel != "" && (e.Flow == nil || !validChannelName(e.FlowChannel)) {
		return false
	}
	if len(e.ChannelAssets) == 0 {
		return true
	}
	total, ok := channelTotal(e.ChannelAssets)
	return ok && e.TotalAssets != nil && total == *e.TotalAssets
}

func cloneChannelAssets(items []ChannelAsset) []ChannelAsset {
	if len(items) == 0 {
		return nil
	}
	result := make([]ChannelAsset, len(items))
	for i, item := range items {
		result[i] = ChannelAsset{Name: item.Name, Amount: copyMoney(item.Amount)}
	}
	return result
}

// Keep arbitrary or inconsistent source text as original.detail. Never turn a
// partial parse into a complete snapshot, infer flow destinations, or change D.
func importChannelAssets(detail string, total *Money) []ChannelAsset {
	if strings.TrimSpace(detail) == "" || len(detail) > 16384 || total == nil {
		return nil
	}
	var items []ChannelAsset
	for _, line := range strings.FieldsFunc(detail, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		separator := strings.LastIndexAny(line, "：:")
		if separator < 0 {
			return nil
		}
		_, size := utf8.DecodeRuneInString(line[separator:])
		amount, err := importMoney(line[separator+size:])
		if err != nil || amount == nil {
			return nil
		}
		items = append(items, ChannelAsset{Name: strings.TrimSpace(line[:separator]), Amount: amount})
		if len(items) > maxAccountChannels {
			return nil
		}
	}
	sum, ok := channelTotal(items)
	if !ok || sum != *total {
		return nil
	}
	return items
}

// Apply only at the read boundary. Older imported records already contain the
// source detail; deriving channels must not rewrite frozen audit/receipt bytes.
// Once edited, an absent snapshot means the editor chose a total-only record.
func withRecordChannels(r AccountRecord) AccountRecord {
	if len(r.ChannelAssets) == 0 && r.Origin == "import" && r.Version == "1" && r.Original != nil {
		r.ChannelAssets = importChannelAssets(r.Original.Detail, r.TotalAssets)
	}
	return r
}

type ImportChannelSummary struct {
	Names          []string `json:"names"`
	SnapshotCount  int      `json:"snapshot_count"`
	UnresolvedRows []int    `json:"unresolved_rows"`
}

func summarizeImportChannels(rows []ImportedRow) *ImportChannelSummary {
	s := &ImportChannelSummary{Names: []string{}, UnresolvedRows: []int{}}
	seen := map[string]bool{}
	for _, row := range rows {
		if strings.TrimSpace(row.Detail) == "" {
			continue
		}
		items := importChannelAssets(row.Detail, row.TotalAssets)
		if len(items) == 0 {
			s.UnresolvedRows = append(s.UnresolvedRows, row.SourceRow)
			continue
		}
		s.SnapshotCount++
		for _, item := range items {
			if !seen[item.Name] {
				seen[item.Name] = true
				s.Names = append(s.Names, item.Name)
			}
		}
	}
	return s
}

type AccountChannels struct {
	AccountID      string         `json:"account_id"`
	AsOf           string         `json:"as_of"`
	SourceRecordID string         `json:"source_record_id"`
	SourceDate     string         `json:"source_date"`
	Items          []ChannelAsset `json:"items"`
}

// These are suggestions for the next entry, explicitly dated last observations.
// Later cash flows or total-only observations do not fabricate channel balances.
func (s *Store) AccountChannels(ctx context.Context, id, to string) (AccountChannels, error) {
	result := AccountChannels{AccountID: id, AsOf: to, Items: []ChannelAsset{}}
	if !validID(id) || !validDate(to) {
		return result, ErrQuery
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id)); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, accountRecordSelect+` WHERE account_id=? AND business_date<=? AND voided=0
			ORDER BY business_date DESC,CAST(stable_sequence AS INTEGER) DESC`, id, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanAccountRecord(rows)
			if err != nil {
				return err
			}
			r = withRecordChannels(r)
			if len(r.ChannelAssets) > 0 {
				result.Items = r.ChannelAssets
				result.SourceRecordID, result.SourceDate = r.ID, r.Date
				break
			}
		}
		return rows.Err()
	})
	return result, err
}

func (h Handler) accountChannels(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	values, err := query(r, "to")
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	to := values.Get("to")
	if to == "" {
		to, _, err = h.Store.cutoff()
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}
	result, err := h.Store.AccountChannels(r.Context(), r.PathValue("id"), to)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}

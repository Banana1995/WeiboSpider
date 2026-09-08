package ledger

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

func (h Handler) reportedAccounts(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	key, ok := writeKey(w, r)
	if !ok {
		return
	}
	input, ok := body[ReportedAccountInput](w, r)
	if !ok {
		return
	}
	result, err := h.Store.CreateReportedAccount(r.Context(), key, input)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 201, result)
}
func (h Handler) writeAccountRecord(w http.ResponseWriter, r *http.Request, action WriteAction) {
	key, ok := writeKey(w, r)
	if !ok {
		return
	}
	c := AccountRecordCommand{Action: action, AccountID: r.PathValue("id"), ID: r.PathValue("recordID")}
	if action == VoidOperation {
		input, ok := body[struct {
			ExpectedVersion string `json:"expected_version"`
			Reason          string `json:"reason"`
		}](w, r)
		if !ok {
			return
		}
		c.ExpectedVersion = input.ExpectedVersion
		c.Reason = input.Reason
	} else {
		input, ok := body[struct {
			ID              string        `json:"id"`
			Entry           *AccountEntry `json:"entry"`
			ExpectedVersion string        `json:"expected_version"`
			Reason          string        `json:"reason"`
		}](w, r)
		if !ok {
			return
		}
		if action == CreateOperation {
			c.ID = input.ID
		} else if input.ID != "" && input.ID != c.ID {
			h.fail(w, r, ErrOperation)
			return
		}
		c.Entry = input.Entry
		c.ExpectedVersion = input.ExpectedVersion
		c.Reason = input.Reason
	}
	result, err := h.Store.WriteAccountRecord(r.Context(), key, c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := 200
	if action == CreateOperation {
		status = 201
	}
	httpapi.Write(w, status, result)
}
func (h Handler) accountRecords(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == "POST" {
		h.writeAccountRecord(w, r, CreateOperation)
		return
	}
	values, limit, err := page(r, "from", "to", "status")
	id := r.PathValue("id")
	if err != nil || r.URL.ForceQuery || !validID(id) {
		h.fail(w, r, ErrQuery)
		return
	}
	from, to, status := values.Get("from"), values.Get("to"), values.Get("status")
	if from != "" && !validDate(from) || to != "" && !validDate(to) || from != "" && to != "" && from > to || status != "" && status != "all" && status != "active" && status != "voided" {
		h.fail(w, r, ErrQuery)
		return
	}
	where := ` WHERE account_id=?`
	args := []any{id}
	if from != "" {
		where += ` AND business_date>=?`
		args = append(args, from)
	}
	if to != "" {
		where += ` AND business_date<=?`
		args = append(args, to)
	}
	if status == "active" {
		where += ` AND COALESCE(json_extract(payload,'$.voided'),0)=0`
	}
	if status == "voided" {
		where += ` AND json_extract(payload,'$.voided')=1`
	}
	if cursor := values.Get("cursor"); cursor != "" {
		date, sequence, ok := strings.Cut(cursor, ":")
		n, e := positiveInteger(sequence)
		if !ok || !validDate(date) || e != nil {
			h.fail(w, r, ErrQuery)
			return
		}
		where += ` AND (business_date<? OR (business_date=? AND CAST(stable_sequence AS INTEGER)<?))`
		args = append(args, date, date, n)
	}
	result := listJSON[AccountRecord]{Items: []AccountRecord{}}
	err = h.Store.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		if _, err := scanAccountInfo(r.Context(), tx.QueryRowContext(r.Context(), accountInfoSelect+` WHERE id=?`, id)); err != nil {
			return err
		}
		rows, err := tx.QueryContext(r.Context(), accountRecordSelect+where+` ORDER BY business_date DESC,CAST(stable_sequence AS INTEGER) DESC LIMIT ?`, append(args, limit)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			record, err := scanAccountRecord(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, record)
		}
		return rows.Err()
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(result.Items) == limit {
		last := result.Items[len(result.Items)-1]
		result.NextCursor = last.Date + ":" + last.Sequence
	}
	httpapi.Write(w, 200, result)
}
func (h Handler) accountRecord(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "PUT", "DELETE") {
		return
	}
	if r.Method == "PUT" {
		h.writeAccountRecord(w, r, ReplaceOperation)
		return
	}
	if r.Method == "DELETE" {
		h.writeAccountRecord(w, r, VoidOperation)
		return
	}
	if _, err := query(r); err != nil || r.URL.ForceQuery || !validID(r.PathValue("id")) || !validID(r.PathValue("recordID")) {
		h.fail(w, r, ErrQuery)
		return
	}
	var record AccountRecord
	err := h.Store.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		var err error
		record, err = scanAccountRecord(tx.QueryRowContext(r.Context(), accountRecordSelect+` WHERE account_id=? AND id=?`, r.PathValue("id"), r.PathValue("recordID")))
		return err
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, record)
}
func (h Handler) accountRecordRevisions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	values, limit, err := page(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	after := int64(0)
	if values.Get("cursor") != "" {
		after, err = positiveInteger(values.Get("cursor"))
	}
	if err != nil || r.URL.ForceQuery || !validID(r.PathValue("id")) || !validID(r.PathValue("recordID")) {
		h.fail(w, r, ErrQuery)
		return
	}
	result := listJSON[AccountRecordRevision]{Items: []AccountRecordRevision{}}
	err = h.Store.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		current, err := scanAccountRecord(tx.QueryRowContext(r.Context(), accountRecordSelect+` WHERE account_id=? AND id=?`, r.PathValue("id"), r.PathValue("recordID")))
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(r.Context(), `SELECT version,after_json,metadata_json,recorded_at
			FROM audit_log WHERE entity_type='account_record' AND account_id=? AND entity_id=? AND version>?
			ORDER BY version LIMIT ?`, current.AccountID, current.ID, after, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var revision AccountRecordRevision
			var payload, metadata, stamp string
			var version int64
			if err := rows.Scan(&version, &payload, &metadata, &stamp); err != nil {
				return err
			}
			var detail struct {
				Reason   string `json:"reason"`
				FromDate string `json:"from_date"`
			}
			if json.Unmarshal([]byte(payload), &revision.Record) != nil || decodeReceipt(metadata, &detail) != nil ||
				revision.Record.AccountID != current.AccountID || revision.Record.ID != current.ID ||
				revision.Record.Version != strconv.FormatInt(version, 10) || revision.Record.UpdatedAt != stamp ||
				!validDate(detail.FromDate) {
				return ErrCorrupt
			}
			revision.Reason = detail.Reason
			result.Items = append(result.Items, revision)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result.Items) < limit {
			last := after
			if len(result.Items) > 0 {
				last, _ = positiveInteger(result.Items[len(result.Items)-1].Record.Version)
			}
			currentVersion, _ := positiveInteger(current.Version)
			if last < currentVersion {
				return ErrCorrupt
			}
		}
		return nil
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(result.Items) == limit {
		result.NextCursor = result.Items[len(result.Items)-1].Record.Version
	}
	httpapi.Write(w, 200, result)
}
func (h Handler) effectiveSummary(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	if _, err := query(r); err != nil || r.URL.ForceQuery || !validID(r.PathValue("id")) {
		h.fail(w, r, ErrQuery)
		return
	}
	result, err := h.Store.EffectiveSummary(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}

package ledger

import (
	"database/sql"
	"net/http"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type auditEntry struct {
	ID            string  `json:"id"`
	CorrelationID string  `json:"correlation_id"`
	Action        string  `json:"action"`
	EntityType    string  `json:"entity_type"`
	EntityID      string  `json:"entity_id"`
	AccountID     *string `json:"account_id"`
	Version       string  `json:"version"`
	RecordedAt    string  `json:"recorded_at"`
	Source        string  `json:"source"`
	Before        *string `json:"before_json,omitempty"`
	After         *string `json:"after_json,omitempty"`
	Metadata      *string `json:"metadata_json,omitempty"`
}

func (h Handler) audit(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	detail := r.PathValue("auditID")
	q, limit, err := page(r, "account_id", "entity_type", "action")
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	where := " WHERE 1=1"
	args := []any{}
	for _, key := range []string{"account_id", "entity_type", "action"} {
		if value := q.Get(key); value != "" {
			if !validID(value) {
				h.fail(w, r, ErrQuery)
				return
			}
			where += " AND " + key + "=?"
			args = append(args, value)
		}
	}
	if detail != "" {
		if _, err := positiveInteger(detail); err != nil || r.URL.RawQuery != "" {
			h.fail(w, r, ErrQuery)
			return
		}
		where += " AND id=?"
		args = append(args, detail)
		limit = 1
	}
	if cursor := q.Get("cursor"); cursor != "" {
		if _, err := positiveInteger(cursor); err != nil {
			h.fail(w, r, ErrQuery)
			return
		}
		where += " AND id<?"
		args = append(args, cursor)
	}
	columns := `CAST(id AS TEXT),correlation_id,action,entity_type,entity_id,account_id,CAST(version AS TEXT),recorded_at,source`
	if detail != "" {
		columns += `,before_json,after_json,metadata_json`
	}
	items := listJSON[auditEntry]{Items: []auditEntry{}}
	rows, err := h.Store.db.QueryContext(r.Context(), `SELECT `+columns+` FROM audit_log`+where+` ORDER BY id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a auditEntry
		fields := []any{&a.ID, &a.CorrelationID, &a.Action, &a.EntityType, &a.EntityID, &a.AccountID, &a.Version, &a.RecordedAt, &a.Source}
		var before sql.NullString
		var after, metadata string
		if detail != "" {
			fields = append(fields, &before, &after, &metadata)
		}
		if err := rows.Scan(fields...); err != nil {
			h.fail(w, r, ErrCorrupt)
			return
		}
		if detail != "" {
			if len(before.String)+len(after)+len(metadata) > 8<<20 {
				h.fail(w, r, ErrQuery)
				return
			}
			if before.Valid {
				a.Before = &before.String
			}
			a.After = &after
			a.Metadata = &metadata
		}
		items.Items = append(items.Items, a)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	if detail != "" {
		if len(items.Items) != 1 {
			h.fail(w, r, ErrNotFound)
			return
		}
		httpapi.Write(w, 200, items.Items[0])
		return
	}
	if len(items.Items) > limit {
		items.Items = items.Items[:limit]
		items.NextCursor = items.Items[limit-1].ID
	}
	httpapi.Write(w, 200, items)
}

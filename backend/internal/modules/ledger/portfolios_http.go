package ledger

import (
	"net/http"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

func (h Handler) portfolios(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == http.MethodPost {
		input, ok := body[struct {
			ID         string   `json:"id"`
			Name       string   `json:"name"`
			AccountIDs []string `json:"account_ids"`
		}](w, r)
		if !ok {
			return
		}
		key, ok := writeKey(w, r)
		if !ok {
			return
		}
		out, err := h.Store.WritePortfolio(r.Context(), key, PortfolioCommand{Action: "create", ID: input.ID, Name: input.Name, AccountIDs: input.AccountIDs})
		if err != nil {
			h.fail(w, r, err)
			return
		}
		httpapi.Write(w, 201, out)
		return
	}
	q, limit, err := page(r)
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	items, err := h.Store.ListPortfolios(r.Context(), PageQuery{Limit: limit, After: q.Get("cursor")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := listJSON[Portfolio]{Items: items}
	if len(items) == limit {
		out.NextCursor = items[len(items)-1].ID
	}
	httpapi.Write(w, 200, out)
}

func (h Handler) portfolio(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "PUT", "DELETE") {
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if _, err := query(r); err != nil || r.URL.ForceQuery {
			h.fail(w, r, ErrQuery)
			return
		}
		out, err := h.Store.Portfolio(r.Context(), r.PathValue("id"))
		if err != nil {
			h.fail(w, r, err)
			return
		}
		httpapi.Write(w, 200, out)
		return
	}
	c := PortfolioCommand{ID: r.PathValue("id")}
	if r.Method == http.MethodDelete {
		input, ok := body[struct {
			ExpectedVersion string `json:"expected_version"`
		}](w, r)
		if !ok {
			return
		}
		c.Action, c.ExpectedVersion = "delete", input.ExpectedVersion
	} else {
		input, ok := body[struct {
			Name            string   `json:"name"`
			AccountIDs      []string `json:"account_ids"`
			ExpectedVersion string   `json:"expected_version"`
		}](w, r)
		if !ok {
			return
		}
		c.Action, c.Name, c.AccountIDs, c.ExpectedVersion = "replace", input.Name, input.AccountIDs, input.ExpectedVersion
	}
	key, ok := writeKey(w, r)
	if !ok {
		return
	}
	out, err := h.Store.WritePortfolio(r.Context(), key, c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, out)
}

func (h Handler) portfolioAnalysis(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	q, err := query(r, "from", "to")
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	out, err := h.Store.PortfolioAnalysis(r.Context(), r.PathValue("id"), q.Get("from"), q.Get("to"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, out)
}

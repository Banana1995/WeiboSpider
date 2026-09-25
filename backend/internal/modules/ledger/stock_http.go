package ledger

import (
	"context"
	"net/http"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

func (h Handler) stockBook(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == http.MethodPost {
		key, ok := writeKey(w, r)
		if !ok {
			return
		}
		input, ok := body[StockCommand](w, r)
		if !ok {
			return
		}
		out, err := h.Store.WriteStock(r.Context(), r.PathValue("id"), key, input)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if h.Dividends != nil {
			h.Dividends.Trigger()
		}
		httpapi.Write(w, 200, out)
		return
	}
	if !validID(r.PathValue("id")) || r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), valuationTimeout)
	defer cancel()
	out, err := h.stockBookView(ctx, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, out)
}

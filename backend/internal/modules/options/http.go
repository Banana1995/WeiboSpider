package options

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

const prefix = "/api/platform/options"

type Handler struct {
	Store    *Store
	Scanner  *Scanner
	Provider Provider
}

func (h Handler) Register(mux *http.ServeMux) { mux.Handle(prefix+"/", http.HandlerFunc(h.serve)) }
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return ErrInput
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		return ErrInput
	}
	if e := dec.Decode(new(any)); e != io.EOF {
		return ErrInput
	}
	return nil
}
func respond(w http.ResponseWriter, v any, e error) {
	if e == nil {
		httpapi.Write(w, 200, v)
		return
	}
	status, code, message := 500, "internal_error", "期权服务暂时无法完成请求"
	switch {
	case errors.Is(e, ErrInput):
		status, code, message = 400, "invalid_input", e.Error()
	case errors.Is(e, ErrNotFound):
		status, code, message = 404, "not_found", ErrNotFound.Error()
	case errors.Is(e, ErrVersion):
		status, code, message = 409, "version_conflict", ErrVersion.Error()
	case errors.Is(e, ErrReplay):
		status, code, message = 409, "request_conflict", ErrReplay.Error()
	case errors.Is(e, ErrBusy):
		status, code, message = 409, "busy", ErrBusy.Error()
	case errors.Is(e, ErrLimit):
		status, code, message = 422, "limit", ErrLimit.Error()
	}
	var pe *ProviderError
	if errors.As(e, &pe) {
		status, code, message = 503, pe.Code, pe.Message
	}
	httpapi.Fail(w, status, code, message)
}
func (h Handler) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(r.URL.Path, prefix+"/")
	if r.Method != "GET" && r.Method != "HEAD" && r.URL.RawQuery != "" {
		respond(w, nil, ErrInput)
		return
	}
	if r.Method == "HEAD" {
		r = r.Clone(r.Context())
		r.Method = "GET"
		w = headWriter{w}
	}
	switch {
	case path == "status" && r.Method == "GET":
		state, run, e := h.Scanner.Status(r.Context())
		if e != nil {
			respond(w, nil, e)
			return
		}
		page, e := h.Store.Favorites(r.Context(), "", 1)
		respond(w, map[string]any{"provider": state, "run": run, "favorite_count": page.Total, "manual_only": true, "history_limited": h.Store.HistoryLimited(r.Context())}, e)
	case path == "instruments/search" && r.Method == "GET":
		h.search(w, r)
	case path == "watchlist" && r.Method == "GET":
		v, e := h.Store.Watchlist(r.Context())
		respond(w, map[string]any{"items": v}, e)
	case path == "watchlist" && r.Method == "POST":
		var input struct {
			Code string `json:"code"`
		}
		e := decode(w, r, &input)
		if e != nil {
			respond(w, nil, e)
			return
		}
		v, e := h.Store.AddWatch(r.Context(), input.Code)
		respond(w, v, e)
	case strings.HasPrefix(path, "watchlist/"):
		code := strings.TrimPrefix(path, "watchlist/")
		if !symbolPattern.MatchString(code) {
			respond(w, nil, ErrInput)
			return
		}
		switch r.Method {
		case "PATCH":
			var input Watch
			e := decode(w, r, &input)
			if e != nil {
				respond(w, nil, e)
				return
			}
			v, e := h.Store.SaveWatch(r.Context(), code, input)
			respond(w, v, e)
		case "DELETE":
			respond(w, map[string]bool{"ok": true}, h.Store.RemoveWatch(r.Context(), code))
		default:
			method(w)
		}
	case path == "settings":
		switch r.Method {
		case "GET":
			v, e := h.Store.Settings(r.Context(), 0)
			respond(w, v, e)
		case "PUT":
			var input Settings
			e := decode(w, r, &input)
			if e != nil {
				respond(w, nil, e)
				return
			}
			v, e := h.Store.SaveSettings(r.Context(), input)
			respond(w, v, e)
		default:
			method(w)
		}
	case path == "candidates" && r.Method == "GET":
		h.candidates(w, r)
	case path == "scans" && r.Method == "POST":
		var input struct {
			RequestID  string   `json:"request_id"`
			Symbols    []string `json:"symbols"`
			OptionType string   `json:"option_type"`
		}
		e := decode(w, r, &input)
		if e != nil {
			respond(w, nil, e)
			return
		}
		v, e := h.Scanner.SubmitType(r.Context(), input.RequestID, input.Symbols, input.OptionType)
		respond(w, v, e)
	case path == "calculations" && r.Method == "POST":
		var input calculationRequest
		e := decode(w, r, &input)
		if e != nil {
			respond(w, nil, e)
			return
		}
		v, _, e := h.Store.Calculate(r.Context(), input.SnapshotID, input.ContractCode, input.Input)
		respond(w, v, e)
	case strings.HasPrefix(path, "contracts/"):
		code := strings.TrimPrefix(path, "contracts/")
		if strings.HasSuffix(code, "/refresh") && r.Method == "POST" {
			code = strings.TrimSuffix(code, "/refresh")
			var input struct {
				SnapshotID string `json:"snapshot_id"`
				RequestID  string `json:"request_id"`
			}
			e := decode(w, r, &input)
			if e != nil {
				respond(w, nil, e)
				return
			}
			v, _, e := h.Store.Calculate(r.Context(), input.SnapshotID, code, Input{Quantity: 1, Basis: "bid", Mode: "full"})
			if e != nil {
				respond(w, nil, e)
				return
			}
			// Refresh the underlying's complete scoped chain, keeping one coherent snapshot.
			run, e := h.Scanner.SubmitType(r.Context(), input.RequestID, []string{v.Quote.Contract.Symbol}, v.Quote.Contract.Type)
			respond(w, run, e)
		} else if r.Method == "GET" {
			v, _, e := h.Store.Calculate(r.Context(), r.URL.Query().Get("snapshot_id"), code, Input{Quantity: 1, Basis: "bid", Mode: "full"})
			respond(w, v, e)
		} else {
			method(w)
		}
	case path == "favorites":
		switch r.Method {
		case "GET":
			limit := 50
			if n := r.URL.Query().Get("limit"); n != "" {
				var e error
				limit, e = strconv.Atoi(n)
				if e != nil {
					respond(w, nil, ErrInput)
					return
				}
			}
			v, e := h.Store.Favorites(r.Context(), r.URL.Query().Get("cursor"), limit)
			respond(w, v, e)
		case "POST":
			var input favoriteRequest
			e := decode(w, r, &input)
			if e != nil {
				respond(w, nil, e)
				return
			}
			v, e := h.Store.SaveFavorite(r.Context(), input.RequestID, input.SnapshotID, input.ContractCode, input.Input)
			respond(w, v, e)
		default:
			method(w)
		}
	case strings.HasPrefix(path, "favorites/"):
		id := strings.TrimPrefix(path, "favorites/")
		if !idPattern.MatchString(id) {
			respond(w, nil, ErrInput)
			return
		}
		switch r.Method {
		case "GET":
			v, e := h.Store.Favorite(r.Context(), id)
			respond(w, v, e)
		case "DELETE":
			respond(w, map[string]bool{"ok": true}, h.Store.DeleteFavorite(r.Context(), id))
		default:
			method(w)
		}
	case path == "history" && r.Method == "GET":
		side := r.URL.Query().Get("option_type")
		if !validSide(side) {
			respond(w, nil, ErrInput)
			return
		}
		v, e := listJSON[Snapshot](r.Context(), h.Store.db, "SELECT data FROM daily_history WHERE symbol=? AND option_type=? ORDER BY day DESC LIMIT 180", r.URL.Query().Get("symbol"), optionSide(side))
		respond(w, map[string]any{"items": v, "sampling": "当日最后一次手动快照"}, e)
	default:
		httpapi.Fail(w, 404, "not_found", "期权接口不存在")
	}
}

type headWriter struct{ http.ResponseWriter }

func (w headWriter) Write(p []byte) (int, error) { return len(p), nil }
func method(w http.ResponseWriter) {
	httpapi.Fail(w, 405, "method_not_allowed", "请求方法不支持")
}

type calculationRequest struct {
	SnapshotID   string `json:"snapshot_id"`
	ContractCode string `json:"contract_code"`
	Input        Input  `json:"input"`
}
type favoriteRequest struct {
	calculationRequest
	RequestID string `json:"request_id"`
}

func (h Handler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	q = strings.TrimPrefix(q, "us.")
	if len(q) > 144 {
		respond(w, nil, ErrInput)
		return
	}
	all, e := h.Store.Instruments(r.Context())
	if e != nil {
		respond(w, nil, e)
		return
	}
	find := func() []Instrument {
		out := []Instrument{}
		for _, v := range all {
			haystack := strings.ToLower(strings.Join([]string{v.Code, v.Name, v.EnglishName, v.Aliases}, " "))
			matches := true
			for _, word := range strings.Fields(q) {
				matches = matches && strings.Contains(haystack, word)
			}
			if matches {
				out = append(out, v)
			}
			if len(out) >= 40 {
				break
			}
		}
		return out
	}
	values := find()
	// Explicit name searches may fetch static security identities, never quotes/chains.
	if q != "" && len(values) == 0 && h.Provider != nil {
		fetched, err := h.Provider.Instruments(r.Context(), q)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if e = h.Store.PutInstruments(r.Context(), fetched); e != nil {
			respond(w, nil, e)
			return
		}
		all, e = h.Store.Instruments(r.Context())
		if e != nil {
			respond(w, nil, e)
			return
		}
		values = find()
	}
	respond(w, map[string]any{"items": values, "limit": 40}, nil)
}
func (h Handler) candidates(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	f := Filter{}
	fields := map[string]*string{"min_days": &f.MinDays, "max_days": &f.MaxDays, "max_strike": &f.MaxStrike, "max_cost": &f.MaxCost, "min_apr": &f.MinAPR, "min_oi": &f.MinOI, "min_volume": &f.MinVolume, "max_spread": &f.MaxSpread, "max_delta": &f.MaxDelta, "budget": &f.Budget, "moneyness": &f.Moneyness}
	fields["min_strike"] = &f.MinStrike
	fields["min_sale_price"] = &f.MinSalePrice
	for k, values := range v {
		if len(values) != 1 {
			respond(w, nil, ErrInput)
			return
		}
		if p, ok := fields[k]; ok {
			*p = values[0]
			continue
		}
		switch k {
		case "symbol", "option_type", "mode", "fee_version", "sort", "direction", "cursor", "limit", "view":
		default:
			respond(w, nil, ErrInput)
			return
		}
	}
	settings, e := h.Store.Settings(r.Context(), 0)
	if e != nil {
		respond(w, nil, e)
		return
	}
	q := CandidateQuery{Symbol: v.Get("symbol"), Mode: v.Get("mode"), Filter: f, Sort: v.Get("sort"), Descending: v.Get("direction") == "desc", Limit: 100, View: v.Get("view")}
	q.OptionType = v.Get("option_type")
	if q.Mode == "" {
		q.Mode = settings.Mode
	}
	if q.Sort == "" {
		q.Sort = "date"
	}
	if settings.Fees != nil {
		q.FeeVersion = settings.Version
	}
	if x := v.Get("fee_version"); x != "" {
		q.FeeVersion, e = strconv.ParseInt(x, 10, 64)
		if e != nil {
			respond(w, nil, ErrInput)
			return
		}
	}
	for key, p := range map[string]*int{"cursor": &q.Offset, "limit": &q.Limit} {
		if x := v.Get(key); x != "" {
			*p, e = strconv.Atoi(x)
			if e != nil {
				respond(w, nil, ErrInput)
				return
			}
		}
	}
	page, e := h.Store.Candidates(r.Context(), q)
	respond(w, page, e)
}

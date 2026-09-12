package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
	"github.com/mattn/go-sqlite3"
)

const ledgerPrefix = "/api/platform/ledger"
const maxLedgerBody = 64 << 10

type Handler struct {
	Store            *Store
	Logger           *slog.Logger
	FX               FXProvider
	Quotes           QuotesProvider
	InstrumentSearch InstrumentSearchProvider
	Benchmark        BenchmarkProvider
	Now              func() time.Time
	Weekly           *WeeklyWorker
}

func (h Handler) Register(mux *http.ServeMux) {
	for path, handle := range map[string]http.HandlerFunc{
		"/audit":                                      h.audit,
		"/audit/{auditID}":                            h.audit,
		"/weekly-status":                              h.weeklyStatus,
		"/accounts/{id}/weekly-jobs":                  h.weeklyJobs,
		"/accounts/{id}/weekly-jobs/{jobID}":          h.weeklyJob,
		"/accounts/{id}/records":                      h.accountRecords,
		"/accounts/{id}/records/{recordID}":           h.accountRecord,
		"/accounts/{id}/records/{recordID}/revisions": h.accountRecordRevisions,
		"/accounts/{id}/effective-summary":            h.effectiveSummary,
		"/accounts/{id}/analysis-basis":               h.analysisBasis,
		"/imports/youzhiyouxing/preview":              h.importPreview,
		"/accounts/{id}/imports/youzhiyouxing":        h.importConfirm,
		"/accounts/{id}/imported-records":             h.importedRecords,
		"/accounts/{id}/import-summary":               h.importSummary,
		"/fx":                                         h.fx,
		"/accounts":                                   h.accounts,
		"/accounts/{id}":                              h.account,
		"/accounts/{id}/current-holdings":             h.currentHoldings,
		"/accounts/{id}/holdings":                     h.holdings,
		"/accounts/{id}/valuation":                    h.valuation,
		"/accounts/{id}/valuations":                   h.valuations,
		"/accounts/{id}/valuations/{historyID}":       h.valuationHistory,
		"/instruments":                                h.instruments,
		"/instruments/search":                         h.searchInstruments,
		"/benchmark":                                  h.benchmark,
		"":                                            h.notFound, "/": h.notFound,
	} {
		mux.HandleFunc(ledgerPrefix+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w = headResponse{w}
			}
			handle(w, r)
		})
	}
}

type headResponse struct{ http.ResponseWriter }

func (headResponse) Write(p []byte) (int, error)                  { return len(p), nil }
func (h Handler) notFound(w http.ResponseWriter, r *http.Request) { h.fail(w, r, ErrNotFound) }
func method(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	if slices.Contains(allowed, r.Method) {
		return true
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	httpapi.Fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	return false
}
func query(r *http.Request, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, ErrQuery
	}
	for key, v := range values {
		if !slices.Contains(allowed, key) || len(v) != 1 || v[0] == "" {
			return nil, ErrQuery
		}
	}
	return values, nil
}
func page(r *http.Request, allowed ...string) (url.Values, int, error) {
	values, err := query(r, append(allowed, "limit", "cursor")...)
	if err != nil {
		return nil, 0, err
	}
	limit := 30
	if s := values.Get("limit"); s != "" {
		n, err := positiveInteger(s)
		if err != nil || n > 100 {
			return nil, 0, ErrQuery
		}
		limit = int(n)
	}
	return values, limit, nil
}

// Reject duplicate, non-ASCII or differently cased wire keys before Go's
// case-insensitive decoder can apply last-value-wins semantics.
func uniqueJSON(decoder *json.Decoder, depth int, stored bool) error {
	if depth > 16 {
		return ErrOperation
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] || name == "" {
				return ErrOperation
			}
			for _, char := range name {
				if !stored && !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_') {
					return ErrOperation
				}
			}
			keys[name] = true
			if err := uniqueJSON(decoder, depth+1, stored); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := uniqueJSON(decoder, depth+1, stored); err != nil {
				return err
			}
		}
	default:
		return ErrOperation
	}
	_, err = decoder.Token()
	return err
}
func body[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var result T
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		httpapi.Fail(w, 400, "invalid_query", "write requests do not accept query parameters")
		return result, false
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || (r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity") {
		httpapi.Fail(w, 415, "content_type", "send uncompressed application/json")
		return result, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLedgerBody))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpapi.Fail(w, 413, "body_too_large", "request body exceeds 64 KiB")
		return result, false
	}
	trimmed := bytes.TrimSpace(data)
	valid := err == nil && utf8.Valid(data) && len(trimmed) > 0 && trimmed[0] == '{'
	if valid {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		valid = uniqueJSON(decoder, 0, false) == nil
		if _, err := decoder.Token(); err != io.EOF {
			valid = false
		}
	}
	if valid {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		valid = decoder.Decode(&result) == nil
	}
	if !valid {
		httpapi.Fail(w, 400, "invalid_body", "expected one JSON object with known, unique fields and decimal strings")
	}
	return result, valid
}
func writeKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !validID(values[0]) {
		httpapi.Fail(w, 400, "invalid_idempotency_key", "send one Idempotency-Key containing 1..128 letters, digits, underscores or hyphens")
		return "", false
	}
	return values[0], true
}
func (h Handler) accounts(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == http.MethodPost {
		h.reportedAccounts(w, r)
		return
	}
	values, limit, err := page(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	accounts, err := h.Store.ListAccounts(r.Context(), PageQuery{Limit: limit, After: values.Get("cursor")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result := listJSON[accountView]{Items: make([]accountView, 0, len(accounts))}
	for _, a := range accounts {
		result.Items = append(result.Items, viewAccount(a))
	}
	if len(accounts) == limit {
		result.NextCursor = accounts[len(accounts)-1].ID
	}
	httpapi.Write(w, 200, result)
}
func (h Handler) account(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	if _, err := query(r); err != nil {
		h.fail(w, r, err)
		return
	}
	info, state, err := h.Store.Account(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var cash *Money
	if state != nil {
		cash = &state.Cash
	}
	httpapi.Write(w, 200, struct {
		accountView
		Cash *Money `json:"cash"`
	}{viewAccount(info), cash})
}
func (h Handler) instruments(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == http.MethodPost {
		input, ok := body[instrumentJSON](w, r)
		if !ok {
			return
		}
		if err := h.Store.AddInstrument(r.Context(), Instrument{ID: input.ID, Market: input.Market, Code: input.Code, Name: input.Name, Currency: input.Currency}); err != nil {
			h.fail(w, r, err)
			return
		}
		httpapi.Write(w, 201, input)
		return
	}
	values, limit, err := page(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items, err := h.Store.ListInstruments(r.Context(), PageQuery{Limit: limit, After: values.Get("cursor")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result := listJSON[instrumentJSON]{Items: make([]instrumentJSON, 0, len(items))}
	for _, i := range items {
		result.Items = append(result.Items, instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency})
	}
	if len(items) == limit {
		result.NextCursor = items[len(items)-1].ID
	}
	httpapi.Write(w, 200, result)
}
func (h Handler) benchmark(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	values, err := query(r, "code", "from", "to")
	if err == nil && (values.Get("code") == "" || values.Get("from") == "" || values.Get("to") == "") {
		err = ErrQuery
	}
	var result Benchmark
	if err == nil {
		err = validateBenchmarkRange(values.Get("code"), values.Get("from"), values.Get("to"))
	}
	if err == nil {
		if h.Benchmark == nil {
			err = ErrBenchmarkUnavailable
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), benchmarkTimeout)
			defer cancel()
			result, err = h.Benchmark.Fetch(ctx, values.Get("code"), values.Get("from"), values.Get("to"))
			if err == nil {
				err = ctx.Err()
			}
		}
	}
	if err != nil {
		switch benchmarkError(err) {
		case ErrBenchmarkTimeout:
			httpapi.Fail(w, 504, "benchmark_timeout", "benchmark data timed out")
		case ErrBenchmarkUnavailable:
			httpapi.Fail(w, 502, "benchmark_unavailable", "benchmark data is temporarily unavailable")
		default:
			h.fail(w, r, err)
		}
		return
	}
	if result.Items == nil {
		result.Items = []BenchmarkItem{}
	}
	httpapi.Write(w, 200, result)
}
func (h Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 500, "internal_error", "ledger request failed"
	var sqliteError sqlite3.Error
	switch {
	case errors.Is(err, ErrCorrupt):
		code, message = "data_integrity", "ledger data requires integrity review"
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = 504, "request_timeout", "ledger request timed out; retry writes with the same idempotency key"
	case errors.Is(err, context.Canceled):
		status, code, message = 408, "request_canceled", "ledger request canceled; retry writes with the same idempotency key"
	case errors.Is(err, ErrNotFound):
		status, code, message = 404, "not_found", "ledger resource not found"
	case errors.Is(err, ErrQuery):
		status, code, message = 400, "invalid_query", "invalid query parameters or resource id"
	case errors.Is(err, ErrIdempotency):
		status, code, message = 409, "idempotency_conflict", "idempotency key was used for a different request"
	case errors.Is(err, ErrVersion):
		status, code, message = 409, "version_conflict", "expected version does not match the current record"
	case errors.Is(err, errWeeklyBasis):
		status, code, message = 409, "basis_changed", "current source changed during valuation; no record saved"
	case errors.Is(err, ErrVoided):
		status, code, message = 409, "operation_voided", "voided records cannot be changed"
	case errors.Is(err, ErrConflict):
		status, code, message = 409, "conflict", "ledger identity or uniqueness conflict"
	case errors.Is(err, ErrUnsupported):
		status, code, message = 422, "unsupported_operation", "operation is not supported"
	case errors.Is(err, ErrPrecision):
		status, code, message = 400, "invalid_precision", "invalid precision, operands or numeric range"
	case errors.Is(err, ErrOperation):
		status, code, message = 400, "invalid_operation", "invalid ledger input"
	case errors.As(err, &sqliteError) && (sqliteError.Code == sqlite3.ErrBusy || sqliteError.Code == sqlite3.ErrLocked):
		status, code, message = 503, "storage_busy", "ledger storage is busy; retry with the same idempotency key"
		w.Header().Set("Retry-After", "1")
	}
	if status >= 500 && h.Logger != nil {
		h.Logger.ErrorContext(r.Context(), "ledger.request.failed", "method", r.Method, "code", code)
	}
	httpapi.Write(w, status, struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message})
}

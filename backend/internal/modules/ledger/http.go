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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
	"github.com/mattn/go-sqlite3"
)

const ledgerPrefix = "/api/platform/ledger"
const maxLedgerBody = 64 << 10

// Handler must be placed behind the app's private/local access boundary.
type Handler struct {
	Store  *Store
	Logger *slog.Logger
	FX     FXProvider
	Quotes QuotesProvider
	Now    func() time.Time
	Weekly *WeeklyWorker
}

func (h Handler) Register(mux *http.ServeMux) {
	for path, handle := range map[string]http.HandlerFunc{
		"/audit":                                      h.audit,
		"/audit/{auditID}":                            h.audit,
		"/weekly-status":                              h.weeklyStatus,
		"/accounts/{id}/weekly-jobs":                  h.weeklyJobs,
		"/accounts/{id}/weekly-jobs/{jobID}":          h.weeklyJob,
		"/reported-accounts":                          h.reportedAccounts,
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
		"/accounts/{id}/positions":                    h.positions,
		"/accounts/{id}/current-holdings":             h.currentHoldings,
		"/accounts/{id}/valuation":                    h.valuation,
		"/accounts/{id}/valuations":                   h.valuations,
		"/accounts/{id}/valuations/{historyID}":       h.valuationHistory,
		"/accounts/{id}/operations":                   h.accountOperations,
		"/instruments":                                h.instruments,
		"/operations":                                 h.operations,
		"/operations/{id}":                            h.operation,
		"/operations/{id}/revisions":                  h.revisions,
		"/transfers":                                  h.transfers,
		"":                                            h.notFound,
		"/":                                           h.notFound,
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

func (headResponse) Write(p []byte) (int, error) { return len(p), nil }

func (h Handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.fail(w, r, ErrNotFound)
}

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

// Reject duplicate or differently cased keys before decoding typed data. The
// standard decoder otherwise accepts last-value-wins or case-insensitive fields.
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
			// encoding/json folds Unicode aliases (e.g. long s) onto ASCII
			// fields. Only our lowercase ASCII wire keys may reach that decoder.
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

type mutationBody struct {
	Operation       operationInput `json:"operation"`
	Note            string         `json:"note"`
	Reason          string         `json:"reason"`
	ExpectedVersion string         `json:"expected_version"`
}

func (h Handler) save(w http.ResponseWriter, r *http.Request, accountID string, transfer bool) {
	key, ok := writeKey(w, r)
	if !ok {
		return
	}
	input, ok := body[mutationBody](w, r)
	if !ok {
		return
	}
	if accountID != "" {
		if input.Operation.AccountID != "" && input.Operation.AccountID != accountID {
			h.fail(w, r, ErrOperation)
			return
		}
		input.Operation.AccountID = accountID
	}
	o, err := input.Operation.operation()
	if err != nil || (transfer && o.Kind != Transfer) {
		h.fail(w, r, ErrOperation)
		return
	}
	command := Command{Action: CreateOperation, Key: key, Operation: o, Note: input.Note, Reason: input.Reason}
	status := http.StatusCreated
	if r.Method == http.MethodPut {
		version, err := positiveInteger(input.ExpectedVersion)
		if err != nil || o.ID != r.PathValue("id") {
			httpapi.Fail(w, 400, "invalid_version_or_id", "send a positive expected_version string and matching operation id")
			return
		}
		command.Action, command.ExpectedVersion, status = ReplaceOperation, version, http.StatusOK
	} else if input.ExpectedVersion != "" {
		h.fail(w, r, ErrOperation)
		return
	}
	record, err := h.Store.Write(r.Context(), command)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", ledgerPrefix+"/operations/"+record.Operation.ID)
	httpapi.Write(w, status, publicRecord(record))
}

func (h Handler) operation(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "PUT", "DELETE") {
		return
	}
	if !validID(r.PathValue("id")) {
		h.fail(w, r, ErrQuery)
		return
	}
	if r.Method == http.MethodPut {
		h.save(w, r, "", false)
		return
	}
	if r.Method == http.MethodDelete {
		key, ok := writeKey(w, r)
		if !ok {
			return
		}
		input, ok := body[struct {
			ExpectedVersion string `json:"expected_version"`
			Reason          string `json:"reason"`
		}](w, r)
		if !ok {
			return
		}
		version, err := positiveInteger(input.ExpectedVersion)
		if err != nil {
			httpapi.Fail(w, 400, "invalid_version", "send a positive expected_version string")
			return
		}
		record, err := h.Store.Write(r.Context(), Command{Action: VoidOperation, Key: key, Operation: Operation{ID: r.PathValue("id")},
			ExpectedVersion: version, Reason: input.Reason})
		if err != nil {
			h.fail(w, r, err)
			return
		}
		httpapi.Write(w, 200, publicRecord(record))
		return
	}
	if _, err := query(r); err != nil {
		h.fail(w, r, err)
		return
	}
	record, err := h.Store.GetOperation(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, publicRecord(record))
}

func (h Handler) transfers(w http.ResponseWriter, r *http.Request) {
	if method(w, r, "POST") {
		h.save(w, r, "", true)
	}
}

func (h Handler) accountOperations(w http.ResponseWriter, r *http.Request) {
	if !validID(r.PathValue("id")) {
		h.fail(w, r, ErrQuery)
		return
	}
	h.operations(w, r)
}

func (h Handler) operations(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == http.MethodPost {
		h.save(w, r, r.PathValue("id"), false)
		return
	}
	values, limit, err := page(r, "account_id", "from", "to", "status")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	accountID := values.Get("account_id")
	if id := r.PathValue("id"); id != "" {
		if accountID != "" && accountID != id {
			h.fail(w, r, ErrQuery)
			return
		}
		accountID = id
	}
	q := OperationQuery{AccountID: accountID, From: values.Get("from"), To: values.Get("to"), Status: values.Get("status"), Limit: limit}
	if cursor := values.Get("cursor"); cursor != "" {
		date, sequence, ok := strings.Cut(cursor, ":")
		n, err := positiveInteger(sequence)
		if !ok || !validDate(date) || err != nil {
			h.fail(w, r, ErrQuery)
			return
		}
		q.BeforeDate, q.BeforeSequence = date, n
	}
	records, err := h.Store.ListOperations(r.Context(), q)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result := listJSON[recordJSON]{Items: make([]recordJSON, 0, len(records))}
	for _, record := range records {
		result.Items = append(result.Items, publicRecord(record))
	}
	if len(records) == limit {
		last := records[len(records)-1].Operation
		result.NextCursor = last.Date + ":" + strconv.FormatInt(last.Sequence, 10)
	}
	httpapi.Write(w, 200, result)
}

func (h Handler) revisions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	values, limit, err := page(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	after := int64(0)
	if cursor := values.Get("cursor"); cursor != "" {
		after, err = positiveInteger(cursor)
		if err != nil {
			h.fail(w, r, ErrQuery)
			return
		}
	}
	revisions, err := h.Store.RevisionPage(r.Context(), r.PathValue("id"), after, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	type revisionJSON struct {
		Record recordJSON `json:"record"`
		Reason string     `json:"reason"`
	}
	result := listJSON[revisionJSON]{Items: make([]revisionJSON, 0, len(revisions))}
	for _, revision := range revisions {
		result.Items = append(result.Items, revisionJSON{Record: publicRecord(revision.Record), Reason: revision.Reason})
	}
	if len(revisions) == limit {
		result.NextCursor = strconv.FormatInt(revisions[len(revisions)-1].Record.Version, 10)
	}
	httpapi.Write(w, 200, result)
}

func (h Handler) accounts(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == http.MethodPost {
		input, ok := body[struct {
			ID          string                `json:"id"`
			Name        string                `json:"name"`
			Currency    Currency              `json:"currency"`
			OpeningDate string                `json:"opening_date"`
			OpeningCash *Money                `json:"opening_cash"`
			Positions   []openingPositionJSON `json:"positions"`
		}](w, r)
		if !ok {
			return
		}
		if input.OpeningCash == nil || len(input.Positions) > 200 {
			h.fail(w, r, ErrOperation)
			return
		}
		opening := Opening{AccountID: input.ID, Currency: input.Currency, Date: input.OpeningDate, Cash: *input.OpeningCash}
		for _, p := range input.Positions {
			opening.Positions = append(opening.Positions, OpeningPosition{InstrumentID: p.InstrumentID, Quantity: p.Quantity, Cost: p.Cost, DilutedBasis: p.DilutedBasis})
		}
		if err := h.Store.InitializeAccount(r.Context(), input.Name, opening); err != nil {
			h.fail(w, r, err)
			return
		}
		w.Header().Set("Location", ledgerPrefix+"/accounts/"+input.ID)
		httpapi.Write(w, 201, accountJSON{ID: input.ID, Name: input.Name, Currency: input.Currency,
			OpeningDate: input.OpeningDate, OpeningCash: input.OpeningCash, Version: "1", AccountingMode: "holdings"})
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
	for _, account := range accounts {
		result.Items = append(result.Items, viewAccount(account))
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
	}{accountView: viewAccount(info), Cash: cash})
}

func (h Handler) positions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	values, limit, err := page(r)
	if err != nil || (values.Get("cursor") != "" && !validID(values.Get("cursor"))) {
		h.fail(w, r, ErrQuery)
		return
	}
	_, state, err := h.Store.Account(r.Context(), r.PathValue("id"))
	if err == nil && state == nil {
		err = ErrUnsupported
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ids := make([]string, 0, len(state.Positions))
	for id := range state.Positions {
		if id > values.Get("cursor") {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	result := listJSON[positionJSON]{Items: make([]positionJSON, 0)}
	for _, id := range ids {
		if len(result.Items) == limit {
			break
		}
		cycle := state.Cycles[state.Positions[id]]
		average, e1 := cycle.MovingAverage()
		diluted, e2 := cycle.DilutedCost()
		if e1 != nil || e2 != nil {
			h.fail(w, r, errors.Join(e1, e2))
			return
		}
		result.Items = append(result.Items, positionJSON{InstrumentID: id, CycleID: cycle.ID, Quantity: cycle.Quantity,
			RemainingCost: cycle.RemainingCost, MovingAverage: average, DilutedBasis: cycle.DilutedBasis,
			DilutedCost: diluted, RealizedProfit: cycle.RealizedProfit, Dividends: cycle.Dividends})
	}
	if len(ids) > limit {
		result.NextCursor = ids[limit-1]
	}
	httpapi.Write(w, 200, result)
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
		result.Items = append(result.Items, instrumentJSON{ID: i.ID, Market: i.Market, Code: i.Code, Name: i.Name, Currency: i.Currency})
	}
	if len(items) == limit {
		result.NextCursor = items[len(items)-1].ID
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
		status, code, message = 409, "version_conflict", "expected version does not match the current operation"
	case errors.Is(err, errWeeklyBasis):
		status, code, message = 409, "basis_changed", "current source changed during valuation; no record saved"
	case errors.Is(err, ErrVoided):
		status, code, message = 409, "operation_voided", "voided operations cannot be changed"
	case errors.Is(err, ErrConflict):
		status, code, message = 409, "conflict", "ledger identity or uniqueness conflict"
	case errors.Is(err, ErrInsufficientCash):
		status, code, message = 422, "insufficient_cash", "resulting history would overdraw cash"
	case errors.Is(err, ErrInsufficientStock):
		status, code, message = 422, "insufficient_position", "resulting history would oversell a position"
	case errors.Is(err, ErrUnsupported):
		status, code, message = 422, "unsupported_operation", "operation is not supported"
	case errors.Is(err, ErrManagedRecord):
		status, code, message = 422, "source_managed_record", "correct or void the linked operation; transfer legs must change together"
	case errors.Is(err, errIncompleteValuation):
		status, code, message = 422, "incomplete_valuation", "complete current valuation required; no record saved"
	case errors.Is(err, ErrPrecision):
		status, code, message = 400, "invalid_precision", "invalid precision, operands or numeric range"
	case errors.Is(err, ErrOperation):
		status, code, message = 400, "invalid_operation", "invalid operation or inconsistent history"
	case errors.As(err, &sqliteError) && (sqliteError.Code == sqlite3.ErrBusy || sqliteError.Code == sqlite3.ErrLocked):
		status, code, message = 503, "storage_busy", "ledger storage is busy; retry with the same idempotency key"
		w.Header().Set("Retry-After", "1")
	}
	if status >= 500 && h.Logger != nil {
		// Never log the body, key, URL identifiers, SQL error or financial values.
		h.Logger.ErrorContext(r.Context(), "ledger.request.failed", "method", r.Method, "code", code)
	}
	response := struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		OperationID string `json:"operation_id,omitempty"`
		Date        string `json:"date,omitempty"`
	}{Code: code, Message: message}
	var conflict *OperationError
	if status < 500 && errors.As(err, &conflict) {
		response.OperationID, response.Date = conflict.ID, conflict.Date
	}
	httpapi.Write(w, status, response)
}

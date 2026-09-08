package ledger

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

func (h Handler) failImport(w http.ResponseWriter, r *http.Request, err error) {
	var e *importError
	if !errors.As(err, &e) {
		h.fail(w, r, err)
		return
	}
	code := e.Code
	if e.Status == 400 && code != "preview_mismatch" {
		code = "invalid_import"
	}
	httpapi.Write(w, e.Status, struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		DetailCode string `json:"detail_code"`
		Row        int    `json:"row,omitempty"`
		Column     string `json:"column,omitempty"`
	}{code, "account import rejected", e.Code, e.Row, e.Column})
}

// MultipartReader streams parts into bounded memory, never ParseMultipartForm
// (which may spill private uploads to disk). File names are ignored entirely.
func importUpload(w http.ResponseWriter, r *http.Request, confirm bool) ([]byte, map[string]string, error) {
	defer r.Body.Close()
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return nil, nil, invalidImport("query", 0, "")
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		return nil, nil, invalidImport("media_type", 0, "")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload+(64<<10))
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, invalidImport("multipart", 0, "")
	}
	fields := map[string]string{}
	var file []byte
	seen := map[string]bool{}
	for {
		if err := r.Context().Err(); err != nil {
			return nil, nil, err
		}
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, uploadReadError(err)
		}
		name := part.FormName()
		if name == "file" && part.FileName() == "" || name != "file" && part.FileName() != "" {
			_ = part.Close()
			return nil, nil, invalidImport("file_part", 0, "")
		}
		if seen[name] || name != "file" && (!confirm || name != "preview_digest" && name != "create_account") {
			_ = part.Close()
			return nil, nil, invalidImport("multipart_field", 0, "")
		}
		seen[name] = true
		limit := int64(256)
		if name == "file" {
			limit = maxImportUpload
		}
		b, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil {
			_ = part.Close()
			return nil, nil, uploadReadError(err)
		}
		if int64(len(b)) > limit {
			_ = part.Close()
			return nil, nil, &importError{Code: "upload_too_large", Status: 413}
		}
		if err := part.Close(); err != nil {
			return nil, nil, uploadReadError(err)
		}
		if name == "file" {
			file = b
		} else {
			fields[name] = string(b)
		}
	}
	if len(file) == 0 || confirm && (len(fields["preview_digest"]) != 64 || fields["create_account"] != "true" && fields["create_account"] != "false") {
		return nil, nil, invalidImport("required_field", 0, "")
	}
	return file, fields, nil
}

func uploadReadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &importError{Code: "upload_too_large", Status: 413}
	}
	return invalidImport("multipart", 0, "")
}

func (h Handler) importPreview(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	data, _, err := importUpload(w, r, false)
	if err != nil {
		h.failImport(w, r, err)
		return
	}
	p, err := ParseAccountImport(r.Context(), data)
	if err != nil {
		h.failImport(w, r, err)
		return
	}
	httpapi.Write(w, 200, p)
}
func (h Handler) importConfirm(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	key, ok := writeKey(w, r)
	if !ok {
		return
	}
	if !validID(r.PathValue("id")) {
		h.failImport(w, r, invalidImport("identity", 0, ""))
		return
	}
	data, fields, err := importUpload(w, r, true)
	if err != nil {
		h.failImport(w, r, err)
		return
	}
	result, err := h.Store.ConfirmAccountImport(r.Context(), r.PathValue("id"), key, fields["preview_digest"], fields["create_account"] == "true", data)
	if err != nil {
		h.failImport(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}
func (h Handler) importSummary(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	if _, err := query(r); err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	result, err := h.Store.ImportSummary(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}
func (h Handler) importedRecords(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	values, limit, err := page(r, "from", "to")
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	q := OperationQuery{From: values.Get("from"), To: values.Get("to"), Limit: limit}
	if c := values.Get("cursor"); c != "" {
		date, row, ok := strings.Cut(c, ":")
		n, e := positiveInteger(row)
		if !ok || !validDate(date) || e != nil || n < 5 || n > maxImportRows+4 {
			h.fail(w, r, ErrQuery)
			return
		}
		q.BeforeDate, q.BeforeSequence = date, n
	}
	// Fetch the public page size; a full page may advertise a final empty page.
	rows, err := h.Store.ImportedRecords(r.Context(), r.PathValue("id"), q)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result := listJSON[ImportedRecord]{Items: rows}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		result.NextCursor = last.Date + ":" + strconv.Itoa(last.SourceRow)
	}
	httpapi.Write(w, 200, result)
}

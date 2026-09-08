package ledger

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const maxImportUpload = 8 << 20
const maxImportExpanded = 32 << 20
const maxImportRows = 10000
const importSheet = "有知有行投资记账"
const importFormatVersion = "youzhiyouxing-account-v2"

type ImportMetadata struct {
	Name              string   `json:"name"`
	Goal              string   `json:"goal"`
	ExpectedReturn    string   `json:"expected_return"`
	ExpectedReturnRaw string   `json:"expected_return_raw"`
	InvestmentHorizon string   `json:"investment_horizon"`
	Currency          Currency `json:"currency"`
	MoneyBucket       string   `json:"money_bucket"`
}

type ImportedRow struct {
	SourceRow       int    `json:"source_row"`
	Kind            string `json:"kind"`
	Date            string `json:"date"`
	Flow            *Money `json:"flow"`
	TotalAssets     *Money `json:"total_assets"`
	Note            string `json:"note"`
	SourceCreatedAt string `json:"source_created_at"`
	Detail          string `json:"detail"`
	DateRaw         string `json:"date_raw"`
	CreatedRaw      string `json:"created_raw"`
}

type ImportSummary struct {
	RowCount        int     `json:"row_count"`
	AssetCount      int     `json:"asset_count"`
	FlowCount       int     `json:"flow_count"`
	From            string  `json:"from"`
	To              string  `json:"to"`
	TotalIn         Money   `json:"total_in"`
	TotalOut        Money   `json:"total_out"`
	LatestAssets    *Money  `json:"latest_assets"`
	LatestAssetDate *string `json:"latest_asset_date"`
}

type ImportPreview struct {
	Digest   string         `json:"digest"`
	Metadata ImportMetadata `json:"metadata"`
	Rows     []ImportedRow  `json:"rows"`
	Summary  ImportSummary  `json:"summary"`
	Warnings []string       `json:"warnings"`
}

// Import errors contain only fixed codes and coordinates, never cell contents.
type importError struct {
	Code   string
	Row    int
	Column string
	Status int
}

func (e *importError) Error() string { return e.Code }
func invalidImport(code string, row int, col string) error {
	return &importError{Code: code, Row: row, Column: col, Status: 400}
}
func importConflict(code string) error { return &importError{Code: code, Status: 409} }

type xlsxText struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

func (s xlsxText) value() string {
	var b strings.Builder
	b.WriteString(s.Text)
	for _, r := range s.Runs {
		b.WriteString(r.Text)
	}
	return decodeXLSXString(b.String())
}

// Decode original escape tokens once, not the result: _x005F_x000D_ is
// literal _x000D_, whereas _x000D_ is CR. UTF-16 surrogate pairs represent
// non-BMP text; malformed/unpaired tokens are retained rather than lost.
func decodeXLSXString(s string) string {
	unit := func(s string) (rune, bool) {
		if len(s) < 7 || s[0:2] != "_x" || s[6] != '_' {
			return 0, false
		}
		n, err := strconv.ParseUint(s[2:6], 16, 16)
		return rune(n), err == nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, ok := unit(s[i:])
		if ok && r == '_' {
			// Consume the protected token as a whole so its trailing underscore
			// cannot become the start of another overlapping escape.
			if _, protected := unit(s[i+6:]); protected {
				b.WriteByte('_')
				b.WriteString(s[i+7 : i+13])
				i += 13
				continue
			}
		}
		if ok && r >= 0xD800 && r <= 0xDBFF {
			if low, yes := unit(s[i+7:]); yes && low >= 0xDC00 && low <= 0xDFFF {
				b.WriteRune(utf16.DecodeRune(r, low))
				i += 14
				continue
			}
		}
		if ok && !utf16.IsSurrogate(r) {
			b.WriteRune(r)
			i += 7
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

type xlsxCell struct {
	Ref     string   `xml:"r,attr"`
	Type    string   `xml:"t,attr"`
	Style   int      `xml:"s,attr"`
	Formula *string  `xml:"f"`
	Value   string   `xml:"v"`
	Inline  xlsxText `xml:"is"`
}

type xlsxStyles struct {
	Formats []struct {
		ID   int    `xml:"numFmtId,attr"`
		Code string `xml:"formatCode,attr"`
	} `xml:"numFmts>numFmt"`
	Cells []struct {
		FormatID int `xml:"numFmtId,attr"`
	} `xml:"cellXfs>xf"`
}

var simpleImportPercent = regexp.MustCompile(`^0(?:\.(0{0,12}#{0,12}))?( ?)%$`)

// Only C2's numeric percentage display is interpreted. Other metadata formats,
// and all money/date cells, retain raw serialized values. FloatString operates
// on the exact rational and rounds display ties away from zero.
func importExpectedReturn(raw string, style int, styles xlsxStyles) (string, error) {
	if style < 0 || style >= len(styles.Cells) {
		if style == 0 && len(styles.Cells) == 0 {
			return raw, nil
		}
		return "", invalidImport("cell_style", 2, "C")
	}
	id := styles.Cells[style].FormatID
	format := ""
	switch id {
	case 9:
		format = "0%"
	case 10:
		format = "0.00%"
	default:
		if id < 164 {
			return raw, nil
		}
		for _, f := range styles.Formats {
			if f.ID == id {
				format = f.Code
				break
			}
		}
		if format == "" {
			return "", invalidImport("number_format", 2, "C")
		}
		if !strings.Contains(format, "%") {
			return raw, nil
		}
	}
	match := simpleImportPercent.FindStringSubmatch(format)
	if match == nil || len(match[1]) > 12 || strings.Contains(format, ".") && match[1] == "" {
		return "", invalidImport("unsupported_percent_format", 2, "C")
	}
	r, err := importRational(raw)
	if err != nil {
		return "", invalidImport("expected_return_number", 2, "C")
	}
	r.Mul(r, big.NewRat(100, 1))
	value := r.FloatString(len(match[1]))
	for n := strings.Count(match[1], "#"); n > 0 && strings.HasSuffix(value, "0"); n-- {
		value = strings.TrimSuffix(value, "0")
	}
	value = strings.TrimSuffix(value, ".")
	return value + match[2] + "%", nil
}

type xlsxRelationships struct {
	Items []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
		Mode   string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

// ParseAccountImport reads OOXML directly so numeric values retain their exact
// serialized decimal text. It never evaluates formulas or writes temporary files.
func ParseAccountImport(ctx context.Context, data []byte) (*ImportPreview, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > maxImportUpload {
		return nil, &importError{Code: "upload_too_large", Status: 413}
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) > 1024 {
		return nil, invalidImport("invalid_zip", 0, "")
	}
	parts := make(map[string][]byte)
	var expanded uint64
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := f.Name
		lower := strings.ToLower(name)
		if f.FileInfo().IsDir() && f.UncompressedSize64 == 0 {
			name = strings.TrimSuffix(name, "/")
			if name != "" && path.Clean(name) == name && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") && !strings.Contains(name, "\\") {
				continue
			}
		}
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || parts[name] != nil {
			return nil, invalidImport("invalid_zip_entry", 0, "")
		}
		if strings.Contains(lower, "vbaproject") || strings.Contains(lower, "externallinks/") || strings.Contains(lower, "embeddings/") {
			return nil, invalidImport("active_content", 0, "")
		}
		if f.UncompressedSize64 > maxImportExpanded || expanded > maxImportExpanded-f.UncompressedSize64 {
			return nil, invalidImport("expanded_size_limit", 0, "")
		}
		expanded += f.UncompressedSize64
		r, err := f.Open()
		if err != nil {
			return nil, invalidImport("invalid_zip_entry", 0, "")
		}
		b, readErr := io.ReadAll(io.LimitReader(r, int64(f.UncompressedSize64)+1))
		closeErr := r.Close()
		if readErr != nil || closeErr != nil || uint64(len(b)) != f.UncompressedSize64 {
			return nil, invalidImport("invalid_zip_entry", 0, "")
		}
		parts[name] = b
		if strings.HasSuffix(lower, ".rels") {
			var rel xlsxRelationships
			if xml.Unmarshal(b, &rel) != nil {
				return nil, invalidImport("invalid_relationships", 0, "")
			}
			for _, item := range rel.Items {
				if item.Mode != "" && item.Mode != "Internal" || strings.Contains(strings.ToLower(item.Type), "external") || strings.Contains(item.Target, ":") || strings.HasPrefix(item.Target, "//") {
					return nil, invalidImport("external_link", 0, "")
				}
			}
		}
	}
	contentTypes := strings.ToLower(string(parts["[Content_Types].xml"]))
	if !strings.Contains(contentTypes, "spreadsheetml.sheet.main+xml") || strings.Contains(contentTypes, "macroenabled") || strings.Contains(contentTypes, "vbaproject") {
		return nil, invalidImport("invalid_workbook_type", 0, "")
	}
	var workbook struct {
		Props struct {
			Date1904 bool `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(parts["xl/workbook.xml"], &workbook) != nil {
		return nil, invalidImport("invalid_workbook", 0, "")
	}
	id := ""
	for _, s := range workbook.Sheets {
		if s.Name == importSheet {
			if id != "" {
				return nil, invalidImport("duplicate_sheet", 0, "")
			}
			id = s.ID
		}
	}
	if id == "" {
		return nil, invalidImport("missing_sheet", 0, "")
	}
	var rels xlsxRelationships
	if xml.Unmarshal(parts["xl/_rels/workbook.xml.rels"], &rels) != nil {
		return nil, invalidImport("invalid_relationships", 0, "")
	}
	sheetPath := ""
	for _, r := range rels.Items {
		if r.ID == id && strings.HasSuffix(r.Type, "/worksheet") {
			if strings.HasPrefix(r.Target, "/") {
				sheetPath = strings.TrimPrefix(path.Clean(r.Target), "/")
			} else {
				sheetPath = path.Clean(path.Join("xl", r.Target))
			}
		}
	}
	if !strings.HasPrefix(sheetPath, "xl/") || parts[sheetPath] == nil {
		return nil, invalidImport("missing_sheet", 0, "")
	}
	var shared struct {
		Items []xlsxText `xml:"si"`
	}
	if b := parts["xl/sharedStrings.xml"]; b != nil && xml.Unmarshal(b, &shared) != nil {
		return nil, invalidImport("invalid_shared_strings", 0, "")
	}
	var styles xlsxStyles
	if b := parts["xl/styles.xml"]; b != nil && xml.Unmarshal(b, &styles) != nil {
		return nil, invalidImport("invalid_styles", 0, "")
	}
	var expectedReturn string
	cells := make(map[int][7]string)
	d := xml.NewDecoder(bytes.NewReader(parts[sheetPath]))
	lastRow := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalidImport("invalid_sheet", 0, "")
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "row" {
			continue
		}
		var row struct {
			Number int        `xml:"r,attr"`
			Cells  []xlsxCell `xml:"c"`
		}
		if d.DecodeElement(&row, &start) != nil {
			return nil, invalidImport("invalid_row", 0, "")
		}
		if row.Number <= lastRow || row.Number > maxImportRows+4 {
			return nil, invalidImport("row_limit_or_order", 0, "")
		}
		lastRow = row.Number
		var values [7]string
		seen := map[int]bool{}
		for _, c := range row.Cells {
			col := strings.TrimRight(c.Ref, "0123456789")
			n, err := strconv.Atoi(strings.TrimPrefix(c.Ref, col))
			if err != nil || n != row.Number || len(col) != 1 || col[0] < 'A' || col[0] > 'G' {
				return nil, invalidImport("column_limit_or_reference", row.Number, "")
			}
			index := int(col[0] - 'A')
			if seen[index] {
				return nil, invalidImport("duplicate_cell", row.Number, col)
			}
			seen[index] = true
			if c.Formula != nil {
				return nil, invalidImport("formula", row.Number, col)
			}
			v := c.Value
			switch c.Type {
			case "", "n", "d":
			case "str":
				v = decodeXLSXString(v)
			case "inlineStr":
				v = c.Inline.value()
			case "s":
				i, err := strconv.Atoi(v)
				if err != nil || i < 0 || i >= len(shared.Items) {
					return nil, invalidImport("invalid_shared_string", row.Number, col)
				}
				v = shared.Items[i].value()
			default:
				return nil, invalidImport("cell_type", row.Number, col)
			}
			values[index] = v
			if row.Number == 2 && col == "C" {
				expectedReturn = v
				if (c.Type == "" || c.Type == "n") && v != "" {
					expectedReturn, err = importExpectedReturn(v, c.Style, styles)
					if err != nil {
						return nil, err
					}
				}
			}
		}
		cells[row.Number] = values
	}
	for n, headers := range map[int][]string{1: {"账户名称", "账户目标", "预期年化收益率", "预计投资时间", "币种", "四笔钱"}, 4: {"记录类型", "记账时间", "转入转出金额", "总资产金额", "投资日志", "创建时间"}} {
		for col, expected := range headers {
			if cells[n][col] != expected {
				return nil, invalidImport("header", n, string(rune('A'+col)))
			}
		}
	}
	if cells[4][6] != "" && cells[4][6] != "明细" {
		return nil, invalidImport("header", 4, "G")
	}
	m := cells[2]
	currency := Currency(strings.TrimSpace(m[4]))
	switch currency {
	case "人民币":
		currency = CNY
	case "港币", "港元":
		currency = HKD
	case "美元":
		currency = USD
	}
	if !currency.valid() {
		return nil, invalidImport("currency", 2, "E")
	}
	if !validText(m[0]) {
		return nil, invalidImport("account_name", 2, "A")
	}
	p := &ImportPreview{Metadata: ImportMetadata{Name: m[0], Goal: m[1], ExpectedReturn: expectedReturn, ExpectedReturnRaw: m[2], InvestmentHorizon: m[3], Currency: currency, MoneyBucket: m[5]}, Rows: make([]ImportedRow, 0), Warnings: []string{"reported_history_only", "incremental_import_not_supported", "creation_time_precision_nanoseconds_truncated"}}
	totalIn, totalOut := new(big.Int), new(big.Int)
	for n := 5; n <= lastRow; n++ {
		v := cells[n]
		blank := true
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		r := ImportedRow{SourceRow: n, Note: v[4], Detail: v[6], DateRaw: v[1], CreatedRaw: v[5]}
		switch v[0] {
		case "记总资产":
			r.Kind = "asset"
		case "转入转出":
			r.Kind = "cash_flow"
		default:
			return nil, invalidImport("record_type", n, "A")
		}
		r.Date, err = importDate(v[1], workbook.Props.Date1904, false)
		if err != nil {
			return nil, invalidImport("date", n, "B")
		}
		if v[5] != "" {
			r.SourceCreatedAt, err = importDate(v[5], workbook.Props.Date1904, true)
			if err != nil {
				return nil, invalidImport("created_at", n, "F")
			}
		}
		r.Flow, err = importMoney(v[2])
		if err != nil {
			return nil, invalidImport("money_precision", n, "C")
		}
		r.TotalAssets, err = importMoney(v[3])
		if err != nil || r.TotalAssets != nil && *r.TotalAssets < 0 {
			return nil, invalidImport("money_precision", n, "D")
		}
		if r.Kind == "asset" && r.TotalAssets == nil {
			return nil, invalidImport("required_total", n, "D")
		}
		if r.Kind == "cash_flow" && r.Flow == nil {
			return nil, invalidImport("required_flow", n, "C")
		}
		p.Rows = append(p.Rows, r)
		s := &p.Summary
		s.RowCount++
		if s.From == "" || r.Date < s.From {
			s.From = r.Date
		}
		if r.Date > s.To {
			s.To = r.Date
		}
		if r.Flow != nil {
			s.FlowCount++
			amount := big.NewInt(int64(*r.Flow))
			if amount.Sign() < 0 {
				totalOut.Sub(totalOut, amount)
			} else {
				totalIn.Add(totalIn, amount)
			}
		}
		if r.TotalAssets != nil {
			s.AssetCount++
			if s.LatestAssetDate == nil || r.Date >= *s.LatestAssetDate {
				date := r.Date
				s.LatestAssetDate = &date
				s.LatestAssets = copyMoney(r.TotalAssets)
			}
		}
	}
	if len(p.Rows) == 0 {
		return nil, invalidImport("empty_records", 0, "")
	}
	if !totalIn.IsInt64() || !totalOut.IsInt64() {
		return nil, invalidImport("summary_overflow", 0, "")
	}
	p.Summary.TotalIn, p.Summary.TotalOut = Money(totalIn.Int64()), Money(totalOut.Int64())
	canonical, err := json.Marshal(struct {
		Version  string         `json:"version"`
		Metadata ImportMetadata `json:"metadata"`
		Rows     []ImportedRow  `json:"rows"`
	}{importFormatVersion, p.Metadata, p.Rows})
	if err != nil {
		return nil, invalidImport("encoding", 0, "")
	}
	hash := sha256.Sum256(canonical)
	p.Digest = hex.EncodeToString(hash[:])
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

var importNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]{1,3})?$`)

func importRational(raw string) (*big.Rat, error) {
	s := strings.TrimSpace(raw)
	if len(s) > 128 || !importNumber.MatchString(s) {
		return nil, ErrPrecision
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, ErrPrecision
	}
	return r, nil
}
func importMoney(raw string) (*Money, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	r, err := importRational(raw)
	if err != nil {
		return nil, err
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return nil, ErrPrecision
	}
	m := Money(r.Num().Int64())
	return &m, nil
}

// Numeric serial fractions are truncated to nanoseconds, never rounded into
// another business day. Excel's fictitious 1900-02-29 (serial 60) is rejected.
// Timezone-less creation timestamps are Beijing local time, returned as UTC.
func importDate(raw string, date1904, created bool) (string, error) {
	s := strings.TrimSpace(raw)
	var t time.Time
	if r, err := importRational(s); err == nil {
		if r.Sign() < 0 || r.Cmp(big.NewRat(2958466, 1)) >= 0 {
			return "", ErrQuery
		}
		days := new(big.Int).Quo(r.Num(), r.Denom()).Int64()
		fraction := new(big.Rat).Sub(r, big.NewRat(days, 1))
		fraction.Mul(fraction, big.NewRat(int64(24*time.Hour), 1))
		ns := new(big.Int).Quo(fraction.Num(), fraction.Denom()).Int64()
		base := time.Date(1899, 12, 31, 0, 0, 0, 0, fxBeijing)
		if date1904 {
			base = time.Date(1904, 1, 1, 0, 0, 0, 0, fxBeijing)
		} else {
			if days == 60 {
				return "", ErrQuery
			}
			if days > 60 {
				days--
			}
		}
		t = base.AddDate(0, 0, int(days)).Add(time.Duration(ns))
	} else {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006/01/02 15:04:05.999999999", "2006-01-02 15:04", "2006/01/02 15:04", "2006-01-02", "2006/01/02", "2006/1/2", "2006-1-2"} {
			parsed, e := time.ParseInLocation(layout, s, fxBeijing)
			if e == nil {
				t = parsed
				break
			}
		}
	}
	if t.IsZero() || t.Year() < 1 || t.Year() > 9999 {
		return "", fmt.Errorf("invalid date")
	}
	if created {
		return t.UTC().Format(time.RFC3339Nano), nil
	}
	return t.In(fxBeijing).Format(time.DateOnly), nil
}

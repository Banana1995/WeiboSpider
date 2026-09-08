package ledger

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountImportStyledPercentageMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, raw, format, want string
		id                      int
	}{
		{"built-in integer", "0.125", "", "13%", 9},
		{"built-in decimal", "0.125", "", "12.50%", 10},
		{"custom decimal", "0.125", "0.0%", "12.5%", 164},
		{"custom negative tie", "-0.1255", "0.0%", "-12.6%", 164},
		{"custom optional decimals", "0.125", "0.0##%", "12.5%", 164},
		{"custom optional fraction", "0.12", "0.##%", "12%", 164},
		{"custom space", "1.25e-1", "0.0 %", "12.5 %", 164},
		{"beyond float precision", "900719925474.0993", "0.00%", "90071992547409.93%", 164},
		{"general", "0.125", "", "0.125", 0},
		{"non-percent format", "0.125", "0.000", "0.125", 164},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := syntheticImportParts(t, [][7]string{{"记总资产", "46023", "", "5000", "", "46023.5", ""}})
			parts["xl/styles.xml"] = fmt.Sprintf(`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="1"><numFmt numFmtId="164" formatCode="%s"/></numFmts><cellXfs count="2"><xf numFmtId="0"/><xf numFmtId="%d" applyNumberFormat="1"/></cellXfs></styleSheet>`, tc.format, tc.id)
			sheet := parts["xl/worksheets/sheet1.xml"]
			sheet = strings.Replace(sheet, `<c r="C2" t="inlineStr"><is><t xml:space="preserve">5%</t></is></c>`, `<c r="C2" s="1"><v>`+tc.raw+`</v></c>`, 1)
			// Even a percentage style on an amount or date must not reinterpret it.
			sheet = strings.Replace(sheet, `<c r="D5" t="inlineStr"><is><t xml:space="preserve">5000</t></is></c>`, `<c r="D5" s="1"><v>5000</v></c>`, 1)
			sheet = strings.Replace(sheet, `<c r="B5" t="inlineStr"><is><t xml:space="preserve">46023</t></is></c>`, `<c r="B5" s="1"><v>46023</v></c>`, 1)
			parts["xl/worksheets/sheet1.xml"] = sheet
			data := syntheticImportZip(t, parts)
			p := parseSynthetic(t, data)
			require.Equal(t, tc.want, p.Metadata.ExpectedReturn)
			require.Equal(t, tc.raw, p.Metadata.ExpectedReturnRaw)
			require.Equal(t, Money(500000), *p.Rows[0].TotalAssets)
			require.Equal(t, "2026-01-01", p.Rows[0].Date)
			require.Equal(t, "46023", p.Rows[0].DateRaw)
			s := importStoreFixture(t)
			_, err := s.ConfirmAccountImport(t.Context(), "synthetic", "percent", p.Digest, true, data)
			require.NoError(t, err)
			saved, err := s.ImportSummary(t.Context(), "synthetic")
			require.NoError(t, err)
			require.Equal(t, p.Metadata, saved.Metadata)
		})
	}
	t.Run("unsupported percent fails closed", func(t *testing.T) {
		var styles xlsxStyles
		// Section/color/literal/conditional Excel format interpretation is not supported.
		for _, code := range []string{"0.0%;[Red]-0.0%", "[>=1]0%", `0.0\%`, `0.0"%"`, "0.%"} {
			styles.Cells = append(styles.Cells[:0], struct {
				FormatID int `xml:"numFmtId,attr"`
			}{164})
			styles.Formats = append(styles.Formats[:0], struct {
				ID   int    `xml:"numFmtId,attr"`
				Code string `xml:"formatCode,attr"`
			}{164, code})
			_, err := importExpectedReturn("0.125", 0, styles)
			var e *importError
			require.ErrorAs(t, err, &e)
			require.Equal(t, "unsupported_percent_format", e.Code)
			require.Equal(t, 2, e.Row)
			require.Equal(t, "C", e.Column)
		}
	})
	// Text already containing a percent sign is preserved rather than multiplied.
	p := parseSynthetic(t, syntheticImport(t))
	require.Equal(t, "5%", p.Metadata.ExpectedReturn)
	require.Equal(t, "5%", p.Metadata.ExpectedReturnRaw)
}

func TestAccountImportOOXMLStringEscapes(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"first_x000D__x000A_second", "first\r\nsecond"},
		{"_x005F_x000D_", "_x000D_"},
		{"_x005F_x005F_x000A_", "_x005F_x000A_"},
		{"_x005F__x000A_", "_\n"},
		{"_xD83D__xDE80_", "\U0001F680"},
		{"native \U0001F680 \U00020000", "native \U0001F680 \U00020000"},
		{"_xD800_ _xDC00_ _xZZZZ_", "_xD800_ _xDC00_ _xZZZZ_"},
	} {
		require.Equal(t, tc.want, decodeXLSXString(tc.raw))
	}
	parts := syntheticImportParts(t, [][7]string{{"记总资产", "2026-01-01", "", "5000", "Synthetic_x000D__x000A_\U0001F680", "", "literal _x005F_x000A_ \U00020000"}})
	parts["xl/sharedStrings.xml"] = `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><r><t>Synthetic_x000D_</t></r><r><t>_x000A__xD83D_</t></r><r><t>_xDE80_</t></r></si></sst>`
	inline := parseSynthetic(t, syntheticImportZip(t, parts))
	parts["xl/worksheets/sheet1.xml"] = strings.Replace(parts["xl/worksheets/sheet1.xml"], `<c r="E5" t="inlineStr"><is><t xml:space="preserve">Synthetic_x000D__x000A_`+"\U0001F680"+`</t></is></c>`, `<c r="E5" t="s"><v>0</v></c>`, 1)
	shared := parseSynthetic(t, syntheticImportZip(t, parts))
	require.Equal(t, "Synthetic\r\n\U0001F680", shared.Rows[0].Note)
	require.Equal(t, "literal _x000A_ \U00020000", shared.Rows[0].Detail)
	require.Equal(t, inline.Rows, shared.Rows)
	require.Equal(t, inline.Digest, shared.Digest)
	encoded, err := json.Marshal(shared.Rows)
	require.NoError(t, err)
	var roundTrip []ImportedRow
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	require.Equal(t, shared.Rows, roundTrip)
}

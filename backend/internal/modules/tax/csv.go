package tax

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxRecords = 10000
const MaxUpload = 8 << 20

type Record struct {
	Date      string `json:"date"`
	Account   string `json:"account"`
	AccountID string `json:"account_id"`
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	Market    string `json:"market"`
	Direction string `json:"direction"`
	Currency  string `json:"currency"`
	Quantity  string `json:"quantity"`
	Price     string `json:"price"`
	Amount    string `json:"amount"`
	Fee       string `json:"fee"`
	Change    string `json:"change"`
	Note      string `json:"note"`
	Reason    string `json:"reason"`
	Source    string `json:"source"`
	Line      int    `json:"line"`
}

var Headers = []string{"日期", "账户名称", "账户号码", "品类", "证券代码", "交易所", "方向", "币种", "数量", "价格", "成交金额", "总费用", "变动金额", "备注", "变动原因"}
var aliases = map[string]string{"成交时间": "日期", "账户名": "账户名称", "类别": "品类", "类型": "品类", "代码名称": "证券代码", "交易所/市场": "交易所", "市场": "交易所", "数量/面值": "数量", "面值": "数量", "费用": "总费用"}

func digest(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
func (r Record) identity() string {
	r.Source, r.Line = "", 0
	b, _ := json.Marshal(r)
	return digest(b)
}
func (r Record) timestamp() string {
	if len(r.Date) == 8 {
		return r.Date[:4] + "-" + r.Date[4:6] + "-" + r.Date[6:] + " 00:00:00"
	}
	if len(r.Date) == 10 {
		return r.Date + " 00:00:00"
	}
	return r.Date
}

func ParseCSV(data []byte, filename string) ([]Record, error) {
	if len(data) > MaxUpload {
		return nil, fmt.Errorf("文件超过 8 MB")
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("请使用 UTF-8 编码的 CSV")
	}
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("无法读取 CSV 表头")
	}
	columns := make(map[string]int)
	for i, text := range header {
		text = strings.TrimSpace(text)
		if alias := aliases[text]; alias != "" {
			text = alias
		}
		if _, exists := columns[text]; exists {
			return nil, fmt.Errorf("重复表头：%s", text)
		}
		columns[text] = i
	}
	for _, name := range Headers {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("缺少表头：%s", name)
		}
	}
	if len(header) != len(Headers) {
		return nil, fmt.Errorf("CSV 应包含标准 15 列")
	}
	result := make([]Record, 0)
	for {
		cells, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 结构错误：%w", err)
		}
		line, _ := reader.FieldPos(0)
		get := func(key string) string { return strings.TrimSpace(cells[columns[key]]) }
		r := Record{Date: get("日期"), Account: get("账户名称"), AccountID: get("账户号码"), Kind: get("品类"), Code: get("证券代码"), Market: get("交易所"), Direction: get("方向"), Currency: get("币种"), Quantity: get("数量"), Price: get("价格"), Amount: get("成交金额"), Fee: get("总费用"), Change: get("变动金额"), Note: get("备注"), Reason: get("变动原因"), Source: filename, Line: line}
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("第 %d 行：%w", line, err)
		}
		result = append(result, r)
		if len(result) > MaxRecords {
			return nil, fmt.Errorf("最多支持 %d 条记录", MaxRecords)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("CSV 没有数据行")
	}
	return result, nil
}

func (r Record) validate() error {
	for _, value := range []string{r.Account, r.AccountID, r.Code, r.Market, r.Reason} {
		if len(value) > 512 || strings.ContainsRune(value, 0) {
			return fmt.Errorf("字段过长或包含无效字符")
		}
	}
	if len(r.Note) > 4096 || strings.ContainsRune(r.Note, 0) {
		return fmt.Errorf("备注过长或包含无效字符")
	}
	if r.Account == "" || r.AccountID == "" {
		return fmt.Errorf("账户名称、账户号码不能为空")
	}
	layout := "2006-01-02 15:04:05"
	if len(r.Date) == 8 {
		layout = "20060102"
	} else if len(r.Date) == 10 {
		layout = time.DateOnly
	}
	date, err := time.ParseInLocation(layout, r.Date, time.FixedZone("Beijing", 8*3600))
	if err != nil || date.Year() < 1900 || date.Year() > 2100 {
		return fmt.Errorf("日期必须是北京时间 YYYY-MM-DD HH:mm:ss 或 YYYYMMDD")
	}
	if r.Currency != "HKD" && r.Currency != "USD" {
		return fmt.Errorf("仅支持 HKD、USD")
	}
	fee, err := decimal(r.Fee)
	if err != nil || fee.Sign() < 0 {
		return fmt.Errorf("总费用必须为非负十进制数")
	}
	change, err := decimal(r.Change)
	if err != nil {
		return fmt.Errorf("变动金额无效")
	}
	if r.Kind == "资金进出" {
		if fee.Sign() != 0 {
			return fmt.Errorf("资金行总费用必须为 0，费用使用变动原因分类")
		}
		if r.Direction != "IN" && r.Direction != "OUT" {
			return fmt.Errorf("资金方向必须为 IN 或 OUT")
		}
		if r.Direction == "IN" && change.Sign() < 0 || r.Direction == "OUT" && change.Sign() > 0 {
			return fmt.Errorf("资金方向与金额正负不符")
		}
		return nil
	}
	if r.Kind != "证券" && r.Kind != "基金" && r.Kind != "债券" {
		return fmt.Errorf("不支持品类 %s（未导入该文件）", r.Kind)
	}
	if r.Code == "" || r.Kind == "证券" && r.Market == "" {
		return fmt.Errorf("缺少证券代码或交易所")
	}
	valid := r.Direction == "申购" || r.Direction == "赎回"
	if r.Kind == "证券" {
		valid = r.Direction == "买入开仓" || r.Direction == "卖出平仓" || r.Direction == "卖出开仓" || r.Direction == "买入平仓"
	}
	if !valid {
		return fmt.Errorf("品类与交易方向不匹配")
	}
	q, err := decimal(r.Quantity)
	if err != nil || q.Sign() == 0 {
		return fmt.Errorf("交易数量必须非零")
	}
	p, err := decimal(r.Price)
	if err != nil || p.Sign() < 0 {
		return fmt.Errorf("价格无效")
	}
	a, err := decimal(r.Amount)
	if err != nil || sub(a, fee).Cmp(change) != 0 {
		return fmt.Errorf("成交金额 − 总费用必须等于变动金额")
	}
	buy := r.Direction == "买入开仓" || r.Direction == "买入平仓" || r.Direction == "申购"
	if buy && a.Sign() > 0 || !buy && a.Sign() < 0 {
		return fmt.Errorf("买卖方向与成交金额正负不符")
	}
	return nil
}

package ledger

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

const maxMCPFieldIssues = 32

type mcpValidationNode struct {
	schema     mcpSchema
	resolved   *jsonschema.Resolved
	properties map[string]*mcpValidationNode
	branches   []*mcpValidationNode
}

func mcpResolveSchema(schema any) *jsonschema.Resolved {
	raw, err := json.Marshal(schema)
	if err != nil {
		panic("ledger MCP schema cannot be encoded")
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(raw, &parsed); err != nil {
		panic("ledger MCP schema cannot be decoded: " + err.Error())
	}
	resolved, err := parsed.Resolve(nil)
	if err != nil {
		panic("ledger MCP schema cannot be resolved: " + err.Error())
	}
	return resolved
}

func mcpCompileInput(schema mcpSchema) *mcpValidationNode {
	n := &mcpValidationNode{schema: schema, resolved: mcpResolveSchema(schema), properties: map[string]*mcpValidationNode{}}
	props, _ := schema["properties"].(mcpSchema)
	for key, value := range props {
		n.properties[key] = mcpCompileInput(value.(mcpSchema))
	}
	branches, _ := schema["oneOf"].([]any)
	for _, branch := range branches {
		n.branches = append(n.branches, mcpCompileInput(branch.(mcpSchema)))
	}
	return n
}

type mcpInputProblems struct {
	err    *MCPToolError
	fields map[string]MCPContractField
	seen   map[string]bool
}

func mcpProblems(tool string, schema mcpSchema) *mcpInputProblems {
	e := mcpIssue("invalid_input", "参数不完整、格式不合法或字段互相冲突；本次调用未产生账务写入", "fix_input")
	e.Tool, e.ContractVersion, e.NextAction = tool, mcpContractVersion, "fix_input_then_resubmit"
	p := &mcpInputProblems{err: e, fields: map[string]MCPContractField{}, seen: map[string]bool{}}
	for _, field := range mcpContractFields(schema, "", "") {
		if _, exists := p.fields[field.Path]; !exists {
			p.fields[field.Path] = field
		}
	}
	return p
}

func (p *mcpInputProblems) add(path, code, message string) {
	if path == "" {
		path = "$"
	}
	// Field names are untrusted too. Do not echo unlimited keys or any values.
	if utf8.RuneCountInString(path) > 256 {
		path = string([]rune(path)[:256]) + "…"
	}
	if p.seen[path] {
		return
	}
	if len(p.err.Fields) >= maxMCPFieldIssues {
		p.err.Truncated = true
		return
	}
	p.seen[path] = true
	p.err.Fields = append(p.err.Fields, path)
	if code == "required" {
		p.err.MissingFields = append(p.err.MissingFields, path)
		p.err.Code = "incomplete_entry"
	} else {
		p.err.InvalidFields = append(p.err.InvalidFields, MCPFieldIssue{path, code, message})
	}
	field := p.fields[path]
	if field.Question == "" {
		return
	}
	action := "question"
	switch field.ValueSource {
	case "ledger":
		action = "read_ledger_context"
	case "instrument":
		action = "resolve_instrument"
	case "client":
		action = "preserve_request_identity"
	case "optional":
		action = "fix_input"
	}
	p.err.Questions = append(p.err.Questions, MCPQuestion{path, field.Question, action})
	p.err.Requirements = append(p.err.Requirements, field)
	if action == "question" {
		p.err.QuestionTool = "question"
		p.err.NextAction = "ask_user_with_question_then_resubmit"
		p.err.Message = "记账数据缺失或不明确，本次调用未产生账务写入。必须调用question工具按questions补问用户，获得明确数据后重新提交，禁止猜填。"
	} else if p.err.NextAction != "ask_user_with_question_then_resubmit" {
		p.err.NextAction = "resolve_context_then_resubmit"
	}
}

func mcpDecodeValue(raw []byte) (any, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	err := d.Decode(&value)
	return mcpSchemaNumbers(value), err
}

// jsonschema-go v0.4.3 classifies json.Number as a string during its type
// check. Decode the protocol's integer counters exactly, without ever turning
// financial input into float64. Non-integer JSON numbers remain distinguishable
// from actual strings and are rejected by the typed/field checks below.
func mcpSchemaNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
	case map[string]any:
		for key, item := range v {
			v[key] = mcpSchemaNumbers(item)
		}
	case []any:
		for i, item := range v {
			v[i] = mcpSchemaNumbers(item)
		}
	}
	return value
}

func (n *mcpValidationNode) validate(tool string, raw json.RawMessage) *MCPToolError {
	p := mcpProblems(tool, n.schema)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !utf8.Valid(raw) || uniqueJSON(d, 0, true) != nil {
		p.add("$", "invalid_json", "参数必须为字段不重复的JSON对象")
		return p.err
	}
	if _, err := d.Token(); err != io.EOF {
		p.add("$", "invalid_json", "只允许一个JSON对象")
		return p.err
	}
	value, err := mcpDecodeValue(raw)
	if err != nil {
		p.add("$", "invalid_json", "参数必须是合法JSON对象")
		return p.err
	}
	// The full compiled JSON Schema is authoritative. The traversal adds bounded,
	// field-addressed diagnostics and exact financial/date checks, not a substitute
	// implementation of JSON Schema.
	schemaErr := n.resolved.Validate(value)
	n.collect(value, "", p)
	if schemaErr != nil && len(p.err.Fields) == 0 {
		p.add("$", "invalid_shape", "参数不符合工具Schema；请查询ledger_get_write_contract核对分支要求")
	}
	if len(p.err.Fields) == 0 {
		return nil
	}
	return p.err
}

func (n *mcpValidationNode) collect(value any, prefix string, problems *mcpInputProblems) {
	object, isObject := value.(map[string]any)
	if n.schema["type"] == "object" || n.schema["properties"] != nil {
		if !isObject {
			problems.add(strings.TrimSuffix(prefix, "."), "invalid_type", "必须提供对象，不能为null或其他类型")
			return
		}
		required, _ := n.schema["required"].([]string)
		for _, key := range required {
			if _, present := object[key]; !present {
				problems.add(prefix+key, "required", "缺少必填字段")
			}
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if child := n.properties[key]; child != nil {
				child.collect(object[key], prefix+key+".", problems)
			} else if allowed, ok := n.schema["additionalProperties"].(bool); ok && !allowed {
				problems.add(prefix+key, "unknown_field", "该字段不属于此工具或当前业务分支")
			}
		}
	}
	if len(n.branches) > 0 && isObject {
		discriminator := mcpSchemaText(n.schema, "x-ledger-discriminator")
		selected, exists := object[discriminator]
		var chosen *mcpValidationNode
		for _, branch := range n.branches {
			if field := branch.properties[discriminator]; field != nil && exists && field.resolved.Validate(selected) == nil {
				chosen = branch
				break
			}
		}
		if chosen != nil {
			chosen.collect(value, prefix, problems)
		} else {
			code := "invalid_value"
			if !exists {
				code = "required"
			}
			problems.add(prefix+discriminator, code, "请先明确业务分支，值必须符合工具Schema")
			// Even with an unknown branch, shared requirements can be reported now.
			common, _ := n.branches[0].schema["required"].([]string)
			for _, key := range common {
				shared := key != discriminator
				for _, other := range n.branches[1:] {
					keys, _ := other.schema["required"].([]string)
					shared = shared && slices.Contains(keys, key)
				}
				if shared {
					if v, present := object[key]; !present {
						problems.add(prefix+key, "required", "缺少各分支共有的必填字段")
					} else if child := n.branches[0].properties[key]; child != nil {
						child.collect(v, prefix+key+".", problems)
					}
				}
			}
		}
	}
	if not, ok := n.schema["not"].(mcpSchema); ok && isObject {
		forbidden, _ := not["required"].([]string)
		for _, key := range forbidden {
			if _, present := object[key]; present {
				problems.add(prefix+key, "forbidden_field", "当前操作禁止提供该字段，包括null")
			}
		}
	}
	if n.schema["type"] == "object" || n.schema["properties"] != nil || len(n.branches) > 0 {
		return
	}
	path := strings.TrimSuffix(prefix, ".")
	if n.schema["type"] == "string" && value != nil {
		if _, ok := value.(string); !ok {
			problems.add(path, "invalid_type", "必须使用字符串；金融数值不能使用JSON number")
			return
		}
	}
	if err := n.resolved.Validate(value); err != nil {
		code, message := "invalid_value", mcpSchemaText(n.schema, "description")
		if value == nil {
			code, message = "null_not_allowed", "null不能替代真实值；未知时先补问或查询"
		} else if n.schema["type"] == "string" {
			if _, ok := value.(string); !ok {
				code, message = "invalid_type", "必须使用字符串；金融数值不能使用JSON number"
			} else if value == "" {
				code, message = "empty_value", "空字符串不能替代真实值"
			}
		}
		problems.add(path, code, message)
		return
	}
	text, ok := value.(string)
	if !ok {
		return
	}
	if n.schema["x-ledger-date"] == true && !validDate(text) {
		problems.add(path, "invalid_date", "请提供真实有效的YYYY-MM-DD日期")
	}
	if scale := mcpSchemaText(n.schema, "x-ledger-scale"); scale != "" {
		digits, _ := strconv.Atoi(scale)
		if _, err := mcpDecimal(text, digits, n.schema["x-ledger-positive"] == true); err != nil {
			problems.add(path, "invalid_precision", mcpSchemaText(n.schema, "description"))
		}
	}
	if limit, ok := n.schema["maxLength"].(int); ok && !mcpText(text, limit, n.schema["x-ledger-nonblank"] == true) {
		problems.add(path, "invalid_text", "文本不得超过字节上限或包含非法控制字符；必填文本不能全为空白")
	}
}

func mcpDescribeFailure(tool string, schema mcpSchema, err error) *MCPToolError {
	// Never mutate errors retained by another caller or stored as evidence.
	copy := *mcpToolFailure(err)
	copy.Tool, copy.ContractVersion = tool, mcpContractVersion
	copy.NextAction = copy.RetryPolicy
	p := mcpProblems(tool, schema)
	for _, path := range copy.Fields {
		if tool == "ledger_edit_entry" && slices.Contains([]string{"kind", "date", "quantity", "price", "fee", "amount", "currency", "fx", "note"}, path) {
			path = "replacement." + path
		}
		code := copy.Code
		if code == "fx_required" {
			code = "required"
		}
		p.add(path, code, copy.Message)
	}
	copy.Fields, copy.MissingFields, copy.InvalidFields, copy.Questions = p.err.Fields, p.err.MissingFields, p.err.InvalidFields, p.err.Questions
	copy.Requirements, copy.QuestionTool = p.err.Requirements, p.err.QuestionTool
	if copy.QuestionTool != "" {
		copy.NextAction = p.err.NextAction
		copy.Message += "；必须调用question按questions向用户补问具体数据，获得明确答案后再提交"
	}
	copy.Truncated = p.err.Truncated
	return &copy
}

func mcpWriteEffect(problem *MCPToolError) string {
	switch problem.Code {
	case "incomplete_entry", "invalid_input", "invalid_precision", "unsupported_instrument", "currency_mismatch", "invalid_observed_date", "fx_required", "entry_not_found", "account_not_found", "instrument_ambiguous", "instrument_unavailable", "version_conflict", "idempotency_conflict", "operation_voided", "stock_quantity", "stock_cash", "dividend_conflict", "conflict":
		return "none"
	default:
		return "unknown"
	}
}

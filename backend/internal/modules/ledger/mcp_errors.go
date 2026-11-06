package ledger

import (
	"context"
	"errors"

	"github.com/mattn/go-sqlite3"
)

type MCPToolError struct {
	Code            string             `json:"code"`
	Message         string             `json:"message"`
	Fields          []string           `json:"fields"`
	RetryPolicy     string             `json:"retry_policy"`
	Details         map[string]string  `json:"details"`
	Tool            string             `json:"tool,omitempty"`
	ContractVersion string             `json:"contract_version,omitempty"`
	MissingFields   []string           `json:"missing_fields,omitempty"`
	InvalidFields   []MCPFieldIssue    `json:"invalid_fields,omitempty"`
	Questions       []MCPQuestion      `json:"questions,omitempty"`
	Requirements    []MCPContractField `json:"requirements,omitempty"`
	QuestionTool    string             `json:"question_tool,omitempty"`
	NextAction      string             `json:"next_action,omitempty"`
	Truncated       bool               `json:"truncated,omitempty"`
}

type MCPFieldIssue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type MCPQuestion struct {
	Field      string `json:"field"`
	Question   string `json:"question"`
	NextAction string `json:"next_action"`
}

func (e *MCPToolError) Error() string { return e.Message }

func mcpIssue(code, message, retry string, fields ...string) *MCPToolError {
	if fields == nil {
		fields = []string{}
	}
	return &MCPToolError{Code: code, Message: message, Fields: fields, RetryPolicy: retry, Details: map[string]string{}}
}

func mcpInvalid(fields ...string) *MCPToolError {
	return mcpIssue("invalid_input", "请提供完整、合法且互相一致的字段", "fix_input", fields...)
}

func mcpToolFailure(err error) *MCPToolError {
	var problem *MCPToolError
	if errors.As(err, &problem) {
		return problem
	}
	code, message, retry := "write_status_unknown", "暂时无法确定结果；写入请查询回执或使用原请求重试", "inspect_receipt_then_retry_same"
	var sqlErr sqlite3.Error
	switch {
	case errors.Is(err, ErrCorrupt):
		code, message, retry = "data_integrity", "账本或回执证据不一致", "stop_and_report"
	case errors.Is(err, ErrVersion):
		return mcpIssue("version_conflict", "持仓已发生变化，请重新读取并核对交易", "reload_state", "expected_version")
	case errors.Is(err, ErrIdempotency):
		return mcpIssue("idempotency_conflict", "同一请求 ID 已用于不同内容", "inspect_receipt", "request_id")
	case errors.Is(err, ErrStockQuantity):
		code, message, retry = "stock_quantity", "历史卖出超过当时持有数量", "correct_history"
	case errors.Is(err, ErrStockCash):
		code, message, retry = "stock_cash", "现金基准后的余额不足", "correct_history_or_balance"
	case errors.Is(err, ErrVoided):
		code, message, retry = "operation_voided", "记录已经作废", "inspect_entry"
	case errors.Is(err, ErrPrecision):
		code, message, retry = "invalid_precision", "数值格式、精度或范围不合法", "fix_input"
	case errors.Is(err, ErrOperation), errors.Is(err, ErrQuery), errors.Is(err, ErrUnsupported):
		return mcpInvalid()
	case errors.Is(err, ErrNotFound):
		code, message, retry = "account_not_found", "账户不存在或已经删除", "select_existing"
	case errors.Is(err, ErrConflict):
		code, message, retry = "conflict", "证券或交易身份存在冲突，请重新读取", "reload_state"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = "request_timeout"
	case errors.As(err, &sqlErr) && (sqlErr.Code == sqlite3.ErrBusy || sqlErr.Code == sqlite3.ErrLocked):
		code, message, retry = "storage_busy", "账本存储暂忙，请使用原请求重试", "retry_same_request"
	}
	out := mcpIssue(code, message, retry)
	var rule *stockRuleError
	if errors.As(err, &rule) {
		out.Details = map[string]string{"entry_id": rule.EntryID, "instrument_id": rule.InstrumentID, "date": rule.Date}
	}
	return out
}

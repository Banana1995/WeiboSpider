package options

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Provider interface {
	State(context.Context) (ProviderState, error)
	Instruments(context.Context, string) ([]Instrument, error)
	Expirations(context.Context, string) ([]string, error)
	Chain(context.Context, string, string, string, string) ([]Contract, error)
	Quotes(context.Context, []Contract, string) (QuoteBatch, error)
}
type QuoteBatch struct {
	Quotes      []Quote `json:"quotes"`
	Underlying  *string `json:"underlying"`
	MarketState string  `json:"market_state"`
}
type ProviderError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *ProviderError) Error() string { return e.Message }

type HTTPProvider struct {
	base   string
	client *http.Client
}

func NewHTTPProvider(base string) (*HTTPProvider, error) {
	u, e := url.Parse(base)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("OPTIONS_QUOTES_URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	return &HTTPProvider{strings.TrimRight(base, "/"), &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *HTTPProvider) call(ctx context.Context, path string, in, out any) error {
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/v1/"+path, bytes.NewBufferString(encode(in)))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := p.client.Do(r)
	if e != nil {
		return &ProviderError{"unavailable", "行情适配服务连接失败，请检查 OpenD 和适配服务", true}
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if e != nil || len(data) > 16<<20 {
		return &ProviderError{"invalid_response", "行情返回超过大小限制", false}
	}
	if resp.StatusCode != 200 {
		var pe ProviderError
		if json.Unmarshal(data, &pe) != nil || pe.Message == "" {
			return &ProviderError{"upstream_error", fmt.Sprintf("行情服务返回 HTTP %d", resp.StatusCode), resp.StatusCode >= 500}
		}
		return &pe
	}
	if e = json.Unmarshal(data, out); e != nil {
		return &ProviderError{"invalid_response", "行情适配服务返回格式不正确", false}
	}
	return nil
}
func (p *HTTPProvider) State(ctx context.Context) (ProviderState, error) {
	var out ProviderState
	e := p.call(ctx, "state", struct{}{}, &out)
	return out, e
}
func (p *HTTPProvider) Instruments(ctx context.Context, q string) ([]Instrument, error) {
	var out struct {
		Items []Instrument `json:"items"`
	}
	e := p.call(ctx, "instruments", map[string]string{"query": q}, &out)
	return out.Items, e
}
func (p *HTTPProvider) Expirations(ctx context.Context, symbol string) ([]string, error) {
	var out struct {
		Dates []string `json:"dates"`
	}
	e := p.call(ctx, "expirations", map[string]string{"symbol": symbol}, &out)
	return out.Dates, e
}
func (p *HTTPProvider) Chain(ctx context.Context, symbol, from, to, side string) ([]Contract, error) {
	var out struct {
		Items []Contract `json:"items"`
	}
	e := p.call(ctx, "chain", map[string]string{"symbol": symbol, "from": from, "to": to, "option_type": optionSide(side)}, &out)
	return out.Items, e
}
func (p *HTTPProvider) Quotes(ctx context.Context, contracts []Contract, symbol string) (QuoteBatch, error) {
	var out QuoteBatch
	e := p.call(ctx, "quotes", map[string]any{"symbol": symbol, "contracts": contracts}, &out)
	return out, e
}

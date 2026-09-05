package liquor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SinaSource struct {
	base     string
	client   *http.Client
	interval time.Duration
}

func NewSinaSource(base string, interval time.Duration) (*SinaSource, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || interval < 0 {
		return nil, fmt.Errorf("%w: source URL or interval", ErrSourceData)
	}
	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	return &SinaSource{base: strings.TrimRight(base, "/"), interval: interval, client: &http.Client{
		Timeout: 20 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (s *SinaSource) Close() { s.client.CloseIdleConnections() }

type sourceEnvelope[T any] struct {
	Result struct {
		Status struct {
			Code *int `json:"code"`
		} `json:"status"`
		Data T `json:"data"`
	} `json:"result"`
}

func getSourceJSON[T any](ctx context.Context, source *SinaSource, path string) (data T, err error) {
	timer := time.NewTimer(source.interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return data, ctx.Err()
	case <-timer.C:
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.base+path, nil)
	if err != nil {
		return data, fmt.Errorf("source request: %w", err)
	}
	request.Header.Set("Referer", "https://finance.sina.cn/")
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response, err := source.client.Do(request)
	if err != nil {
		return data, fmt.Errorf("fetch source: %w", err)
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		return data, fmt.Errorf("source HTTP status %d", response.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return data, fmt.Errorf("read source: %w", err)
	}
	if len(body) > maxBody {
		return data, fmt.Errorf("%w: response too large", ErrSourceData)
	}
	var envelope sourceEnvelope[T]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return data, fmt.Errorf("decode source: %w", err)
	}
	if envelope.Result.Status.Code == nil || *envelope.Result.Status.Code != 0 {
		return data, fmt.Errorf("%w: source status", ErrSourceData)
	}
	return envelope.Result.Data, nil
}

func (s *SinaSource) Fetch(ctx context.Context) (Snapshot, error) {
	list, err := getSourceJSON[rawList](ctx, s, "/list")
	if err != nil {
		return Snapshot{}, err
	}
	if len(list.Items) == 0 || len(list.Items) > 200 || list.Count != len(list.Items) {
		return Snapshot{}, fmt.Errorf("%w: product count", ErrSourceData)
	}
	snapshot := Snapshot{Series: make([]Series, 0, len(list.Items))}
	seen := make(map[ProductID]bool)
	for _, item := range list.Items {
		product, latest, err := item.parse()
		if err != nil {
			return Snapshot{}, err
		}
		if seen[product.ID] {
			return Snapshot{}, fmt.Errorf("%w: duplicate product", ErrSourceData)
		}
		seen[product.ID] = true
		if snapshot.Date == "" {
			snapshot.Date = latest.Date
		}
		if snapshot.Date != latest.Date {
			return Snapshot{}, fmt.Errorf("%w: mixed snapshot dates", ErrSourceData)
		}
		detail, err := getSourceJSON[rawDetail](ctx, s, "/detail/"+strconv.FormatInt(int64(product.ID), 10))
		if err != nil {
			return Snapshot{}, fmt.Errorf("product %d: %w", product.ID, err)
		}
		series, err := detail.parse(product, latest)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Series = append(snapshot.Series, series)
	}
	return snapshot, nil
}

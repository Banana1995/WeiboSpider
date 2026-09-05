//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type processLog struct {
	mu      sync.Mutex
	all     bytes.Buffer
	pending []byte
	ready   chan string
}

func (l *processLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all.Write(p)
	l.pending = append(l.pending, p...)
	for {
		line, rest, found := bytes.Cut(l.pending, []byte{'\n'})
		if !found {
			break
		}
		var event struct {
			Message string `json:"msg"`
			Address string `json:"address"`
		}
		if json.Unmarshal(line, &event) == nil && event.Message == "backend.listening" {
			select {
			case l.ready <- event.Address:
			default:
			}
		}
		l.pending = rest
	}
	return len(p), nil
}

func (l *processLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.all.String()
}

type process struct {
	cmd  *exec.Cmd
	log  *processLog
	done chan struct{}
	err  error
	url  string
}

func buildServer(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", binary, "./cmd/server")
	cmd.Dir = ".."
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return binary
}

func cleanEnvironment(directory string, override map[string]string) []string {
	values := map[string]string{
		"BACKEND_ADDR": "127.0.0.1:0", "BACKEND_DATA_DIR": directory, "BACKEND_API_TOKEN": "",
		"LIQUOR_AUTO_SYNC": "false", "LIQUOR_REQUEST_INTERVAL": "1s", "LIQUOR_SYNC_TIMEOUT": "2m",
		"LIQUOR_SOURCE_URL": "https://business.cj.sina.cn/api/liquor_price",
	}
	for key, value := range override {
		values[key] = value
	}
	environment := make([]string, 0)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "BACKEND_") && !strings.HasPrefix(value, "LIQUOR_") {
			environment = append(environment, value)
		}
	}
	for key, value := range values {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func startProcess(t *testing.T, binary, directory string, override map[string]string) *process {
	t.Helper()
	p := &process{cmd: exec.Command(binary), log: &processLog{ready: make(chan string, 1)}, done: make(chan struct{})}
	p.cmd.Env = cleanEnvironment(directory, override)
	p.cmd.Stdout, p.cmd.Stderr = p.log, p.log
	require.NoError(t, p.cmd.Start())
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() { p.stop(t, false) })
	select {
	case address := <-p.log.ready:
		p.url = "http://" + address
	case <-p.done:
		t.Fatalf("server exited before listening: %v\n%s", p.err, p.log.String())
	case <-time.After(15 * time.Second):
		t.Fatalf("server did not become ready\n%s", p.log.String())
	}
	t.Logf("started actual binary pid=%d address=%s", p.cmd.Process.Pid, p.url)
	return p
}

func (p *process) stop(t *testing.T, kill bool) {
	t.Helper()
	select {
	case <-p.done:
		return
	default:
	}
	var err error
	if kill {
		err = p.cmd.Process.Kill()
	} else {
		err = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	require.NoError(t, err)
	select {
	case <-p.done:
		if !kill {
			require.NoError(t, p.err, "%s", p.log.String())
		}
	case <-time.After(15 * time.Second):
		killErr := p.cmd.Process.Kill()
		<-p.done
		t.Fatalf("server did not stop: %v\n%s", killErr, p.log.String())
	}
}

func request(t *testing.T, p *process, method, path, body string, headers map[string]string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, p.url+path, strings.NewReader(body))
	require.NoError(t, err)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if strings.EqualFold(key, "Host") {
			req.Host = value
		} else {
			req.Header.Set(key, value)
		}
	}
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "%s", p.log.String())
	defer func() { require.NoError(t, response.Body.Close()) }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	require.Contains(t, response.Header.Get("Content-Type"), "application/json")
	require.True(t, json.Valid(data), "%s", data)
	return response.StatusCode, data
}

type syncStatus struct {
	State       string `json:"state"`
	RunID       string `json:"run_id"`
	Records     int    `json:"records"`
	Date        string `json:"last_price_date"`
	LastSuccess string `json:"last_success_at"`
	Error       string `json:"error_code"`
}

const prefix = "/api/platform/liquor"

func status(t *testing.T, p *process) syncStatus {
	t.Helper()
	code, body := request(t, p, "GET", prefix+"/sync", "", nil)
	require.Equal(t, 200, code, "%s", body)
	var result syncStatus
	require.NoError(t, json.Unmarshal(body, &result))
	return result
}

func trigger(t *testing.T, p *process) string {
	t.Helper()
	code, body := request(t, p, "POST", prefix+"/sync", `{}`, nil)
	require.Equal(t, 202, code, "%s", body)
	var result struct {
		RunID string `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	require.NotEmpty(t, result.RunID)
	return result.RunID
}

func waitSync(t *testing.T, p *process, runID string) syncStatus {
	t.Helper()
	timeout := time.NewTimer(150 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			t.Fatalf("process exited during sync: %v\n%s", p.err, p.log.String())
		case <-timeout.C:
			t.Fatalf("sync timeout\n%s", p.log.String())
		case <-ticker.C:
			s := status(t, p)
			require.Equal(t, runID, s.RunID)
			if s.State != "running" {
				return s
			}
		}
	}
}

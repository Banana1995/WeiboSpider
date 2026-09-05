//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessAccessProtection(t *testing.T) {
	binary, directory := buildServer(t), t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = cleanEnvironment(directory, map[string]string{"BACKEND_ADDR": "0.0.0.0:0"})
	output, err := command.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "BACKEND_API_TOKEN is required")
	t.Log("PASS configuration: externally reachable listener is rejected without a token, before listening")

	token := rand.Text() + rand.Text()
	p := startProcess(t, binary, directory, map[string]string{"BACKEND_API_TOKEN": token})
	code, _ := request(t, p, "GET", "/healthz", "", nil)
	require.Equal(t, 200, code)
	code, _ = request(t, p, "GET", prefix+"/latest", "", nil)
	require.Equal(t, 401, code)
	code, _ = request(t, p, "GET", prefix+"/latest", "", map[string]string{"Authorization": "Bearer wrong"})
	require.Equal(t, 401, code)
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		code, _ = request(t, p, "GET", prefix+"/latest", "", map[string]string{"Authorization": scheme + " " + token})
		require.Equal(t, 200, code)
	}
	code, _ = request(t, p, "POST", prefix+"/sync", `{}`, map[string]string{"Authorization": "Bearer " + token, "Origin": "https://evil.invalid"})
	require.Equal(t, 403, code)
	require.NotContains(t, p.log.String(), token)
	t.Log("PASS token: absent/wrong=401, correct=200, cross-origin write=403, token absent from process logs")
	p.stop(t, false)
	p = startProcess(t, binary, directory, nil)
	code, _ = request(t, p, "GET", prefix+"/latest", "", map[string]string{"Host": "rebind.invalid"})
	require.Equal(t, 403, code)
	code, _ = request(t, p, "POST", prefix+"/sync", `{}`, map[string]string{"Sec-Fetch-Site": "cross-site"})
	require.Equal(t, 403, code)
	t.Log("PASS local mode: DNS-rebinding Host and cross-site browser writes rejected")
}

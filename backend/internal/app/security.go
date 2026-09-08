package app

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"path"
	"strings"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

func protect(cfg Config, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(cfg.APIToken))
	csrf := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			next.ServeHTTP(w, r)
			return
		}
		// Only canonical ledger paths are public; never exempt sibling APIs or redirects.
		ledgerPath := r.URL.Path == "/api/platform/ledger" || strings.HasPrefix(r.URL.Path, "/api/platform/ledger/")
		publicLedger := cfg.LedgerEnabled && ledgerPath && path.Clean(r.URL.Path) == strings.TrimSuffix(r.URL.Path, "/")
		if !publicLedger && cfg.APIToken != "" {
			authorization := r.Header.Get("Authorization")
			scheme, token, _ := strings.Cut(authorization, " ")
			actual := sha256.Sum256([]byte(token))
			if !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				httpapi.Fail(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
				return
			}
		} else if !publicLedger && cfg.APIToken == "" && !loopbackHost(r.Host) {
			// Local-only mode must also reject DNS-rebinding hosts, not just CORS.
			httpapi.Fail(w, http.StatusForbidden, "invalid_host", "loopback host required")
			return
		}
		if err := csrf.Check(r); err != nil {
			httpapi.Fail(w, http.StatusForbidden, "cross_origin", "cross-origin writes are not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

package accounts

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
	"golang.org/x/time/rate"
)

const Prefix = "/api/platform/auth/"

type HTTP struct {
	store   *Store
	secure  bool
	global  *rate.Limiter
	mu      sync.Mutex
	names   map[string]*rate.Limiter
	workers chan struct{}
}

func NewHTTP(s *Store, secure bool) *HTTP {
	return &HTTP{store: s, secure: secure, global: rate.NewLimiter(1, 16), names: map[string]*rate.Limiter{}, workers: make(chan struct{}, 2)}
}
func (h *HTTP) cookie(token string, age int) *http.Cookie {
	name := "site_local"
	if h.secure {
		name = "__Host-site_session"
	}
	return &http.Cookie{Name: name, Value: token, MaxAge: age, Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode}
}
func (h *HTTP) token(r *http.Request) string {
	cookies := r.CookiesNamed(h.cookie("", 0).Name)
	if len(cookies) != 1 {
		return ""
	}
	return cookies[0].Value
}

func (h *HTTP) Require(w http.ResponseWriter, r *http.Request) (User, bool) {
	w.Header().Set("Cache-Control", "no-store")
	u, e := h.store.lookup(r.Context(), h.token(r))
	if errors.Is(e, errCredentials) {
		httpapi.Fail(w, 401, "login_required", "请先登录")
		return User{}, false
	}
	if e != nil {
		httpapi.Fail(w, 503, "unavailable", "账户服务暂时不可用")
		return User{}, false
	}
	return u, true
}

func (h *HTTP) allowed(name string) bool {
	if !h.global.Allow() {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.names[name] == nil {
		if len(h.names) >= 512 {
			for k, v := range h.names {
				if v.Tokens() >= 8 {
					delete(h.names, k)
				}
			}
		}
		if len(h.names) >= 512 {
			return false
		}
		h.names[name] = rate.NewLimiter(rate.Every(time.Minute), 8)
	}
	return h.names[name].Allow()
}

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action := r.URL.Path
	if r.URL.RawQuery != "" {
		httpapi.Fail(w, 400, "invalid", "不接受查询参数")
		return
	}
	switch action {
	case Prefix + "me", Prefix + "owner":
		// Nginx authorizes Flask requests via a GET subrequest, preserving the original method.
		if action == Prefix+"owner" && r.Header.Get("X-Site-Method") != "" {
			original := r.Clone(r.Context())
			original.Method = r.Header.Get("X-Site-Method")
			if http.NewCrossOriginProtection().Check(original) != nil {
				httpapi.Fail(w, 403, "cross_origin", "需要同源请求")
				return
			}
		}
		if r.Method != "GET" {
			httpapi.Fail(w, 405, "method", "需要 GET")
			return
		}
		u, ok := h.Require(w, r)
		if !ok {
			return
		}
		if action == Prefix+"owner" && !u.Owner() {
			httpapi.Fail(w, 403, "forbidden", "此页仅对站主开放")
			return
		}
		httpapi.Write(w, 200, u)
		return
	case Prefix + "login", Prefix + "activate", Prefix + "logout":
	default:
		httpapi.Fail(w, 404, "not_found", "接口不存在")
		return
	}
	if r.Method != "POST" {
		httpapi.Fail(w, 405, "method", "需要 POST")
		return
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		httpapi.Fail(w, 415, "invalid", "需要 JSON")
		return
	}
	if action == Prefix+"logout" {
		if _, e := h.store.ExecContext(r.Context(), `DELETE FROM sessions WHERE digest=?`, digest(h.token(r))); e != nil {
			httpapi.Fail(w, 503, "unavailable", "退出失败，请重试")
			return
		}
		http.SetCookie(w, h.cookie("", -1))
		w.WriteHeader(204)
		return
	}
	var input struct{ Username, Password, Code string }
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(&struct{}{}) != io.EOF {
		httpapi.Fail(w, 400, "invalid", "请检查输入")
		return
	}
	name := normalize(input.Username)
	if !usernamePattern.MatchString(name) || len(input.Password) > 256 {
		httpapi.Fail(w, 400, "invalid", "请检查用户名和密码")
		return
	}
	if !h.allowed(name) {
		w.Header().Set("Retry-After", "60")
		httpapi.Fail(w, 429, "rate_limited", "尝试次数过多，请稍后再试")
		return
	}
	select {
	case h.workers <- struct{}{}:
		defer func() { <-h.workers }()
	default:
		httpapi.Fail(w, 429, "busy", "请稍后再试")
		return
	}
	var u User
	var token string
	var e error
	if action == Prefix+"activate" {
		u, token, e = h.store.activate(r.Context(), name, input.Code, input.Password)
	} else {
		u, token, e = h.store.login(r.Context(), name, input.Password)
	}
	if errors.Is(e, ErrPassword) {
		httpapi.Fail(w, 400, "invalid_password", ErrPassword.Error())
		return
	}
	if errors.Is(e, errCredentials) {
		msg := "用户名或密码不正确"
		if action == Prefix+"activate" {
			msg = "设置链接已失效，请重新获取"
		}
		httpapi.Fail(w, 401, "denied", msg)
		return
	}
	if e != nil {
		httpapi.Fail(w, 503, "unavailable", "账户服务暂时不可用")
		return
	}
	http.SetCookie(w, h.cookie(token, int(sessionLife.Seconds())))
	httpapi.Write(w, 200, u)
}

package tax

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

var keyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Handler struct {
	Store *Store
	Names *NamesClient
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.Handle("/api/platform/tax/", http.HandlerFunc(h.serve))
}
func fail(w http.ResponseWriter, code int, message string) {
	httpapi.Fail(w, code, "tax_error", message)
}
func (h Handler) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	keys := r.Header.Values("X-Tax-Key")
	if len(keys) != 1 || !keyPattern.MatchString(keys[0]) {
		fail(w, 401, "税务空间密钥无效，请重新打开页面")
		return
	}
	workspace := digest([]byte(keys[0]))
	path := strings.TrimPrefix(r.URL.Path, "/api/platform/tax/")
	if r.Method != "GET" && r.URL.RawQuery != "" {
		fail(w, 400, "写入请求不接受查询参数")
		return
	}
	switch {
	case path == "names" && r.Method == "GET":
		if r.URL.RawQuery != "" {
			fail(w, 400, "名称查询不接受额外参数")
			return
		}
		state, err := h.Store.State(r.Context(), workspace)
		if err != nil {
			fail(w, 500, "读取证券列表失败")
			return
		}
		httpapi.Write(w, 200, struct {
			Names map[string]string `json:"names"`
		}{h.Names.Lookup(r.Context(), state.Records)})
	case path == "state" && r.Method == "GET":
		state, err := h.Store.State(r.Context(), workspace)
		if err != nil {
			fail(w, 500, "读取税务资料失败")
			return
		}
		httpapi.Write(w, 200, state)
	case path == "report" && r.Method == "GET":
		values, err := parseFilter(r)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		state, err := h.Store.State(r.Context(), workspace)
		if err != nil {
			fail(w, 500, "读取税务资料失败")
			return
		}
		httpapi.Write(w, 200, Calculate(state.Records, state.Rates, values))
	case (path == "preview" || path == "imports") && r.Method == "POST":
		h.upload(w, r, workspace, path == "imports")
	case strings.HasPrefix(path, "imports/") && r.Method == "DELETE":
		id, err := strconv.ParseInt(strings.TrimPrefix(path, "imports/"), 10, 64)
		if err != nil || id <= 0 {
			fail(w, 400, "导入批次无效")
			return
		}
		if err = h.Store.Delete(r.Context(), workspace, id); err != nil {
			fail(w, 500, "删除批次失败")
			return
		}
		httpapi.Write(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, "税务接口不存在或请求方法不支持")
	}
}
func parseFilter(r *http.Request) (Filter, error) {
	q := r.URL.Query()
	f := Filter{q.Get("from"), q.Get("to"), q.Get("account")}
	for key, values := range q {
		if (key != "from" && key != "to" && key != "account") || len(values) != 1 {
			return f, errors.New("筛选参数无效")
		}
	}
	for _, d := range []string{f.From, f.To} {
		if d != "" {
			if _, err := time.Parse(time.DateOnly, d); err != nil {
				return f, errors.New("日期格式无效")
			}
		}
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return f, errors.New("开始日期不能晚于结束日期")
	}
	if len(f.Account) > 512 {
		return f, errors.New("账户筛选无效")
	}
	return f, nil
}
func (h Handler) upload(w http.ResponseWriter, r *http.Request, workspace string, confirm bool) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload+(64<<10))
	if err := r.ParseMultipartForm(MaxUpload); err != nil {
		fail(w, 400, "请上传不超过 8 MB 的 CSV 文件")
		return
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.File["file"]) != 1 {
		fail(w, 400, "每次请求应上传一个 CSV 文件")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "缺少 CSV 文件")
		return
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if len(name) > 255 || !strings.EqualFold(filepath.Ext(name), ".csv") {
		fail(w, 400, "请选择 CSV 文件")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxUpload+1))
	if err != nil {
		fail(w, 400, "读取文件失败")
		return
	}
	records, err := ParseCSV(data, name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	hash := digest(data)
	if confirm {
		if r.FormValue("digest") != hash {
			fail(w, 409, "文件与预览不一致，请重新预览")
			return
		}
		result, err := h.Store.Import(r.Context(), workspace, name, hash, records)
		if errors.Is(err, ErrLimit) {
			fail(w, 400, ErrLimit.Error())
			return
		}
		if err != nil {
			fail(w, 500, "导入失败，可重试同一文件")
			return
		}
		httpapi.Write(w, 200, result)
		return
	}
	state, err := h.Store.State(r.Context(), workspace)
	if err != nil {
		fail(w, 500, "读取已有导入失败")
		return
	}
	added := len(mergeRecords(state.Records, records)) - len(state.Records)
	httpapi.Write(w, 200, struct {
		Digest string   `json:"digest"`
		Count  int      `json:"count"`
		Added  int      `json:"added"`
		Rows   []Record `json:"rows"`
	}{hash, len(records), added, records[:min(len(records), 5)]})
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	DataDir, DownloadDir, AdminPassword, PublicURL string
	AllowPrivate                                   bool
	Assets                                         fs.FS
	Version                                        string
}
type session struct{ Expires time.Time }
type attempt struct {
	Count int
	Reset time.Time
}
type App struct {
	Config    Config
	Store     *Store
	Providers *Providers
	Manager   *Manager
	mu        sync.Mutex
	sessions  map[string]session
	attempts  map[string]attempt
	parseMu   sync.Mutex
	parses    map[string]*ParseSession
	password  string
	handler   http.Handler
}

func NewApp(c Config, baseURL string) (*App, error) {
	s, e := OpenStore(c.DataDir)
	if e != nil {
		return nil, e
	}
	a := &App{Config: c, Store: s, Providers: &Providers{Store: s, Net: NewNetwork(c.AllowPrivate)}, sessions: map[string]session{}, attempts: map[string]attempt{}, parses: map[string]*ParseSession{}}
	var auth struct {
		Hash string `json:"hash"`
	}
	ok, e := s.Get("config", "auth", &auth)
	if e != nil {
		s.DB.Close()
		return nil, e
	}
	if !ok {
		pw := c.AdminPassword
		if pw == "" {
			pw = randomID()[:20]
		}
		if len(pw) < 12 {
			s.DB.Close()
			return nil, errors.New("初始密码至少需要 12 个字符")
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(pw), 12)
		if e != nil {
			s.DB.Close()
			return nil, e
		}
		auth.Hash = string(hash)
		if e = s.Put("config", "auth", auth); e != nil {
			s.DB.Close()
			return nil, e
		}
		if c.AdminPassword == "" {
			if e = os.WriteFile(filepath.Join(c.DataDir, "initial-password.txt"), []byte(pw+"\n"), 0600); e != nil {
				s.DB.Close()
				return nil, e
			}
		}
	}
	a.password = auth.Hash
	a.Manager, e = NewManager(s, a.Providers, c.DownloadDir, baseURL)
	if e != nil {
		s.DB.Close()
		return nil, e
	}
	a.handler = a.routes()
	return a, nil
}
func (a *App) Close() error {
	e := a.Manager.Close()
	if e2 := a.Store.DB.Close(); e == nil {
		e = e2
	}
	return e
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errorOut(w http.ResponseWriter, e error) {
	var api *APIError
	if !errors.As(e, &api) {
		api = &APIError{"internal", "服务暂时无法完成操作，请检查网络和存储状态", 500}
	}
	jsonOut(w, api.Status, map[string]any{"error": api})
}
func readJSON(r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return &APIError{"content_type", "请求必须为 JSON", 415}
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		return fail("invalid_json", "请求字段或格式不正确")
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		return fail("invalid_json", "请求只能包含一个 JSON 对象")
	}
	return nil
}
func (a *App) authenticated(r *http.Request) bool {
	c, e := r.Cookie("yunx_session")
	if e != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[c.Value]
	if ok && s.Expires.After(time.Now()) {
		return true
	}
	delete(a.sessions, c.Value)
	return false
}
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]string{"status": "ok", "version": a.Config.Version})
	})
	mux.HandleFunc("GET /internal/transfer/{id}/{part}", a.Manager.Transfer)
	mux.HandleFunc("GET /api/auth/session", func(w http.ResponseWriter, r *http.Request) {
		s, _ := a.Store.Settings()
		jsonOut(w, 200, map[string]any{"authenticated": a.authenticated(r), "name": s.Name, "version": a.Config.Version})
	})
	mux.HandleFunc("POST /api/auth/login", a.login)
	api := func(pattern string, h func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if !a.authenticated(r) {
				errorOut(w, &APIError{"unauthorized", "请先登录", 401})
				return
			}
			if e := h(w, r); e != nil {
				errorOut(w, e)
			}
		})
	}
	api("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) error {
		c, _ := r.Cookie("yunx_session")
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
		a.cookie(w, r, "", -1)
		jsonOut(w, 200, map[string]bool{"ok": true})
		return nil
	})
	api("POST /api/auth/password", a.changePassword)
	api("GET /api/state", func(w http.ResponseWriter, r *http.Request) error {
		v, e := a.state()
		if e != nil {
			return e
		}
		jsonOut(w, 200, v)
		return nil
	})
	api("GET /api/events", a.events)
	api("POST /api/accounts", a.saveAccount)
	api("POST /api/accounts/{id}/check", a.checkAccount)
	api("DELETE /api/accounts/{id}", a.deleteAccount)
	api("POST /api/resolve", a.resolve)
	api("POST /api/browse", a.browse)
	api("POST /api/tasks", a.createTasks)
	api("POST /api/tasks/{id}/action", a.taskAction)
	api("PUT /api/settings", a.settings)
	api("GET /api/directories", a.directories)
	api("POST /api/directories", a.createDirectory)
	api("GET /api/tasks/{id}/file", a.file)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if a.Config.Assets == nil {
			http.Error(w, "Frontend not built: npm --prefix web run build", 503)
			return
		}
		if _, e := fs.Stat(a.Config.Assets, name); e != nil {
			name = "index.html"
		}
		b, e := fs.ReadFile(a.Config.Assets, name)
		if e != nil {
			http.Error(w, "Frontend not built", 503)
			return
		}
		if contentType := mime.TypeByExtension(filepath.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if strings.HasSuffix(name, ".js") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		} else if strings.HasSuffix(name, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else if strings.HasSuffix(name, ".html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.Write(b)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("X-YunX-Request") != "1" {
				errorOut(w, &APIError{"request_origin", "请求来源不允许", 403})
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				expected := r.Host
				if a.Config.PublicURL != "" {
					p, _ := url.Parse(a.Config.PublicURL)
					expected = p.Host
				}
				if e != nil || u.Host != expected {
					errorOut(w, &APIError{"request_origin", "请求来源不允许", 403})
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) cookie(w http.ResponseWriter, r *http.Request, value string, age int) {
	secure := r.TLS != nil || strings.HasPrefix(a.Config.PublicURL, "https://")
	http.SetCookie(w, &http.Cookie{Name: "yunx_session", Value: value, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if e := readJSON(r, &b); e != nil {
		errorOut(w, e)
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	v := a.attempts[host]
	if time.Now().After(v.Reset) {
		v = attempt{Reset: time.Now().Add(5 * time.Minute)}
	}
	if v.Count >= 5 {
		a.mu.Unlock()
		errorOut(w, &APIError{"rate_limited", "登录尝试过多，请在 5 分钟后重试", 429})
		return
	}
	v.Count++
	if len(a.attempts) > 1024 {
		for k, x := range a.attempts {
			if time.Now().After(x.Reset) {
				delete(a.attempts, k)
			}
		}
	}
	a.attempts[host] = v
	hash := a.password
	a.mu.Unlock()
	good := bcrypt.CompareHashAndPassword([]byte(hash), []byte(b.Password)) == nil
	if b.Username != "admin" || !good {
		errorOut(w, &APIError{"invalid_login", "用户名或密码不正确", 401})
		return
	}
	token := randomID()
	a.mu.Lock()
	for k, v := range a.sessions {
		if v.Expires.Before(time.Now()) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 32 {
		a.mu.Unlock()
		errorOut(w, fail("session_limit", "登录会话过多，请先退出其他设备"))
		return
	}
	if a.password != hash {
		a.mu.Unlock()
		errorOut(w, &APIError{"invalid_login", "密码已更新，请重新登录", 401})
		return
	}
	a.sessions[token] = session{time.Now().Add(12 * time.Hour)}
	delete(a.attempts, host)
	a.mu.Unlock()
	a.cookie(w, r, token, 43200)
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) changePassword(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	if len(b.Password) < 12 || len(b.Password) > 72 {
		return fail("password_length", "密码长度须为 12–72 字节")
	}
	a.mu.Lock()
	hash := a.password
	a.mu.Unlock()
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(b.Current)) != nil {
		return fail("password_current", "当前密码不正确")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
	if e != nil {
		return e
	}
	if e = a.Store.Put("config", "auth", map[string]string{"hash": string(h)}); e != nil {
		return e
	}
	a.mu.Lock()
	a.password = string(h)
	a.sessions = map[string]session{}
	a.mu.Unlock()
	_ = os.Remove(filepath.Join(a.Config.DataDir, "initial-password.txt"))
	a.cookie(w, r, "", -1)
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) accounts() ([]Account, error) {
	rows, e := a.Store.List("accounts")
	if e != nil {
		return nil, e
	}
	out := []Account{}
	for _, b := range rows {
		var x Account
		if e = json.Unmarshal(b, &x); e != nil {
			return nil, e
		}
		out = append(out, x.Public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (a *App) state() (map[string]any, error) {
	accounts, e := a.accounts()
	if e != nil {
		return nil, e
	}
	s, e := a.Store.Settings()
	if e != nil {
		return nil, e
	}
	events, e := a.Store.Events()
	if e != nil {
		return nil, e
	}
	var stat syscall.Statfs_t
	disk := map[string]any{"available": nil, "total": nil}
	if syscall.Statfs(a.Manager.root, &stat) == nil {
		disk["available"] = stat.Bavail * uint64(stat.Bsize)
		disk["total"] = stat.Blocks * uint64(stat.Bsize)
	}
	return map[string]any{"tasks": a.Manager.Snapshot(), "accounts": accounts, "settings": s, "platforms": Platforms, "events": events, "disk": disk, "version": a.Config.Version, "root": "/downloads", "updated_at": now()}, nil
}
func (a *App) events(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("stream unavailable")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	c, unsubscribe := a.Manager.Subscribe()
	defer unsubscribe()
	send := func() bool {
		if !a.authenticated(r) {
			return false
		}
		v, e := a.state()
		if e != nil {
			return false
		}
		b, e := json.Marshal(v)
		if e != nil {
			return false
		}
		_, e = fmt.Fprintf(w, "event: state\ndata: %s\n\n", b)
		flusher.Flush()
		return e == nil
	}
	if !send() {
		return nil
	}
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-a.Manager.ctx.Done():
			return nil
		case <-c:
			if !send() {
				return nil
			}
		case <-heartbeat.C:
			if !a.authenticated(r) {
				fmt.Fprint(w, "event: expired\ndata: {}\n\n")
				flusher.Flush()
				return nil
			}
			if _, e := fmt.Fprint(w, ": keepalive\n\n"); e != nil {
				return nil
			}
			flusher.Flush()
		}
	}
}
func (a *App) saveAccount(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ID         string `json:"id"`
		Platform   string `json:"platform"`
		Name       string `json:"name"`
		Credential string `json:"credential"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	valid := false
	key := "cookie"
	for _, p := range Platforms {
		if p.ID == b.Platform {
			valid = true
			key = p.Credential
		}
	}
	if !valid || strings.TrimSpace(b.Name) == "" || len(b.Name) > 80 {
		return fail("invalid_account", "请选择平台并填写 1–80 字符的账号名称")
	}
	if len(b.Credential) > 65536 || strings.ContainsAny(b.Credential, "\r\n") {
		return fail("invalid_credential", "凭据格式错误，请粘贴完整单行 Cookie 或 Token")
	}
	a.Providers.mu.Lock()
	defer a.Providers.mu.Unlock()
	var account Account
	if b.ID != "" {
		v, e := a.Providers.account(b.ID)
		if e != nil {
			return e
		}
		account = v
		if account.Platform != b.Platform {
			return fail("platform_changed", "已保存账号不能更换平台，请新建账号")
		}
	} else {
		accounts, e := a.accounts()
		if e != nil {
			return e
		}
		if len(accounts) >= 30 {
			return fail("account_limit", "最多保存 30 个账号")
		}
		account = Account{ID: randomID(), Platform: b.Platform, Credentials: map[string]string{"device_id": randomID()}, Status: "unchecked"}
	}
	if b.Credential != "" {
		account.Credentials[key] = strings.TrimSpace(b.Credential)
		account.Status = "unchecked"
		account.Message = ""
		account.CheckedAt = ""
	}
	if account.Credentials[key] == "" {
		return fail("credential_required", "请填写平台登录凭据")
	}
	account.Name = strings.TrimSpace(b.Name)
	account.UpdatedAt = now()
	if e := a.Store.Put("accounts", account.ID, account); e != nil {
		return e
	}
	jsonOut(w, 200, account.Public())
	return nil
}
func (a *App) checkAccount(w http.ResponseWriter, r *http.Request) error {
	account, e := a.Providers.account(r.PathValue("id"))
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	checked, _ := a.Providers.CheckAccount(ctx, account)
	a.Providers.mu.Lock()
	current, e := a.Providers.account(account.ID)
	if e == nil && current.UpdatedAt == account.UpdatedAt {
		current.Status = checked.Status
		current.Message = checked.Message
		current.Nickname = checked.Nickname
		current.CheckedAt = checked.CheckedAt
		e = a.Store.Put("accounts", current.ID, current)
	}
	a.Providers.mu.Unlock()
	if e != nil {
		return e
	}
	jsonOut(w, 200, current.Public())
	return nil
}
func (a *App) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	for _, t := range a.Manager.Snapshot() {
		if t.AccountID == id && (active(t.Status) || t.Status == "queued") {
			return fail("account_busy", "此账号有运行或排队中的任务，请先暂停任务")
		}
	}
	if e := a.Store.Delete("accounts", id); e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}
func publicBrowse(v BrowseResult) BrowseResult {
	for i := range v.Items {
		v.Items[i].Extra = nil
	}
	sort.SliceStable(v.Items, func(i, j int) bool {
		if v.Items[i].Directory != v.Items[j].Directory {
			return v.Items[i].Directory
		}
		return v.Items[i].Name < v.Items[j].Name
	})
	return v
}
func (a *App) resolve(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Text      string `json:"text"`
		Password  string `json:"password"`
		AccountID string `json:"account_id"`
		Cloud     bool   `json:"cloud"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	if len(b.Text) > 8192 {
		return fail("input_length", "链接内容过长")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	s, e := a.Providers.Resolve(ctx, b.Text, b.Password, b.AccountID, b.Cloud)
	if e != nil {
		return e
	}
	result, e := a.Providers.Browse(ctx, s, "0", 1)
	if e != nil {
		return e
	}
	a.parseMu.Lock()
	for id, v := range a.parses {
		t, _ := time.Parse(time.RFC3339, v.CreatedAt)
		if time.Since(t) > time.Hour {
			delete(a.parses, id)
		}
	}
	if len(a.parses) >= 100 {
		a.parseMu.Unlock()
		return fail("parse_limit", "解析会话已达上限，请稍后重试")
	}
	a.parses[s.ID] = s
	a.parseMu.Unlock()
	jsonOut(w, 200, publicBrowse(result))
	return nil
}
func (a *App) browse(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		SessionID string `json:"session_id"`
		Parent    string `json:"parent"`
		Page      int    `json:"page"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	a.parseMu.Lock()
	defer a.parseMu.Unlock()
	s := a.parses[b.SessionID]
	if s == nil {
		return fail("parse_expired", "解析会话已失效，请重新解析")
	}
	if b.Parent != "0" {
		f, ok := s.Items[b.Parent]
		if !ok || !f.Directory {
			return fail("invalid_folder", "请从已列出的文件夹进入")
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v, e := a.Providers.Browse(ctx, s, b.Parent, b.Page)
	if e != nil {
		return e
	}
	jsonOut(w, 200, publicBrowse(v))
	return nil
}
func (a *App) createTasks(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		SessionID string   `json:"session_id"`
		ItemIDs   []string `json:"item_ids"`
		Directory string   `json:"directory"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	if len(b.ItemIDs) == 0 || len(b.ItemIDs) > 1000 {
		return fail("selection_limit", "请选择 1–1000 项")
	}
	a.parseMu.Lock()
	defer a.parseMu.Unlock()
	s := a.parses[b.SessionID]
	if s == nil {
		return fail("parse_expired", "解析会话已失效，请重新解析")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	items := []Item{}
	seen := map[string]bool{}
	var expand func(Item, string, int) error
	expand = func(item Item, dir string, depth int) error {
		if depth > 20 || len(items) >= 1000 {
			return fail("folder_limit", "本次选择超过 20 层目录或 1000 个文件，请缩小选择范围")
		}
		if seen[item.ID] {
			return nil
		}
		seen[item.ID] = true
		if _, e := safeName(item.Name); e != nil {
			return e
		}
		if !item.Directory {
			extra := map[string]string{}
			for k, v := range item.Extra {
				extra[k] = v
			}
			item.Extra = extra
			item.Extra["relative_path"] = dir
			items = append(items, item)
			return nil
		}
		for page := 1; page <= 200; page++ {
			v, e := a.Providers.Browse(ctx, s, item.ID, page)
			if e != nil {
				return e
			}
			fresh := 0
			for _, child := range v.Items {
				if !seen[child.ID] {
					fresh++
					if e = expand(child, filepath.Join(dir, item.Name), depth+1); e != nil {
						return e
					}
				}
			}
			if !v.More {
				return nil
			}
			if len(v.Items) > 0 && fresh == 0 {
				return fail("pagination_loop", "平台重复返回目录页，已停止以避免重复下载")
			}
		}
		return fail("page_limit", "目录超过 200 页，请缩小选择范围")
	}
	for _, id := range b.ItemIDs {
		item, ok := s.Items[id]
		if !ok {
			return fail("invalid_item", "所选文件不在当前解析结果中")
		}
		if e := expand(item, "", 0); e != nil {
			return e
		}
	}
	if len(items) == 0 {
		return fail("empty_folder", "所选文件夹没有可下载文件")
	}
	tasks, e := a.Manager.Add(s, items, b.Directory)
	if e != nil {
		return e
	}
	jsonOut(w, 201, map[string]any{"tasks": tasks})
	return nil
}
func (a *App) taskAction(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Action     string `json:"action"`
		DeleteFile bool   `json:"delete_file"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	if e := a.Manager.Action(r.PathValue("id"), b.Action, b.DeleteFile); e != nil {
		return e
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) settings(w http.ResponseWriter, r *http.Request) error {
	var s Settings
	if e := readJSON(r, &s); e != nil {
		return e
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 60 || s.Concurrency < 1 || s.Concurrency > 8 || s.Connections < 1 || s.Connections > 32 || s.SpeedLimit < 0 || s.SpeedLimit > 1<<40 {
		return fail("invalid_settings", "名称须为 1–60 字符，并行任务为 1–8，单任务连接为 1–32，限速不能为负")
	}
	if s.Theme != "light" && s.Theme != "dark" && s.Theme != "auto" {
		return fail("invalid_theme", "主题选项无效")
	}
	if _, e := a.Manager.Directory(s.DefaultDir, true); e != nil {
		return e
	}
	if e := a.Manager.ApplySettings(s); e != nil {
		return e
	}
	if e := a.Store.Put("config", "settings", s); e != nil {
		return e
	}
	jsonOut(w, 200, s)
	return nil
}
func (a *App) directories(w http.ResponseWriter, r *http.Request) error {
	relative := r.URL.Query().Get("path")
	dir, e := a.Manager.Directory(relative, false)
	if e != nil {
		return e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return fail("directory_unavailable", "无法读取下载目录")
	}
	out := []string{}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != ".yunx" && entry.Type()&os.ModeSymlink == 0 {
			out = append(out, filepath.Join(relative, entry.Name()))
		}
	}
	jsonOut(w, 200, map[string]any{"path": relative, "directories": out})
	return nil
}
func (a *App) createDirectory(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Path string `json:"path"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	if _, e := a.Manager.Directory(b.Path, true); e != nil {
		return e
	}
	jsonOut(w, 201, map[string]bool{"ok": true})
	return nil
}
func (a *App) file(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	a.Manager.mu.Lock()
	t := a.Manager.tasks[id]
	if t == nil || !t.HasFile {
		a.Manager.mu.Unlock()
		return fail("file_missing", "任务还没有可读取的文件")
	}
	name, relative := t.Name, t.Directory
	a.Manager.mu.Unlock()
	dir, e := a.Manager.Directory(relative, false)
	if e != nil {
		return e
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return e
	}
	defer root.Close()
	f, e := root.Open(name)
	if e != nil {
		return fail("file_missing", "文件已被移动或删除")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return fail("file_missing", "目标不是普通文件")
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeContent(w, r, name, info.ModTime(), f)
	return nil
}

package server

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GopeedLab/gopeed/pkg/base"
	"github.com/GopeedLab/gopeed/pkg/download"
	ghttp "github.com/GopeedLab/gopeed/pkg/protocol/http"
	"golang.org/x/time/rate"
)

type engineStorage struct{ s *Store }

func (e engineStorage) Setup([]string) error                 { return nil }
func (e engineStorage) Put(b, k string, v any) error         { return e.s.Put("engine_"+b, k, v) }
func (e engineStorage) Get(b, k string, v any) (bool, error) { return e.s.Get("engine_"+b, k, v) }
func (e engineStorage) Delete(b, k string) error             { return e.s.Delete("engine_"+b, k) }
func (e engineStorage) List(b string, v any) error {
	rows, err := e.s.List("engine_" + b)
	if err != nil {
		return err
	}
	items := []json.RawMessage{}
	for _, b := range rows {
		items = append(items, b)
	}
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
func (e engineStorage) Pop(b, k string, v any) error {
	tx, err := e.s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	bucket := "engine_" + b
	err = tx.QueryRow("SELECT body FROM records WHERE bucket=? AND id=?", bucket, k).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	data, err = e.s.open(bucket, k, data)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, v); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM records WHERE bucket=? AND id=?", bucket, k); err != nil {
		return err
	}
	return tx.Commit()
}
func (e engineStorage) Close() error { return nil }
func (e engineStorage) Clear() error {
	_, err := e.s.DB.Exec(`DELETE FROM records WHERE substr(bucket,1,7)='engine_'`)
	return err
}

type engineEvent struct {
	id, engineID, key string
	part              int
	done, speed, size int64
}
type Manager struct {
	store         *Store
	providers     *Providers
	engine        *download.Downloader
	root, baseURL string
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	mu            sync.Mutex
	tasks         map[string]*Task
	settings      Settings
	lastProgress  map[string]time.Time
	cancels       map[string]context.CancelFunc
	eventMu       sync.Mutex
	events        []engineEvent
	eventReady    chan struct{}
	wake          chan struct{}
	listeners     map[chan struct{}]struct{}
	limiter       *rate.Limiter
	closing       bool
	engineState   map[string]string
	partProgress  map[string]engineEvent
	stateChanged  chan struct{}
}

func NewManager(s *Store, p *Providers, root, baseURL string) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0750); err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	workRoot := filepath.Join(abs, ".yunx")
	if err = os.Mkdir(workRoot, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(workRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fail("unsafe_directory", "任务目录不能是符号链接")
	}
	ctx, cancel := context.WithCancel(context.Background())
	settings, err := s.Settings()
	if err != nil {
		cancel()
		return nil, err
	}
	m := &Manager{store: s, providers: p, root: abs, baseURL: baseURL, ctx: ctx, cancel: cancel, tasks: map[string]*Task{}, engineState: map[string]string{}, partProgress: map[string]engineEvent{}, stateChanged: make(chan struct{}, 1), settings: settings, lastProgress: map[string]time.Time{}, cancels: map[string]context.CancelFunc{}, eventReady: make(chan struct{}, 1), wake: make(chan struct{}, 1), listeners: map[chan struct{}]struct{}{}, limiter: rate.NewLimiter(rate.Inf, 64*1024)}
	rows, err := s.List("tasks")
	if err != nil {
		cancel()
		return nil, err
	}
	for id, b := range rows {
		var t Task
		if err = json.Unmarshal(b, &t); err != nil {
			cancel()
			return nil, err
		}
		if t.Status == "pausing" {
			t.Status = "paused"
		}
		if active(t.Status) || t.Status == "queued" {
			if settings.ResumeOnRestart {
				t.Status = "queued"
			} else {
				t.Status = "paused"
			}
		}
		t.Speed = 0
		m.tasks[id] = &t
	}
	cfg := (&download.DownloaderConfig{Storage: engineStorage{s}, StorageDir: filepath.Join(abs, ".yunx", "engine"), RefreshInterval: 1000, WhiteDownloadDirs: []string{filepath.Join(abs, ".yunx", "*")}}).Init()
	fms := cfg.FetchManagers[:0]
	for _, fm := range cfg.FetchManagers {
		if fm.Name() == "http" {
			fms = append(fms, fm)
		}
	}
	cfg.FetchManagers = fms
	m.engine = download.NewDownloader(cfg)
	m.engine.Listener(m.onEngine)
	if err = m.engine.Setup(); err != nil {
		cancel()
		return nil, err
	}
	if err = m.ApplySettings(settings); err != nil {
		cancel()
		m.engine.Close()
		return nil, err
	}
	m.wg.Add(1)
	go m.run()
	m.signal()
	return m, nil
}
func active(s string) bool {
	return s == "resolving" || s == "downloading" || s == "merging" || s == "pausing"
}
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) notifyLocked() {
	for c := range m.listeners {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}
func (m *Manager) Subscribe() (chan struct{}, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := make(chan struct{}, 1)
	m.listeners[c] = struct{}{}
	return c, func() { m.mu.Lock(); delete(m.listeners, c); m.mu.Unlock() }
}
func (m *Manager) Snapshot() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Task{}
	for _, t := range m.tasks {
		out = append(out, t.Public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}
func (m *Manager) saveLocked(t *Task) {
	t.UpdatedAt = now()
	if err := m.store.Put("tasks", t.ID, t); err != nil {
		t.Error = "任务状态无法写入数据库，请检查数据目录"
		t.ErrorCode = "storage_error"
	}
	m.notifyLocked()
}
func (m *Manager) ApplySettings(s Settings) error {
	cfg, err := m.engine.GetConfig()
	if err != nil {
		return err
	}
	cfg.MaxRunning = s.Concurrency * 2
	cfg.DownloadDir = m.root
	cfg.AutoTorrent.Enable = false
	cfg.Archive.AutoExtract = false
	cfg.AutoDeleteMissingFileTasks = false
	if cfg.ProtocolConfig == nil {
		cfg.ProtocolConfig = map[string]any{}
	}
	cfg.ProtocolConfig["http"] = map[string]any{"connections": s.Connections, "userAgent": "YunX-Server", "useServerCtime": false}
	if err = m.engine.PutConfig(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	m.settings = s
	if s.SpeedLimit > 0 {
		m.limiter.SetLimit(rate.Limit(s.SpeedLimit))
	} else {
		m.limiter.SetLimit(rate.Inf)
	}
	m.mu.Unlock()
	m.signal()
	return nil
}
func safeName(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n") || len(name) > 220 {
		return "", fail("invalid_filename", "文件名包含不安全字符或过长，请更换名称")
	}
	return name, nil
}
func (m *Manager) Directory(relative string, create bool) (string, error) {
	if filepath.IsAbs(relative) || strings.Contains(relative, "\\") || strings.ContainsRune(relative, 0) {
		return "", fail("invalid_directory", "保存位置必须是下载目录内的相对路径")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || strings.HasPrefix(clean, ".yunx/") || clean == ".yunx" {
		return "", fail("invalid_directory", "保存位置不能超出下载目录或使用系统任务目录")
	}
	full := m.root
	for _, part := range strings.Split(clean, string(os.PathSeparator)) {
		if part == "." || part == "" {
			continue
		}
		if _, e := safeName(part); e != nil {
			return "", e
		}
		full = filepath.Join(full, part)
		info, e := os.Lstat(full)
		if errors.Is(e, os.ErrNotExist) && create {
			e = os.Mkdir(full, 0750)
			if e == nil {
				info, e = os.Lstat(full)
			}
		}
		if e != nil {
			return "", fail("directory_unavailable", "目录不存在或无法写入，请检查 NAS 挂载权限")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fail("unsafe_directory", "目标包含符号链接或非目录路径")
		}
	}
	return full, nil
}
func (m *Manager) Add(s *ParseSession, items []Item, dir string) ([]Task, error) {
	if len(items) == 0 || len(items) > 1000 {
		return nil, fail("selection_limit", "每批请选择 1–1000 个文件")
	}
	if _, e := m.Directory(dir, true); e != nil {
		return nil, e
	}
	for _, f := range items {
		if f.Directory {
			return nil, fail("directory_selection", "文件夹需要先展开")
		}
		if _, e := safeName(f.Name); e != nil {
			return nil, e
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tasks)+len(items) > 10000 {
		return nil, fail("task_limit", "任务记录已达 10000 条，请清理完成记录")
	}
	out := []Task{}
	pending := []*Task{}
	for _, f := range items {
		id := randomID()
		rel := dir
		if sub := f.Extra["relative_path"]; sub != "" {
			rel = filepath.Join(dir, sub)
			if _, e := m.Directory(rel, true); e != nil {
				return nil, e
			}
		}
		name := f.Name
		for suffix := 0; ; suffix++ {
			if suffix > 0 {
				name = strings.TrimSuffix(f.Name, filepath.Ext(f.Name)) + fmt.Sprintf(" (%d)", suffix) + filepath.Ext(f.Name)
			}
			_, e := os.Lstat(filepath.Join(m.root, rel, name))
			used := !errors.Is(e, os.ErrNotExist)
			for _, existing := range m.tasks {
				if existing.Name == name && existing.Directory == rel && existing.Status != "cancelled" {
					used = true
				}
			}
			for _, existing := range pending {
				if existing.Name == name && existing.Directory == rel {
					used = true
				}
			}
			if !used {
				break
			}
			if suffix > 9999 {
				return nil, fail("name_conflict", "同名文件过多，请选择其他目录")
			}
		}
		clone := *s
		clone.Items = nil
		clone.Cursors = nil
		extra := map[string]string{}
		for k, v := range f.Extra {
			extra[k] = v
		}
		f.Extra = extra
		t := &Task{ID: id, Name: name, Platform: s.Platform, AccountID: s.AccountID, Status: "queued", Size: f.Size, Directory: rel, CreatedAt: now(), UpdatedAt: now(), Session: &clone, Item: f, TransferKey: randomID()}
		pending = append(pending, t)
	}
	tx, err := m.store.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, t := range pending {
		b, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO records(bucket,id,body) VALUES(?,?,?)", "tasks", t.ID, m.store.seal("tasks", t.ID, b)); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for _, t := range pending {
		m.tasks[t.ID] = t
		m.store.Event(t.ID, "queued", "任务已加入队列")
		out = append(out, t.Public())
	}
	m.notifyLocked()
	m.signal()
	return out, nil
}
func (m *Manager) onEngine(e *download.Event) {
	if e.Task == nil || e.Task.Meta == nil || e.Task.Meta.Req == nil {
		return
	}
	labels := e.Task.Meta.Req.Labels
	id := labels["yunxTaskId"]
	if id == "" {
		return
	}
	part, _ := strconv.Atoi(labels["part"])
	v := engineEvent{id: id, engineID: e.Task.ID, key: string(e.Key), part: part}
	if e.Task.Progress != nil {
		v.done = e.Task.Progress.Downloaded
		v.speed = e.Task.Progress.Speed
	}
	if e.Task.Meta.Res != nil {
		v.size = e.Task.Meta.Res.Size
	}
	m.eventMu.Lock()
	m.events = append(m.events, v)
	m.eventMu.Unlock()
	select {
	case m.eventReady <- struct{}{}:
	default:
	}
}
func (m *Manager) run() {
	defer m.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
			m.schedule()
		case <-m.eventReady:
			m.eventMu.Lock()
			batch := m.events
			m.events = nil
			m.eventMu.Unlock()
			for _, e := range batch {
				m.handleEvent(e)
			}
		case <-ticker.C:
			m.watchdog()
		}
	}
}
func (m *Manager) schedule() {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	count := 0
	for _, t := range m.tasks {
		if active(t.Status) {
			count++
		}
	}
	pending := []*Task{}
	for _, t := range m.tasks {
		if t.Status == "queued" {
			pending = append(pending, t)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt < pending[j].CreatedAt })
	for _, t := range pending {
		if count >= m.settings.Concurrency {
			break
		}
		count++
		t.Status = "resolving"
		m.lastProgress[t.ID] = time.Now()
		ctx, cancel := context.WithCancel(m.ctx)
		m.cancels[t.ID] = cancel
		m.saveLocked(t)
		id := t.ID
		m.wg.Add(1)
		go func() { defer m.wg.Done(); m.prepare(ctx, id) }()
	}
	m.mu.Unlock()
}
func (m *Manager) prepare(ctx context.Context, id string) {
	m.mu.Lock()
	t := m.tasks[id]
	copyTask := *t
	connections := m.settings.Connections
	m.mu.Unlock()
	ctx, timeout := context.WithTimeout(ctx, 90*time.Second)
	defer timeout()
	s := copyTask.Session
	item := copyTask.Item
	if len(copyTask.Sources) > 0 && s.Platform != "direct" && s.Platform != "bilibili" {
		fresh, e := m.providers.Resolve(ctx, s.Input, s.Password, s.AccountID, s.Cloud)
		if e != nil {
			m.taskError(id, e)
			return
		}
		found := false
		for page := 1; page <= 200; page++ {
			r, e := m.providers.Browse(ctx, fresh, item.Extra["parent"], page)
			if e != nil {
				m.taskError(id, e)
				return
			}
			for _, f := range r.Items {
				if f.ID == item.ID {
					item = f
					found = true
					break
				}
			}
			if found || !r.More {
				break
			}
		}
		if !found {
			m.taskError(id, fail("source_changed", "原文件已不存在，请重新解析"))
			return
		}
		s = fresh
	}
	sources, e := m.providers.Sources(ctx, s, item)
	if e != nil {
		m.taskError(id, e)
		return
	}
	if len(sources) == 2 {
		if _, e = exec.LookPath("ffmpeg"); e != nil {
			m.taskError(id, fail("ffmpeg_missing", "此视频需要音视频合轨，请安装 FFmpeg 后继续任务"))
			return
		}
	}
	for i := range sources {
		if e = m.probe(ctx, &sources[i]); e != nil {
			m.taskError(id, e)
			return
		}
		if i < len(copyTask.Sources) && copyTask.Downloaded > 0 {
			old := copyTask.Sources[i]
			if old.Size != sources[i].Size || (old.ETag != "" && old.ETag != sources[i].ETag) || (old.LastModified != "" && old.LastModified != sources[i].LastModified) {
				m.taskError(id, fail("source_changed", "源文件已变化，已保留分片并停止续传，请重新建立任务"))
				return
			}
			if old.ETag == "" && old.LastModified == "" && copyTask.Platform == "direct" {
				m.taskError(id, fail("source_unverifiable", "源服务器未提供文件校验标识，无法安全续传，请重新建立任务"))
				return
			}
		}
	}
	work := filepath.Join(m.root, ".yunx", id)
	if e = os.MkdirAll(work, 0700); e != nil {
		m.taskError(id, e)
		return
	}
	m.mu.Lock()
	t = m.tasks[id]
	if t == nil || t.Status != "resolving" || ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	t.Sources = sources
	t.Size = 0
	for _, src := range sources {
		if src.Size < 0 {
			t.Size = -1
			break
		}
		t.Size += src.Size
	}
	if len(t.PartDone) != len(sources) {
		t.PartDone = make([]bool, len(sources))
	}
	if len(t.EngineIDs) != len(sources) {
		t.EngineIDs = make([]string, len(sources))
	}
	t.Status = "downloading"
	m.lastProgress[id] = time.Now()
	m.saveLocked(t)
	token := t.TransferKey
	engineIDs := append([]string{}, t.EngineIDs...)
	done := append([]bool{}, t.PartDone...)
	m.mu.Unlock()
	for part := range sources {
		if done[part] {
			continue
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		req := &base.Request{URL: fmt.Sprintf("%s/internal/transfer/%s/%d", m.baseURL, id, part), Labels: map[string]string{"yunxTaskId": id, "part": strconv.Itoa(part)}, Proxy: &base.RequestProxy{Mode: base.RequestProxyModeNone}, Extra: &ghttp.ReqExtra{Method: "GET", Header: map[string]string{"X-YunX-Transfer-Token": token}}}
		engineID := engineIDs[part]
		if engineID != "" {
			m.mu.Lock()
			m.engineState[engineID] = "running"
			m.mu.Unlock()
		}
		if engineID != "" && m.engine.GetTask(engineID) != nil {
			e = m.engine.Patch(engineID, req, nil)
			if e == nil {
				e = m.engine.Continue(&download.TaskFilter{IDs: []string{engineID}})
			}
		} else {
			no := false
			engineID, e = m.engine.CreateDirect(req, &base.Options{Path: work, Name: fmt.Sprintf("part-%d", part), Extra: &ghttp.OptsExtra{Connections: connections, AutoTorrent: &no, AutoExtract: &no}})
		}
		if e != nil {
			m.taskError(id, fail("engine_error", "下载引擎未能启动任务，请检查目录和网络后继续"))
			return
		}
		m.mu.Lock()
		if t = m.tasks[id]; t != nil {
			t.EngineIDs[part] = engineID
			if _, ok := m.engineState[engineID]; !ok {
				m.engineState[engineID] = "running"
			}
			m.saveLocked(t)
		}
		mustPause := t == nil || t.Status != "downloading"
		m.mu.Unlock()
		if mustPause {
			_ = m.engine.Pause(&download.TaskFilter{IDs: []string{engineID}})
		}
	}
	all := true
	for _, v := range done {
		all = all && v
	}
	if all {
		m.finish(id)
	}
}
func (m *Manager) probe(ctx context.Context, s *MediaSource) error {
	if e := validURL(s.URL); e != nil {
		return fail("source_invalid", "平台未返回有效的媒体地址")
	}
	h := map[string]string{}
	for k, v := range s.Headers {
		h[k] = v
	}
	h["Range"] = "bytes=0-0"
	res, e := m.providers.Net.Request(ctx, "GET", s.URL, h, nil)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if strings.Contains(res.Header.Get("Content-Type"), "json") && res.ContentLength >= 0 && res.ContentLength < 8192 {
		var j map[string]any
		if json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&j) == nil {
			next := str(obj(j["data"])["redirect_url"])
			if next != "" {
				s.URL = next
				res.Body.Close()
				res, e = m.providers.Net.Request(ctx, "GET", next, h, nil)
				if e != nil {
					return e
				}
				defer res.Body.Close()
			}
		}
	}
	if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 412 {
		return fail("account_required", "下载地址需要重新授权，请检查账号后继续任务")
	}
	if res.StatusCode != 200 && res.StatusCode != 206 {
		return fail("source_http", fmt.Sprintf("下载源返回 HTTP %d", res.StatusCode))
	}
	if strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		return fail("source_html", "下载源返回网页而非文件，请检查链接或账号")
	}
	s.ETag = res.Header.Get("ETag")
	s.LastModified = res.Header.Get("Last-Modified")
	if cr := res.Header.Get("Content-Range"); strings.Contains(cr, "/") {
		s.Size, _ = strconv.ParseInt(strings.Split(cr, "/")[1], 10, 64)
	} else {
		s.Size = res.ContentLength
	}
	return nil
}
func (m *Manager) handleEvent(e engineEvent) {
	m.mu.Lock()
	t := m.tasks[e.id]
	if t == nil {
		m.mu.Unlock()
		return
	}
	if e.part >= len(t.EngineIDs) {
		m.mu.Unlock()
		return
	}
	t.EngineIDs[e.part] = e.engineID
	m.partProgress[e.engineID] = e
	switch e.key {
	case download.EventKeyStart:
		if !t.PartDone[e.part] {
			m.engineState[e.engineID] = "running"
		}
	case download.EventKeyPause, download.EventKeyError, download.EventKeyDone, download.EventKeyDelete:
		m.engineState[e.engineID] = "stopped"
	}
	select {
	case m.stateChanged <- struct{}{}:
	default:
	}
	switch e.key {
	case download.EventKeyProgress:
		if t.Status == "downloading" {
			var downloaded, speed int64
			for _, id := range t.EngineIDs {
				if et, ok := m.partProgress[id]; ok {
					downloaded += et.done
					speed += et.speed
				}
			}
			if downloaded > t.Downloaded {
				m.lastProgress[t.ID] = time.Now()
			}
			t.Downloaded = downloaded
			t.Speed = speed
			m.saveLocked(t)
		}
	case download.EventKeyDone:
		t.PartDone[e.part] = true
		t.Speed = 0
		m.saveLocked(t)
		all := true
		for _, d := range t.PartDone {
			all = all && d
		}
		run := all && t.Status == "downloading"
		m.mu.Unlock()
		if run {
			m.wg.Add(1)
			go func() { defer m.wg.Done(); m.finish(e.id) }()
		}
		return
	case download.EventKeyError:
		if t.Status == "downloading" {
			t.Status = "failed"
			t.Speed = 0
			t.Error = "下载失败，分片已保留；请检查网络或账号后继续"
			t.ErrorCode = "download_failed"
			m.saveLocked(t)
			m.store.Event(t.ID, "failed", t.Error)
			m.signal()
		}
	case download.EventKeyPause:
		if t.Status == "pausing" {
			all := true
			for _, id := range t.EngineIDs {
				if m.engineState[id] == "running" {
					all = false
				}
			}
			if all {
				t.Status = "paused"
				t.Speed = 0
				m.saveLocked(t)
				m.signal()
			}
		}
	}
	m.mu.Unlock()
}
func (m *Manager) taskError(id string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	m.mu.Lock()
	t := m.tasks[id]
	if m.closing || t == nil || t.Status == "paused" || t.Status == "pausing" || t.Status == "cancelled" {
		m.mu.Unlock()
		return
	}
	t.Status = "failed"
	t.Error = "操作失败，请检查网络、磁盘权限和平台状态"
	t.ErrorCode = "operation_failed"
	var a *APIError
	if errors.As(err, &a) {
		t.Error = a.Message
		t.ErrorCode = a.Code
		if a.Code == "account_required" {
			t.Status = "waiting_account"
		}
	}
	t.Speed = 0
	ids := nonempty(t.EngineIDs)
	m.saveLocked(t)
	m.store.Event(id, t.Status, t.Error)
	m.mu.Unlock()
	if len(ids) > 0 {
		_ = m.engine.Pause(&download.TaskFilter{IDs: ids})
	}
	m.signal()
}
func nonempty(v []string) []string {
	out := []string{}
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
func (m *Manager) finish(id string) {
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil || t.Status != "downloading" {
		m.mu.Unlock()
		return
	}
	t.Status = "merging"
	m.saveLocked(t)
	copyTask := *t
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Minute)
	m.cancels[id] = cancel
	m.mu.Unlock()
	defer cancel()
	work := filepath.Join(m.root, ".yunx", id)
	output := filepath.Join(work, "part-0")
	if len(copyTask.Sources) == 2 {
		output = filepath.Join(work, "merged.mp4")
		cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", filepath.Join(work, "part-0"), "-i", filepath.Join(work, "part-1"), "-map", "0:v:0", "-map", "1:a:0", "-c", "copy", "-movflags", "+faststart", output)
		if e := cmd.Run(); e != nil {
			m.taskError(id, fail("merge_failed", "音视频合轨失败，已保留两轨；请检查磁盘空间和 FFmpeg 后继续"))
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t = m.tasks[id]
	if t == nil || t.Status != "merging" || ctx.Err() != nil {
		return
	}
	dir, e := m.Directory(t.Directory, true)
	if e != nil {
		t.Status = "failed"
		t.Error = e.Error()
		m.saveLocked(t)
		return
	}
	dest := filepath.Join(dir, t.Name)
	if _, e = os.Lstat(dest); !errors.Is(e, os.ErrNotExist) {
		t.Status = "failed"
		t.Error = "目标文件已存在，已保留下载结果；请移走同名文件后继续"
		m.saveLocked(t)
		return
	}
	if e = os.Link(output, dest); e != nil {
		t.Status = "failed"
		t.Error = "写入目标目录失败，已保留下载结果"
		m.saveLocked(t)
		return
	}
	_ = os.Remove(output)
	if info, e := os.Stat(dest); e == nil {
		t.Downloaded = info.Size()
		t.Size = info.Size()
	}
	t.Status = "completed"
	t.HasFile = true
	t.Speed = 0
	t.FinishedAt = now()
	t.Error = ""
	t.ErrorCode = ""
	m.saveLocked(t)
	m.store.Event(id, "completed", "文件已保存到下载目录")
	_ = os.RemoveAll(work)
	m.signal()
}
func (m *Manager) Action(id, action string, deleteFile bool) error {
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil {
		m.mu.Unlock()
		return fail("task_missing", "任务不存在")
	}
	ids := nonempty(t.EngineIDs)
	switch action {
	case "pause":
		if t.Status == "queued" || t.Status == "resolving" || t.Status == "merging" {
			if c := m.cancels[id]; c != nil {
				c()
			}
			t.Status = "paused"
		} else if t.Status == "downloading" {
			if c := m.cancels[id]; c != nil {
				c()
			}
			t.Status = "pausing"
			if len(ids) == 0 {
				t.Status = "paused"
			}
		} else {
			m.mu.Unlock()
			return nil
		}
		t.Speed = 0
		m.saveLocked(t)
		m.mu.Unlock()
		if len(ids) > 0 {
			_ = m.engine.Pause(&download.TaskFilter{IDs: ids})
		}
		m.signal()
		return nil
	case "resume":
		if active(t.Status) || t.Status == "queued" || t.Status == "completed" {
			m.mu.Unlock()
			return nil
		}
		if t.Status == "cancelled" {
			m.mu.Unlock()
			return fail("task_cancelled", "已取消任务不能继续，请重新添加")
		}
		t.Status = "queued"
		t.Error = ""
		t.ErrorCode = ""
		m.saveLocked(t)
		m.mu.Unlock()
		m.signal()
		return nil
	case "cancel":
		if t.Status == "completed" {
			m.mu.Unlock()
			return fail("already_completed", "任务已完成，请使用删除记录")
		}
		if c := m.cancels[id]; c != nil {
			c()
		}
		t.Status = "cancelled"
		t.Speed = 0
		m.saveLocked(t)
		m.mu.Unlock()
		if len(ids) > 0 {
			_ = m.engine.Pause(&download.TaskFilter{IDs: ids})
		}
		m.signal()
		return nil
	case "delete":
		if active(t.Status) || t.Status == "queued" {
			m.mu.Unlock()
			return fail("task_active", "请先暂停或取消任务再删除")
		}
		if deleteFile && t.HasFile {
			dir, e := m.Directory(t.Directory, false)
			if e != nil {
				m.mu.Unlock()
				return e
			}
			r, e := os.OpenRoot(dir)
			if e != nil {
				m.mu.Unlock()
				return e
			}
			e = r.Remove(t.Name)
			r.Close()
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				m.mu.Unlock()
				return fail("delete_failed", "无法删除下载文件，请检查权限")
			}
		}
		if e := m.store.Delete("tasks", id); e != nil {
			m.mu.Unlock()
			return e
		}
		delete(m.tasks, id)
		m.notifyLocked()
		m.mu.Unlock()
		if len(ids) > 0 {
			_ = m.engine.Delete(&download.TaskFilter{IDs: ids}, false)
		}
		_ = os.RemoveAll(filepath.Join(m.root, ".yunx", id))
		return nil
	}
	m.mu.Unlock()
	return fail("invalid_action", "未知任务操作")
}
func (m *Manager) watchdog() {
	m.mu.Lock()
	expired := []string{}
	for id, t := range m.tasks {
		if t.Status == "downloading" && time.Since(m.lastProgress[id]) > 2*time.Minute {
			expired = append(expired, id)
		}
	}
	m.mu.Unlock()
	for _, id := range expired {
		m.taskError(id, fail("stalled", "下载已连续 120 秒无进展，自动暂停引擎；请检查网络后手动继续"))
	}
}
func (m *Manager) Transfer(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !net.ParseIP(host).IsLoopback() {
		http.Error(w, "forbidden", 403)
		return
	}
	id := r.PathValue("id")
	part, e := strconv.Atoi(r.PathValue("part"))
	m.mu.Lock()
	t := m.tasks[id]
	if e != nil || t == nil || part < 0 || part >= len(t.Sources) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-YunX-Transfer-Token")), []byte(t.TransferKey)) != 1 {
		m.mu.Unlock()
		http.Error(w, "forbidden", 403)
		return
	}
	src := t.Sources[part]
	status := t.Status
	m.mu.Unlock()
	if status != "downloading" && status != "resolving" {
		http.Error(w, "paused", 409)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	h := map[string]string{}
	for k, v := range src.Headers {
		h[k] = v
	}
	for _, k := range []string{"Range", "If-Range"} {
		if v := r.Header.Get(k); v != "" {
			h[k] = v
		}
	}
	h["Accept-Encoding"] = "identity"
	res, e := m.providers.Net.Request(ctx, r.Method, src.URL, h, nil)
	if e != nil {
		http.Error(w, "upstream unavailable", 502)
		return
	}
	defer res.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	if r.Method == "HEAD" {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, e := res.Body.Read(buf)
		if n > 0 {
			if m.limiter.WaitN(ctx, n) != nil {
				return
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
		}
		if e != nil {
			return
		}
	}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closing = true
	ids := []string{}
	for _, t := range m.tasks {
		if t.Status == "pausing" {
			t.Status = "paused"
			m.saveLocked(t)
		}
		if active(t.Status) {
			t.Status = "queued"
			t.Speed = 0
			m.saveLocked(t)
		}
		ids = append(ids, nonempty(t.EngineIDs)...)
	}
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()
	if len(ids) > 0 {
		_ = m.engine.Pause(&download.TaskFilter{IDs: ids})
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
wait:
	for {
		m.mu.Lock()
		all := true
		for _, id := range ids {
			if m.engineState[id] == "running" {
				all = false
				break
			}
		}
		m.mu.Unlock()
		if all {
			break
		}
		select {
		case <-deadline.C:
			break wait
		case <-m.stateChanged:
		}
	}
	m.cancel()
	m.wg.Wait()
	return m.engine.Close()
}

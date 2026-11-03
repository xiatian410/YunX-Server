package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type harness struct {
	app        *App
	server     *httptest.Server
	client     *http.Client
	data, root string
}

func startTest(t *testing.T, data, root string) *harness {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	addr := srv.Listener.Addr().String()
	app, err := NewApp(Config{DataDir: data, DownloadDir: root, AdminPassword: "test-password-123", AllowPrivate: true, Version: "test"}, "http://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = app
	srv.Start()
	jar, _ := cookiejar.New(nil)
	h := &harness{app, srv, &http.Client{Jar: jar, Timeout: 10 * time.Second}, data, root}
	return h
}
func (h *harness) close() { _ = h.app.Manager.Close(); h.server.Close(); _ = h.app.Store.DB.Close() }
func (h *harness) request(t *testing.T, method, path string, body any, status int) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, h.server.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-YunX-Request", "1")
	res, e := h.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, res.StatusCode, status, data)
	}
	var v map[string]any
	if e = json.Unmarshal(data, &v); e != nil {
		t.Fatalf("decode %s: %v", data, e)
	}
	return v
}
func (h *harness) login(t *testing.T) {
	h.request(t, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": "test-password-123"}, 200)
}
func waitTask(t *testing.T, m *Manager, id string, predicate func(Task) bool) Task {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, task := range m.Snapshot() {
			if task.ID == id {
				if predicate(task) {
					return task
				}
				if task.Status == "failed" || task.Status == "waiting_account" {
					t.Fatalf("unexpected task failure: %+v", task)
				}
			}
		}
		select {
		case <-deadline:
			t.Fatalf("task timeout: %+v", m.Snapshot())
		case <-tick.C:
		}
	}
}
func directTask(t *testing.T, h *harness, url string) string {
	t.Helper()
	v := h.request(t, "POST", "/api/resolve", map[string]any{"text": url}, 200)
	v = h.request(t, "POST", "/api/tasks", map[string]any{"session_id": v["session_id"], "item_ids": []string{"file"}, "directory": "tests"}, 201)
	return str(obj(arr(v["tasks"])[0])["id"])
}

func TestAuthSettingsAndCredentialIsolation(t *testing.T) {
	h := startTest(t, t.TempDir(), t.TempDir())
	defer h.close()
	h.request(t, "GET", "/api/state", nil, 401)
	h.request(t, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong"}, 401)
	h.login(t)
	req, _ := http.NewRequest("POST", h.server.URL+"/api/accounts", strings.NewReader(`{}`))
	res, e := h.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("missing CSRF header accepted")
	}
	req, _ = http.NewRequest("POST", h.server.URL+"/api/accounts", strings.NewReader(`{}`))
	req.Header.Set("X-YunX-Request", "1")
	req.Header.Set("Origin", "https://other.example")
	res, e = h.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("foreign origin accepted")
	}
	secret := "SESSDATA=secret-fixture-123"
	a := h.request(t, "POST", "/api/accounts", map[string]string{"name": "Bili Test", "platform": "bilibili", "credential": secret}, 200)
	if _, ok := a["credentials"]; ok {
		t.Fatal("credential echoed")
	}
	id := str(a["id"])
	h.request(t, "POST", "/api/accounts", map[string]string{"id": id, "name": "Renamed", "platform": "bilibili", "credential": ""}, 200)
	stored, e := h.app.Providers.account(id)
	if e != nil || stored.Credentials["cookie"] != secret {
		t.Fatal("empty edit lost credential")
	}
	s := h.request(t, "GET", "/api/state", nil, 200)
	raw, _ := json.Marshal(s)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("credential leaked into state")
	}
	var blob []byte
	if e = h.app.Store.DB.QueryRow("SELECT body FROM records WHERE bucket='accounts' AND id=?", id).Scan(&blob); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(blob, []byte(secret)) {
		t.Fatal("plaintext stored")
	}
	cfg := DefaultSettings()
	cfg.Name = "测试 NAS"
	cfg.DefaultDir = "电影/纪录片"
	h.request(t, "PUT", "/api/settings", cfg, 200)
	cfg.Concurrency = 0
	h.request(t, "PUT", "/api/settings", cfg, 400)
	saved, _ := h.app.Store.Settings()
	if saved.Concurrency != 2 || saved.Name != "测试 NAS" {
		t.Fatal("invalid settings changed stored state")
	}
	h.request(t, "POST", "/api/directories", map[string]string{"path": "../outside"}, 400)
	outside := t.TempDir()
	if e = os.Symlink(outside, filepath.Join(h.root, "escape")); e != nil {
		t.Fatal(e)
	}
	h.request(t, "POST", "/api/directories", map[string]string{"path": "escape/sub"}, 400)
	h.request(t, "POST", "/api/auth/password", map[string]string{"current": "wrong", "password": "new-password-456"}, 400)
	h.request(t, "POST", "/api/auth/password", map[string]string{"current": "test-password-123", "password": "new-password-456"}, 200)
	h.request(t, "GET", "/api/state", nil, 401)
}

func TestHTTPDownloadPauseRestartAndHash(t *testing.T) {
	payload := bytes.Repeat([]byte("yunx-range-test-0123456789"), 128*1024)
	var ranged atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged.Add(1)
		}
		w.Header().Set("ETag", `"stable-fixture"`)
		http.ServeContent(w, r, "sample.bin", time.Unix(1700000000, 0), bytes.NewReader(payload))
	}))
	defer upstream.Close()
	h := startTest(t, t.TempDir(), t.TempDir())
	h.login(t)
	cfg := DefaultSettings()
	cfg.SpeedLimit = 512 * 1024
	cfg.Connections = 2
	h.request(t, "PUT", "/api/settings", cfg, 200)
	id := directTask(t, h, upstream.URL+"/sample.bin")
	waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Downloaded > 0 && x.Status == "downloading" })
	h.request(t, "POST", "/api/tasks/"+id+"/action", map[string]string{"action": "pause"}, 200)
	paused := waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "paused" })
	if paused.Downloaded <= 0 {
		t.Fatal("no checkpoint")
	}
	h.close()
	h = startTest(t, h.data, h.root)
	defer h.close()
	h.login(t)
	tasks := h.app.Manager.Snapshot()
	if len(tasks) != 1 || tasks[0].Status != "paused" {
		t.Fatalf("explicit pause not retained: %+v", tasks)
	}
	cfg.SpeedLimit = 0
	h.request(t, "PUT", "/api/settings", cfg, 200)
	h.request(t, "POST", "/api/tasks/"+id+"/action", map[string]string{"action": "resume"}, 200)
	done := waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "completed" })
	b, e := os.ReadFile(filepath.Join(h.root, "tests", done.Name))
	if e != nil {
		t.Fatal(e)
	}
	if sha256.Sum256(b) != sha256.Sum256(payload) {
		t.Fatalf("hash mismatch: got %d bytes want %d", len(b), len(payload))
	}
	if ranged.Load() < 3 {
		t.Fatal("range resume not exercised")
	}
	res, e := h.client.Get(h.server.URL + "/api/tasks/" + id + "/file")
	if e != nil {
		t.Fatal(e)
	}
	download, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if sha256.Sum256(download) != sha256.Sum256(payload) {
		t.Fatal("authenticated file download mismatch")
	}
	h.request(t, "POST", "/api/tasks/"+id+"/action", map[string]any{"action": "delete", "delete_file": false}, 200)
	if _, e = os.Stat(filepath.Join(h.root, "tests", done.Name)); e != nil {
		t.Fatal("record delete removed file")
	}
}

func TestSourceChangedAndCancellation(t *testing.T) {
	payload := bytes.Repeat([]byte("changed-file-"), 256*1024)
	var changed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag := `"old"`
		if changed.Load() {
			etag = `"new"`
		}
		w.Header().Set("ETag", etag)
		http.ServeContent(w, r, "change.bin", time.Unix(1700000000, 0), bytes.NewReader(payload))
	}))
	defer upstream.Close()
	h := startTest(t, t.TempDir(), t.TempDir())
	defer h.close()
	h.login(t)
	cfg := DefaultSettings()
	cfg.SpeedLimit = 256 * 1024
	h.request(t, "PUT", "/api/settings", cfg, 200)
	id := directTask(t, h, upstream.URL+"/change.bin")
	waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Downloaded > 0 })
	h.request(t, "POST", "/api/tasks/"+id+"/action", map[string]string{"action": "pause"}, 200)
	waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "paused" })
	changed.Store(true)
	h.request(t, "POST", "/api/tasks/"+id+"/action", map[string]string{"action": "resume"}, 200)
	failed := waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "failed" })
	if failed.ErrorCode != "source_changed" {
		t.Fatalf("wrong error: %+v", failed)
	}
	h.request(t, "POST", "/api/tasks/"+id+"/action", map[string]string{"action": "cancel"}, 200)
	h.request(t, "POST", "/api/tasks/"+id+"/action", map[string]string{"action": "resume"}, 400)
}

func TestNetworkAndStoreGuards(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "192.168.1.2", "169.254.169.254", "100.64.0.1", "198.18.0.1"} {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("internal IP accepted %s", s)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
	n := NewNetwork(false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := n.Request(ctx, "GET", "http://127.0.0.1/", nil, nil); e == nil {
		t.Fatal("local target accepted")
	}
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	secret := map[string]string{"token": "fixture"}
	if e = s.Put("a", "1", secret); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	if e = os.Remove(filepath.Join(dir, "credential.key")); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenStore(dir); e == nil {
		t.Fatal("missing key silently replaced")
	}
	root := t.TempDir()
	if e = os.Symlink(t.TempDir(), filepath.Join(root, ".yunx")); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if _, e = NewManager(s, &Providers{Store: s, Net: NewNetwork(false)}, root, "http://127.0.0.1"); e == nil {
		t.Fatal("unsafe internal work directory accepted")
	}
}

func TestBatchAtomicity(t *testing.T) {
	h := startTest(t, t.TempDir(), t.TempDir())
	defer h.close()
	s := &ParseSession{ID: "session", Platform: "direct", Input: "https://example.com/file"}
	_, e := h.app.Manager.Add(s, []Item{{ID: "1", Name: "ok.bin"}, {ID: "2", Name: "bad.bin", Extra: map[string]string{"relative_path": "../../outside"}}}, "")
	if e == nil {
		t.Fatal("invalid batch accepted")
	}
	if len(h.app.Manager.Snapshot()) != 0 {
		t.Fatal("partial batch was queued")
	}
	rows, _ := h.app.Store.List("tasks")
	if len(rows) != 0 {
		t.Fatal("partial batch persisted")
	}
}

func Example_sign123() {
	k, v := sign123("/api/file/download_info", 1700000000, 12345)
	fmt.Println(len(k) > 0, strings.HasPrefix(v, "1700000000-12345-")) // Output: true true
}

func TestActiveDownloadAutoResumeAfterRestart(t *testing.T) {
	payload := bytes.Repeat([]byte("automatic-recovery"), 128*1024)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"recover"`)
		http.ServeContent(w, r, "recover.bin", time.Unix(1700000000, 0), bytes.NewReader(payload))
	}))
	defer upstream.Close()
	h := startTest(t, t.TempDir(), t.TempDir())
	h.login(t)
	cfg := DefaultSettings()
	cfg.SpeedLimit = 512 * 1024
	h.request(t, "PUT", "/api/settings", cfg, 200)
	id := directTask(t, h, upstream.URL+"/recover.bin")
	waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Downloaded > 0 })
	h.close()
	h = startTest(t, h.data, h.root)
	defer h.close()
	h.login(t)
	cfg.SpeedLimit = 0
	h.request(t, "PUT", "/api/settings", cfg, 200)
	done := waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "completed" })
	b, e := os.ReadFile(filepath.Join(h.root, "tests", done.Name))
	if e != nil {
		t.Fatal(e)
	}
	if sha256.Sum256(b) != sha256.Sum256(payload) {
		t.Fatal("automatic recovery corrupted file")
	}
}

func TestBiliDASHMergeWithFFmpeg(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg unavailable")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "video.mp4")
	audio := filepath.Join(dir, "audio.m4a")
	for _, args := range [][]string{{"-nostdin", "-y", "-f", "lavfi", "-i", "color=black:s=16x16:r=10", "-t", "0.5", "-an", "-c:v", "mpeg4", video}, {"-nostdin", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "0.5", "-vn", "-c:a", "aac", audio}} {
		if output, e := exec.Command(ffmpeg, args...).CombinedOutput(); e != nil {
			t.Fatalf("fixture creation: %v %s", e, output)
		}
	}
	upstream := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer upstream.Close()
	h := startTest(t, t.TempDir(), t.TempDir())
	defer h.close()
	baseTransport := h.app.Providers.Net.Client.Transport
	h.app.Providers.Net.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "api.bilibili.com" {
			body := fmt.Sprintf(`{"code":0,"data":{"dash":{"video":[{"id":80,"codecid":7,"baseUrl":%q}],"audio":[{"id":30280,"base_url":%q}]}}}`, upstream.URL+"/video.mp4", upstream.URL+"/audio.m4a")
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		return baseTransport.RoundTrip(r)
	})
	tasks, e := h.app.Manager.Add(&ParseSession{ID: randomID(), Platform: "bilibili", ShareID: "BV1234567890"}, []Item{{ID: "1", Name: "dash-fixture.mp4", Size: -1}}, "")
	if e != nil {
		t.Fatal(e)
	}
	done := waitTask(t, h.app.Manager, tasks[0].ID, func(x Task) bool { return x.Status == "completed" })
	out, e := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "json", filepath.Join(h.root, done.Name)).Output()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out, []byte(`"video"`)) || !bytes.Contains(out, []byte(`"audio"`)) {
		t.Fatalf("output lacks both tracks: %s", out)
	}
}

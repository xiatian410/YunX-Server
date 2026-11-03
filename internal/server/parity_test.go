package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestProviderProcessAndAllPlatformDetection(t *testing.T) {
	jar, _ := filepath.Abs("../../provider/build/libs/yunx-provider.jar")
	if _, e := os.Stat(jar); e != nil {
		t.Skip("run make provider to test full runtime")
	}
	browser, _ := filepath.Abs("../../provider/browser.mjs")
	p, e := StartProvider(jar, "0123456789abcdef0123456789abcdef", browser, false)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	links := map[string]string{"quark": "https://pan.quark.cn/s/abcdef", "uc": "https://drive.uc.cn/s/abcdef", "pan123": "https://www.123pan.com/s/Abc123-Def456", "baidu": "https://pan.baidu.com/s/1abcdef", "xunlei": "https://pan.xunlei.com/s/abcdef", "c139": "https://yun.139.com/shareweb/#/w/i/abcdef", "pan115": "https://115.com/s/swabcdef?password=1234", "guangya": "https://www.guangyapan.com/s/abcdef", "lanzou": "https://example.lanzou.com/abcdef", "ilanzou": "https://www.ilanzou.com/s/abcdef", "bilibili": "https://www.bilibili.com/video/BV1xx411c7mD", "douyin": "https://www.douyin.com/video/1234567890", "xhs": "https://www.xiaohongshu.com/explore/1234567890abcdef", "kuaishou": "https://www.kuaishou.com/short-video/abcdef", "weibo": "https://weibo.com/123456/abcdef", "acfun": "https://www.acfun.cn/v/ac123456", "pixiv": "https://www.pixiv.net/artworks/123456"}
	for id, link := range links {
		t.Run(id, func(t *testing.T) {
			var result struct {
				Platform string `json:"platform"`
			}
			if e := p.Call(context.Background(), map[string]any{"op": "detect", "text": link}, &result); e != nil || result.Platform != id {
				t.Fatalf("detect %s: %+v %v", link, result, e)
			}
		})
	}
	var result any
	if e = p.Call(context.Background(), map[string]any{"op": "invalid", "credentials": map[string]string{"token": "SECRET"}}, &result); e == nil || strings.Contains(e.Error(), "SECRET") {
		t.Fatalf("error leaked or absent: %v", e)
	}
	p.Close()
	if e = p.Call(context.Background(), map[string]any{"op": "health"}, &result); e == nil {
		t.Fatal("closed process accepted request")
	}
}

func TestBrowserProxyAndWorker(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<script>window.fixture={files:["one","two"]}</script>`)
	}))
	defer source.Close()
	for _, allow := range []bool{false, true} {
		addr, token, closeProxy, e := startBrowserProxy(allow)
		if e != nil {
			t.Fatal(e)
		}
		proxyURL, _ := url.Parse(addr)
		proxyURL.User = url.UserPassword("yunx", token)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
		res, e := client.Get(source.URL)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if allow && res.StatusCode != 200 || !allow && res.StatusCode != 403 {
			t.Fatalf("private boundary allow=%v status=%d", allow, res.StatusCode)
		}
		if allow {
			script, _ := filepath.Abs("../../provider/browser.mjs")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			cmd := exec.CommandContext(ctx, "node", script)
			cmd.Env = append(os.Environ(), "YUNX_BROWSER_PROXY="+addr, "YUNX_BROWSER_PROXY_TOKEN="+token, "YUNX_BROWSER_DIAGNOSTICS=1")
			input, _ := json.Marshal(map[string]any{"url": source.URL, "readyCheckJs": "() => !!window.fixture", "extractJs": "() => JSON.stringify(window.fixture)", "timeout": 10000})
			cmd.Stdin = bytes.NewReader(input)
			out, e := cmd.CombinedOutput()
			cancel()
			if e != nil || !bytes.Contains(out, []byte(`"one"`)) {
				t.Fatalf("browser: %v %s", e, out)
			}
		}
		closeProxy()
	}
}

func TestHLSDownloadAESAndMaster(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{42}, 16)
	os.WriteFile(filepath.Join(dir, "key.bin"), key, 0600)
	info := filepath.Join(dir, "key-info")
	os.WriteFile(info, []byte("key.bin\n"+filepath.Join(dir, "key.bin")+"\n00000000000000000000000000000001\n"), 0600)
	args := []string{"-nostdin", "-y", "-f", "lavfi", "-i", "color=black:s=32x32:r=10", "-t", "1", "-c:v", "libx264", "-g", "5", "-hls_time", "0.5", "-hls_key_info_file", info, "-hls_playlist_type", "vod", filepath.Join(dir, "media.m3u8")}
	if out, e := exec.Command("ffmpeg", args...).CombinedOutput(); e != nil {
		t.Fatalf("HLS fixture %v %s", e, out)
	}
	os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100\nmissing-low.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nmedia.m3u8\n"), 0600)
	upstream := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer upstream.Close()
	h := startTest(t, t.TempDir(), t.TempDir())
	defer h.close()
	h.login(t)
	id := directTask(t, h, upstream.URL+"/master.m3u8")
	done := waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "completed" })
	out, e := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "json", filepath.Join(h.root, "tests", done.Name)).CombinedOutput()
	if e != nil || !bytes.Contains(out, []byte(`"video"`)) {
		t.Fatalf("HLS output %v %s", e, out)
	}
}

func TestHLSLimitsAndSourceChanges(t *testing.T) {
	playlist := "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nseg.ts\n#EXT-X-ENDLIST\n"
	segment := "first"
	etag := "one"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "m3u8") {
			io.WriteString(w, playlist)
			return
		}
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304)
			return
		}
		io.WriteString(w, segment)
	}))
	defer upstream.Close()
	h := startTest(t, t.TempDir(), t.TempDir())
	defer h.close()
	dir := t.TempDir()
	fresh := func() *hlsDownload {
		return &hlsDownload{m: h.app.Manager, ctx: context.Background(), dir: dir, source: MediaSource{URL: upstream.URL + "/media.m3u8"}, seen: map[string]string{}}
	}
	if _, e := fresh().resource(upstream.URL+"/media.m3u8", true, 0); e != nil {
		t.Fatal(e)
	}
	if _, e := fresh().resource(upstream.URL+"/media.m3u8", true, 0); e != nil {
		t.Fatal("unchanged resume", e)
	}
	segment = "changed"
	etag = "two"
	if _, e := fresh().resource(upstream.URL+"/media.m3u8", true, 0); e == nil {
		t.Fatal("changed segment accepted")
	}
	playlist = strings.ReplaceAll(playlist, "#EXT-X-ENDLIST", "")
	if _, e := fresh().resource(upstream.URL+"/media.m3u8", true, 0); e == nil {
		t.Fatal("unbounded live accepted")
	}
	if _, e := fresh().resource(upstream.URL, true, 6); e == nil {
		t.Fatal("nested playlist limit missing")
	}
}

func torrentFixture(t *testing.T, dir string, baseURL string) []byte {
	t.Helper()
	var info metainfo.Info
	info.PieceLength = 16384
	private := true
	info.Private = &private
	if e := info.BuildFromFilePath(dir); e != nil {
		t.Fatal(e)
	}
	encoded, e := bencode.Marshal(info)
	if e != nil {
		t.Fatal(e)
	}
	meta := metainfo.MetaInfo{InfoBytes: encoded, UrlList: metainfo.UrlList{baseURL + "/"}}
	var b bytes.Buffer
	if e = meta.Write(&b); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestTorrentSelectionDownloadAndArchive(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "fixture")
	os.Mkdir(dir, 0700)
	one := bytes.Repeat([]byte("selected file\n"), 4096)
	os.WriteFile(filepath.Join(dir, "one.txt"), one, 0600)
	os.WriteFile(filepath.Join(dir, "two.txt"), bytes.Repeat([]byte("not selected\n"), 4096), 0600)
	upstream := httptest.NewServer(http.FileServer(http.Dir(base)))
	defer upstream.Close()
	data := torrentFixture(t, dir, upstream.URL)
	h := startTest(t, t.TempDir(), t.TempDir())
	defer h.close()
	h.login(t)
	s, e := h.app.Providers.resolveP2P(context.Background(), "data:application/x-bittorrent;base64,"+base64.StdEncoding.EncodeToString(data))
	if e != nil {
		t.Fatal(e)
	}
	var selected Item
	for _, item := range s.Items {
		if item.Name == "one.txt" {
			selected = item
		}
	}
	tasks, e := h.app.Manager.Add(s, []Item{selected}, "")
	if e != nil {
		t.Fatal(e)
	}
	done := waitTask(t, h.app.Manager, tasks[0].ID, func(x Task) bool { return x.Status == "completed" })
	got, e := os.ReadFile(filepath.Join(h.root, done.Name, "one.txt"))
	if e != nil || sha256.Sum256(got) != sha256.Sum256(one) {
		t.Fatal("selected torrent file hash", e)
	}
	if _, e = os.Stat(filepath.Join(h.root, done.Name, "two.txt")); !os.IsNotExist(e) {
		t.Fatal("unselected file published")
	}
	res, e := h.client.Get(h.server.URL + "/api/tasks/" + done.ID + "/file")
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	prefix := make([]byte, 4)
	io.ReadFull(res.Body, prefix)
	if res.StatusCode != 200 || string(prefix) != "PK\x03\x04" {
		t.Fatalf("archive %d %x", res.StatusCode, prefix)
	}
}
func TestP2PMalformedInput(t *testing.T) {
	p := fixtureProvider(t, func(r *http.Request) string { return "" })
	for _, s := range []string{"ed2k://|file|..%2Fescape|10|0123456789abcdef0123456789abcdef|/", "ed2k://|file|x|10|bad|/", "data:application/x-bittorrent;base64,bad"} {
		if _, e := p.resolveP2P(context.Background(), s); e == nil {
			t.Fatal("bad input accepted", s)
		}
	}
	s, e := p.resolveP2P(context.Background(), "ed2k://|file|example.txt|10|0123456789abcdef0123456789abcdef|/")
	if e != nil || s.Platform != "ed2k" {
		t.Fatal(e)
	}
}
func TestGitHubReleaseAndPrivateAccount(t *testing.T) {
	p := fixtureProvider(t, func(r *http.Request) string {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("PAT missing")
		}
		switch r.URL.Path {
		case "/user":
			return `{"login":"owner"}`
		case "/user/repos":
			return `[{"full_name":"owner/private","name":"private","default_branch":"main"}]`
		case "/repos/owner/private/releases":
			return `[{"tag_name":"v1","assets":[{"id":3,"name":"binary.zip","size":99,"url":"https://api.github.com/repos/owner/private/releases/assets/3"}]}]`
		default:
			t.Errorf("unexpected github API %s", r.URL)
			return `{}`
		}
	})
	p.Store.Put("accounts", "github", Account{ID: "github", Platform: "github", Credentials: map[string]string{"token": "fixture"}})
	s, e := p.Resolve(context.Background(), "", "", "github", true)
	if e != nil {
		t.Fatal(e)
	}
	v, e := p.Browse(context.Background(), s, "0", 1)
	if e != nil || len(v.Items) != 1 {
		t.Fatalf("private repos: %+v %v", v, e)
	}
	s, e = p.Resolve(context.Background(), "https://github.com/owner/private/releases", "", "github", false)
	if e != nil {
		t.Fatal(e)
	}
	v, e = p.Browse(context.Background(), s, "0", 1)
	if e != nil {
		t.Fatal(e)
	}
	var file Item
	for _, it := range v.Items {
		if it.Name == "binary.zip" {
			file = it
		}
	}
	sources, e := p.Sources(context.Background(), s, file)
	if e != nil || len(sources) != 1 || !strings.Contains(sources[0].URL, "assets/3") {
		t.Fatal(fmt.Sprint(sources), e)
	}
}

func TestMotionPhotoPreservesStillAndVideo(t *testing.T) {
	dir := t.TempDir()
	photo := filepath.Join(dir, "still.jpg")
	video := filepath.Join(dir, "motion.mp4")
	out := filepath.Join(dir, "out.jpg")
	f, _ := os.Create(photo)
	jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil)
	f.Close()
	payload := []byte("fixture MP4 bytes")
	os.WriteFile(video, payload, 0600)
	if e := writeMotionPhoto(photo, video, out); e != nil {
		t.Fatal(e)
	}
	result, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.HasSuffix(result, payload) || !bytes.Contains(result, []byte(`Item:Length="17"`)) || !bytes.Contains(result, []byte("oplus_8388608")) {
		t.Fatal("Motion Photo container missing media or metadata")
	}
	if _, e = jpeg.DecodeConfig(bytes.NewReader(result)); e != nil {
		t.Fatal("still no longer readable", e)
	}
}

type slowFixtureWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w slowFixtureWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := min(len(p), 8192)
		select {
		case <-w.ctx.Done():
			return total, w.ctx.Err()
		case <-time.After(4 * time.Millisecond):
		}
		written, e := w.ResponseWriter.Write(p[:n])
		total += written
		if e != nil {
			return total, e
		}
		p = p[n:]
	}
	return total, nil
}
func TestTorrentPauseRestartAndHash(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "resume-fixture")
	os.Mkdir(dir, 0700)
	payload := bytes.Repeat([]byte("resume verified media\n"), 400000)
	os.WriteFile(filepath.Join(dir, "large.bin"), payload, 0600)
	files := http.FileServer(http.Dir(base))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { files.ServeHTTP(slowFixtureWriter{w, r.Context()}, r) }))
	defer upstream.Close()
	meta := torrentFixture(t, dir, upstream.URL)
	data, root := t.TempDir(), t.TempDir()
	h := startTest(t, data, root)
	defer func() { h.close() }()
	s, e := h.app.Providers.resolveP2P(context.Background(), "data:application/x-bittorrent;base64,"+base64.StdEncoding.EncodeToString(meta))
	if e != nil {
		t.Fatal(e)
	}
	tasks, e := h.app.Manager.Add(s, []Item{s.Items["0"]}, "")
	if e != nil {
		t.Fatal(e)
	}
	id := tasks[0].ID
	waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "downloading" && x.Downloaded > 0 && x.Downloaded < x.Size })
	if e = h.app.Manager.Action(id, "pause", false); e != nil {
		t.Fatal(e)
	}
	waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "paused" })
	h.close()
	h = startTest(t, data, root)
	restored := h.app.Manager.Snapshot()[0]
	if restored.Status != "paused" {
		t.Fatalf("manual pause lost: %s", restored.Status)
	}
	if e = h.app.Manager.Action(id, "resume", false); e != nil {
		t.Fatal(e)
	}
	done := waitTask(t, h.app.Manager, id, func(x Task) bool { return x.Status == "completed" })
	got, e := os.ReadFile(filepath.Join(root, done.Name, "large.bin"))
	if e != nil || sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatal("resumed torrent hash", e)
	}
}

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type hlsCache struct {
	ETag string `json:"etag"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}
type hlsDownload struct {
	m          *Manager
	ctx        context.Context
	id, dir    string
	source     MediaSource
	count      int
	downloaded int64
	last       time.Time
	lastBytes  int64
	seen       map[string]string
}

var hlsURI = regexp.MustCompile(`URI="([^"]+)"`)

func (h *hlsDownload) progress(n int64) {
	h.downloaded += n
	if time.Since(h.last) < time.Second {
		return
	}
	elapsed := time.Since(h.last).Seconds()
	speed := int64(0)
	if !h.last.IsZero() && elapsed > 0 {
		speed = int64(float64(h.downloaded-h.lastBytes) / elapsed)
	}
	h.lastBytes = h.downloaded
	h.last = time.Now()
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	t := h.m.tasks[h.id]
	if t != nil && t.Status == "downloading" {
		t.Downloaded = h.downloaded
		t.Speed = speed
		h.m.lastProgress[h.id] = time.Now()
		h.m.saveLocked(t)
	}
}
func (h *hlsDownload) resource(raw string, playlist bool, depth int) (string, error) {
	if depth > 5 {
		return "", fail("hls_depth", "HLS 播放列表嵌套超过 5 层")
	}
	if name, ok := h.seen[raw]; ok {
		return name, nil
	}
	h.count++
	if h.count > 10000 {
		return "", fail("hls_limit", "HLS 资源超过 10000 项")
	}
	u, e := url.Parse(raw)
	if e != nil {
		return "", e
	}
	stable := *u
	stable.RawQuery = ""
	hash := sha256.Sum256([]byte(stable.String()))
	name := fmt.Sprintf("%05d-", h.count) + hex.EncodeToString(hash[:8])
	if playlist {
		name += ".m3u8"
	} else {
		ext := strings.ToLower(filepath.Ext(u.Path))
		switch ext {
		case ".ts", ".m4s", ".mp4", ".aac", ".mp3", ".vtt", ".webvtt", ".key":
		default:
			ext = ".ts"
		}
		name += ext
	}
	target := filepath.Join(h.dir, name)
	headers := map[string]string{}
	for k, v := range h.source.Headers {
		headers[k] = v
	}
	origin, _ := url.Parse(h.source.URL)
	if origin.Hostname() != u.Hostname() {
		delete(headers, "Cookie")
		delete(headers, "Authorization")
	}
	var old hlsCache
	if b, err := os.ReadFile(target + ".json"); err == nil {
		_ = json.Unmarshal(b, &old)
	}
	if !playlist && old.ETag != "" {
		headers["If-None-Match"] = old.ETag
	}
	response, e := h.m.providers.Net.Request(h.ctx, "GET", raw, headers, nil)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified && !playlist {
		f, err := os.Open(target)
		if err != nil {
			return "", err
		}
		sum := sha256.New()
		n, err := io.Copy(sum, f)
		f.Close()
		if err != nil || n != old.Size || hex.EncodeToString(sum.Sum(nil)) != old.SHA {
			return "", fail("hls_corrupt", "已有 HLS 分片校验失败，请重新创建任务")
		}
		h.progress(n)
		h.seen[raw] = name
		return name, nil
	}
	if response.StatusCode != 200 {
		return "", fail("hls_http", fmt.Sprintf("HLS 资源返回 %d，请更新账号或稍后继续", response.StatusCode))
	}
	if playlist {
		data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil {
			return "", err
		}
		if len(data) > 2<<20 || !strings.HasPrefix(strings.TrimSpace(string(data)), "#EXTM3U") {
			return "", fail("hls_format", "不是有效的 HLS 播放列表")
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		master := strings.Contains(text, "#EXT-X-STREAM-INF")
		if !master && !strings.Contains(text, "#EXT-X-ENDLIST") {
			return "", fail("hls_live", "当前链接为持续直播；请使用已结束的回放或点播链接")
		}
		if strings.Contains(text, "METHOD=SAMPLE-AES") || hlsHasDRM(text) {
			return "", fail("hls_drm", "该媒体需要 DRM 授权播放器，无法作为普通 HLS 下载")
		}
		lines := strings.Split(text, "\n")
		if master {
			lines = selectHLSVariant(lines)
		}
		nextPlaylist := false
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "#") {
				if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
					nextPlaylist = true
				}
				if strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF") {
					lines[i] = ""
					continue
				}
				if match := hlsURI.FindStringSubmatch(line); len(match) > 0 {
					part, err := u.Parse(match[1])
					if err != nil {
						return "", err
					}
					isPlaylist := strings.HasPrefix(line, "#EXT-X-MEDIA")
					local, err := h.resource(part.String(), isPlaylist, depth+1)
					if err != nil {
						return "", err
					}
					lines[i] = strings.Replace(line, match[0], `URI="`+local+`"`, 1)
				}
				continue
			}
			part, err := u.Parse(line)
			if err != nil {
				return "", err
			}
			local, err := h.resource(part.String(), nextPlaylist, depth+1)
			if err != nil {
				return "", err
			}
			lines[i] = local
			nextPlaylist = false
		}
		rewritten := strings.Join(lines, "\n")
		if prior, err := os.ReadFile(target); err == nil && string(prior) != rewritten {
			return "", fail("source_changed", "HLS 分片列表已改变，已保留旧分片，请重新创建任务")
		}
		if err = os.WriteFile(target, []byte(rewritten), 0600); err != nil {
			return "", err
		}
		h.seen[raw] = name
		return name, nil
	}
	file, e := os.OpenFile(target+".partial", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	sum := sha256.New()
	buf := make([]byte, 64*1024)
	var n int64
	for {
		size, readErr := response.Body.Read(buf)
		if size > 0 {
			if e = h.m.limiter.WaitN(h.ctx, size); e != nil {
				break
			}
			if _, e = file.Write(buf[:size]); e != nil {
				break
			}
			sum.Write(buf[:size])
			n += int64(size)
			h.progress(int64(size))
		}
		if readErr != nil {
			if readErr != io.EOF {
				e = readErr
			}
			break
		}
	}
	if closeErr := file.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return "", e
	}
	digest := hex.EncodeToString(sum.Sum(nil))
	if old.SHA != "" && (old.SHA != digest || old.Size != n) {
		return "", fail("source_changed", "HLS 分片内容已改变，请重新创建任务")
	}
	if e = os.Rename(target+".partial", target); e != nil {
		return "", e
	}
	metadata, _ := json.Marshal(hlsCache{ETag: response.Header.Get("ETag"), SHA: digest, Size: n})
	if e = os.WriteFile(target+".json", metadata, 0600); e != nil {
		return "", e
	}
	h.seen[raw] = name
	return name, nil
}
func (m *Manager) runHLS(id string, sources []MediaSource) {
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil || t.Status != "resolving" {
		m.mu.Unlock()
		return
	}
	t.Sources = sources
	t.Status = "downloading"
	t.Size = -1
	t.EngineIDs = nil
	t.PartDone = []bool{false}
	m.cancels[id] = cancel
	m.lastProgress[id] = time.Now()
	m.saveLocked(t)
	m.mu.Unlock()
	work := filepath.Join(m.root, ".yunx", id)
	dir := filepath.Join(work, "hls")
	if e := os.MkdirAll(dir, 0700); e != nil {
		m.taskError(id, e)
		return
	}
	h := &hlsDownload{m: m, ctx: ctx, id: id, dir: dir, source: sources[0], seen: map[string]string{}}
	manifest, e := h.resource(sources[0].URL, true, 0)
	if e != nil {
		m.taskError(id, e)
		return
	}
	mergeCtx, stop := context.WithTimeout(ctx, 10*time.Minute)
	defer stop()
	cmd := exec.CommandContext(mergeCtx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,crypto,data", "-allowed_extensions", "ALL", "-i", filepath.Join(dir, manifest), "-c", "copy", "-f", "mp4", filepath.Join(work, "part-0"))
	if e = cmd.Run(); e != nil {
		m.taskError(id, fail("hls_merge", "HLS 合并失败，分片已保留，请检查 FFmpeg 和磁盘空间"))
		return
	}
	if ctx.Err() == nil {
		m.finish(id)
	}
}

// Fetch one variant and its declared audio/subtitle group, not every bitrate.
func selectHLSVariant(lines []string) []string {
	best := -1
	var bandwidth int64
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		attrs := hlsAttributes(line)
		n, _ := strconv.ParseInt(attrs["BANDWIDTH"], 10, 64)
		if best < 0 || n > bandwidth {
			best = i
			bandwidth = n
		}
	}
	if best < 0 {
		return lines
	}
	selected := hlsAttributes(lines[best])
	out := []string{"#EXTM3U"}
	for _, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-MEDIA:") {
			a := hlsAttributes(line)
			if a["GROUP-ID"] == selected[a["TYPE"]] && a["GROUP-ID"] != "" {
				out = append(out, line)
			}
		}
	}
	out = append(out, lines[best])
	for i := best + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(lines[i], "#") {
			out = append(out, lines[i])
			break
		}
	}
	return out
}
func hlsAttributes(line string) map[string]string {
	result := map[string]string{}
	pattern := regexp.MustCompile(`([A-Z0-9-]+)=("[^"]*"|[^,]*)`)
	for _, m := range pattern.FindAllStringSubmatch(line, -1) {
		result[m[1]] = strings.Trim(m[2], "\"")
	}
	return result
}

func hlsHasDRM(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#EXT-X-KEY:") {
			format := hlsAttributes(line)["KEYFORMAT"]
			if format != "" && format != "identity" {
				return true
			}
		}
	}
	return false
}

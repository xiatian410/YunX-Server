package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GopeedLab/gopeed/pkg/base"
	"github.com/GopeedLab/gopeed/pkg/download"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/sys/unix"
)

var metadataSlots = make(chan struct{}, 1)

func p2pInput(text string) bool {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "magnet:?") || strings.HasPrefix(text, "ed2k://") || strings.HasPrefix(text, "data:application/x-bittorrent;base64,") {
		return true
	}
	u, e := url.Parse(text)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.HasSuffix(strings.ToLower(u.Path), ".torrent")
}
func (p *Providers) resolveP2P(ctx context.Context, text string) (*ParseSession, error) {
	s := &ParseSession{ID: randomID(), Input: text, CreatedAt: now(), Items: map[string]Item{}, Cursors: map[string]string{}}
	if strings.HasPrefix(text, "ed2k://") {
		fields := strings.Split(text, "|")
		if len(fields) < 6 || fields[1] != "file" {
			return nil, fail("ed2k_link", "仅接受 ed2k 文件链接")
		}
		name, e := url.PathUnescape(fields[2])
		if e != nil {
			return nil, e
		}
		if _, e = safeName(name); e != nil {
			return nil, e
		}
		size, e := strconv.ParseInt(fields[3], 10, 64)
		hash, e2 := hex.DecodeString(fields[4])
		if e != nil || e2 != nil || size <= 0 || len(hash) != 16 {
			return nil, fail("ed2k_link", "ed2k 文件大小或哈希无效")
		}
		s.Platform = "ed2k"
		s.Title = name
		s.Items[fields[4]] = Item{ID: fields[4], Name: name, Size: size}
		return s, nil
	}
	var data []byte
	var e error
	if strings.HasPrefix(text, "magnet:?") {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		select {
		case metadataSlots <- struct{}{}:
			defer func() { <-metadataSlots }()
		case <-ctx.Done():
			return nil, fail("metadata_busy", "磁力元数据解析繁忙，请稍后手动重试")
		}
		temp, e := os.MkdirTemp("", "yunx-metadata-")
		if e != nil {
			return nil, e
		}
		defer os.RemoveAll(temp)
		cfg := torrent.NewDefaultClientConfig()
		cfg.DataDir = temp
		cfg.NoUpload = true
		cfg.DisableWebtorrent = true
		cfg.DisableWebseeds = true
		cfg.ListenPort = 0
		if p.P2PProxy != nil {
			cfg.HTTPProxy = http.ProxyURL(p.P2PProxy)
		}
		client, e := torrent.NewClient(cfg)
		if e != nil {
			return nil, e
		}
		defer client.Close()
		task, e := client.AddMagnet(text)
		if e != nil {
			return nil, fail("magnet_link", "磁力链接无效")
		}
		defer task.Drop()
		select {
		case <-task.GotInfo():
		case <-ctx.Done():
			return nil, fail("magnet_timeout", "45 秒内未取得磁力元数据，请检查网络或提供种子文件")
		}
		meta := task.Metainfo()
		var b bytes.Buffer
		if e = meta.Write(&b); e != nil {
			return nil, e
		}
		data = b.Bytes()
	} else if strings.HasPrefix(text, "data:application/x-bittorrent;base64,") {
		data, e = base64.StdEncoding.DecodeString(strings.TrimPrefix(text, "data:application/x-bittorrent;base64,"))
		if e != nil {
			return nil, fail("torrent_format", "种子文件编码无效")
		}
	} else {
		res, e := p.Net.Request(ctx, "GET", text, nil, nil)
		if e != nil {
			return nil, e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fail("torrent_http", "无法读取种子文件")
		}
		data, e = io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
		if e != nil {
			return nil, e
		}
	}
	if len(data) > 2<<20 {
		return nil, fail("torrent_limit", "种子文件不能超过 2 MiB")
	}
	meta, e := metainfo.Load(bytes.NewReader(data))
	if e != nil {
		return nil, fail("torrent_format", "种子文件格式无效")
	}
	info, e := meta.UnmarshalInfo()
	if e != nil {
		return nil, fail("torrent_format", "种子元数据无效")
	}
	if info.PieceLength <= 0 {
		return nil, fail("torrent_format", "种子分片长度无效")
	}
	if !info.HasV2() && len(info.Pieces)%20 != 0 {
		return nil, fail("torrent_format", "种子分片哈希格式无效")
	}
	if _, e = safeName(info.BestName()); e != nil {
		return nil, e
	}
	files := info.UpvertedFiles()
	if len(files) == 0 || len(files) > 1000 {
		return nil, fail("torrent_limit", "种子须包含 1–1000 个文件")
	}
	var totalSize int64
	for i, file := range files {
		parts := file.BestPath()
		for _, part := range parts {
			if _, e = safeName(part); e != nil {
				return nil, e
			}
		}
		name := info.BestName()
		relative := ""
		if len(parts) > 0 {
			name = parts[len(parts)-1]
			relative = filepath.Join(parts[:len(parts)-1]...)
		}
		if file.Length < 0 || file.Length > math.MaxInt64-totalSize {
			return nil, fail("torrent_format", "种子文件大小无效")
		}
		totalSize += file.Length
		id := strconv.Itoa(i)
		s.Items[id] = Item{ID: id, Name: name, Size: file.Length, Extra: map[string]string{"index": id, "relative_path": relative, "path": filepath.Join(parts...)}}
	}
	if !info.HasV2() {
		pieces := totalSize / info.PieceLength
		if totalSize%info.PieceLength != 0 {
			pieces++
		}
		if pieces != int64(len(info.Pieces)/20) {
			return nil, fail("torrent_format", "种子文件大小与分片数量不一致")
		}
	}
	hash := meta.HashInfoBytes().HexString()
	if e = p.Store.Put("torrent_meta", hash, map[string][]byte{"data": data}); e != nil {
		return nil, e
	}
	s.Platform = "bt"
	s.ShareID = hash
	s.Input = "torrent:" + hash
	s.Title = info.BestName()
	return s, nil
}
func (p *Providers) p2pSources(s *ParseSession) ([]MediaSource, error) {
	if s.Platform == "ed2k" {
		return []MediaSource{{URL: s.Input, Size: -1, Kind: "ed2k"}}, nil
	}
	var stored struct {
		Data []byte `json:"data"`
	}
	ok, e := p.Store.Get("torrent_meta", s.ShareID, &stored)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, fail("torrent_missing", "种子元数据缺失，请重新导入")
	}
	return []MediaSource{{URL: "data:application/x-bittorrent;base64," + base64.StdEncoding.EncodeToString(stored.Data), Size: -1, Kind: "bt"}}, nil
}
func torrentBundle(s *ParseSession, items []Item) (Item, error) {
	selected := []int{}
	paths := []string{}
	var size int64
	for _, item := range items {
		index, e := strconv.Atoi(item.ID)
		if e != nil || index < 0 {
			return Item{}, fail("torrent_selection", "种子文件选择无效")
		}
		selected = append(selected, index)
		paths = append(paths, item.Extra["path"])
		if item.Size < 0 || item.Size > math.MaxInt64-size {
			return Item{}, fail("torrent_size", "种子文件大小超出范围")
		}
		size += item.Size
	}
	indexes, _ := json.Marshal(selected)
	names, _ := json.Marshal(paths)
	return Item{ID: s.ShareID, Name: s.Title, Size: size, Extra: map[string]string{"selected": string(indexes), "paths": string(names), "torrent_name": s.Title}}, nil
}
func (m *Manager) prepareP2P(ctx context.Context, id string, sources []MediaSource, item Item) {
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil || t.Status != "resolving" {
		m.mu.Unlock()
		return
	}
	old := append([]string{}, t.EngineIDs...)
	t.EngineIDs = []string{""}
	if len(old) > 0 {
		t.EngineIDs[0] = old[0]
	}
	t.PartDone = []bool{false}
	t.Sources = sources
	t.Status = "downloading"
	m.lastProgress[id] = time.Now()
	m.saveLocked(t)
	m.mu.Unlock()
	work := filepath.Join(m.root, ".yunx", id)
	if e := os.MkdirAll(work, 0700); e != nil {
		m.taskError(id, e)
		return
	}
	req := &base.Request{URL: sources[0].URL, Labels: map[string]string{"yunxTaskId": id, "part": "0"}, Proxy: &base.RequestProxy{Mode: base.RequestProxyModeNone}}
	opts := &base.Options{Path: work, Name: "part-0"}
	if sources[0].Kind == "bt" {
		req.Proxy = &base.RequestProxy{Mode: base.RequestProxyModeFollow}
		opts.Path = filepath.Join(work, "payload")
		opts.Name = ""
		if e := json.Unmarshal([]byte(item.Extra["selected"]), &opts.SelectFiles); e != nil {
			m.taskError(id, e)
			return
		}
	}
	engineID := ""
	var e error
	if len(old) > 0 && old[0] != "" && m.engine.GetTask(old[0]) != nil {
		engineID = old[0]
		e = m.engine.Continue(&download.TaskFilter{IDs: []string{engineID}})
	} else {
		engineID, e = m.engine.CreateDirect(req, opts)
	}
	if e != nil {
		m.taskError(id, fail("p2p_start", "P2P 引擎启动失败，请检查种子、网络和保存目录"))
		return
	}
	m.mu.Lock()
	t = m.tasks[id]
	pause := t == nil || t.Status != "downloading" || ctx.Err() != nil
	if t != nil {
		t.EngineIDs[0] = engineID
		m.engineState[engineID] = "running"
		m.saveLocked(t)
	}
	m.mu.Unlock()
	if pause {
		_ = m.engine.Pause(&download.TaskFilter{IDs: []string{engineID}})
	}
}
func (m *Manager) finishTorrent(id string) {
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil || t.Status != "downloading" {
		m.mu.Unlock()
		return
	}
	t.Status = "merging"
	copyTask := *t
	m.saveLocked(t)
	m.mu.Unlock()
	// Stop seeding and close file handles before publishing the selected files.
	if e := m.engine.Delete(&download.TaskFilter{IDs: nonempty(copyTask.EngineIDs)}, false); e != nil {
		m.taskError(id, e)
		return
	}
	work := filepath.Join(m.root, ".yunx", id)
	source := filepath.Join(work, "payload", copyTask.Item.Extra["torrent_name"])
	var paths []string
	if e := json.Unmarshal([]byte(copyTask.Item.Extra["paths"]), &paths); e != nil {
		m.taskError(id, e)
		return
	}
	publish := filepath.Join(work, "selected")
	if e := os.MkdirAll(publish, 0700); e != nil {
		m.taskError(id, e)
		return
	}
	single := len(paths) == 1 && paths[0] == ""
	if single {
		publish = source
	} else {
		sourceRoot, e := os.OpenRoot(source)
		if e != nil {
			m.taskError(id, e)
			return
		}
		defer sourceRoot.Close()
		for _, relative := range paths {
			if !filepath.IsLocal(relative) {
				m.taskError(id, fail("unsafe_path", "种子文件路径无效"))
				return
			}
			info, e := sourceRoot.Stat(relative)
			if e != nil || !info.Mode().IsRegular() {
				m.taskError(id, fail("torrent_file", "下载结果不是普通文件"))
				return
			}
			target := filepath.Join(publish, relative)
			if e = os.MkdirAll(filepath.Dir(target), 0750); e != nil {
				m.taskError(id, e)
				return
			}
			if e = os.Link(filepath.Join(source, relative), target); e != nil && !errors.Is(e, os.ErrExist) {
				m.taskError(id, e)
				return
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t = m.tasks[id]
	if t == nil || t.Status != "merging" {
		return
	}
	dir, e := m.Directory(t.Directory, true)
	if e == nil {
		e = unix.Renameat2(unix.AT_FDCWD, publish, unix.AT_FDCWD, filepath.Join(dir, t.Name), unix.RENAME_NOREPLACE)
	}
	if e != nil {
		t.Status = "failed"
		t.Error = "发布下载结果失败，已保留文件，请检查同名路径和目录权限"
		m.saveLocked(t)
		return
	}
	t.Status = "completed"
	t.HasFile = true
	t.Downloaded = t.Size
	t.Speed = 0
	t.FinishedAt = now()
	t.EngineIDs = nil
	m.saveLocked(t)
	m.store.Event(id, "completed", "种子所选文件已保存，做种已停止")
	_ = os.RemoveAll(work)
	m.signal()
}

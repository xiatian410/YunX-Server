package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type CredentialField struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Help string `json:"help"`
}

var credentialFields = map[string][]CredentialField{
	"xunlei":  {{"device_id", "设备 ID", "与登录令牌对应的设备 ID；留空保留已有值"}, {"captcha_token", "验证令牌", "仅填写平台正常验证后取得的令牌"}},
	"ilanzou": {{"uuid", "设备 UUID", "与登录时使用的 UUID 一致"}, {"user_id", "用户 ID", "平台账号的 userId"}},
	"guangya": {{"device_id", "设备 ID", "与登录令牌对应的设备 ID"}, {"device_sign", "设备签名", "与平台正常登录时取得的签名一致"}},
}

func (a *App) importTorrent(w http.ResponseWriter, r *http.Request) error {
	data, e := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if e != nil {
		return e
	}
	if len(data) > 2<<20 {
		return fail("torrent_limit", "种子文件不能超过 2 MiB")
	}
	s, e := a.Providers.resolveP2P(r.Context(), "data:application/x-bittorrent;base64,"+base64.StdEncoding.EncodeToString(data))
	if e != nil {
		return e
	}
	result, e := a.Providers.Browse(r.Context(), s, "0", 1)
	if e != nil {
		return e
	}
	a.parseMu.Lock()
	for id, v := range a.parses {
		created, _ := time.Parse(time.RFC3339, v.CreatedAt)
		if time.Since(created) > time.Hour {
			delete(a.parses, id)
		}
	}
	if len(a.parses) >= 100 {
		a.parseMu.Unlock()
		return fail("parse_limit", "解析会话已达上限")
	}
	a.parses[s.ID] = s
	a.parseMu.Unlock()
	jsonOut(w, 200, publicBrowse(result))
	return nil
}
func (a *App) changeQuality(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		SessionID string `json:"session_id"`
		Quality   int    `json:"quality"`
	}
	if e := readJSON(r, &b); e != nil {
		return e
	}
	a.parseMu.Lock()
	old := a.parses[b.SessionID]
	if old == nil {
		a.parseMu.Unlock()
		return fail("session_missing", "解析会话已失效")
	}
	s := *old
	s.Items = map[string]Item{}
	a.parseMu.Unlock()
	valid := false
	for _, q := range s.Qualities {
		if q.ID == b.Quality {
			valid = true
		}
	}
	if !valid || a.Providers.Bridge == nil {
		return fail("quality_invalid", "请选择平台返回的清晰度")
	}
	s.Quality = b.Quality
	req, e := a.Providers.bridgeRequest(&s, "list")
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var result struct {
		Items     []Item    `json:"items"`
		Quality   int       `json:"quality"`
		Qualities []Quality `json:"qualities"`
		Notice    string    `json:"notice"`
	}
	if e = a.Providers.Bridge.Call(ctx, req, &result); e != nil {
		return e
	}
	if len(result.Items) == 0 {
		return fail("upstream_empty", "该清晰度没有可下载媒体")
	}
	s.Quality = result.Quality
	s.Qualities = result.Qualities
	s.Notice = result.Notice
	for _, item := range result.Items {
		s.Items[item.ID] = item
	}
	a.parseMu.Lock()
	a.parses[s.ID] = &s
	a.parseMu.Unlock()
	jsonOut(w, 200, publicBrowse(BrowseResult{SessionID: s.ID, Platform: s.Platform, Title: s.Title, Items: result.Items, Parent: "0", Page: 1, Qualities: s.Qualities, Quality: s.Quality, Notice: s.Notice}))
	return nil
}

func (a *App) retryCleanup(w http.ResponseWriter, r *http.Request) error {
	rows, e := a.Store.List("provider_cleanup")
	if e != nil {
		return e
	}
	held := map[string]bool{}
	a.Manager.mu.Lock()
	for _, task := range a.Manager.tasks {
		if task.Status == "resolving" {
			a.Manager.mu.Unlock()
			return fail("cleanup_busy", "请等待正在解析的任务完成后再清理")
		}
		if task.Session != nil && task.Status != "completed" && task.Status != "cancelled" {
			for _, source := range task.Sources {
				held[cleanupID(task.Session, source)] = true
			}
		}
	}
	a.Manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	cleaned, pending := 0, len(rows)
	for id, data := range rows {
		if held[id] {
			continue
		}
		var job providerCleanup
		if e = json.Unmarshal(data, &job); e != nil {
			return e
		}
		if e = a.Providers.Cleanup(ctx, &job.Session, []MediaSource{job.Source}); e != nil {
			// Keep failed cleanup pending.
		} else {
			cleaned++
			pending--
		}
		if ctx.Err() != nil {
			break
		}
	}
	if remaining, err := a.Store.List("provider_cleanup"); err == nil {
		pending = len(remaining)
	}
	jsonOut(w, 200, map[string]int{"cleaned": cleaned, "pending": pending})
	return nil
}

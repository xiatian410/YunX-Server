package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const quarkUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) quark-cloud-drive/2.5.20 Chrome/100.0.4896.160 Electron/18.3.5.12-a038f7b798 Safari/537.36 Channel/pckk_other_ch"
const ucUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) uc-cloud-drive/2.5.20 Chrome/100.0.4896.160 Electron/18.3.5.4-b478491100 Safari/537.36 Channel/pckk_other_ch"

type Providers struct {
	P2PProxy *url.URL
	Bridge   *ProviderProcess
	Store    *Store
	Net      *Network
	mu       sync.Mutex
}
type Platform struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Credential string `json:"credential"`
	Cloud      bool   `json:"cloud"`
}

var Platforms = []Platform{{"quark", "夸克网盘", "cookie", true}, {"uc", "UC 网盘", "cookie", true}, {"pan123", "123 云盘", "token", true}, {"bilibili", "哔哩哔哩", "cookie", false}}

type Quality struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type BrowseResult struct {
	Qualities []Quality `json:"qualities,omitempty"`
	Quality   int       `json:"quality,omitempty"`
	Notice    string    `json:"notice,omitempty"`
	SessionID string    `json:"session_id"`
	Platform  string    `json:"platform"`
	Title     string    `json:"title"`
	Items     []Item    `json:"items"`
	Parent    string    `json:"parent"`
	Page      int       `json:"page"`
	More      bool      `json:"more"`
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"'，。]+`)
var bvPattern = regexp.MustCompile(`(?i)(BV[0-9a-zA-Z]{10}|av[0-9]+)`)

func (p *Providers) account(id string) (Account, error) {
	var a Account
	if id == "" {
		a.Credentials = map[string]string{}
		return a, nil
	}
	ok, e := p.Store.Get("accounts", id, &a)
	if e != nil {
		return a, e
	}
	if !ok {
		return a, fail("account_missing", "账号不存在，请重新选择")
	}
	return a, nil
}
func (p *Providers) credentials(s *ParseSession) (map[string]string, error) {
	a, e := p.account(s.AccountID)
	if e != nil {
		return nil, e
	}
	if a.ID != "" && a.Platform != s.Platform {
		return nil, fail("account_platform", "所选账号与链接平台不一致")
	}
	return a.Credentials, nil
}
func (p *Providers) Resolve(ctx context.Context, text, password, accountID string, cloud bool) (*ParseSession, error) {
	if !cloud && p2pInput(strings.TrimSpace(text)) {
		return p.resolveP2P(ctx, strings.TrimSpace(text))
	}
	if p.Bridge != nil {
		s, e := p.bridgeResolve(ctx, text, password, accountID, cloud)
		if e != nil || s != nil {
			return s, e
		}
	}
	if !cloud && githubURL(urlPattern.FindString(text)) {
		return p.githubResolve(ctx, urlPattern.FindString(text), accountID)
	}
	if cloud {
		a, e := p.account(accountID)
		if e != nil {
			return nil, e
		}
		if a.Platform == "github" {
			c := a.Credentials
			h := map[string]string{"Authorization": "Bearer " + c["token"]}
			j, _, err := p.Net.JSON(ctx, "GET", "https://api.github.com/user", h, nil)
			if err != nil {
				return nil, err
			}
			s, err := p.githubResolve(ctx, "https://github.com/"+str(j["login"]), accountID)
			if s != nil {
				s.Cloud = true
			}
			return s, err
		}
	}
	s := &ParseSession{ID: randomID(), Input: strings.TrimSpace(text), Password: password, AccountID: accountID, Cloud: cloud, CreatedAt: now(), Items: map[string]Item{}, Cursors: map[string]string{}}
	if cloud {
		a, e := p.account(accountID)
		if e != nil {
			return nil, e
		}
		if a.ID == "" || a.Platform == "bilibili" {
			return nil, fail("unsupported", "该账号不支持个人网盘浏览")
		}
		s.Platform = a.Platform
		s.Title = a.Name
		return s, nil
	}
	raw := urlPattern.FindString(text)
	if raw == "" && bvPattern.MatchString(text) {
		raw = "https://www.bilibili.com/video/" + bvPattern.FindString(text)
	}
	if e := validURL(raw); e != nil {
		return nil, e
	}
	u, _ := url.Parse(raw)
	if u.Hostname() == "b23.tv" {
		res, e := p.Net.Request(ctx, "GET", raw, nil, nil)
		if e != nil {
			return nil, e
		}
		res.Body.Close()
		u = res.Request.URL
		raw = u.String()
	}
	s.Input = raw
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case host == "pan.quark.cn":
		s.Platform = "quark"
	case host == "drive.uc.cn" || host == "fast.uc.cn":
		s.Platform = "uc"
	case host == "123pan.com" || host == "www.123pan.com" || host == "www.123865.com" || host == "123865.com" || host == "www.123912.com" || host == "yun.123pan.cn":
		s.Platform = "pan123"
	case host == "www.bilibili.com" || host == "bilibili.com" || host == "m.bilibili.com":
		s.Platform = "bilibili"
	default:
		s.Platform = "direct"
	}
	if s.Platform == "direct" {
		name, _ := url.PathUnescape(path.Base(u.Path))
		if name == "" || name == "/" || name == "." {
			name = "download.bin"
		}
		if strings.HasSuffix(strings.ToLower(u.Path), ".m3u8") {
			name = strings.TrimSuffix(name, path.Ext(name)) + ".mp4"
		}
		s.Title = name
		s.Items["file"] = Item{ID: "file", Name: name, Size: -1}
		return s, nil
	}
	if _, e := p.credentials(s); e != nil {
		return nil, e
	}
	if s.Platform == "bilibili" {
		id := bvPattern.FindString(raw)
		if id == "" {
			return nil, fail("unsupported", "仅支持 Bilibili BV/av 视频链接")
		}
		s.ShareID = id
		data, _, e := p.api(ctx, s, "GET", params("https://api.bilibili.com/x/web-interface/view", biliID(id)), nil)
		if e != nil {
			return nil, e
		}
		d := obj(data["data"])
		s.Title = str(d["title"])
		s.ShareID = str(d["bvid"])
		for i, v := range arr(d["pages"]) {
			page := obj(v)
			id := str(page["cid"])
			if id == "" {
				continue
			}
			name := s.Title
			if len(arr(d["pages"])) > 1 {
				name = fmt.Sprintf("%s - P%d %s", s.Title, i+1, str(page["part"]))
			}
			s.Items[id] = Item{ID: id, Name: name + ".mp4", Size: -1}
		}
		if len(s.Items) == 0 {
			return nil, fail("upstream_empty", "视频接口没有返回分 P 信息")
		}
		return s, nil
	}
	if len(parts) < 2 || parts[0] != "s" {
		return nil, fail("invalid_share", "请输入平台的 /s/ 分享链接")
	}
	s.ShareID = strings.TrimSuffix(parts[1], ".html")
	s.Title = "分享文件"
	if s.Password == "" {
		s.Password = u.Query().Get("pwd")
		if s.Password == "" {
			m := regexp.MustCompile(`(?:提取码|访问码|密码)[:：\s]*([a-zA-Z0-9]{4})`).FindStringSubmatch(text)
			if len(m) > 1 {
				s.Password = m[1]
			}
		}
	}
	if s.Platform == "quark" || s.Platform == "uc" {
		payload := map[string]any{"pwd_id": s.ShareID, "passcode": s.Password}
		if s.Platform == "quark" {
			payload["support_visit_limit_private_share"] = true
		} else {
			payload["share_for_transfer"] = true
		}
		j, _, e := p.api(ctx, s, "POST", p.panURL(s, "share/sharepage/token", nil), payload)
		if e != nil {
			return nil, e
		}
		d := obj(j["data"])
		s.Token = str(d["stoken"])
		if s.Token == "" {
			return nil, fail("upstream_format", "平台未返回分享访问凭证")
		}
		if name := str(d["title"]); name != "" {
			s.Title = name
		}
	}
	return s, nil
}
func biliID(id string) map[string]string {
	if strings.HasPrefix(strings.ToLower(id), "av") {
		return map[string]string{"aid": id[2:]}
	}
	return map[string]string{"bvid": id}
}
func (p *Providers) panURL(s *ParseSession, endpoint string, q map[string]string) string {
	base := "https://drive-pc.quark.cn/1/clouddrive/"
	v := map[string]string{"pr": "ucpro", "fr": "pc"}
	if s.Platform == "uc" {
		base = "https://pc-api.uc.cn/1/clouddrive/"
		v = map[string]string{"pr": "UCBrowser", "fr": "pc"}
		if !s.Cloud && endpoint != "share/sharepage/token" {
			v["entry"] = "ft"
		}
		if s.Cloud && endpoint == "file/download" {
			return base + endpoint
		}
	}
	for k, x := range q {
		v[k] = x
	}
	return params(base+endpoint, v)
}
func (p *Providers) headers(s *ParseSession) (map[string]string, error) {
	c, e := p.credentials(s)
	if e != nil {
		return nil, e
	}
	h := map[string]string{"User-Agent": chromeUA}
	if c["cookie"] != "" {
		h["Cookie"] = c["cookie"]
	}
	switch s.Platform {
	case "quark":
		h["User-Agent"] = quarkUA
		h["Referer"] = "https://pan.quark.cn/"
		h["Origin"] = "https://pan.quark.cn"
	case "uc":
		h["Referer"] = "https://fast.uc.cn/"
		h["Origin"] = "https://fast.uc.cn"
		if s.Cloud {
			h["Referer"] = "https://drive.uc.cn/"
			h["Origin"] = "https://drive.uc.cn"
			h["User-Agent"] = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) uc-cloud-drive/1.6.1 Chrome/100.0.4896.160 Electron/18.3.5.16-b62cf9c50d Safari/537.36 Channel/ucpan_other_ch"
		} else if c["cookie"] == "" {
			h["User-Agent"] = ucUA
			h["Sec-Ch-Ua"] = `"Not=A?Brand";v="99", "Chromium";v="100", "Google Chrome";v="100"`
		}
	case "pan123":
		h["Referer"] = "https://yun.123pan.cn/"
		h["platform"] = "android"
		h["app-version"] = "39"
		h["loginuuid"] = c["device_id"]
		if c["token"] != "" {
			h["Authorization"] = "Bearer " + strings.TrimPrefix(c["token"], "Bearer ")
		}
	case "bilibili":
		h["Referer"] = "https://www.bilibili.com/"
	}
	return h, nil
}
func (p *Providers) api(ctx context.Context, s *ParseSession, method, raw string, body any) (map[string]any, http.Header, error) {
	h, e := p.headers(s)
	if e != nil {
		return nil, nil, e
	}
	if s.Platform == "pan123" {
		u, _ := url.Parse(raw)
		k, v := sign123(u.Path, time.Now().Unix(), randomUint())
		h["auth-key"] = k
		h["auth-value"] = v
		if strings.Contains(u.Path, "share/get") {
			h["User-Agent"] = "Dart/3.12 (dart:io)"
			delete(h, "Authorization")
		} else if s.Cloud || strings.HasSuffix(u.Path, "user/info") {
			h["platform"] = "web"
			h["app-version"] = "3"
		}
	}
	j, headers, e := p.Net.JSON(ctx, method, raw, h, body)
	if e != nil {
		return nil, headers, e
	}
	if s.Platform == "quark" || s.Platform == "uc" {
		if num(j["status"]) != 200 {
			code := str(j["code"])
			if code == "31001" || code == "23018" {
				return nil, headers, fail("account_required", "平台要求登录后下载，请添加账号并重新解析")
			}
			if code == "41020" {
				return nil, headers, fail("source_expired", "分享文件令牌已过期，请重新解析")
			}
			return nil, headers, fail("upstream_rejected", "平台拒绝请求（代码 "+code+"），请检查分享提取码和账号状态")
		}
		p.mergeCookies(s.AccountID, headers)
	} else if v, ok := j["code"]; !ok || num(v) != 0 {
		return nil, headers, fail("upstream_rejected", "平台拒绝请求（代码 "+str(j["code"])+"），请检查链接、权限和登录状态")
	}
	return j, headers, nil
}
func (p *Providers) mergeCookies(accountID string, h http.Header) {
	if accountID == "" || len(h.Values("Set-Cookie")) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a, e := p.account(accountID)
	if e != nil {
		return
	}
	cookies := parseCookies(a.Credentials["cookie"])
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == "__pus" || c.Name == "__puus" || c.Name == "__pugs" {
			cookies[c.Name] = c.Value
		}
	}
	parts := []string{}
	for k, v := range cookies {
		parts = append(parts, k+"="+v)
	}
	a.Credentials["cookie"] = strings.Join(parts, "; ")
	_ = p.Store.Put("accounts", a.ID, a)
}
func parseCookies(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			out[k] = v
		}
	}
	return out
}
func (p *Providers) Browse(ctx context.Context, s *ParseSession, parent string, page int) (BrowseResult, error) {
	if s.Platform == "bt" || s.Platform == "ed2k" {
		items := []Item{}
		for _, item := range s.Items {
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		return BrowseResult{SessionID: s.ID, Platform: s.Platform, Title: s.Title, Parent: parent, Page: 1, Items: items}, nil
	}
	if s.Platform == "github" {
		return p.githubBrowse(ctx, s, parent, page)
	}
	if p.bridgePlatform(s.Platform) {
		return p.bridgeBrowse(ctx, s, parent, page)
	}
	out := BrowseResult{SessionID: s.ID, Platform: s.Platform, Title: s.Title, Items: []Item{}, Parent: parent, Page: page}
	if page < 1 || page > 200 {
		return out, fail("page_limit", "页码范围为 1–200，请缩小目录范围")
	}
	if parent == "" {
		parent = "0"
		out.Parent = parent
	}
	if s.Platform == "direct" || s.Platform == "bilibili" {
		if page == 1 {
			for _, item := range s.Items {
				out.Items = append(out.Items, item)
			}
		}
		return out, nil
	}
	var data map[string]any
	var list []any
	if s.Platform == "quark" || s.Platform == "uc" {
		q := map[string]string{"pdir_fid": parent, "_page": strconv.Itoa(page), "_size": "50", "_fetch_total": "1", "_sort": "file_type:asc,file_name:asc"}
		endpoint := "share/sharepage/detail"
		if s.Cloud {
			endpoint = "file/sort"
			q["_fetch_sub_dirs"] = "0"
			q["_sort"] = "file_type:asc,updated_at:desc"
			if s.Platform == "quark" {
				q["uc_param_str"] = ""
				q["fetch_all_file"] = "1"
				q["fetch_risk_file_name"] = "1"
			}
		} else {
			q["pwd_id"] = s.ShareID
			q["stoken"] = s.Token
			q["ver"] = "2"
			q["force"] = "0"
			q["_fetch_share"] = "0"
			q["_fetch_banner"] = "0"
			q["fetch_relate_conversation"] = "0"
			if s.Platform == "uc" {
				endpoint = "transfer_share/detail"
				q["fetch_file_list"] = "1"
				q["passcode"] = s.Password
				q["_fetch_task"] = "1"
				q["_fetch_share"] = "1"
				q["_sort"] = ""
			}
		}
		j, _, e := p.api(ctx, s, "GET", p.panURL(s, endpoint, q), nil)
		if e != nil {
			return out, e
		}
		data = obj(j["data"])
		list = arr(data["list"])
		if list == nil {
			list = arr(obj(data["detail_info"])["list"])
		}
		if list == nil {
			return out, fail("upstream_format", "目录响应缺少文件列表，未将异常当成空目录")
		}
		out.More = len(list) > 0
		for _, v := range list {
			f := obj(v)
			id := str(f["fid"])
			if id == "" {
				return out, fail("upstream_format", "文件缺少标识")
			}
			out.Items = append(out.Items, Item{ID: id, Name: str(f["file_name"]), Size: num(f["size"]), Directory: boolv(f["dir"]), Extra: map[string]string{"parent": parent, "token": str(f["share_fid_token"])}})
		}
	} else if s.Platform == "pan123" {
		endpoint := "https://yun.123pan.cn/b/api/share/get"
		q := map[string]string{"limit": "100", "next": "0", "orderBy": "file_name", "orderDirection": "asc", "ParentFileId": parent, "Page": strconv.Itoa(page)}
		if s.Cloud {
			endpoint = "https://yun.123pan.cn/b/api/file/list/new"
			q = map[string]string{"driveId": "0", "limit": "100", "next": "0", "orderBy": "update_time", "orderDirection": "desc", "parentFileId": parent, "trashed": "false", "SearchData": "", "Page": strconv.Itoa(page), "OnlyLookAbnormalFile": "0", "event": "homeListFile", "operateType": "1", "inDirectSpace": "false"}
			if page > 1 {
				cursor, ok := s.Cursors[parent+":"+strconv.Itoa(page)]
				if !ok {
					return out, fail("page_order", "请按顺序加载目录页")
				}
				q["next"] = cursor
			}
		} else {
			q["shareKey"] = s.ShareID
			if s.Password != "" {
				q["SharePwd"] = s.Password
			}
		}
		j, _, e := p.api(ctx, s, "GET", params(endpoint, q), nil)
		if e != nil {
			return out, e
		}
		data = obj(j["data"])
		if boolv(data["Expired"]) {
			return out, fail("share_expired", "分享已经失效")
		}
		list = arr(data["InfoList"])
		if list == nil {
			return out, fail("upstream_format", "平台响应缺少 InfoList")
		}
		out.More = str(data["Next"]) != "-1" && len(list) > 0
		if s.Cloud {
			if s.Cursors == nil {
				s.Cursors = map[string]string{}
			}
			s.Cursors[parent+":"+strconv.Itoa(page+1)] = str(data["Next"])
		}
		for _, v := range list {
			f := obj(v)
			id := str(f["FileId"])
			if id == "" {
				return out, fail("upstream_format", "文件缺少标识")
			}
			out.Items = append(out.Items, Item{ID: id, Name: str(f["FileName"]), Size: num(f["Size"]), Directory: num(f["Type"]) == 1, Extra: map[string]string{"parent": parent, "s3": str(f["S3KeyFlag"]), "etag": str(f["Etag"]), "node": str(f["StorageNode"])}})
		}
	}
	for _, item := range out.Items {
		s.Items[item.ID] = item
	}
	return out, nil
}
func (p *Providers) Sources(ctx context.Context, s *ParseSession, item Item) ([]MediaSource, error) {
	if s.Platform == "bt" || s.Platform == "ed2k" {
		return p.p2pSources(s)
	}
	if s.Platform == "github" {
		return p.githubSources(ctx, s, item)
	}
	if p.bridgePlatform(s.Platform) {
		return p.bridgeSources(ctx, s, item)
	}
	h, e := p.headers(s)
	if e != nil {
		return nil, e
	}
	switch s.Platform {
	case "direct":
		kind := "http"
		u, _ := url.Parse(s.Input)
		if strings.HasSuffix(strings.ToLower(u.Path), ".m3u8") {
			kind = "hls"
		}
		return []MediaSource{{URL: s.Input, Headers: map[string]string{"User-Agent": chromeUA}, Size: -1, Kind: kind}}, nil
	case "quark", "uc":
		payload := map[string]any{"fids": []string{item.ID}}
		if !s.Cloud {
			payload["pwd_id"] = s.ShareID
			payload["stoken"] = s.Token
			payload["fids_token"] = []string{item.Extra["token"]}
		}
		q := map[string]string{}
		if s.Platform == "quark" {
			q["sys"] = "win32"
			q["ve"] = "3.23.2"
			if !s.Cloud {
				payload["speedup_session"] = ""
				payload["token"] = ""
			}
		}
		j, rh, e := p.api(ctx, s, "POST", p.panURL(s, "file/download", q), payload)
		if e != nil {
			return nil, e
		}
		list := arr(j["data"])
		if len(list) == 0 {
			return nil, fail("upstream_format", "取链接口未返回文件")
		}
		u := str(obj(list[0])["download_url"])
		if e = validURL(u); e != nil {
			return nil, fail("upstream_format", "取链接口缺少有效下载地址")
		}
		h, _ = p.headers(s)
		delete(h, "Origin")
		if s.Platform == "uc" {
			h["Referer"] = "https://drive.uc.cn/"
		}
		if s.AccountID == "" {
			cookies := []string{}
			for _, c := range (&http.Response{Header: rh}).Cookies() {
				if c.Name == "__pugs" {
					cookies = append(cookies, c.Name+"="+c.Value)
				}
			}
			if len(cookies) > 0 {
				h["Cookie"] = strings.Join(cookies, "; ")
			}
		}
		return []MediaSource{{URL: u, Headers: h, Size: item.Size}}, nil
	case "pan123":
		c, _ := p.credentials(s)
		if c["token"] == "" {
			return nil, fail("account_required", "123 云盘下载需要登录凭据，请添加账号并重新解析")
		}
		endpoint := "https://www.123865.com/b/api/share/download/info"
		body := map[string]any{"ShareKey": s.ShareID, "FileID": item.ID, "S3KeyFlag": item.Extra["s3"], "Size": item.Size, "Etag": item.Extra["etag"]}
		if s.Cloud {
			endpoint = "https://yun.123pan.cn/api/file/download_info"
			body = map[string]any{"driveId": 0, "etag": item.Extra["etag"], "fileId": json.Number(item.ID), "s3keyFlag": item.Extra["s3"], "type": 0, "fileName": item.Name, "size": item.Size}
		}
		j, _, e := p.api(ctx, s, "POST", endpoint, body)
		if e != nil {
			return nil, e
		}
		field := "DownloadURL"
		if s.Cloud {
			field = "DownloadUrl"
		}
		u, e := decode123(str(obj(j["data"])[field]))
		if e != nil {
			return nil, e
		}
		return []MediaSource{{URL: u, Headers: map[string]string{"User-Agent": chromeUA, "Referer": "https://yun.123pan.cn/"}, Size: item.Size}}, nil
	case "bilibili":
		q := map[string]string{"bvid": s.ShareID, "cid": item.ID, "qn": "80", "fnver": "0", "fourk": "1", "fnval": "1", "platform": "html5", "high_quality": "1"}
		if h["Cookie"] != "" {
			q["qn"] = "120"
			q["fnval"] = "4048"
			delete(q, "platform")
		}
		j, _, e := p.api(ctx, s, "GET", params("https://api.bilibili.com/x/player/playurl", q), nil)
		if e != nil {
			return nil, e
		}
		d := obj(j["data"])
		dash := obj(d["dash"])
		if videos := arr(dash["video"]); len(videos) > 0 {
			best := obj(videos[0])
			for _, v := range videos {
				f := obj(v)
				if num(f["codecid"]) == 7 {
					best = f
					break
				}
			}
			audios := arr(dash["audio"])
			if len(audios) == 0 {
				return nil, fail("upstream_format", "DASH 响应缺少音轨，未将无声视频标为完整下载")
			}
			audio := obj(audios[0])
			for _, a := range audios {
				if num(obj(a)["id"]) > num(audio["id"]) {
					audio = obj(a)
				}
			}
			return []MediaSource{{URL: mediaURL(best), Headers: h, Size: -1}, {URL: mediaURL(audio), Headers: h, Size: -1}}, nil
		}
		files := arr(d["durl"])
		if len(files) != 1 {
			return nil, fail("unsupported_media", "当前视频未返回单文件或 DASH 双轨，暂不支持此媒体封装")
		}
		f := obj(files[0])
		return []MediaSource{{URL: str(f["url"]), Headers: h, Size: num(f["size"])}}, nil
	}
	return nil, fail("unsupported", "不支持该平台")
}
func mediaURL(j map[string]any) string {
	u := str(j["baseUrl"])
	if u == "" {
		u = str(j["base_url"])
	}
	return u
}
func randomUint() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:]) % 10000000
}
func sign123(p string, ts int64, r uint32) (string, string) {
	digits := time.Unix(ts+57600, 0).UTC().Format("200601021504")
	alphabet := "adefghlmyijnopkqrstubcvwsz"
	b := make([]byte, len(digits))
	for i, c := range []byte(digits) {
		b[i] = alphabet[int(c-'0')]
	}
	key := fmt.Sprintf("%x", crc32.ChecksumIEEE(b))
	v := fmt.Sprintf("%d|%d|%s|web|3|%s", ts, r, p, key)
	return key, fmt.Sprintf("%d-%d-%x", ts, r, crc32.ChecksumIEEE([]byte(v)))
}
func decode123(raw string) (string, error) {
	decode := func(s string) string {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, e := enc.DecodeString(s); e == nil {
				return string(b)
			}
		}
		return s
	}
	if !strings.HasPrefix(raw, "http") {
		raw = decode(raw)
	}
	u, e := url.Parse(raw)
	if e == nil && u.Query().Get("params") != "" {
		raw = decode(u.Query().Get("params"))
	}
	if e = validURL(raw); e != nil {
		return "", fail("upstream_format", "123 云盘未返回有效下载地址")
	}
	return raw, nil
}
func (p *Providers) CheckAccount(ctx context.Context, a Account) (Account, error) {
	if a.Platform == "github" {
		j, _, e := p.Net.JSON(ctx, "GET", "https://api.github.com/user", map[string]string{"Authorization": "Bearer " + a.Credentials["token"]}, nil)
		a.CheckedAt = now()
		if e != nil {
			a.Status = "unavailable"
			a.Message = "GitHub 检查失败，凭据已保留"
			return a, e
		}
		a.Nickname = str(j["login"])
		a.Status = "expired"
		if a.Nickname != "" {
			a.Status = "valid"
		}
		return a, nil
	}
	if p.bridgePlatform(a.Platform) {
		return p.bridgeCheck(ctx, a)
	}
	s := &ParseSession{Platform: a.Platform, AccountID: a.ID}
	h, e := p.headers(s)
	if e != nil {
		return a, e
	}
	var raw string
	switch a.Platform {
	case "quark":
		raw = "https://pan.quark.cn/account/info"
	case "uc":
		raw = "https://drive.uc.cn/account/info"
	case "pan123":
		raw = "https://yun.123pan.cn/b/api/user/info"
		k, v := sign123("/b/api/user/info", time.Now().Unix(), randomUint())
		h["auth-key"] = k
		h["auth-value"] = v
		h["platform"] = "web"
		h["app-version"] = "3"
	case "bilibili":
		raw = "https://api.bilibili.com/x/web-interface/nav"
	default:
		return a, fail("unsupported", "平台不存在")
	}
	j, _, e := p.Net.JSON(ctx, "GET", raw, h, nil)
	a.CheckedAt = now()
	if e != nil {
		a.Status = "unavailable"
		a.Message = "暂时无法连接平台，凭据已保留"
		return a, e
	}
	d := obj(j["data"])
	valid := false
	switch a.Platform {
	case "quark", "uc":
		valid = boolv(j["success"])
		a.Nickname = str(d["nickname"])
	case "pan123":
		v, ok := j["code"]
		valid = ok && num(v) == 0
		a.Nickname = str(d["Nickname"])
	case "bilibili":
		valid = boolv(d["isLogin"])
		a.Nickname = str(d["uname"])
	}
	if valid {
		a.Status = "valid"
		a.Message = "账号检查通过"
	} else {
		a.Status = "expired"
		a.Message = "平台未确认登录状态，请更新凭据"
	}
	return a, nil
}

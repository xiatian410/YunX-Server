package server

import (
	"context"
	"fmt"
	"strings"
)

var additionalPlatforms = []Platform{
	{"baidu", "百度网盘", "cookie", true}, {"xunlei", "迅雷网盘", "token", true},
	{"c139", "139 网盘", "cookie", true}, {"pan115", "115 网盘", "cookie", true},
	{"guangya", "光鸭云盘", "token", true}, {"lanzou", "蓝奏云", "cookie", true},
	{"ilanzou", "蓝奏云优享版", "token", true}, {"douyin", "抖音", "cookie", false},
	{"xhs", "小红书", "cookie", false}, {"kuaishou", "快手", "cookie", false},
	{"weibo", "微博", "cookie", false}, {"acfun", "AcFun", "cookie", false}, {"pixiv", "Pixiv", "cookie", false},
}

func (p *Providers) Platforms() []Platform {
	list := append([]Platform{}, Platforms...)
	list = append(list, Platform{"github", "GitHub", "token", true})
	if p.Bridge != nil {
		list = append(list, additionalPlatforms...)
	}
	return list
}
func (p *Providers) bridgePlatform(id string) bool {
	if p.Bridge == nil {
		return false
	}
	for _, v := range p.Platforms() {
		if v.ID == id && id != "github" {
			return true
		}
	}
	return false
}
func (p *Providers) bridgeRequest(s *ParseSession, op string) (map[string]any, error) {
	c, e := p.credentials(s)
	if e != nil {
		return nil, e
	}
	return map[string]any{"op": op, "platform": s.Platform, "text": s.Input, "password": s.Password, "cloud": s.Cloud, "credentials": c, "quality": s.Quality}, nil
}
func (p *Providers) bridgeResolve(ctx context.Context, text, password, accountID string, cloud bool) (*ParseSession, error) {
	s := &ParseSession{ID: randomID(), Input: strings.TrimSpace(text), Password: password, AccountID: accountID, Cloud: cloud, CreatedAt: now(), Items: map[string]Item{}, Cursors: map[string]string{}}
	if cloud {
		a, e := p.account(accountID)
		if e != nil {
			return nil, e
		}
		allowed := false
		for _, v := range p.Platforms() {
			if v.ID == a.Platform && v.Cloud {
				allowed = true
			}
		}
		if !allowed {
			return nil, fail("unsupported", "该账号不支持个人网盘浏览")
		}
		if a.Platform == "github" {
			return nil, nil
		}
		s.Platform = a.Platform
		s.Title = a.Name
		return s, nil
	}
	var detect struct {
		Platform string `json:"platform"`
		Password string `json:"password"`
	}
	if e := p.Bridge.Call(ctx, map[string]any{"op": "detect", "text": text}, &detect); e != nil {
		return nil, e
	}
	if detect.Platform == "" || detect.Platform == "github" {
		return nil, nil
	}
	if !p.bridgePlatform(detect.Platform) {
		return nil, fail("unsupported", "尚未接入该平台")
	}
	s.Platform = detect.Platform
	if s.Password == "" {
		s.Password = detect.Password
	}
	req, e := p.bridgeRequest(s, "resolve")
	if e != nil {
		return nil, e
	}
	var result struct {
		Title     string    `json:"title"`
		Items     []Item    `json:"items"`
		Qualities []Quality `json:"qualities"`
		Quality   int       `json:"quality"`
		Notice    string    `json:"notice"`
	}
	if e = p.Bridge.Call(ctx, req, &result); e != nil {
		return nil, e
	}
	s.Title = result.Title
	s.Qualities = result.Qualities
	s.Quality = result.Quality
	s.Notice = result.Notice
	for _, item := range result.Items {
		s.Items[item.ID] = item
	}
	return s, nil
}
func (p *Providers) bridgeBrowse(ctx context.Context, s *ParseSession, parent string, page int) (BrowseResult, error) {
	out := BrowseResult{SessionID: s.ID, Platform: s.Platform, Title: s.Title, Parent: parent, Page: page, Items: []Item{}, Qualities: s.Qualities, Quality: s.Quality, Notice: s.Notice}
	if !s.Cloud && len(s.Items) > 0 {
		video := false
		for _, item := range s.Items {
			if item.Extra["media_index"] != "" {
				video = true
				break
			}
		}
		if video {
			for i := 0; i < len(s.Items); i++ {
				out.Items = append(out.Items, s.Items[fmt.Sprintf("media-%d", i)])
			}
			return out, nil
		}
	}
	if page < 1 || page > 200 {
		return out, fail("page_limit", "目录分页超出范围")
	}
	req, e := p.bridgeRequest(s, "list")
	if e != nil {
		return out, e
	}
	req["parent"] = parent
	req["page"] = page
	key := fmt.Sprintf("bridge:%s:%d", parent, page)
	req["cursor"] = s.Cursors[key]
	if page > 1 && (s.Platform == "c139" || s.Platform == "xunlei" || (s.Platform == "pan123" && s.Cloud)) && s.Cursors[key] == "" {
		return out, fail("page_cursor", "请按顺序读取目录")
	}
	var result struct {
		Items  []Item `json:"items"`
		More   bool   `json:"more"`
		Cursor string `json:"cursor"`
	}
	if e = p.Bridge.Call(ctx, req, &result); e != nil {
		return out, e
	}
	out.Items = result.Items
	out.More = result.More
	if out.Items == nil {
		return out, fail("upstream_format", "平台没有返回有效文件列表")
	}
	if s.Cursors == nil {
		s.Cursors = map[string]string{}
	}
	if result.Cursor != "" {
		if result.Cursor == s.Cursors[key] {
			return out, fail("page_cursor", "平台分页游标没有前进")
		}
		s.Cursors[fmt.Sprintf("bridge:%s:%d", parent, page+1)] = result.Cursor
	}
	for _, item := range out.Items {
		if item.ID == "" || item.Name == "" {
			return out, fail("upstream_format", "平台文件字段不完整")
		}
		if item.Extra == nil {
			item.Extra = map[string]string{}
		}
		item.Extra["parent"] = parent
		s.Items[item.ID] = item
	}
	return out, nil
}
func (p *Providers) bridgeSources(ctx context.Context, s *ParseSession, item Item) ([]MediaSource, error) {
	req, e := p.bridgeRequest(s, "sources")
	if e != nil {
		return nil, e
	}
	req["item"] = item
	var result struct {
		Sources   []MediaSource `json:"sources"`
		MotionURL string        `json:"motion_url"`
	}
	if e = p.Bridge.Call(ctx, req, &result); e != nil {
		return nil, e
	}
	if len(result.Sources) == 0 || len(result.Sources) > 2 {
		return nil, fail("upstream_format", "平台返回的媒体轨道无效")
	}
	for _, source := range result.Sources {
		if e = validURL(source.URL); e != nil {
			return nil, e
		}
	}
	if e = p.rememberCleanup(s, result.Sources); e != nil {
		return nil, e
	}
	return result.Sources, nil
}
func (p *Providers) bridgeCheck(ctx context.Context, a Account) (Account, error) {
	var result struct {
		Known    bool   `json:"known"`
		Valid    bool   `json:"valid"`
		Nickname string `json:"nickname"`
	}
	e := p.Bridge.Call(ctx, map[string]any{"op": "check", "platform": a.Platform, "credentials": a.Credentials}, &result)
	a.CheckedAt = now()
	if e != nil {
		a.Status = "unavailable"
		a.Message = "平台检查未完成，凭据已保留"
		return a, e
	}
	if !result.Known {
		a.Status = "unchecked"
		a.Message = "该平台没有独立的账号校验接口，请通过实际解析确认登录状态"
		return a, nil
	}
	a.Nickname = result.Nickname
	a.Status = "expired"
	a.Message = "平台未确认登录状态，请更新凭据"
	if result.Valid {
		a.Status = "valid"
		a.Message = "账号检查通过"
	}
	return a, nil
}
func (p *Providers) Cleanup(ctx context.Context, s *ParseSession, sources []MediaSource) error {
	if p.Bridge == nil {
		return nil
	}
	for _, source := range sources {
		if source.Cleanup == "" {
			continue
		}
		req, e := p.bridgeRequest(s, "cleanup")
		if e != nil {
			return e
		}
		req["directory"] = source.Cleanup
		var result any
		if e = p.Bridge.Call(ctx, req, &result); e != nil {
			return e
		}
		if e = p.Store.Delete("provider_cleanup", cleanupID(s, source)); e != nil {
			return e
		}
	}
	return nil
}

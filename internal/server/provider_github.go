package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

func githubURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Hostname() == "github.com" || u.Hostname() == "www.github.com")
}
func (p *Providers) githubJSON(ctx context.Context, s *ParseSession, endpoint string) (any, error) {
	c, e := p.credentials(s)
	if e != nil {
		return nil, e
	}
	h := map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}
	if c["token"] != "" {
		h["Authorization"] = "Bearer " + c["token"]
	}
	res, e := p.Net.Request(ctx, "GET", "https://api.github.com"+endpoint, h, nil)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fail("github_api", fmt.Sprintf("GitHub 返回 %d，请检查仓库权限、链接或 API 额度", res.StatusCode))
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 8<<20))
	dec.UseNumber()
	var v any
	if e = dec.Decode(&v); e != nil {
		return nil, fail("github_format", "GitHub 返回格式错误")
	}
	return v, nil
}
func (p *Providers) githubResolve(ctx context.Context, raw, accountID string) (*ParseSession, error) {
	u, _ := url.Parse(raw)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fail("github_link", "请填写 GitHub 用户、仓库或文件链接")
	}
	s := &ParseSession{ID: randomID(), Input: raw, AccountID: accountID, Platform: "github", Title: strings.Join(parts[:min(2, len(parts))], "/"), CreatedAt: now(), Items: map[string]Item{}, Cursors: map[string]string{}}
	if _, e := p.credentials(s); e != nil {
		return nil, e
	}
	if len(parts) == 1 {
		s.ShareID = parts[0]
		s.Token = "account"
		return s, nil
	}
	s.ShareID = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if len(parts) == 2 {
		s.Token = "repo"
		return s, nil
	}
	if parts[2] == "releases" {
		s.Token = "releases"
		if len(parts) > 4 && parts[3] == "tag" {
			s.Cursors["tag"] = strings.Join(parts[4:], "/")
		}
		return s, nil
	}
	if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob" || parts[2] == "raw") {
		// Longest valid ref preserves branches containing slashes.
		for i := len(parts); i >= 4; i-- {
			ref := strings.Join(parts[3:i], "/")
			v, e := p.githubJSON(ctx, s, "/repos/"+s.ShareID+"/commits/"+url.PathEscape(ref))
			if e != nil {
				continue
			}
			commit := str(obj(v)["sha"])
			if commit != "" {
				s.Token = "content"
				s.Cursors["ref"] = commit
				s.Cursors["path"] = strings.Join(parts[i:], "/")
				return s, nil
			}
		}
		return nil, fail("github_ref", "未找到该 GitHub 分支、标签或文件")
	}
	return nil, fail("github_link", "该 GitHub 链接不是可浏览的仓库、文件或 Release")
}
func (p *Providers) githubBrowse(ctx context.Context, s *ParseSession, parent string, page int) (BrowseResult, error) {
	out := BrowseResult{SessionID: s.ID, Platform: s.Platform, Title: s.Title, Items: []Item{}, Parent: parent, Page: page}
	if page < 1 || page > 200 {
		return out, fail("page_limit", "页码超出范围")
	}
	add := func(id, name string, size int64, dir bool, extra map[string]string) {
		item := Item{ID: id, Name: name, Size: size, Directory: dir, Extra: extra}
		out.Items = append(out.Items, item)
		s.Items[id] = item
	}
	mode := s.Token
	repo := s.ShareID
	ref := s.Cursors["ref"]
	filePath := s.Cursors["path"]
	if parent != "0" && parent != "" {
		item, ok := s.Items[parent]
		if !ok {
			return out, fail("directory_missing", "请从已列出的目录继续浏览")
		}
		repo = item.Extra["repo"]
		ref = item.Extra["ref"]
		filePath = item.Extra["path"]
		mode = item.Extra["mode"]
	}
	if mode == "account" {
		endpoint := fmt.Sprintf("/users/%s/repos?per_page=100&page=%d", url.PathEscape(s.ShareID), page)
		if s.Cloud {
			endpoint = fmt.Sprintf("/user/repos?per_page=100&page=%d&affiliation=owner,collaborator,organization_member", page)
		}
		v, e := p.githubJSON(ctx, s, endpoint)
		if e != nil {
			return out, e
		}
		rows := arr(v)
		out.More = len(rows) == 100
		for _, r := range rows {
			j := obj(r)
			full := str(j["full_name"])
			add("repo:"+full, str(j["name"]), 0, true, map[string]string{"repo": full, "mode": "repo"})
		}
		return out, nil
	}
	if mode == "repo" {
		v, e := p.githubJSON(ctx, s, "/repos/"+repo)
		if e != nil {
			return out, e
		}
		ref = str(obj(v)["default_branch"])
		add("tree:"+repo, "仓库文件", 0, true, map[string]string{"repo": repo, "ref": ref, "mode": "content"})
		add("releases:"+repo, "Releases", 0, true, map[string]string{"repo": repo, "mode": "releases"})
		add("archive:"+repo, path.Base(repo)+".zip", -1, false, map[string]string{"url": "https://api.github.com/repos/" + repo + "/zipball/" + url.PathEscape(ref), "repo": repo})
		return out, nil
	}
	if mode == "releases" {
		endpoint := fmt.Sprintf("/repos/%s/releases?per_page=30&page=%d", repo, page)
		if tag := s.Cursors["tag"]; tag != "" {
			endpoint = "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
		}
		v, e := p.githubJSON(ctx, s, endpoint)
		if e != nil {
			return out, e
		}
		rows := arr(v)
		if _, ok := v.(map[string]any); ok {
			rows = []any{v}
		}
		out.More = len(rows) == 30
		for _, r := range rows {
			j := obj(r)
			tag := str(j["tag_name"])
			for _, a := range arr(j["assets"]) {
				asset := obj(a)
				add("asset:"+str(asset["id"]), str(asset["name"]), num(asset["size"]), false, map[string]string{"url": str(asset["url"]), "repo": repo, "relative_path": tag})
			}
			add("release-zip:"+str(j["id"]), path.Base(repo)+"-"+strings.ReplaceAll(tag, "/", "_")+".zip", -1, false, map[string]string{"url": str(j["zipball_url"]), "repo": repo})
		}
		return out, nil
	}
	endpoint := "/repos/" + repo + "/contents/" + strings.Join(escapedSegments(filePath), "/") + "?ref=" + url.QueryEscape(ref)
	v, e := p.githubJSON(ctx, s, endpoint)
	if e != nil {
		return out, e
	}
	rows := arr(v)
	if _, ok := v.(map[string]any); ok {
		rows = []any{v}
	}
	for _, r := range rows {
		j := obj(r)
		typ := str(j["type"])
		if typ != "dir" && typ != "file" {
			continue
		}
		fp := str(j["path"])
		add("content:"+repo+":"+fp, str(j["name"]), num(j["size"]), typ == "dir", map[string]string{"repo": repo, "ref": ref, "path": fp, "mode": "content", "url": str(j["url"]), "sha": str(j["sha"])})
	}
	return out, nil
}
func escapedSegments(v string) []string {
	parts := strings.Split(v, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return parts
}
func (p *Providers) githubSources(ctx context.Context, s *ParseSession, item Item) ([]MediaSource, error) {
	c, e := p.credentials(s)
	if e != nil {
		return nil, e
	}
	raw := item.Extra["url"]
	if e = validURL(raw); e != nil {
		return nil, e
	}
	u, _ := url.Parse(raw)
	if u.Hostname() != "api.github.com" {
		return nil, fail("github_url", "GitHub 下载接口地址无效")
	}
	h := map[string]string{"User-Agent": chromeUA, "Accept": "application/vnd.github.raw+json"}
	if c["token"] != "" {
		h["Authorization"] = "Bearer " + c["token"]
	}
	if strings.HasPrefix(item.ID, "asset:") {
		h["Accept"] = "application/octet-stream"
	}
	return []MediaSource{{URL: raw, Headers: h, Size: item.Size}}, nil
}

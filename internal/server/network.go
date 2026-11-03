package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type Network struct {
	Client       *http.Client
	AllowPrivate bool
}

func NewNetwork(allowPrivate bool) *Network {
	n := &Network{AllowPrivate: allowPrivate}
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 24, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 25 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fail("network", "无法解析下载服务器域名")
		}
		for _, v := range ips {
			if !allowPrivate && !publicIP(v.IP) {
				return nil, fail("blocked_address", "目标地址属于本机或内部网络")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("DNS 未返回地址")
		}
		d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	n.Client = &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向超过 5 次")
		}
		if err := validURL(req.URL.String()); err != nil {
			return err
		}
		if len(via) > 0 && req.URL.Hostname() != via[len(via)-1].URL.Hostname() {
			req.Header.Del("Cookie")
			req.Header.Del("Authorization")
		}
		return nil
	}}
	return n
}
func publicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !net.ParseIP("100.64.0.0").Equal(ip) && !inCIDR(ip, "100.64.0.0/10") && !inCIDR(ip, "198.18.0.0/15")
}
func inCIDR(ip net.IP, cidr string) bool { _, n, _ := net.ParseCIDR(cidr); return n.Contains(ip) }
func validURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return fail("invalid_url", "请输入 http 或 https 链接，且不能包含用户名或密码")
	}
	return nil
}
func (n *Network) Request(ctx context.Context, method, raw string, headers map[string]string, body io.Reader) (*http.Response, error) {
	if e := validURL(raw); e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, method, raw, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", chromeUA)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return n.Client.Do(req)
}
func (n *Network) JSON(ctx context.Context, method, raw string, h map[string]string, body any) (map[string]any, http.Header, error) {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, nil, e
		}
		reader = strings.NewReader(string(b))
		if h == nil {
			h = map[string]string{}
		}
		h["Content-Type"] = "application/json"
	}
	res, e := n.Request(ctx, method, raw, h, reader)
	if e != nil {
		return nil, nil, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, res.Header, fail("upstream_http", fmt.Sprintf("平台接口返回 HTTP %d，请检查登录状态或稍后手动重试", res.StatusCode))
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if e != nil {
		return nil, res.Header, e
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&obj) != nil {
		return nil, res.Header, fail("upstream_format", "平台未返回有效 JSON，可能需要登录或完成平台验证")
	}
	return obj, res.Header, nil
}
func obj(v any) map[string]any {
	x, _ := v.(map[string]any)
	if x == nil {
		return map[string]any{}
	}
	return x
}
func arr(v any) []any { x, _ := v.([]any); return x }
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	case json.Number:
		return x.String()
	}
	return ""
}
func num(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := x.Int64()
		return n
	case float64:
		return int64(x)
	case string:
		var n int64
		fmt.Sscan(x, &n)
		return n
	}
	return 0
}
func boolv(v any) bool { b, ok := v.(bool); return ok && b }
func params(raw string, q map[string]string) string {
	u, _ := url.Parse(raw)
	v := u.Query()
	for k, s := range q {
		v.Set(k, s)
	}
	u.RawQuery = v.Encode()
	return u.String()
}

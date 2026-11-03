package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type providerReply struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
		Type string `json:"type"`
	} `json:"error"`
}
type ProviderProcess struct {
	cmd        *exec.Cmd
	input      io.WriteCloser
	writeMu    sync.Mutex
	mu         sync.Mutex
	pending    map[string]chan providerReply
	done       chan struct{}
	slots      chan struct{}
	closeOnce  sync.Once
	proxyClose func()
}

func StartProvider(jar, device, browser string, allowPrivate bool) (*ProviderProcess, error) {
	jar, err := filepath.Abs(jar)
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(jar); err != nil {
		return nil, errors.New("解析模块不存在，请先运行 make provider，或指定 YUNX_PROVIDER_JAR")
	}
	p := &ProviderProcess{pending: map[string]chan providerReply{}, done: make(chan struct{}), slots: make(chan struct{}, 4)}
	proxy, proxyToken, stopProxy, err := startBrowserProxy(allowPrivate)
	if err != nil {
		return nil, err
	}
	p.proxyClose = stopProxy
	p.cmd = exec.Command("java", "-Xms16m", "-Xmx192m", "-Djava.awt.headless=true", "-jar", jar)
	p.cmd.Env = append(os.Environ(), "YUNX_PROVIDER_DEVICE_ID="+device, "YUNX_ALLOW_PRIVATE_DOWNLOADS="+strconv.FormatBool(allowPrivate), "YUNX_BROWSER_SCRIPT="+browser, "YUNX_BROWSER_PROXY="+proxy, "YUNX_BROWSER_PROXY_TOKEN="+proxyToken)
	p.cmd.Stderr = io.Discard
	p.input, err = p.cmd.StdinPipe()
	if err != nil {
		p.proxyClose()
		return nil, err
	}
	output, err := p.cmd.StdoutPipe()
	if err != nil {
		p.input.Close()
		p.proxyClose()
		return nil, err
	}
	if err = p.cmd.Start(); err != nil {
		p.proxyClose()
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			var reply providerReply
			if json.Unmarshal(scanner.Bytes(), &reply) != nil {
				break
			}
			p.mu.Lock()
			ch := p.pending[reply.ID]
			delete(p.pending, reply.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- reply
			}
		}
		p.input.Close()
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
		close(p.done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var health struct {
		Protocol int `json:"protocol"`
	}
	if err = p.Call(ctx, map[string]any{"op": "health"}, &health); err != nil || health.Protocol != 1 {
		p.Close()
		return nil, errors.New("解析模块启动失败或协议版本不匹配")
	}
	return p, nil
}
func (p *ProviderProcess) Close() {
	p.closeOnce.Do(func() {
		defer p.proxyClose()
		p.input.Close()
		_ = p.cmd.Process.Kill()
		<-p.done
	})
}
func (p *ProviderProcess) Call(ctx context.Context, request map[string]any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return fail("provider_unavailable", "解析模块已退出，请重启服务")
	}
	id := randomID()
	request["id"] = id
	b, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(b) > 2*1024*1024 {
		return fail("provider_limit", "解析请求过大")
	}
	ch := make(chan providerReply, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	p.writeMu.Lock()
	_, err = p.input.Write(append(b, '\n'))
	p.writeMu.Unlock()
	if err != nil {
		return fail("provider_unavailable", "解析模块不可用，请重启服务")
	}
	select {
	case reply := <-ch:
		if reply.Error != nil {
			messages := map[string]string{"provider_timeout": "平台请求超时，请检查网络后手动重试", "blocked_address": "平台返回了受限网络地址", "video_blocked": "平台要求安全验证，请在平台完成验证后更新登录凭据", "video_not_found": "内容不存在或当前账号没有访问权限", "video_unsupported": "该链接形态或登录状态不满足平台要求"}
			message := messages[reply.Error.Code]
			if message == "" {
				message = "平台请求未完成，请检查提取码、登录凭据和文件权限；凭据已保留"
			}
			return fail(reply.Error.Code, message)
		}
		return json.Unmarshal(reply.Result, result)
	case <-ctx.Done():
		return fail("provider_timeout", "平台请求超时，可稍后手动重试")
	case <-p.done:
		return fail("provider_unavailable", "解析模块已退出，请重启服务")
	}
}

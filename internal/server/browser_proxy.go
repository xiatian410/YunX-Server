package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"time"
)

// Chromium must use the same public-IP boundary as the download service.
func startBrowserProxy(allowPrivate bool) (string, string, func(), error) {
	return startCheckedProxy(allowPrivate, 50*time.Second, true)
}
func startCheckedProxy(allowPrivate bool, lifetime time.Duration, browser bool) (string, string, func(), error) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", "", nil, e
	}
	token := randomID()
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("yunx:"+token))
	network := NewNetwork(allowPrivate)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(auth)) != 1 {
			w.Header().Set("Proxy-Authenticate", `Basic realm="yunx"`)
			w.WriteHeader(407)
			return
		}
		if r.Method == "CONNECT" {
			_, port, e := net.SplitHostPort(r.Host)
			if e != nil || (browser && port != "443" && port != "80") {
				http.Error(w, "Port blocked", 403)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			up, e := network.Client.Transport.(*http.Transport).DialContext(ctx, "tcp", r.Host)
			if e != nil {
				http.Error(w, "Destination blocked", 403)
				return
			}
			defer up.Close()
			down, buf, e := w.(http.Hijacker).Hijack()
			if e != nil {
				return
			}
			defer down.Close()
			deadline := time.Now().Add(lifetime)
			_ = up.SetDeadline(deadline)
			_ = down.SetDeadline(deadline)
			if _, e = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); e != nil {
				return
			}
			if e = buf.Flush(); e != nil {
				return
			}
			done := make(chan struct{})
			go func() {
				_, _ = io.Copy(up, buf)
				if t, ok := up.(*net.TCPConn); ok {
					_ = t.CloseWrite()
				}
				close(done)
			}()
			_, _ = io.Copy(down, up)
			_ = down.Close()
			_ = up.Close()
			<-done
			return
		}
		h := map[string]string{}
		for key, vs := range r.Header {
			if key != "Proxy-Authorization" && key != "Connection" && len(vs) > 0 {
				h[key] = vs[0]
			}
		}
		res, e := network.Request(r.Context(), r.Method, r.URL.String(), h, r.Body)
		if e != nil {
			http.Error(w, "Destination blocked", 403)
			return
		}
		defer res.Body.Close()
		for k, v := range res.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: lifetime, IdleTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), token, func() { _ = srv.Close(); network.Client.CloseIdleConnections() }, nil
}

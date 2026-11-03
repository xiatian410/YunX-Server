package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"yunx-server/internal/server"
	"yunx-server/internal/webassets"
)

var version = "0.2.0-draft"

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	addr := env("YUNX_ADDR", ":8080")
	listener, e := net.Listen("tcp", addr)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	data := env("YUNX_DATA_DIR", "data")
	root := env("YUNX_DOWNLOAD_DIR", "downloads")
	cfg := server.Config{DataDir: data, DownloadDir: root, AdminPassword: os.Getenv("YUNX_ADMIN_PASSWORD"), PublicURL: os.Getenv("YUNX_PUBLIC_URL"), AllowPrivate: os.Getenv("YUNX_ALLOW_PRIVATE_DOWNLOADS") == "true", Assets: webassets.FS(), Version: version}
	defaultJar, defaultBrowser := "provider/build/libs/yunx-provider.jar", "provider/browser.mjs"
	if exe, err := os.Executable(); err == nil {
		packaged := filepath.Join(filepath.Dir(exe), "provider", "provider.jar")
		if _, err = os.Stat(packaged); err == nil {
			defaultJar = packaged
			defaultBrowser = filepath.Join(filepath.Dir(exe), "provider", "browser.mjs")
		}
	}
	cfg.ProviderJar = env("YUNX_PROVIDER_JAR", defaultJar)
	if cfg.ProviderJar == "disabled" {
		cfg.ProviderJar = ""
	}
	cfg.BrowserScript = env("YUNX_BROWSER_SCRIPT", defaultBrowser)
	cfg.BrowserScript, _ = filepath.Abs(cfg.BrowserScript)
	if webDir := os.Getenv("YUNX_WEB_DIR"); webDir != "" {
		cfg.Assets = os.DirFS(webDir)
	}
	app, e := server.NewApp(cfg, fmt.Sprintf("http://127.0.0.1:%d", port))
	if e != nil {
		listener.Close()
		fmt.Fprintln(os.Stderr, "启动失败:", e)
		os.Exit(1)
	}
	srv := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		if e := srv.Serve(listener); e != nil && e != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, e)
		}
	}()
	fmt.Printf("YunX Server %s listening on %s\n", version, listener.Addr())
	if _, e = os.Stat(filepath.Join(data, "initial-password.txt")); e == nil {
		fmt.Printf("初始密码保存在 %s（用户名 admin）\n", filepath.Join(data, "initial-password.txt"))
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	if e = app.Manager.Close(); e != nil {
		fmt.Fprintln(os.Stderr, "保存下载检查点失败:", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	if app.Providers.Bridge != nil {
		app.Providers.Bridge.Close()
	}
	_ = app.Store.DB.Close()
}

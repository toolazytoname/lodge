// lodge-hub 是 Lodge 的中心服务：拉取各 agent、存储、给前端提供 API。
// P0：内存存储 + JSON 快照，无认证（只在 tailnet 内、绑 127.0.0.1）。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/toolazytoname/lodge/internal/hub"
)

const hubVersion = "0.1.0"

func main() {
	addr := flag.String("addr", "127.0.0.1:9102", "监听地址（只应 127.0.0.1，由 tailscale serve 前置）")
	configPath := flag.String("config", "/etc/lodge-hub/config.json", "agents 配置文件")
	statePath := flag.String("state", "/etc/lodge-hub/state.json", "注解/配置快照落盘路径")
	interval := flag.Duration("interval", 30*time.Second, "采集间隔")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	if *showVersion {
		fmt.Println("lodge-hub", hubVersion)
		return
	}

	agents, password, err := hub.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	if len(agents) == 0 {
		fmt.Fprintf(os.Stderr, "警告：配置 %s 里没有 agent，hub 将空跑\n", *configPath)
	}
	if password == "" {
		fmt.Fprintln(os.Stderr, "警告：未设 password，hub 不启用登录认证 —— 仅限 tailnet 内访问，切勿直接公网暴露")
	}

	store := hub.NewMemStore(*statePath)
	store.SetAgents(agents)

	// 采集器后台运行
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scraper := hub.NewScraper(store, *interval)
	go scraper.Run(ctx)

	srv := hub.NewServer(store, password)
	go srv.RunCleanup(ctx)
	httpSrv := &http.Server{Addr: *addr, Handler: srv}

	// 优雅关闭
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "正在关闭…")
		cancel()
		_ = httpSrv.Close()
	}()

	fmt.Fprintf(os.Stderr, "lodge-hub %s 监听 %s，管理 %d 台 agent\n", hubVersion, *addr, len(agents))
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "服务退出:", err)
		os.Exit(1)
	}
}

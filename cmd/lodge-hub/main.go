// lodge-hub is Lodge's central collector, API, and embedded Web console.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/toolazytoname/lodge/internal/hub"
)

const hubVersion = "0.1.0"

func main() {
	addr := flag.String("addr", "127.0.0.1:9102", "监听地址（只应 127.0.0.1，由 tailscale serve 前置）")
	configPath := flag.String("config", "/etc/lodge-hub/config.json", "agents 配置文件")
	statePath := flag.String("state", "/etc/lodge-hub/state.json", "注解/配置快照落盘路径")
	sessionSecretPath := flag.String("session-secret", "/etc/lodge-hub/session-secret", "独立会话签名密钥文件（自动生成，必须 0600）")
	interval := flag.Duration("interval", 30*time.Second, "采集间隔")
	hashPassword := flag.Bool("hash-password", false, "从标准输入读取密码并输出 Argon2id verifier 后退出")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	if *showVersion {
		fmt.Println("lodge-hub", hubVersion)
		return
	}
	if *hashPassword {
		if err := printPasswordHash(); err != nil {
			fmt.Fprintln(os.Stderr, "生成 passwordHash 失败:", err)
			os.Exit(1)
		}
		return
	}

	config, err := hub.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	if len(config.Agents) == 0 {
		fmt.Fprintf(os.Stderr, "警告：配置 %s 里没有 agent，hub 将空跑\n", *configPath)
	}
	passwordHash, legacyPassword, err := hub.ResolvePasswordHash(config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载认证配置失败:", err)
		os.Exit(1)
	}
	if legacyPassword {
		fmt.Fprintln(os.Stderr, "警告：配置仍含明文 password；本次仅在内存中转换，请尽快迁移为 passwordHash")
	}
	if passwordHash == "" {
		fmt.Fprintln(os.Stderr, "警告：未设 passwordHash，hub 不启用登录认证 —— 仅限受严格 grants 保护的 tailnet")
	}

	store := hub.NewMemStore(*statePath)
	store.SetAgents(config.Agents)

	// 采集器后台运行
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scraper := hub.NewScraper(store, *interval)
	go scraper.Run(ctx)

	var sessionKey []byte
	if passwordHash != "" {
		sessionKey, err = hub.LoadOrCreateSessionKey(*sessionSecretPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "加载会话密钥失败:", err)
			os.Exit(1)
		}
	}
	srv, err := hub.NewServerWithAuth(store, passwordHash, sessionKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化认证失败:", err)
		os.Exit(1)
	}
	go srv.RunCleanup(ctx)
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// 优雅关闭
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "正在关闭…")
		cancel()
		_ = httpSrv.Close()
	}()

	fmt.Fprintf(os.Stderr, "lodge-hub %s 监听 %s，管理 %d 台 agent\n", hubVersion, *addr, len(config.Agents))
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "服务退出:", err)
		os.Exit(1)
	}
}

func printPasswordHash() error {
	info, err := os.Stdin.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("为避免密码回显，请通过标准输入传入（示例见 deploy/hub-config.example.json）")
	}
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 2048))
	password, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	password = strings.TrimRight(password, "\r\n")
	if len(password) < 12 {
		return fmt.Errorf("新密码至少 12 个字符")
	}
	hash, err := hub.HashPassword(password)
	if err != nil {
		return err
	}
	fmt.Println(hash)
	return nil
}

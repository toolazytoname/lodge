// lodge-hub is Lodge's central collector, API, and embedded Web console.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/toolazytoname/lodge/internal/hub"
)

const hubVersion = "0.10.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lodge-hub:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:9102", "监听地址（只应 127.0.0.1，由 tailscale serve 前置）")
	configPath := flag.String("config", "/etc/lodge-hub/config.json", "agents 私有配置文件")
	databasePath := flag.String("database", "/var/lib/lodge-hub/lodge.db", "SQLite 数据库路径（必须 0600）")
	legacyStatePath := flag.String("state", "/etc/lodge-hub/state.json", "旧 JSON 状态，仅用于一次性导入注解；迁移确认后删除")
	sessionSecretPath := flag.String("session-secret", "/etc/lodge-hub/session-secret", "独立会话签名密钥文件（自动生成，必须 0600）")
	interval := flag.Duration("interval", 30*time.Second, "采集间隔")
	historyRetention := flag.Duration("history-retention", 30*24*time.Hour, "观测历史保留期；0 表示不自动裁剪")
	backupDestination := flag.String("backup", "", "创建一致性 SQLite 备份到新文件并退出")
	migrateConfigPassword := flag.Bool("migrate-config-password", false, "将 config 中旧明文 password 原子迁移为 Argon2id 后退出")
	upsertAgent := flag.String("upsert-agent", "", "从标准输入读取 token，原子添加或更新指定 Agent 后退出")
	agentName := flag.String("agent-name", "", "与 --upsert-agent 配合使用的展示名称；默认等于 ID")
	agentURL := flag.String("agent-url", "", "与 --upsert-agent 配合使用的 tailnet Agent base URL")
	agentPublicHost := flag.String("agent-public-host", "", "与 --upsert-agent 配合使用的无端口公网域名或 IP（可选）")
	hashPassword := flag.Bool("hash-password", false, "从标准输入读取密码并输出 Argon2id verifier 后退出")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	if *upsertAgent == "" && (*agentName != "" || *agentURL != "" || *agentPublicHost != "") {
		return errors.New("agent-name、agent-url 和 agent-public-host 必须与 --upsert-agent 一起使用")
	}
	specialModes := 0
	for _, selected := range []bool{*showVersion, *hashPassword, *migrateConfigPassword, *upsertAgent != "", *backupDestination != ""} {
		if selected {
			specialModes++
		}
	}
	if specialModes > 1 {
		return errors.New("version、hash-password、migrate-config-password、upsert-agent 和 backup 只能选择一个")
	}

	if *showVersion {
		fmt.Println("lodge-hub", hubVersion)
		return nil
	}
	if *hashPassword {
		return printPasswordHash()
	}
	if *migrateConfigPassword {
		migrated, err := hub.MigrateConfigPassword(*configPath)
		if err != nil {
			return fmt.Errorf("迁移配置密码: %w", err)
		}
		if migrated {
			fmt.Println("Hub 配置密码已原子迁移为 Argon2id verifier")
		} else {
			fmt.Println("Hub 配置无需明文密码迁移")
		}
		return nil
	}
	if *upsertAgent != "" {
		token, err := readAgentToken(os.Stdin)
		if err != nil {
			return fmt.Errorf("读取 Agent token: %w", err)
		}
		changed, err := hub.UpsertAgentConfig(*configPath, hub.AgentConfig{
			ID:         *upsertAgent,
			Name:       *agentName,
			URL:        *agentURL,
			Token:      token,
			PublicHost: *agentPublicHost,
		})
		if err != nil {
			return fmt.Errorf("更新 Agent 配置: %w", err)
		}
		if changed {
			fmt.Printf("Agent %s 已原子写入 Hub 配置\n", *upsertAgent)
		} else {
			fmt.Printf("Agent %s 的 Hub 配置无需更新\n", *upsertAgent)
		}
		return nil
	}
	if *backupDestination != "" {
		if err := hub.BackupSQLiteDatabase(context.Background(), *databasePath, *backupDestination); err != nil {
			return fmt.Errorf("备份数据库: %w", err)
		}
		fmt.Printf("SQLite 备份已通过完整性校验：%s\n", *backupDestination)
		return nil
	}
	if *interval <= 0 {
		return errors.New("interval 必须大于 0")
	}
	if *historyRetention < 0 {
		return errors.New("history-retention 不能为负数")
	}

	config, err := hub.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	if len(config.Agents) == 0 {
		fmt.Fprintf(os.Stderr, "警告：配置 %s 里没有 agent，hub 将空跑\n", *configPath)
	}
	passwordHash, legacyPassword, err := hub.ResolvePasswordHash(config)
	if err != nil {
		return fmt.Errorf("加载认证配置: %w", err)
	}
	if legacyPassword {
		fmt.Fprintln(os.Stderr, "警告：配置仍含明文 password；本次仅在内存中转换，请尽快迁移为 passwordHash")
	}
	if passwordHash == "" {
		fmt.Fprintln(os.Stderr, "警告：未设 passwordHash，hub 不启用登录认证 —— 仅限受严格 grants 保护的 tailnet")
	}
	var webhookSecret string
	var notificationPolicies []hub.NotificationPolicy
	if config.Webhook != nil {
		webhookSecret, err = hub.LoadWebhookSecret(config.Webhook.SecretFile)
		if err != nil {
			return fmt.Errorf("加载 Webhook 密钥: %w", err)
		}
		notificationPolicies = append(notificationPolicies, hub.NotificationPolicy{
			Channel: "webhook", Cooldown: config.Webhook.Cooldown(),
		})
	}
	var sessionKey []byte
	if passwordHash != "" {
		sessionKey, err = hub.LoadOrCreateSessionKey(*sessionSecretPath)
		if err != nil {
			return fmt.Errorf("加载会话密钥: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := hub.OpenSQLiteStore(ctx, *databasePath, config.Agents, notificationPolicies...)
	if err != nil {
		return fmt.Errorf("打开持久化存储: %w", err)
	}
	defer store.Close()
	legacyImport, err := store.ImportLegacyState(ctx, *legacyStatePath)
	if err != nil {
		return fmt.Errorf("迁移旧状态: %w", err)
	}
	if legacyImport.Found {
		if legacyImport.Performed {
			fmt.Fprintf(os.Stderr, "已从旧状态导入 %d 条注解，跳过 %d 条未知主机注解；旧 Agent 连接记录未入库\n",
				legacyImport.ImportedAnnotations, legacyImport.SkippedUnknownHosts)
		}
		fmt.Fprintf(os.Stderr, "警告：旧状态 %s 可能仍含 Agent token；验证注解后请安全删除该文件\n", *legacyStatePath)
	}

	srv, err := hub.NewServerWithAuth(store, passwordHash, sessionKey)
	if err != nil {
		return fmt.Errorf("初始化认证: %w", err)
	}
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	var notifier *hub.WebhookNotifier
	if config.Webhook != nil {
		notifier, err = hub.NewWebhookNotifier(store, config.Webhook.URL, webhookSecret)
		if err != nil {
			return fmt.Errorf("初始化 Webhook 通知: %w", err)
		}
	}

	var background sync.WaitGroup
	startBackground := func(run func()) {
		background.Add(1)
		go func() {
			defer background.Done()
			run()
		}()
	}
	scraper := hub.NewScraper(store, *interval)
	startBackground(func() { scraper.Run(ctx) })
	startBackground(func() { srv.RunCleanup(ctx) })
	startBackground(func() { store.RunObservationRetention(ctx, *historyRetention) })
	if notifier != nil {
		startBackground(func() { notifier.Run(ctx) })
	}
	startBackground(func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			fmt.Fprintln(os.Stderr, "HTTP 优雅关闭失败:", err)
		}
	})

	webhookState := "关闭"
	if config.Webhook != nil {
		webhookState = "开启"
	}
	fmt.Fprintf(os.Stderr, "lodge-hub %s 监听 %s，管理 %d 台 agent，历史保留 %s，Webhook %s\n",
		hubVersion, *addr, len(config.Agents), *historyRetention, webhookState)
	serveErr := httpServer.ListenAndServe()
	stop()
	background.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务退出: %w", serveErr)
	}
	return nil
}

func readAgentToken(input *os.File) (string, error) {
	info, err := input.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", errors.New("为避免 token 回显，请通过标准输入重定向 owner-only token 文件")
	}
	const maxTokenBytes = 4096
	contents, err := io.ReadAll(io.LimitReader(input, maxTokenBytes+3))
	if err != nil {
		return "", err
	}
	contents = bytes.TrimSuffix(contents, []byte{'\n'})
	contents = bytes.TrimSuffix(contents, []byte{'\r'})
	if len(contents) == 0 || len(contents) > maxTokenBytes {
		return "", fmt.Errorf("token 必须为 1..%d 字节", maxTokenBytes)
	}
	return string(contents), nil
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

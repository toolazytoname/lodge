// lodge-agent 是 Lodge 的受管端：以非 root 的 lodge 账号运行，
// 采集本机状态与服务清单，经 sudo 白名单执行有限动作。
// 只监听 127.0.0.1，由 tailscale serve 套 TLS 暴露给 hub。
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/toolazytoname/lodge/internal/agent"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9101", "监听地址（只应 127.0.0.1，由 tailscale serve 前置 TLS）")
	tokenFile := flag.String("token-file", "/etc/lodge-agent/token", "token 文件路径")
	check := flag.Bool("check", false, "采集一次状态与服务清单并打印 JSON，用于验证（不启动服务）")
	printSudoers := flag.Bool("print-sudoers", false, "生成本机 sudoers 内容并退出（供 install-agent.sh 写入）")
	collectProcessOrigins := flag.Bool("collect-process-origins", false, "以 JSONL 输出脱敏进程来源并退出（仅供精确 sudoers 调用）")
	collectComposeMetadata := flag.Bool("collect-compose-metadata", false, "以 JSONL 输出校验后的 Compose 身份并退出（仅供精确 sudoers 调用）")
	collectProxyRoutes := flag.Bool("collect-proxy-routes", false, "以 JSONL 输出脱敏的 Caddy/Nginx 路由并退出（仅供精确 sudoers 调用）")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println("lodge-agent", agent.AgentVersion)
		return
	case *printSudoers:
		out, err := agent.PrintSudoers()
		if err != nil {
			fmt.Fprintln(os.Stderr, "生成 sudoers 失败:", err)
			os.Exit(1)
		}
		fmt.Print(out)
		return
	case *collectProcessOrigins:
		if err := agent.WriteProcessOrigins(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "采集进程来源失败:", err)
			os.Exit(1)
		}
		return
	case *collectComposeMetadata:
		if err := agent.WriteComposeMetadata(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "采集 Compose 身份失败:", err)
			os.Exit(1)
		}
		return
	case *collectProxyRoutes:
		if err := agent.WriteProxyRoutes(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "采集反向代理路由失败:", err)
			os.Exit(1)
		}
		return
	case *check:
		runCheck()
		return
	}

	token, err := agent.LoadToken(*tokenFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载 token 失败:", err)
		os.Exit(1)
	}

	srv := agent.NewServer(token)
	fmt.Fprintf(os.Stderr, "lodge-agent %s 监听 %s\n", agent.AgentVersion, *addr)
	httpSrv := &http.Server{Addr: *addr, Handler: srv}
	if err := httpSrv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "服务退出:", err)
		os.Exit(1)
	}
}

// runCheck 一次性采集，把 status 与 services 打印出来。
// 验证步骤 1：sudo -u lodge /usr/local/bin/lodge-agent --check
func runCheck() {
	fmt.Println("=== status ===")
	agent.PrintJSON(agent.CollectStatus())
	fmt.Println()
	fmt.Println("=== services ===")
	agent.PrintJSON(agent.Discover())
}

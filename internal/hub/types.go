// Package hub 是 lodge 的中心服务：定时拉取各 agent，存储观测结果，
// 给前端提供 API。P0 用内存存储 + JSON 快照；数据层抽象成 Store 接口，
// P3 加历史/告警时换 SQLite 不用改上层。
package hub

import (
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/toolazytoname/lodge/internal/shared"
)

// AgentConfig 描述一台被管的 agent：怎么连、用什么 token。
// 来源于 hub 的配置文件（/etc/lodge-hub/config.json）。
type AgentConfig struct {
	ID         string `json:"id"`         // 稳定标识，跨重启不变
	Name       string `json:"name"`       // 展示名
	URL        string `json:"url"`        // agent 的 base URL，如 http://127.0.0.1:9101
	Token      string `json:"token"`      // 与 agent /etc/lodge-agent/token 一致
	PublicHost string `json:"publicHost"` // 公网访问该机器服务用的主机（域名/IP），用于点服务直达的 URL 猜测
}

// AgentSnapshot 是某台 agent 最近一次采集的结果。是前端「按机器分组」视图的数据源。
type AgentSnapshot struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Online    bool             `json:"online"`
	LastSeen  string           `json:"lastSeen,omitempty"`  // RFC3339，从未连上则为空
	LastError string           `json:"lastError,omitempty"` // 失败原因，前端据此提示
	AgentVer  string           `json:"agentVersion,omitempty"`
	Status    *shared.Status   `json:"status,omitempty"`
	Services  []shared.Service `json:"services,omitempty"`
}

// Annotation 是用户对某个服务的注解，长期保存，按 Service.Key 关联。
//
// 设计核心（plan 的支点）：agent 每轮刷新 observed_service（真相），
// 用户只维护「它叫什么、重不重要」，二者 join 成 UI。用户永远不必手填「有什么」。
type Annotation struct {
	Key     string `json:"key"`     // = Service.Key，如 docker:nginx
	AgentID string `json:"agentId"` // 注解属于哪台机器（同 key 可能多机都有）
	Alias   string `json:"alias,omitempty"`
	URL     string `json:"url,omitempty"` // 点服务直达的链接（用户填的真实域名/URL）
	Hidden  bool   `json:"hidden,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// ServiceView 是 observed ⨝ annotation 的结果，直接喂给前端。
type ServiceView struct {
	shared.Service
	Routes []RouteView `json:"routes,omitempty"`
	Alias  string      `json:"alias,omitempty"`
	URL    string      `json:"url,omitempty"` // 解析后的访问链接：优先注解，否则按端口猜测
	Hidden bool        `json:"hidden"`
	Notes  string      `json:"notes,omitempty"`
}

type RouteView struct {
	shared.ProxyRoute
	URL string `json:"url,omitempty"`
}

// JoinServices 把某台 agent 的观测结果与注解合并成视图，并解析点击直达的 URL。
func JoinServices(services []shared.Service, ann map[string]Annotation, publicHost string) []ServiceView {
	out := make([]ServiceView, 0, len(services))
	for _, s := range services {
		a := ann[s.Key]
		routes := make([]RouteView, 0, len(s.Routes))
		for _, route := range s.Routes {
			routes = append(routes, RouteView{ProxyRoute: route, URL: proxyRouteURL(route, publicHost)})
		}
		sort.SliceStable(routes, func(left, right int) bool {
			leftRank, rightRank := proxyRouteRank(routes[left]), proxyRouteRank(routes[right])
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			return routes[left].URL < routes[right].URL
		})
		url := a.URL
		if url == "" {
			for _, route := range routes {
				if route.URL != "" {
					url = route.URL
					break
				}
			}
		}
		if url == "" {
			url = guessURL(s, publicHost)
		}
		out = append(out, ServiceView{Service: s, Routes: routes, Alias: a.Alias, URL: url, Hidden: a.Hidden, Notes: a.Notes})
	}
	return out
}

func proxyRouteRank(route RouteView) int {
	rank := 0
	if route.Host == "" {
		rank += 4
	}
	if route.Scheme != "https" {
		rank += 2
	}
	if route.Path != "/" {
		rank++
	}
	return rank
}

func proxyRouteURL(route shared.ProxyRoute, publicHost string) string {
	host := route.Host
	if host == "" {
		host = publicHost
	}
	if host == "" || strings.Contains(host, "*") || (route.Scheme != "http" && route.Scheme != "https") || route.Port < 1 || route.Port > 65535 {
		return ""
	}
	authority := host
	if (route.Scheme == "http" && route.Port != 80) || (route.Scheme == "https" && route.Port != 443) {
		authority = net.JoinHostPort(host, strconv.Itoa(route.Port))
	} else if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	path := route.Path
	if wildcard := strings.IndexAny(path, "*{"); wildcard >= 0 {
		path = path[:wildcard]
	}
	if path == "" {
		path = "/"
	}
	return (&url.URL{Scheme: route.Scheme, Host: authority, Path: path}).String()
}

// guessURL 按服务的端口猜测一个访问链接。只在注解没填 URL 时兜底。
//
// 原则：只在「正经 web 端口」上猜，优先 80/443，其次常见 web 应用端口。
// 对 ssh(22)、随机高端口、admin 端口等一律不猜 —— 瞎猜一个 :22/:9090 的链接
// 比没有链接更糟。拿不准就返回空，让用户自己标真实 URL。
var webPortScheme = map[int]string{
	80: "http", 443: "https",
	3000: "http", 5000: "http", 8000: "http", 8080: "http", 8888: "http",
	8443: "https", 9443: "https",
}

func guessURL(s shared.Service, publicHost string) string {
	if publicHost == "" {
		return ""
	}
	// 第一优先：80/443（canonical web）
	for _, p := range s.Ports {
		if p.Port == 80 {
			return "http://" + publicHost
		}
		if p.Port == 443 {
			return "https://" + publicHost
		}
	}
	// 第二优先：常见 web 应用端口（取最小的）
	best := 0
	for _, p := range s.Ports {
		if _, ok := webPortScheme[p.Port]; ok && (best == 0 || p.Port < best) {
			best = p.Port
		}
	}
	if best != 0 {
		return webPortScheme[best] + "://" + publicHost + ":" + strconv.Itoa(best)
	}
	return ""
}

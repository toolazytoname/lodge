// Package shared 定义 agent 与 hub 之间的线上契约。
//
// 这些类型是 API 的唯一真相来源：agent 序列化它们，hub 反序列化它们，
// 前端按同样的字段名渲染。改字段必须同时改三处。
package shared

// APIVersion 是 agent HTTP 路径中的版本段（/v1/...）。
// 不兼容变更时递增，hub 据此拒绝过旧的 agent。
const APIVersion = "v1"

// Exposure 是端口的暴露级别 —— Lodge 的差异化维度。
//
// 「有哪些服务」监控工具都能给，「这些服务暴露到哪里」才是运维真正缺的信息。
type Exposure string

const (
	// ExposureLocal 仅本机可达（127.0.0.1 / ::1）。
	ExposureLocal Exposure = "local"
	// ExposureTailnet 仅 tailnet 内可达（100.64.0.0/10 / fd7a:115c:a1e0::/48）。
	ExposureTailnet Exposure = "tailnet"
	// ExposurePublic is a legacy wire value for a wildcard bind
	// (0.0.0.0 / :: / *). It does not prove internet reachability; the Hub
	// projects it to domain.BindingWildcard and requires separate evidence.
	ExposurePublic Exposure = "public"
	// ExposureOther 绑定某个具体的非回环、非 tailnet 地址，需人工判断。
	ExposureOther Exposure = "other"
)

// Kind 是服务的运行方式。三态，宁可标 KindProcess 也不猜错归属。
type Kind string

const (
	// KindDocker 归属某个 docker 容器（/proc/<pid>/cgroup 含 /docker/<id>）。
	KindDocker Kind = "docker"
	// KindSystemd 归属某个 systemd 单元（cgroup 含 /system.slice/<unit>.service）。
	KindSystemd Kind = "systemd"
	// KindProcess 裸进程，无法归到容器或单元。
	KindProcess Kind = "process"
)

// Ping 是 GET /v1/ping 的响应：最轻量的存活与版本探测。
type Ping struct {
	OK         bool   `json:"ok"`
	Hostname   string `json:"hostname"`
	AgentVer   string `json:"agentVersion"`
	APIVersion string `json:"apiVersion"`
	// UptimeSec 是主机（非 agent 进程）的运行秒数。
	UptimeSec int64 `json:"uptimeSec"`
}

// Status 是 GET /v1/status 的响应：一台机器的系统快照。
type Status struct {
	Hostname    string         `json:"hostname"`
	Kernel      string         `json:"kernel"`
	OS          string         `json:"os"`
	UptimeSec   int64          `json:"uptimeSec"`
	CollectedAt string         `json:"collectedAt"` // RFC3339
	Load        Load           `json:"load"`
	Memory      Memory         `json:"memory"`
	Disks       []Disk         `json:"disks"`
	Docker      *DockerSummary `json:"docker,omitempty"` // nil 表示本机没有 docker 或采集失败
	// Warnings 记录部分采集失败的原因。单项失败不应让整个 status 失败 ——
	// 半份数据远比一个 500 有用。
	Warnings []string `json:"warnings,omitempty"`
}

// Load 是 /proc/loadavg 的三个平均负载。
type Load struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
	// CPUs 用于判断负载是否真的高（load 4 在 8 核上很闲）。
	CPUs int `json:"cpus"`
}

// Memory 单位统一为字节。
type Memory struct {
	TotalBytes     int64 `json:"totalBytes"`
	AvailableBytes int64 `json:"availableBytes"`
	// UsedBytes = Total - Available（不是 Total - Free）。
	// MemAvailable 才是内核对「还能给应用多少」的判断，Free 会把 cache 算成已用。
	UsedBytes      int64 `json:"usedBytes"`
	SwapTotalBytes int64 `json:"swapTotalBytes"`
	SwapUsedBytes  int64 `json:"swapUsedBytes"`
}

// Disk 是一个挂载点的容量。只上报真实存储，跳过 tmpfs/overlay 等伪文件系统。
type Disk struct {
	Mount      string `json:"mount"`
	Filesystem string `json:"filesystem"`
	TotalBytes int64  `json:"totalBytes"`
	FreeBytes  int64  `json:"freeBytes"`
	UsedBytes  int64  `json:"usedBytes"`
}

// DockerSummary 对应 docker system df，用于回答「能回收多少空间」。
type DockerSummary struct {
	Containers        int `json:"containers"`
	ContainersRunning int `json:"containersRunning"`
	Images            int `json:"images"`
	Volumes           int `json:"volumes"`
	// ReclaimableBytes 是 prune 能释放的空间，直接驱动「清理」动作的价值展示。
	ReclaimableBytes int64 `json:"reclaimableBytes"`
	TotalBytes       int64 `json:"totalBytes"`
}

// Port 是一个监听中的套接字。
type Port struct {
	Proto string `json:"proto"` // tcp / tcp6 / udp / udp6
	Port  int    `json:"port"`
	// Bind 是原始绑定地址，保留原文便于排查（0.0.0.0 / :: / 127.0.0.1 / 100.x）。
	Bind     string   `json:"bind"`
	Exposure Exposure `json:"exposure"`
}

// Service 是发现出来的一个服务 —— GET /v1/services 的元素。
//
// Key 是跨轮次的稳定标识，hub 用它把「本轮观测」与「用户注解」关联起来：
//
//	docker:<容器名>    systemd:<单元名>    port:<proto>/<端口>
//
// 容器重建、进程重启后 Key 不变，用户起的别名就不会丢。
type Service struct {
	Key    string `json:"key"`
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"` // running / exited / active / failed ...
	// Since 是启动时间（RFC3339），可能为空 —— 不是所有来源都给得出。
	Since string `json:"since,omitempty"`
	Image string `json:"image,omitempty"` // 仅 KindDocker
	Unit  string `json:"unit,omitempty"`  // 仅 KindSystemd
	// Health 是 docker 的健康检查结果，容器没配置健康检查时为空。
	Health string `json:"health,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Ports  []Port `json:"ports,omitempty"`
	// Unidentified 标记「监听了端口但归不到任何容器或单元」的进程。
	// 这类恰恰是最值得看的 —— 本次会话就是这样发现 bytebunny 上
	// 有个身份不明的 python3 在 0.0.0.0:8765 上听着。
	Unidentified bool `json:"unidentified,omitempty"`
	// MaxExposure 是本服务所有端口中最开放的那一级，UI 直接拿它排序。
	MaxExposure Exposure `json:"maxExposure"`
}

// ServicesResponse 是 GET /v1/services 的响应。
type ServicesResponse struct {
	Hostname    string    `json:"hostname"`
	CollectedAt string    `json:"collectedAt"` // RFC3339
	Services    []Service `json:"services"`
	// Warnings 同 Status.Warnings：譬如没权限跑 ss，则端口维度缺失，
	// 但 docker/systemd 两路结果仍然有效，要说清楚缺了什么。
	Warnings []string `json:"warnings,omitempty"`
}

// Error 是所有非 2xx 响应的统一格式。
type Error struct {
	Error string `json:"error"`
}

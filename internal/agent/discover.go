package agent

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

// 与 commands.go 里 privilegedRead 逐字对应的命令。单独成变量，Discover 复用。
var (
	dockerPS = []string{"docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}"}
	ssCmd    = []string{"ss", "-tlnpH"}
)

// runPriv 与 cgroupFor 是 Discover 的两个外部依赖，抽成变量便于注入测试。
// 生产环境分别指向 runPrivileged（经 sudo）与 readCgroup（读 /proc）。
var (
	runPriv   func(argv []string) (stdout, stderr []byte, err error) = runPrivileged
	cgroupFor func(pid int) cgroupOwner                              = readCgroup
)

// socketInfo 是 ss 解析出的一条监听套接字。
type socketInfo struct {
	proto string // tcp / udp（family 从 bind 推断，proto 不带 6）
	bind  string
	port  int
	pid   int
	proc  string // 进程名（取不到为空）
}

// cgroupOwner 是 /proc/<pid>/cgroup 的归属判定结果。
type cgroupOwner struct {
	kind shared.Kind
	id   string // KindDocker: 容器完整 ID
	unit string // KindSystemd: 单元名（含 .service）
}

var (
	pidRe         = regexp.MustCompile(`pid=(\d+)`)
	procRe        = regexp.MustCompile(`\(\("([^"]+)"`)
	dockerPathRe  = regexp.MustCompile(`/docker/([0-9a-f]{12,64})(?:/|$)`)
	dockerScopeRe = regexp.MustCompile(`(?:^|/)docker-([0-9a-f]{12,64})\.scope(?:/|$)`)
)

// parseSS 解析 ss -tlnpH 的输出，返回所有监听套接字。
// 纯函数，可测。
//
// ⚠ ss 的列布局不固定：Netid（tcp/udp）列有时出现、有时不出现（取决于版本与
// 筛选条件 —— 实测 Ubuntu 24.04 的 ss 在 -t 下省略 Netid 列，行以 "LISTEN" 开头）。
// 因此不能用固定下标取 Local Address。可靠做法：找 "users:(" 进程列（它恒在
// Local、Peer 之后），Local = 进程列前两列；无进程列时 Local = 倒数第二列。
func parseSS(content string) []socketInfo {
	var out []socketInfo
	for _, line := range strings.Split(content, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		usersIdx := -1
		for i, fld := range f {
			if strings.HasPrefix(fld, "users:(") {
				usersIdx = i
				break
			}
		}
		localIdx := -1
		if usersIdx >= 0 {
			localIdx = usersIdx - 2 // 顺序为 Local、Peer、users
		} else {
			localIdx = len(f) - 2 // 无进程列时：Local、Peer 在末尾两列
		}
		if localIdx < 0 {
			continue
		}
		bind, port, ok := shared.SplitHostPort(f[localIdx])
		if !ok {
			continue
		}
		// proto：f[0] 是 netid 就用（去掉尾部 6），否则默认 tcp（我们只查 -t）。
		proto := "tcp"
		if isNetid(f[0]) {
			proto = strings.TrimSuffix(f[0], "6")
		}
		var pid int
		var proc string
		if usersIdx >= 0 {
			procCol := strings.Join(f[usersIdx:], " ")
			if m := pidRe.FindStringSubmatch(procCol); m != nil {
				pid, _ = strconv.Atoi(m[1])
			}
			if m := procRe.FindStringSubmatch(procCol); m != nil {
				proc = m[1]
			}
		}
		out = append(out, socketInfo{proto: proto, bind: bind, port: port, pid: pid, proc: proc})
	}
	return out
}

// isNetid 判断一个字段是否是 ss 的 Netid 列（tcp/udp/...），用来区分列布局。
func isNetid(s string) bool {
	switch s {
	case "tcp", "tcp6", "udp", "udp6", "raw", "raw6", "sctp", "sctp6":
		return true
	}
	return false
}

// rawContainer 是 docker ps --format '{{json .}}' 单行的原始结构。
// docker 把 Names/Ports 序列化成字符串（逗号分隔），故用 string。
type rawContainer struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Ports  string `json:"Ports"`
}

// parseDockerPS 解析 docker ps 的 JSON 行。纯函数，可测。
func parseDockerPS(content string) []rawContainer {
	var out []rawContainer
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var r rawContainer
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.ID == "" {
			continue
		}
		// Names 可能多个（容器别名），取第一个主名。
		if i := strings.IndexByte(r.Names, ','); i >= 0 {
			r.Names = r.Names[:i]
		}
		r.Names = strings.TrimPrefix(r.Names, "/") // docker 有时给 /name
		out = append(out, r)
	}
	return out
}

// parseDockerPorts 解析容器的 .Ports 字段，如
// "0.0.0.0:443->443/tcp, 127.0.0.1:8080->80/tcp, :::443->443/tcp"。
//
// 只取宿主侧的绑定地址与端口（那才是暴露面）；容器内部端口不影响宿主暴露。
// 纯函数，可测。
func parseDockerPorts(s string) []shared.Port {
	var ports []shared.Port
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// entry 形如 "0.0.0.0:443->443/tcp" 或 ":::443->443/tcp"
		arrow := strings.Index(entry, "->")
		hostPart := entry
		if arrow >= 0 {
			hostPart = entry[:arrow]
		}
		// proto 默认 tcp；容器侧 "->443/tcp" 里有就取
		proto := "tcp"
		if arrow >= 0 {
			if slash := strings.IndexByte(entry[arrow:], '/'); slash >= 0 {
				proto = strings.TrimSpace(entry[arrow+slash+1:])
			}
		}
		bind, port, ok := shared.SplitHostPort(hostPart)
		if !ok {
			continue // 形如 "443/tcp"（仅 EXPOSE 未发布），无宿主绑定，不构成暴露
		}
		ports = append(ports, shared.Port{
			Proto: proto, Port: port, Bind: bind, Exposure: shared.ClassifyBind(bind),
		})
	}
	return ports
}

// parseCgroup 解析 /proc/<pid>/cgroup，判定进程归属。
//
// 同时兼容 cgroup v2（"0::/path"）与 v1（"hier:subsys:/path"）。
// 纯函数，可测。
func parseCgroup(content string) cgroupOwner {
	for _, line := range strings.Split(content, "\n") {
		// 取路径部分：最后一个冒号之后。v2 "0::/x" 和 v1 "1:name=systemd:/x" 都成立。
		path := line
		if i := strings.LastIndex(line, ":"); i >= 0 {
			path = line[i+1:]
		}
		if match := dockerPathRe.FindStringSubmatch(path); match != nil {
			return cgroupOwner{kind: shared.KindDocker, id: match[1]}
		}
		if match := dockerScopeRe.FindStringSubmatch(path); match != nil {
			return cgroupOwner{kind: shared.KindDocker, id: match[1]}
		}
		if strings.Contains(path, ".service") {
			for _, seg := range strings.Split(path, "/") {
				if strings.HasSuffix(seg, ".service") {
					return cgroupOwner{kind: shared.KindSystemd, unit: seg}
				}
			}
		}
	}
	return cgroupOwner{kind: shared.KindProcess}
}

// readCgroup 读单个 pid 的 cgroup。读不到（进程已退出/无权限）→ 视为裸进程。
func readCgroup(pid int) cgroupOwner {
	if pid == 0 {
		return cgroupOwner{kind: shared.KindProcess}
	}
	content, err := readFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return cgroupOwner{kind: shared.KindProcess}
	}
	return parseCgroup(content)
}

// unitName 把 "caddy.service" 渲染成展示名 "caddy"。
func unitName(unit string) string {
	return strings.TrimSuffix(unit, ".service")
}

// Discover 执行服务发现，返回这台机器上在跑什么、各暴露到哪。
//
// 多路采集：
//  1. docker 容器（全部，含已停止）—— 端口取自 .Ports 的宿主侧映射；
//  2. Docker Compose 官方 project/service 标签，以及自定义/failed systemd unit；
//  3. 监听套接字（ss）—— 经 /proc/<pid>/cgroup 归属到 docker / systemd / 裸进程；
//  4. 归属后去重：容器已声明的宿主端口不再重复计入（docker-proxy 的套接字
//     常归到 systemd:docker.service，靠端口集合去重，而非靠 cgroup 判断）。
func Discover() shared.ServicesResponse {
	var warns []string
	hostname, _, _, _ := hostInfo()
	now := time.Now().UTC().Format(time.RFC3339)

	services := []*shared.Service{}
	containerPorts := map[string]bool{} // "tcp/443"，容器已声明的宿主端口
	containersByID := map[string]*shared.Service{}

	// 1. docker 容器
	if stdout, stderr, err := runPriv(dockerPS); err == nil {
		for _, c := range parseDockerPS(string(stdout)) {
			ports := parseDockerPorts(c.Ports)
			svc := &shared.Service{
				Key:    "docker:" + c.Names,
				Kind:   shared.KindDocker,
				Name:   c.Names,
				Status: stateOrStatus(c.State, c.Status),
				Image:  c.Image,
				Ports:  ports,
			}
			containersByID[c.ID] = svc
			svc.MaxExposure = shared.MaxExposure(ports)
			for _, p := range ports {
				containerPorts[p.Proto+"/"+strconv.Itoa(p.Port)] = true
			}
			services = append(services, svc)
		}
	} else if !isDockerMissing(string(stderr)) {
		warns = append(warns, "docker ps 失败: "+firstLine(string(stderr)))
	}
	if stdout, stderr, err := runPriv(dockerComposePSCommand); err == nil {
		for containerID, metadata := range parseComposeMetadata(stdout) {
			if svc := findContainerByID(containersByID, containerID); svc != nil {
				svc.ComposeProject = metadata.Project
				svc.ComposeService = metadata.Service
			}
		}
	} else if !isDockerMissing(string(stderr)) {
		warns = append(warns, "Docker Compose 标签采集失败: "+firstLine(string(stderr)))
	}
	processOrigins := map[int]processOrigin{}
	if stdout, stderr, err := runPriv(processOriginsCommand); err == nil {
		processOrigins = parseProcessOrigins(stdout)
	} else {
		warns = append(warns, "进程来源采集失败: "+firstLine(string(stderr)))
	}
	systemdUnits := map[string]systemdUnitMetadata{}
	if stdout, stderr, err := runPriv(systemdUnitsCommand); err == nil {
		for _, unit := range parseSystemdUnits(stdout) {
			systemdUnits[unit.ID] = unit
			if !unit.relevant() {
				continue
			}
			services = append(services, &shared.Service{
				Key: "systemd:" + unit.ID, Kind: shared.KindSystemd,
				Unit: unit.ID, Name: unitName(unit.ID), Status: unit.status(),
			})
		}
	} else {
		warns = append(warns, "systemd unit 采集失败: "+firstLine(string(stderr)))
	}

	// 2. 监听套接字
	seenProcPort := map[string]bool{} // 裸进程端口去重
	processServices := map[string]*shared.Service{}
	if stdout, stderr, err := runPriv(ssCmd); err == nil {
		for _, s := range parseSS(string(stdout)) {
			portKey := s.proto + "/" + strconv.Itoa(s.port)

			// 已被某容器声明（含 docker-proxy 套接字）→ 跳过，避免重复
			if containerPorts[portKey] {
				continue
			}

			owner := cgroupFor(s.pid)
			exposure := shared.ClassifyBind(s.bind)
			port := shared.Port{Proto: s.proto, Port: s.port, Bind: s.bind, Exposure: exposure}

			switch owner.kind {
			case shared.KindDocker:
				// bridge 容器的内部监听不会出现在宿主 ss；能看到且未被 .Ports
				// 声明的通常是 host-network 容器。归回容器，不能误报成裸进程。
				if svc := findContainerByID(containersByID, owner.id); svc != nil && !hasPort(svc.Ports, port) {
					svc.Ports = append(svc.Ports, port)
				}
			case shared.KindSystemd:
				svc := findService(services, "systemd:"+owner.unit)
				if svc == nil {
					status := "running"
					if unit, found := systemdUnits[owner.unit]; found {
						status = unit.status()
					}
					svc = &shared.Service{
						Key: "systemd:" + owner.unit, Kind: shared.KindSystemd,
						Unit: owner.unit, Name: unitName(owner.unit), Status: status,
					}
					services = append(services, svc)
				}
				svc.Ports = append(svc.Ports, port)
			default:
				// 裸进程：监听了端口却归不到容器或单元 —— 最值得看的对象。
				if origin, identified := processOrigins[s.pid]; identified {
					key := origin.workloadKey()
					svc := processServices[key]
					if svc == nil {
						svc = &shared.Service{
							Key: key, Kind: shared.KindProcess, Name: origin.workloadName(s.proc),
							PID: s.pid, Status: "running",
						}
						processServices[key] = svc
						services = append(services, svc)
					} else if svc.PID != s.pid {
						svc.PID = 0
					}
					if !hasPort(svc.Ports, port) {
						svc.Ports = append(svc.Ports, port)
					}
					continue
				}
				if seenProcPort[portKey] {
					continue
				}
				seenProcPort[portKey] = true
				// Unidentified 标记「非受管的 docker/systemd 服务」的裸进程，
				// 而非「拿不到名字」。python3 在 0.0.0.0:8765 上听着、名字已知，
				// 仍属此类 —— 正是它当初把用户吓到的。名字拿不到时退回 "unknown"。
				name := s.proc
				if name == "" {
					name = "unknown"
				}
				services = append(services, &shared.Service{
					Key:          "port:" + portKey,
					Kind:         shared.KindProcess,
					Name:         name,
					PID:          s.pid,
					Status:       "running",
					Ports:        []shared.Port{port},
					Unidentified: true,
				})
			}
		}
	} else {
		// ss 失败通常因 sudoers 没配好 —— 此时端口维度整体缺失，但容器维度仍有效。
		warns = append(warns, "ss 采集失败（端口维度将缺失）: "+firstLine(string(stderr)))
	}

	// 3. 重算 MaxExposure（host-network 端口可能在 docker ps 之后补入）
	out := make([]shared.Service, 0, len(services))
	for _, svc := range services {
		svc.MaxExposure = shared.MaxExposure(svc.Ports)
		out = append(out, *svc)
	}

	return shared.ServicesResponse{
		Hostname:    hostname,
		CollectedAt: now,
		Services:    out,
		Warnings:    warns,
	}
}

func findContainerByID(containers map[string]*shared.Service, ownerID string) *shared.Service {
	if service := containers[ownerID]; service != nil {
		return service
	}
	for containerID, service := range containers {
		if strings.HasPrefix(containerID, ownerID) || strings.HasPrefix(ownerID, containerID) {
			return service
		}
	}
	return nil
}

func hasPort(ports []shared.Port, candidate shared.Port) bool {
	for _, port := range ports {
		if port.Proto == candidate.Proto && port.Port == candidate.Port && port.Bind == candidate.Bind {
			return true
		}
	}
	return false
}

func findService(services []*shared.Service, key string) *shared.Service {
	for _, s := range services {
		if s.Key == key {
			return s
		}
	}
	return nil
}

// stateOrStatus 优先用 State（running/exited），缺失则退回 Status（"Up 2 hours"）。
func stateOrStatus(state, status string) string {
	if state != "" {
		return state
	}
	return status
}

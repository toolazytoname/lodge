package agent

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

// parseMeminfo 解析 /proc/meminfo，返回字节单位。
//
// 关键：用 MemAvailable 而非 MemFree 算「已用」。MemAvailable 是内核综合
// （含可回收的 cache/buffer）后对「还能给应用多少」的判断；MemFree 会把
// cache 误算成已用，在跑 docker 的机器上能差出好几个 GB，让用户以为内存吃紧。
func parseMeminfo(content string) shared.Memory {
	var total, avail, free, swapTotal, swapFree int64
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		parts := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		val, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		// /proc/meminfo 的单位是 kB（小写 k，即 1024 字节）。
		if len(parts) >= 3 && parts[2] == "kB" {
			val *= 1024
		}
		switch key {
		case "MemTotal":
			total = val
		case "MemAvailable":
			avail = val
		case "MemFree":
			free = val
		case "SwapTotal":
			swapTotal = val
		case "SwapFree":
			swapFree = val
		}
	}

	// MemAvailable 在 3.14 之前的内核不存在，退回 MemFree（不准，但聊胜于无）。
	if avail == 0 {
		avail = free
	}

	used := total - avail
	if used < 0 {
		used = 0
	}
	swapUsed := swapTotal - swapFree
	if swapUsed < 0 {
		swapUsed = 0
	}
	return shared.Memory{
		TotalBytes:     total,
		AvailableBytes: avail,
		UsedBytes:      used,
		SwapTotalBytes: swapTotal,
		SwapUsedBytes:  swapUsed,
	}
}

// parseLoadavg 解析 /proc/loadavg："0.52 0.43 0.59 2/312 12345"。
func parseLoadavg(content string) shared.Load {
	fields := strings.Fields(strings.TrimSpace(content))
	l := shared.Load{CPUs: runtime.NumCPU()}
	if len(fields) >= 1 {
		l.One, _ = strconv.ParseFloat(fields[0], 64)
	}
	if len(fields) >= 2 {
		l.Five, _ = strconv.ParseFloat(fields[1], 64)
	}
	if len(fields) >= 3 {
		l.Fifteen, _ = strconv.ParseFloat(fields[2], 64)
	}
	return l
}

// parseUptime 解析 /proc/uptime 的第一个字段（秒）。
func parseUptime(content string) int64 {
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) == 0 {
		return 0
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return int64(f)
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// hostInfo 收集主机静态信息。
func hostInfo() (hostname, kernel, osRelease string, uptimeSec int64) {
	hostname, _ = os.Hostname()
	kernel, _ = readFile("/proc/sys/kernel/osrelease")
	kernel = strings.TrimSpace(kernel)
	osRelease = readOSRelease()
	uptimeSec = readUptime()
	return
}

func readOSRelease() string {
	content, err := readFile("/etc/os-release")
	if err != nil {
		return ""
	}
	// 取 PRETTY_NAME，例如 "Ubuntu 22.04.4 LTS"。
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return ""
}

func readUptime() int64 {
	if content, err := readFile("/proc/uptime"); err == nil {
		return parseUptime(content)
	}
	if bt := bootTime(); bt > 0 {
		return time.Now().Unix() - bt
	}
	return 0
}

// bootTime 从 /proc/stat 的 btime 行读启动时间戳，作为 /proc/uptime 的兜底。
func bootTime() int64 {
	content, err := readFile("/proc/stat")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "btime ") {
			if v, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64); err == nil {
				return v
			}
		}
	}
	return 0
}

// statfsFn 让磁盘逻辑的 statfs 依赖可注入，便于在任意平台测试。
// 生产实现 realStatfs 在 disk_linux.go（真 statfs）/ disk_other.go（stub）。
type statfsFn func(mount string) (totalBytes, freeBytes int64, err error)

// CollectStatus 组装一份系统快照。
//
// 设计：单项采集失败只记进 Warnings，不让整体失败 —— 半份数据（有内存、
// 没 docker）远比一个 500 有用。docker 失败通常因为没装 docker 或 sudoers
// 没配好，前者正常后者需运维介入，都值得明说而非静默。
func CollectStatus() shared.Status {
	hostname, kernel, osRel, uptime := hostInfo()
	s := shared.Status{
		Hostname:    hostname,
		Kernel:      kernel,
		OS:          osRel,
		UptimeSec:   uptime,
		CollectedAt: time.Now().UTC().Format(time.RFC3339),
		// CPU 数不依赖 /proc，永远应上报，故先置位，loadavg 读取成功时会被覆盖。
		Load: shared.Load{CPUs: runtime.NumCPU()},
	}

	if content, err := readFile("/proc/meminfo"); err == nil {
		s.Memory = parseMeminfo(content)
	} else {
		s.Warnings = append(s.Warnings, "读取 /proc/meminfo 失败: "+err.Error())
	}

	if content, err := readFile("/proc/loadavg"); err == nil {
		s.Load = parseLoadavg(content)
	} else {
		s.Warnings = append(s.Warnings, "读取 /proc/loadavg 失败: "+err.Error())
	}

	disks, derr := listDisks(realStatfs)
	if derr != nil {
		s.Warnings = append(s.Warnings, "采集磁盘失败: "+derr.Error())
	}
	s.Disks = disks

	dsum, dwarn := collectDockerSummary()
	if dsum != nil {
		s.Docker = dsum
	}
	if dwarn != "" {
		s.Warnings = append(s.Warnings, dwarn)
	}

	ssh, sshWarning := collectSSHAuthSummary()
	if ssh != nil {
		s.SSH = ssh
	}
	if sshWarning != "" {
		s.Warnings = append(s.Warnings, sshWarning)
	}

	security, securityWarning := collectSecurityPosture()
	if security != nil {
		s.Security = security
	}
	if securityWarning != "" {
		s.Warnings = append(s.Warnings, securityWarning)
	}

	return s
}

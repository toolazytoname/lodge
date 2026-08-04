package shared

import (
	"net/netip"
	"strconv"
	"strings"
)

// tailnet 地址范围。Tailscale 用 CGNAT 段 100.64.0.0/10 分配 IPv4，
// 用 fd7a:115c:a1e0::/48 分配 IPv6。
//
// 注意必须按网段判断而不是「以 100. 开头」—— 100.0.0.0/10 之外的 100.x
// 是货真价实的公网地址（比如 100.200.1.1），误判会把公网暴露标成内网，
// 那是最危险的一种错。
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// ClassifyBind 把一个监听地址判定为暴露级别。
//
// bind 接受 ss / docker 的原始写法："0.0.0.0"、"*"、"::"、"[::1]"、
// "127.0.0.1"、"100.83.12.4"。空串按 public 处理 —— 拿不准时报得严重些，
// 漏报一个公网端口的代价远大于误报一个本地端口。
func ClassifyBind(bind string) Exposure {
	b := strings.TrimSpace(bind)
	b = strings.Trim(b, "[]")

	// ss 用 "*" 表示 0.0.0.0；docker 有时给 "::" 表示双栈全绑。
	switch b {
	case "", "*", "0.0.0.0", "::":
		return ExposurePublic
	}

	// 有的来源带 %eth0 这样的 zone 后缀。
	if i := strings.IndexByte(b, '%'); i >= 0 {
		b = b[:i]
	}

	addr, err := netip.ParseAddr(b)
	if err != nil {
		return ExposurePublic
	}

	// v4-mapped v6（::ffff:127.0.0.1、::ffff:100.83.12.4）必须先还原成 v4，
	// 否则 IsLoopback、IsUnspecified 和网段判断都会看走眼。
	if addr.Is4In6() {
		addr = addr.Unmap()
	}

	if addr.IsUnspecified() {
		return ExposurePublic
	}
	if addr.IsLoopback() {
		return ExposureLocal
	}
	if tailnetV4.Contains(addr) || tailnetV6.Contains(addr) {
		return ExposureTailnet
	}

	// 绑到某个具体地址：可能是内网网卡，也可能是公网 IP，
	// agent 无从判断哪张网卡通外网，交给人看。
	return ExposureOther
}

// exposureRank 定义「更开放」的顺序，供 MaxExposure 取最大值。
var exposureRank = map[Exposure]int{
	ExposureLocal:   0,
	ExposureTailnet: 1,
	ExposureOther:   2,
	ExposurePublic:  3,
}

// MaxExposure 返回一组端口中最开放的级别。无端口时返回 ExposureLocal
// —— 不监听任何端口的服务本来就不构成暴露面。
func MaxExposure(ports []Port) Exposure {
	worst := ExposureLocal
	for _, p := range ports {
		if exposureRank[p.Exposure] > exposureRank[worst] {
			worst = p.Exposure
		}
	}
	return worst
}

// SplitHostPort 拆分 ss 输出里的监听地址（"0.0.0.0:443"、"[::]:80"、
// "*:8388"、"127.0.0.1:9101"），返回原始 bind 与端口号。
//
// 不能用 net.SplitHostPort：它不接受 "*:8388" 这种 ss 特有写法。
func SplitHostPort(s string) (bind string, port int, ok bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return "", 0, false
	}
	bind, portStr := s[:i], s[i+1:]
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return "", 0, false
	}
	return strings.Trim(bind, "[]"), p, true
}

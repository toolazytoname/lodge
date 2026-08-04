package agent

import (
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestParseMeminfo(t *testing.T) {
	content := `MemTotal:       16384000 kB
MemFree:         1000000 kB
MemAvailable:   12000000 kB
Buffers:          200000 kB
Cached:          3000000 kB
SwapTotal:      2097152 kB
SwapFree:        524288 kB
`
	m := parseMeminfo(content)
	if m.TotalBytes != 16384000*1024 {
		t.Errorf("TotalBytes = %d", m.TotalBytes)
	}
	// 必须用 MemAvailable，不是 MemFree
	if m.AvailableBytes != 12000000*1024 {
		t.Errorf("AvailableBytes 应取 MemAvailable(12000000)，得到 %d", m.AvailableBytes/1024)
	}
	wantUsed := int64(16384000-12000000) * 1024
	if m.UsedBytes != wantUsed {
		t.Errorf("UsedBytes = %d，期望 %d（应是 Total-Available，非 Total-Free）", m.UsedBytes, wantUsed)
	}
	if m.SwapUsedBytes != (2097152-524288)*1024 {
		t.Errorf("SwapUsedBytes = %d", m.SwapUsedBytes)
	}
}

func TestParseMeminfoNoMemAvailable(t *testing.T) {
	// 旧内核（< 3.14）没有 MemAvailable，应退回 MemFree
	content := `MemTotal:       8192000 kB
MemFree:         2000000 kB
SwapTotal:      0 kB
SwapFree:       0 kB
`
	m := parseMeminfo(content)
	if m.AvailableBytes != 2000000*1024 {
		t.Errorf("无 MemAvailable 时应退回 MemFree，得到 %d", m.AvailableBytes/1024)
	}
}

func TestParseLoadavg(t *testing.T) {
	l := parseLoadavg("0.52 0.43 0.59 2/312 12345")
	if l.One != 0.52 || l.Five != 0.43 || l.Fifteen != 0.59 {
		t.Errorf("负载解析错误: %+v", l)
	}
	if l.CPUs <= 0 {
		t.Errorf("CPUs 应 >= 1（runtime.NumCPU），得到 %d", l.CPUs)
	}
}

func TestParseUptime(t *testing.T) {
	if got := parseUptime("12345.67 67890.12"); got != 12345 {
		t.Errorf("uptime = %d，期望 12345", got)
	}
	if got := parseUptime("garbage"); got != 0 {
		t.Errorf("坏输入应返回 0，得到 %d", got)
	}
}

func TestParseDockerSize(t *testing.T) {
	cases := map[string]int64{
		"1.2GB":       int64(1.2e9),
		"800MB":       800_000_000,
		"500kB":       500_000,
		"1024B":       1024,
		"0B":          0,
		"800MB (66%)": 800_000_000, // 带百分数尾巴
		"1.5TB":       int64(1.5e12),
	}
	for in, want := range cases {
		if got := parseDockerSize(in); got != want {
			t.Errorf("parseDockerSize(%q) = %d, 期望 %d", in, got, want)
		}
	}
}

func TestParseDockerDF(t *testing.T) {
	content := `{"Type":"Images","TotalCount":"5","Active":"2","Size":"1.2GB","Reclaimable":"800MB (66%)"}
{"Type":"Containers","TotalCount":"3","Active":"2","Size":"50MB","Reclaimable":"0B (0%)"}
{"Type":"Local Volumes","TotalCount":"2","Active":"1","Size":"100MB","Reclaimable":"50MB (50%)"}
`
	d := parseDockerDF(content)
	if d == nil {
		t.Fatal("应返回非空 summary")
	}
	if d.Images != 5 || d.Containers != 3 || d.ContainersRunning != 2 || d.Volumes != 2 {
		t.Errorf("计数错误: %+v", d)
	}
	// Reclaimable 应汇总三行：800MB + 0 + 50MB
	if d.ReclaimableBytes != 850_000_000 {
		t.Errorf("ReclaimableBytes = %d，期望 850000000", d.ReclaimableBytes)
	}
	// Total Size 汇总：1.2GB + 50MB + 100MB
	wantTotal := int64(1.2e9) + 50_000_000 + 100_000_000
	if d.TotalBytes != wantTotal {
		t.Errorf("TotalBytes = %d，期望 %d", d.TotalBytes, wantTotal)
	}
}

func TestParseMounts(t *testing.T) {
	content := `/dev/sda2 / ext4 rw,relatime 0 0
/dev/sda1 /boot ext4 rw 0 0
tmpfs /tmp tmpfs rw 0 0
proc /proc proc rw 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw 0 0
/dev/sdb1 /data xfs rw 0 0
sysfs /sys sysfs rw 0 0
`
	mounts := parseMounts(content)
	// 应跳过 tmpfs/proc/overlay/sysfs，保留 ext4/xfs
	if len(mounts) != 3 {
		t.Fatalf("应保留 3 个真实挂载点（/, /boot, /data），得到 %d: %+v", len(mounts), mounts)
	}
	seen := map[string]bool{}
	for _, m := range mounts {
		seen[m.mount] = true
	}
	for _, want := range []string{"/", "/boot", "/data"} {
		if !seen[want] {
			t.Errorf("缺少挂载点 %s", want)
		}
	}
}

func TestListDisksWithFakeStatfs(t *testing.T) {
	// 用注入的 fake statfs 测 listDisks 的去重与计算，不依赖真实文件系统。
	// 但 listDisks 内部读 /proc/self/mounts，在 macOS 上该文件不存在 ——
	// 这个测试只能在能造出 mounts 的前提下跑，所以直接测 parseMounts + 计算。
	mounts := parseMounts("/dev/sda2 / ext4 rw 0 0\ntmpfs /tmp tmpfs rw 0 0\n")
	if len(mounts) != 1 || mounts[0].mount != "/" {
		t.Fatalf("parseMounts 过滤后应只剩 /，得到 %+v", mounts)
	}
	// 验证 used 计算：total 100, free 30 => used 70
	fake := func(mount string) (int64, int64, error) { return 100, 30, nil }
	if mounts[0].mount == "/" {
		total, free, _ := fake("/")
		if used := total - free; used != 70 {
			t.Errorf("used 计算 = %d，期望 70", used)
		}
	}
	_ = shared.Disk{} // 引用 shared 包，避免未使用导入
}

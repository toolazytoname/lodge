package agent

import (
	"os"
	"strings"

	"github.com/toolazytoname/lodge/internal/shared"
)

// pseudoFilesystems 是永远不该当作「真实存储盘」上报的文件系统类型。
// 它们要么是内存盘（tmpfs）、要么是内核虚拟视图（proc/sysfs/cgroup），
// 或容器叠层（overlay）。把它们当作磁盘只会制造噪音。
var pseudoFilesystems = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "tmpfs2": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"devpts": true, "mqueue": true, "hugetlbfs": true,
	"overlay": true, "aufs": true, "squashfs": true,
	"fusectl": true, "fuse.gvfsd-fuse": true, "fuse.snapfuse": true,
	"binfmt_misc": true, "securityfs": true, "tracefs": true,
	"debugfs": true, "configfs": true, "pstore": true,
	"bpf": true, "ramfs": true, "autofs": true, "rpc_pipefs": true,
	"efivarfs": true, "nsfs": true, "mtdblockfs": true,
}

// mountEntry 是 /proc/self/mounts 的一行。
type mountEntry struct {
	device string
	mount  string
	fstype string
}

// parseMounts 解析 /proc/self/mounts，返回真实文件系统的挂载点。
//
// 过滤规则：跳过伪文件系统；跳过 device 为 "none" 或以 /dev/loop 开头的（
// snap loop 设备）；跳过挂载点含 /snap / /docker 等容器内部挂载。目的是只
// 报「运维真正关心的盘」：根分区、数据盘、可能还有 /home、/var。
func parseMounts(content string) []mountEntry {
	var out []mountEntry
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		dev, mount, fstype := fields[0], fields[1], fields[2]
		if pseudoFilesystems[fstype] {
			continue
		}
		if dev == "none" {
			continue
		}
		out = append(out, mountEntry{device: dev, mount: mount, fstype: fstype})
	}
	return out
}

// listDisks 列出各挂载点的容量。
//
// stat 由注入的 statfsFn 提供（生产 realStatfs / 测试 fake）。同一挂载点
// 出现多次时只取第一个（bind mount 会重复）。
func listDisks(stat statfsFn) ([]shared.Disk, error) {
	content, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var disks []shared.Disk
	for _, m := range parseMounts(string(content)) {
		if seen[m.mount] {
			continue
		}
		total, free, serr := stat(m.mount)
		if serr != nil || total <= 0 {
			continue // 某些挂载点 statfs 会失败（已卸载、无权限），跳过而非整体失败
		}
		seen[m.mount] = true
		used := total - free
		if used < 0 {
			used = 0
		}
		disks = append(disks, shared.Disk{
			Mount:      m.mount,
			Filesystem: m.fstype,
			TotalBytes: total,
			FreeBytes:  free,
			UsedBytes:  used,
		})
	}
	return disks, nil
}

//go:build linux

package agent

import "syscall"

// linuxStatfs 用 statfs(2) 读取一个挂载点的总容量与空闲字节数。
func linuxStatfs(mount string) (totalBytes, freeBytes int64, err error) {
	var s syscall.Statfs_t
	if err = syscall.Statfs(mount, &s); err != nil {
		return 0, 0, err
	}
	// Bsize 是块大小；Blocks 是总块数；Bfree 是空闲块（含 root reserve）。
	totalBytes = int64(s.Blocks) * int64(s.Bsize)
	freeBytes = int64(s.Bfree) * int64(s.Bsize)
	return totalBytes, freeBytes, nil
}

var realStatfs statfsFn = linuxStatfs

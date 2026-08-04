//go:build !linux

// 非 Linux 平台的 stub。agent 生产环境只在 Linux 运行；这份文件存在的
// 唯一目的是让 macOS 上 `go build ./...` 和 `go test ./...` 能编译通过，
// 方便开发。realStatfs 在这里返回错误，listDisks 会据此跳过磁盘维度。
package agent

import "errors"

func stubStatfs(mount string) (int64, int64, error) {
	return 0, 0, errors.New("disk 采集仅在 Linux 可用")
}

var realStatfs statfsFn = stubStatfs

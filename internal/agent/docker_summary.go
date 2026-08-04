package agent

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/toolazytoname/lodge/internal/shared"
)

// rawDF 是 docker system df --format '{{json .}}' 单行的原始结构。
// docker 把数值都序列化成「带单位的字符串」（如 "1.2GB"、"800MB (66%)"），
// 所以这里全用 string，再由 parseDockerSize 还原成字节。
type rawDF struct {
	Type        string `json:"Type"`
	TotalCount  string `json:"TotalCount"`
	Active      string `json:"Active"`
	Size        string `json:"Size"`
	Reclaimable string `json:"Reclaimable"`
}

// parseDockerDF 解析 docker system df 的全部 JSON 行，汇总成 DockerSummary。
// 纯函数，可测。
func parseDockerDF(content string) *shared.DockerSummary {
	var d shared.DockerSummary
	seen := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var r rawDF
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		seen = true
		count, _ := strconv.Atoi(r.TotalCount)
		switch r.Type {
		case "Images":
			d.Images = count
		case "Containers":
			d.Containers = count
			d.ContainersRunning, _ = strconv.Atoi(r.Active)
		case "Local Volumes":
			d.Volumes = count
		}
		d.TotalBytes += parseDockerSize(r.Size)
		d.ReclaimableBytes += parseDockerSize(stripPct(r.Reclaimable))
	}
	if !seen {
		return nil
	}
	return &d
}

// stripPct 去掉 Reclaimable 字段的百分数尾巴："800MB (66%)" -> "800MB"。
func stripPct(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

// parseDockerSize 把 docker 的带单位字符串还原成字节数。
//
// docker 用 go-units.HumanSize 渲染，按 SI 十进制（1000 进制）：
//
//	"1.2GB" = 1.2 × 1000³。
//
// 因此这里按 1000 换算。结果是约值 —— 对「能回收多少」的判断足够，
// 不需要字节级精确（精确值得跑 docker system df -v 逐项解析）。
func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	s = stripPct(s)
	if s == "" || s == "0B" {
		return 0
	}
	// 找到第一个非数字字符，分割数值与单位。
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return 0
	}
	num, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	unit := strings.TrimSpace(s[i:])
	mult := map[string]float64{
		"B":  1,
		"kB": 1e3, "KB": 1e3,
		"MB": 1e6,
		"GB": 1e9,
		"TB": 1e12,
		"PB": 1e15,
	}
	if m, ok := mult[unit]; ok {
		return int64(num * m)
	}
	return int64(num)
}

// collectDockerSummary 执行 docker system df。
//
// 三种结果：
//  1. 没装 docker → 返回 (nil, "")：正常状态，不报 warning，summary 字段 omitempty；
//  2. 装了但失败（sudoers 没配好、daemon 没起）→ 返回 (nil, "原因")：进 Warnings；
//  3. 成功 → 返回 summary。
func collectDockerSummary() (*shared.DockerSummary, string) {
	stdout, stderr, err := runPrivileged([]string{"docker", "system", "df", "--format", "{{json .}}"})
	if err != nil {
		// sudo 找不到 docker = 本机没装 docker，这是正常状态，静默。
		if isDockerMissing(string(stderr)) {
			return nil, ""
		}
		return nil, "docker system df 失败: " + firstLine(string(stderr))
	}
	return parseDockerDF(string(stdout)), ""
}

// isDockerMissing 判断失败是不是因为本机根本没装 docker。
func isDockerMissing(stderr string) bool {
	low := strings.ToLower(stderr)
	return strings.Contains(low, "command not found") ||
		strings.Contains(low, "no such file or directory") ||
		strings.Contains(low, "not found")
}

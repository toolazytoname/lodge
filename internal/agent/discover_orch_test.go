package agent

import (
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

// TestDiscoverCorrelation 验证编排核心：docker 端口去重、systemd 归属、
// 裸进程标记、暴露分级聚合。用注入的假命令运行器与假 cgroup 隔离真实环境。
func TestDiscoverCorrelation(t *testing.T) {
	// 假 docker ps：nginx 发布了 443
	dockerOut := `{"ID":"aaa","Names":"nginx","Image":"nginx:alpine","State":"running","Status":"Up","Ports":"0.0.0.0:443->443/tcp"}
`
	// 假 ss：caddy:443(已被容器占,应去重)、caddy:9443(systemd)、python3:8765(裸进程public)
	ssOut := `tcp   LISTEN 0      128    0.0.0.0:443         0.0.0.0:*           users:(("docker-proxy",pid=100,fd=7))
tcp   LISTEN 0      128    0.0.0.0:9443        0.0.0.0:*           users:(("caddy",pid=200,fd=7))
tcp   LISTEN 0      128    0.0.0.0:8765        0.0.0.0:*           users:(("python3",pid=300,fd=3))
tcp   LISTEN 0      128    127.0.0.1:9101      0.0.0.0:*           users:(("lodge-agent",pid=400,fd=3))
`
	origRun := runPriv
	origCg := cgroupFor
	t.Cleanup(func() {
		runPriv = origRun
		cgroupFor = origCg
	})
	runPriv = func(argv []string) ([]byte, []byte, error) {
		switch argv[0] {
		case "docker":
			return []byte(dockerOut), nil, nil
		case "ss":
			return []byte(ssOut), nil, nil
		}
		return nil, nil, nil
	}
	// pid 200 → caddy.service（systemd）；其余 → 裸进程（含 docker-proxy pid 100）
	cgroupFor = func(pid int) cgroupOwner {
		if pid == 200 {
			return cgroupOwner{kind: shared.KindSystemd, unit: "caddy.service"}
		}
		return cgroupOwner{kind: shared.KindProcess}
	}

	resp := Discover()
	byKey := map[string]shared.Service{}
	for _, s := range resp.Services {
		byKey[s.Key] = s
	}

	// nginx 容器：443 public
	ng, ok := byKey["docker:nginx"]
	if !ok {
		t.Fatal("应发现 docker:nginx")
	}
	if ng.MaxExposure != shared.ExposurePublic {
		t.Errorf("nginx 应为 public，得到 %v", ng.MaxExposure)
	}

	// caddy systemd：9443 public，且 443 不应重复计入（docker-proxy 去重）
	cd, ok := byKey["systemd:caddy.service"]
	if !ok {
		t.Fatal("应发现 systemd:caddy.service")
	}
	if len(cd.Ports) != 1 || cd.Ports[0].Port != 9443 {
		t.Errorf("caddy 应只有 9443（443 被 docker-proxy 去重），得到 %+v", cd.Ports)
	}
	if cd.Name != "caddy" {
		t.Errorf("caddy 展示名应为 caddy，得到 %q", cd.Name)
	}

	// python3 裸进程：8765 public，unidentified
	py, ok := byKey["port:tcp/8765"]
	if !ok {
		t.Fatal("应发现 port:tcp/8765")
	}
	if py.MaxExposure != shared.ExposurePublic {
		t.Errorf("python3:8765 应为 public，得到 %v", py.MaxExposure)
	}
	if !py.Unidentified {
		t.Error("python3 裸进程应标记 unidentified")
	}

	// lodge-agent：9101 local。它是裸进程（归不到容器/单元），故 unidentified=true；
	// 但因为只听本地，MaxExposure=local，严重度低 —— UI 据此淡化而非据名字。
	la, ok := byKey["port:tcp/9101"]
	if !ok {
		t.Fatal("应发现 port:tcp/9101")
	}
	if la.MaxExposure != shared.ExposureLocal {
		t.Errorf("9101 应为 local，得到 %v", la.MaxExposure)
	}
	if !la.Unidentified {
		t.Error("lodge-agent 是裸进程，应标记 unidentified（名字已知无妨）")
	}

	// docker-proxy 的 443 不应作为裸进程重复出现
	if _, dup := byKey["port:tcp/443"]; dup {
		t.Error("443 已被 nginx 容器声明，不应再出现裸进程 port:tcp/443")
	}
}

// TestDiscoverNoDocker 验证没装 docker 时仍能从 ss 发现裸进程，且不报错。
func TestDiscoverNoDocker(t *testing.T) {
	origRun := runPriv
	origCg := cgroupFor
	t.Cleanup(func() {
		runPriv = origRun
		cgroupFor = origCg
	})
	runPriv = func(argv []string) ([]byte, []byte, error) {
		if argv[0] == "docker" {
			// sudo 找不到 docker
			return nil, []byte("sudo: docker: command not found"), errFake
		}
		return []byte("tcp   LISTEN 0 128 *:8388 0.0.0.0:* users:((\"mihomo\",pid=500,fd=3))\n"), nil, nil
	}
	cgroupFor = func(pid int) cgroupOwner { return cgroupOwner{kind: shared.KindProcess} }

	resp := Discover()
	if len(resp.Warnings) != 0 {
		t.Errorf("没装 docker 不应产生 warning，得到 %v", resp.Warnings)
	}
	mi := resp.Services[0]
	if mi.Name != "mihomo" || mi.MaxExposure != shared.ExposurePublic {
		t.Errorf("应发现 mihomo public，得到 %+v", mi)
	}
}

// errFake 给 TestDiscoverNoDocker 用，模拟命令失败。
var errFake = fakeErr{}

type fakeErr struct{}

func (fakeErr) Error() string { return "exit status 1" }

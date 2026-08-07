package agent

import (
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestParseSS(t *testing.T) {
	content := `tcp   LISTEN 0      4096  127.0.0.1:9101      0.0.0.0:*           users:(("lodge-agent",pid=1234,fd=3))
tcp   LISTEN 0      128    0.0.0.0:443         0.0.0.0:*           users:(("caddy",pid=5678,fd=7))
tcp   LISTEN 0      128    *:8765              0.0.0.0:*           users:(("python3",pid=9012,fd=3))
tcp   LISTEN 0      128    [::]:443            [::]:*              users:(("caddy",pid=5678,fd=9))
`
	socks := parseSS(content)
	if len(socks) != 4 {
		t.Fatalf("应解析出 4 个套接字，得到 %d", len(socks))
	}
	// 第一个：127.0.0.1:9101, pid 1234, lodge-agent
	if socks[0].bind != "127.0.0.1" || socks[0].port != 9101 || socks[0].pid != 1234 || socks[0].proc != "lodge-agent" {
		t.Errorf("第一个套接字解析错误: %+v", socks[0])
	}
	// 第三个：*:8765 bind 应为 "*"，pid 9012
	if socks[2].bind != "*" || socks[2].port != 8765 || socks[2].pid != 9012 {
		t.Errorf("*:8765 解析错误: %+v", socks[2])
	}
	// 第四个：[::]:443 → bind "::"
	if socks[3].bind != "::" || socks[3].port != 443 {
		t.Errorf("[::]:443 解析错误: %+v", socks[3])
	}
}

// TestParseSSNoNetidColumn 覆盖真实环境布局：Ubuntu 24.04 的 ss 在 -t 下
// 省略 Netid 列，行以 "LISTEN" 开头。这是在 bytedragon 实测踩到的坑。
func TestParseSSNoNetidColumn(t *testing.T) {
	content := `LISTEN 0      511                      127.0.0.1:18792 0.0.0.0:* users:(("clawdbot-gatewa",pid=978,fd=31))
LISTEN 0      4096                     127.0.0.1:9101 0.0.0.0:* users:(("lodge-agent",pid=3428657,fd=6))
LISTEN 0      4096                 100.105.249.48:50149 0.0.0.0:* users:(("tailscaled",pid=1815838,fd=11))
`
	socks := parseSS(content)
	if len(socks) != 3 {
		t.Fatalf("应解析出 3 个套接字，得到 %d", len(socks))
	}
	// 第一个：无 Netid 列，Local 在进程列前两列
	if socks[0].bind != "127.0.0.1" || socks[0].port != 18792 || socks[0].proc != "clawdbot-gatewa" {
		t.Errorf("无 Netid 布局解析错误: %+v", socks[0])
	}
	// tailscaled 绑 tailnet IP
	if socks[2].bind != "100.105.249.48" || socks[2].port != 50149 {
		t.Errorf("tailscaled 地址解析错误: %+v", socks[2])
	}
}

func TestParseDockerPorts(t *testing.T) {
	ports := parseDockerPorts("0.0.0.0:443->443/tcp, 127.0.0.1:8080->80/tcp, :::443->443/tcp")
	if len(ports) != 3 {
		t.Fatalf("应解析出 3 个端口，得到 %d", len(ports))
	}
	// 0.0.0.0:443 → public
	if ports[0].Bind != "0.0.0.0" || ports[0].Port != 443 || ports[0].Exposure != shared.ExposurePublic {
		t.Errorf("443 端口暴露分级错误: %+v", ports[0])
	}
	// 127.0.0.1:8080 → local
	if ports[1].Bind != "127.0.0.1" || ports[1].Port != 8080 || ports[1].Exposure != shared.ExposureLocal {
		t.Errorf("8080 应为 local: %+v", ports[1])
	}
	// :::443 → bind "::" → public
	if ports[2].Bind != "::" || ports[2].Exposure != shared.ExposurePublic {
		t.Errorf(":::443 应为 public: %+v", ports[2])
	}

	// 仅 EXPOSE 未发布（无宿主绑定）应被忽略
	if p := parseDockerPorts("443/tcp"); len(p) != 0 {
		t.Errorf("未发布的 EXPOSE 端口应忽略，得到 %+v", p)
	}
}

func TestParseDockerPS(t *testing.T) {
	content := `{"ID":"abc123def4567890123456789012345678901234567890123456789012345678","Names":"/nginx","Image":"nginx:alpine","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:443->443/tcp"}
{"ID":"xyz","Names":"redis,redis2","Image":"redis:7","State":"exited","Status":"Exited","Ports":""}
`
	cs := parseDockerPS(content)
	if len(cs) != 2 {
		t.Fatalf("应解析出 2 个容器，得到 %d", len(cs))
	}
	// 第一个：名字应去掉前导 /
	if cs[0].Names != "nginx" || cs[0].Image != "nginx:alpine" || cs[0].State != "running" {
		t.Errorf("第一个容器解析错误: %+v", cs[0])
	}
	// 第二个：多名字取第一个
	if cs[1].Names != "redis" {
		t.Errorf("多名字应取第一个: %+v", cs[1])
	}
}

func TestParseCgroupDockerV2(t *testing.T) {
	content := "0::/docker/abc123def4567890123456789012345678901234567890123456789012345678\n"
	o := parseCgroup(content)
	if o.kind != shared.KindDocker {
		t.Errorf("应为 docker，得到 %v", o.kind)
	}
}

func TestParseCgroupDockerV1(t *testing.T) {
	content := "11:blkio:/docker/abc123def4567890123456789012345678901234567890123456789012345678\n"
	o := parseCgroup(content)
	if o.kind != shared.KindDocker {
		t.Errorf("v1 也应识别 docker，得到 %v", o.kind)
	}
}

func TestParseCgroupDockerSystemdScope(t *testing.T) {
	content := "0::/system.slice/docker-6687817628f3e5d6be80ea1692004cf7d3019ecb11487f074f8aff65fc22577c.scope\n"
	o := parseCgroup(content)
	if o.kind != shared.KindDocker || o.id != "6687817628f3e5d6be80ea1692004cf7d3019ecb11487f074f8aff65fc22577c" {
		t.Errorf("systemd docker scope should be attributed to its container, got %+v", o)
	}
}

func TestParseCgroupSystemd(t *testing.T) {
	content := "0::/system.slice/caddy.service\n"
	o := parseCgroup(content)
	if o.kind != shared.KindSystemd || o.unit != "caddy.service" {
		t.Errorf("应为 systemd:caddy.service，得到 %+v", o)
	}
}

func TestParseCgroupSystemdV1Named(t *testing.T) {
	// v1 的 name=systemd 层级
	content := "1:name=systemd:/system.slice/sshd.service\n"
	o := parseCgroup(content)
	if o.kind != shared.KindSystemd || o.unit != "sshd.service" {
		t.Errorf("v1 systemd 应识别 sshd.service，得到 %+v", o)
	}
}

func TestParseCgroupBare(t *testing.T) {
	content := "0::/user.slice/user-1000.slice/session-1.scope\n"
	o := parseCgroup(content)
	if o.kind != shared.KindProcess {
		t.Errorf("无归属应为裸进程，得到 %v", o.kind)
	}
}

func TestStateOrStatus(t *testing.T) {
	if got := stateOrStatus("running", "Up 2h"); got != "running" {
		t.Errorf("应优先 State: %q", got)
	}
	if got := stateOrStatus("", "Exited (0)"); got != "Exited (0)" {
		t.Errorf("State 空时应退回 Status: %q", got)
	}
}

func TestDiscoverAttributesHostNetworkSocketToDockerContainer(t *testing.T) {
	originalRunPriv := runPriv
	originalCgroupFor := cgroupFor
	t.Cleanup(func() {
		runPriv = originalRunPriv
		cgroupFor = originalCgroupFor
	})

	const containerID = "6687817628f3e5d6be80ea1692004cf7d3019ecb11487f074f8aff65fc22577c"
	runPriv = func(argv []string) ([]byte, []byte, error) {
		switch {
		case argvEqual(argv, dockerPS):
			return []byte(`{"ID":"` + containerID + `","Names":"cpa-manager-plus","Image":"example/cpa:latest","State":"running","Status":"Up","Ports":""}` + "\n"), nil, nil
		case argvEqual(argv, dockerComposePSCommand), argvEqual(argv, systemdUnitsCommand):
			return nil, nil, nil
		case argvEqual(argv, processOriginsCommand):
			return nil, nil, nil
		case argvEqual(argv, ssCmd):
			return []byte(`LISTEN 0 4096 *:18317 *:* users:(("cpa-manager-plu",pid=2397979,fd=3))` + "\n"), nil, nil
		default:
			t.Fatalf("unexpected privileged command: %v", argv)
			return nil, nil, nil
		}
	}
	cgroupFor = func(pid int) cgroupOwner {
		if pid != 2397979 {
			t.Fatalf("unexpected pid: %d", pid)
		}
		return cgroupOwner{kind: shared.KindDocker, id: containerID}
	}

	result := Discover()
	if len(result.Services) != 1 {
		t.Fatalf("host-network container should not create a duplicate process workload: %+v", result.Services)
	}
	service := result.Services[0]
	if service.Key != "docker:cpa-manager-plus" || service.Unidentified || len(service.Ports) != 1 {
		t.Fatalf("host-network listener was not attributed to Docker: %+v", service)
	}
	if service.Ports[0].Port != 18317 || service.MaxExposure != shared.ExposurePublic {
		t.Fatalf("host-network endpoint was not retained: %+v", service)
	}
}

func TestDiscoverGroupsCustomProcessPortsByRedactedOrigin(t *testing.T) {
	originalRunPriv := runPriv
	originalCgroupFor := cgroupFor
	t.Cleanup(func() {
		runPriv = originalRunPriv
		cgroupFor = originalCgroupFor
	})

	runPriv = func(argv []string) ([]byte, []byte, error) {
		switch {
		case argvEqual(argv, dockerPS):
			return nil, nil, nil
		case argvEqual(argv, dockerComposePSCommand), argvEqual(argv, systemdUnitsCommand):
			return nil, nil, nil
		case argvEqual(argv, processOriginsCommand):
			return []byte(`{"pid":481732,"uid":1001,"comm":"node","executable":"node","cwdBase":"image","cwdFingerprint":"0123456789abcdef"}` + "\n"), nil, nil
		case argvEqual(argv, ssCmd):
			return []byte("LISTEN 0 511 *:44101 *:* users:((\"node\",pid=481732,fd=18))\n" +
				"LISTEN 0 511 127.0.0.1:33271 *:* users:((\"node\",pid=481732,fd=19))\n"), nil, nil
		default:
			t.Fatalf("unexpected privileged command: %v", argv)
			return nil, nil, nil
		}
	}
	cgroupFor = func(pid int) cgroupOwner {
		if pid != 481732 {
			t.Fatalf("unexpected pid: %d", pid)
		}
		return cgroupOwner{kind: shared.KindProcess}
	}

	result := Discover()
	if len(result.Warnings) != 0 || len(result.Services) != 1 {
		t.Fatalf("custom process discovery failed: %+v", result)
	}
	service := result.Services[0]
	if service.Key != "process:838af199f2579ac5" {
		t.Fatalf("unexpected stable process key: %q", service.Key)
	}
	if service.Name != "image · node" || service.Unidentified || service.PID != 481732 {
		t.Fatalf("custom process was not identified: %+v", service)
	}
	if len(service.Ports) != 2 || service.MaxExposure != shared.ExposurePublic {
		t.Fatalf("custom process ports were not grouped: %+v", service)
	}
}

func TestDiscoverAddsComposeIdentityAndRelevantSystemdUnits(t *testing.T) {
	originalRunPriv := runPriv
	originalCgroupFor := cgroupFor
	t.Cleanup(func() {
		runPriv = originalRunPriv
		cgroupFor = originalCgroupFor
	})

	const containerID = "5790b9f8a68de25ae1666aa6fbf3386b2da0f8ac9990eafc991d048b52f4e9de"
	runPriv = func(argv []string) ([]byte, []byte, error) {
		switch {
		case argvEqual(argv, dockerPS):
			return []byte(`{"ID":"` + containerID + `","Names":"postgres","Image":"postgres:15","State":"running","Status":"Up","Ports":""}` + "\n"), nil, nil
		case argvEqual(argv, dockerComposePSCommand):
			return []byte(`["` + containerID + `","new-api","postgres"]` + "\n"), nil, nil
		case argvEqual(argv, systemdUnitsCommand):
			return []byte("Id=custom.service\nLoadState=loaded\nActiveState=active\nSubState=running\nFragmentPath=/etc/systemd/system/custom.service\n\n" +
				"Id=failed-package.service\nLoadState=loaded\nActiveState=failed\nSubState=failed\nFragmentPath=/usr/lib/systemd/system/failed-package.service\n\n" +
				"Id=ssh.service\nLoadState=loaded\nActiveState=active\nSubState=running\nFragmentPath=/usr/lib/systemd/system/ssh.service\n"), nil, nil
		case argvEqual(argv, processOriginsCommand), argvEqual(argv, ssCmd):
			return nil, nil, nil
		default:
			t.Fatalf("unexpected privileged command: %v", argv)
			return nil, nil, nil
		}
	}
	cgroupFor = func(int) cgroupOwner { return cgroupOwner{kind: shared.KindProcess} }

	result := Discover()
	byKey := make(map[string]shared.Service)
	for _, service := range result.Services {
		byKey[service.Key] = service
	}
	container := byKey["docker:postgres"]
	if container.ComposeProject != "new-api" || container.ComposeService != "postgres" {
		t.Fatalf("Compose identity was not attached to its container: %+v", container)
	}
	if byKey["systemd:custom.service"].Status != "active/running" {
		t.Fatalf("active custom unit was not discovered: %+v", byKey)
	}
	if byKey["systemd:failed-package.service"].Status != "failed" {
		t.Fatalf("failed package unit was not discovered: %+v", byKey)
	}
	if _, noisy := byKey["systemd:ssh.service"]; noisy {
		t.Fatal("active package unit without a listener should stay folded")
	}
}

package shared

import "testing"

func TestClassifyBind(t *testing.T) {
	cases := []struct {
		bind string
		want Exposure
	}{
		{"127.0.0.1", ExposureLocal},
		{"::1", ExposureLocal},
		{"[::1]", ExposureLocal},
		{"::ffff:127.0.0.1", ExposureLocal},

		{"0.0.0.0", ExposurePublic},
		{"*", ExposurePublic},
		{"::", ExposurePublic},
		{"[::]", ExposurePublic},
		{"", ExposurePublic},
		{"garbage", ExposurePublic}, // 拿不准时报严重

		{"100.83.12.4", ExposureTailnet},
		{"100.64.0.0", ExposureTailnet},
		{"100.127.255.255", ExposureTailnet},
		{"fd7a:115c:a1e0::1", ExposureTailnet},

		// 关键回归：CGNAT 段之外的 100.x 是公网地址，不能因为前缀是 100 就当内网
		{"100.63.255.255", ExposureOther},
		{"100.128.0.1", ExposureOther},
		{"100.200.1.1", ExposureOther},

		{"192.168.1.10", ExposureOther},
		{"203.0.113.5", ExposureOther}, // TEST-NET-3 文档保留段，代表公网 IP
	}
	for _, c := range cases {
		if got := ClassifyBind(c.bind); got != c.want {
			t.Errorf("ClassifyBind(%q) = %q, 期望 %q", c.bind, got, c.want)
		}
	}
}

func TestMaxExposure(t *testing.T) {
	if got := MaxExposure(nil); got != ExposureLocal {
		t.Errorf("无端口应为 local，得到 %q", got)
	}
	ports := []Port{
		{Exposure: ExposureLocal},
		{Exposure: ExposurePublic},
		{Exposure: ExposureTailnet},
	}
	if got := MaxExposure(ports); got != ExposurePublic {
		t.Errorf("应取最开放的 public，得到 %q", got)
	}
	if got := MaxExposure([]Port{{Exposure: ExposureLocal}, {Exposure: ExposureTailnet}}); got != ExposureTailnet {
		t.Errorf("应为 tailnet，得到 %q", got)
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in   string
		bind string
		port int
		ok   bool
	}{
		{"0.0.0.0:443", "0.0.0.0", 443, true},
		{"[::]:80", "::", 80, true},
		{"*:8388", "*", 8388, true},
		{"127.0.0.1:9101", "127.0.0.1", 9101, true},
		{"[fd7a:115c:a1e0::1]:22", "fd7a:115c:a1e0::1", 22, true},
		{"noport", "", 0, false},
		{"0.0.0.0:0", "", 0, false},
		{"0.0.0.0:99999", "", 0, false},
	}
	for _, c := range cases {
		bind, port, ok := SplitHostPort(c.in)
		if ok != c.ok || bind != c.bind || port != c.port {
			t.Errorf("SplitHostPort(%q) = (%q,%d,%v), 期望 (%q,%d,%v)",
				c.in, bind, port, ok, c.bind, c.port, c.ok)
		}
	}
}

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNginxConfigTreeExpandsSafeIncludesAndRelativeSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"conf.d", "sites-available", "sites-enabled"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestNginxConfig(t, filepath.Join(root, "nginx.conf"), `
events {}
http {
  include /etc/nginx/conf.d/*.conf;
  include sites-enabled/*;
	  include /etc/letsencrypt/options-ssl-nginx.conf;
}
`)
	writeTestNginxConfig(t, filepath.Join(root, "conf.d", "api.conf"), `
server { listen 443 ssl; server_name api.example.test; location / { proxy_pass http://127.0.0.1:3000; } }
`)
	writeTestNginxConfig(t, filepath.Join(root, "sites-available", "web"), `
server { listen 80; server_name web.example.test; location /app { proxy_pass http://127.0.0.1:4000; } }
`)
	if err := os.Symlink("../sites-available/web", filepath.Join(root, "sites-enabled", "web")); err != nil {
		t.Fatal(err)
	}

	content, found, err := readNginxConfigTree(root)
	if err != nil || !found {
		t.Fatalf("safe config tree was not read: found=%v err=%v", found, err)
	}
	routes, err := parseNginxRoutes(content, "systemd:nginx.service")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected routes from both includes, got %+v", routes)
	}
}

func TestReadNginxConfigTreeRejectsEscapingInclude(t *testing.T) {
	root := t.TempDir()
	writeTestNginxConfig(t, filepath.Join(root, "nginx.conf"), "events {}\nhttp { include /tmp/secret.conf; }\n")
	_, found, err := readNginxConfigTree(root)
	if !found || err == nil || !strings.Contains(err.Error(), "escaped") {
		t.Fatalf("escaping include was not rejected: found=%v err=%v", found, err)
	}
}

func TestReadNginxConfigTreeRejectsAbsoluteSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sites-enabled"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestNginxConfig(t, filepath.Join(root, "nginx.conf"), "events {}\nhttp { include sites-enabled/*; }\n")
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "sites-enabled", "escape")); err != nil {
		t.Fatal(err)
	}
	_, found, err := readNginxConfigTree(root)
	if !found || err == nil {
		t.Fatalf("absolute symlink was not rejected: found=%v err=%v", found, err)
	}
}

func TestReadNginxConfigTreeAcceptsAbsoluteSymlinkWithinStandardRoot(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"sites-available", "sites-enabled"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestNginxConfig(t, filepath.Join(root, "nginx.conf"), "events {}\nhttp { include sites-enabled/*; }\n")
	target := filepath.Join(root, "sites-available", "app")
	writeTestNginxConfig(t, target, `server { listen 443 ssl; server_name app.example.test; location / { proxy_pass http://127.0.0.1:3000; } }`)
	if err := os.Symlink(target, filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	content, found, err := readNginxConfigTree(root)
	if err != nil || !found {
		t.Fatalf("absolute in-root symlink was not accepted: found=%v err=%v", found, err)
	}
	routes, err := parseNginxRoutes(content, "systemd:nginx.service")
	if err != nil || len(routes) != 1 || routes[0].Route.Host != "app.example.test" {
		t.Fatalf("absolute in-root symlink route missing: routes=%+v err=%v", routes, err)
	}
}

func writeTestNginxConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

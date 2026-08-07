package agent

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxNginxConfigFiles  = 128
	maxNginxIncludeDepth = 16
)

// readNginxConfigTree expands the standard /etc/nginx configuration tree
// without invoking nginx -T. The Agent service deliberately hides home
// directories; nginx -T can therefore fail merely because a TLS certificate
// lives under /root, even though route directives are all readable below
// /etc/nginx. os.Root keeps every followed relative symlink beneath the
// trusted tree while the file/count/size limits bound resource use.
func readNginxConfigTree(rootPath string) ([]byte, bool, error) {
	canonicalRoot, err := filepath.EvalSymlinks(rootPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rootPath = canonicalRoot
	root, err := os.OpenRoot(rootPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	if _, err := root.Stat("nginx.conf"); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, true, err
	}

	reader := nginxConfigTreeReader{
		rootPath: rootPath,
		root:     root,
		visited:  make(map[string]bool),
	}
	content, err := reader.read("nginx.conf", 0)
	if err != nil {
		return nil, true, err
	}
	return content, true, nil
}

type nginxConfigTreeReader struct {
	rootPath string
	root     *os.Root
	visited  map[string]bool
	total    int
}

func (reader *nginxConfigTreeReader) read(name string, depth int) ([]byte, error) {
	var err error
	name, err = reader.resolve(name)
	if err != nil {
		return nil, err
	}
	if reader.visited[name] {
		return nil, nil
	}
	if depth > maxNginxIncludeDepth || len(reader.visited) >= maxNginxConfigFiles {
		return nil, errors.New("Nginx include tree exceeded its traversal limit")
	}

	file, err := reader.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxProxyConfigBytes {
		return nil, errors.New("Nginx include is not a bounded regular file")
	}
	remaining := maxProxyConfigBytes - reader.total
	if remaining <= 0 {
		return nil, errors.New("Nginx config tree exceeded its size limit")
	}
	content, err := io.ReadAll(io.LimitReader(file, int64(remaining)+1))
	if err != nil || len(content) > remaining {
		return nil, errors.New("Nginx config tree exceeded its size limit")
	}
	reader.visited[name] = true
	reader.total += len(content)

	result := append(append([]byte(nil), content...), '\n')
	for _, pattern := range nginxIncludePatterns(content) {
		if nginxNonRoutingInclude(pattern) {
			continue
		}
		relativePattern, ok := nginxRelativeInclude(pattern)
		if !ok {
			return nil, errors.New("Nginx include escaped the standard config tree")
		}
		matches, globErr := filepath.Glob(filepath.Join(reader.rootPath, relativePattern))
		if globErr != nil {
			return nil, errors.New("Nginx include pattern is invalid")
		}
		if len(matches) == 0 && !strings.ContainsAny(relativePattern, "*?[") {
			return nil, os.ErrNotExist
		}
		for _, match := range matches {
			relative, relErr := filepath.Rel(reader.rootPath, match)
			if relErr != nil || !filepath.IsLocal(relative) {
				return nil, errors.New("Nginx include escaped the standard config tree")
			}
			expanded, readErr := reader.read(relative, depth+1)
			if readErr != nil {
				return nil, readErr
			}
			result = append(result, expanded...)
		}
	}
	return result, nil
}

// resolve follows a selected include once, then converts its destination back
// to a path for os.Root.Open. This permits the common absolute symlink form
// /etc/nginx/sites-enabled/app -> /etc/nginx/sites-available/app, while an
// external target remains rejected. Opening the canonical relative target via
// os.Root also prevents a symlink swap from escaping after resolution.
func (reader *nginxConfigTreeReader) resolve(name string) (string, error) {
	name = filepath.Clean(name)
	if !filepath.IsLocal(name) {
		return "", errors.New("Nginx include escaped the standard config tree")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(reader.rootPath, name))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(reader.rootPath, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return "", errors.New("Nginx include escaped the standard config tree")
	}
	return relative, nil
}

func nginxIncludePatterns(content []byte) []string {
	tokens := lexConfig(string(content))
	var patterns []string
	for index := 0; index+2 < len(tokens); index++ {
		if tokens[index] == "include" && tokens[index+1] != ";" && tokens[index+2] == ";" {
			patterns = append(patterns, tokens[index+1])
			index += 2
		}
	}
	return patterns
}

// Certbot injects this policy file into server blocks. It contains TLS
// protocol/cipher/session settings rather than listeners, names, locations, or
// upstreams. Ignoring the exact conventional path avoids reading outside the
// confined route tree and avoids nginx -T touching certificate material.
func nginxNonRoutingInclude(pattern string) bool {
	return filepath.Clean(pattern) == "/etc/letsencrypt/options-ssl-nginx.conf"
}

func nginxRelativeInclude(pattern string) (string, bool) {
	if pattern == "" || strings.Contains(pattern, "$") {
		return "", false
	}
	pattern = filepath.Clean(pattern)
	if filepath.IsAbs(pattern) {
		var err error
		pattern, err = filepath.Rel("/etc/nginx", pattern)
		if err != nil {
			return "", false
		}
	}
	return pattern, filepath.IsLocal(pattern)
}

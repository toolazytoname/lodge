package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	maxProxyConfigBytes = 4 << 20
	maxProxyRoutes      = 256
	maxProxyWarnings    = 32
)

type discoveredProxyRoute struct {
	WorkloadKey string
	Route       shared.ProxyRoute
}

type proxyDiscovery struct {
	Routes   []discoveredProxyRoute
	Warnings []string
}

type proxyDiscoveryRecord struct {
	Type        string           `json:"type"`
	WorkloadKey string           `json:"workloadKey,omitempty"`
	Kind        shared.RouteKind `json:"kind,omitempty"`
	Scheme      string           `json:"scheme,omitempty"`
	Host        string           `json:"host,omitempty"`
	Port        int              `json:"port,omitempty"`
	Path        string           `json:"path,omitempty"`
	Upstreams   []string         `json:"upstreams,omitempty"`
	Message     string           `json:"message,omitempty"`
}

func parseProxyDiscovery(content []byte) proxyDiscovery {
	var result proxyDiscovery
	for _, line := range bytes.Split(content, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record proxyDiscoveryRecord
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&record) != nil {
			continue
		}
		switch record.Type {
		case "route":
			if len(result.Routes) >= maxProxyRoutes {
				continue
			}
			route, ok := normalizeProxyRoute(record.WorkloadKey, shared.ProxyRoute{
				Kind:   record.Kind,
				Scheme: record.Scheme, Host: record.Host, Port: record.Port,
				Path: record.Path, Upstreams: record.Upstreams,
			})
			if ok {
				result.Routes = append(result.Routes, route)
			}
		case "warning":
			if len(result.Warnings) < maxProxyWarnings && validProxyWarning(record.Message) {
				result.Warnings = append(result.Warnings, record.Message)
			}
		}
	}
	result.Routes = mergeProxyRoutes(result.Routes)
	return result
}

func writeProxyDiscovery(discovery proxyDiscovery, writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	routes := mergeProxyRoutes(discovery.Routes)
	if len(routes) > maxProxyRoutes {
		routes = routes[:maxProxyRoutes]
	}
	for _, route := range routes {
		record := proxyDiscoveryRecord{
			Type: "route", WorkloadKey: route.WorkloadKey,
			Kind:   route.Route.Kind,
			Scheme: route.Route.Scheme, Host: route.Route.Host, Port: route.Route.Port,
			Path: route.Route.Path, Upstreams: route.Route.Upstreams,
		}
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	for index, warning := range discovery.Warnings {
		if index >= maxProxyWarnings {
			break
		}
		if !validProxyWarning(warning) {
			continue
		}
		if err := encoder.Encode(proxyDiscoveryRecord{Type: "warning", Message: warning}); err != nil {
			return err
		}
	}
	return nil
}

func mergeProxyRoutes(routes []discoveredProxyRoute) []discoveredProxyRoute {
	byKey := make(map[string]discoveredProxyRoute)
	for _, candidate := range routes {
		route, ok := normalizeProxyRoute(candidate.WorkloadKey, candidate.Route)
		if !ok {
			continue
		}
		key := route.WorkloadKey + "\x00" + route.Route.Scheme + "\x00" + route.Route.Host + "\x00" +
			strconv.Itoa(route.Route.Port) + "\x00" + route.Route.Path
		if existing, found := byKey[key]; found {
			existing.Route.Kind = strongerRouteKind(existing.Route.Kind, route.Route.Kind)
			existing.Route.Upstreams = uniqueSorted(append(existing.Route.Upstreams, route.Route.Upstreams...))
			byKey[key] = existing
		} else {
			byKey[key] = route
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]discoveredProxyRoute, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func normalizeProxyRoute(workloadKey string, route shared.ProxyRoute) (discoveredProxyRoute, bool) {
	if !validProxyWorkloadKey(workloadKey) {
		return discoveredProxyRoute{}, false
	}
	route.Scheme = strings.ToLower(strings.TrimSpace(route.Scheme))
	route.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(route.Host), "."))
	route.Path = strings.TrimSpace(route.Path)
	if route.Kind == "" {
		if len(route.Upstreams) > 0 {
			route.Kind = shared.RouteKindProxy
		} else {
			route.Kind = shared.RouteKindUnknown
		}
	}
	if route.Path == "" {
		route.Path = "/"
	}
	if !validRouteKind(route.Kind) || route.Scheme != "http" && route.Scheme != "https" || route.Port < 1 || route.Port > 65535 ||
		!validProxyHost(route.Host, true) || !validRoutePath(route.Path) {
		return discoveredProxyRoute{}, false
	}
	upstreams := make([]string, 0, len(route.Upstreams))
	for _, upstream := range route.Upstreams {
		if normalized := sanitizeUpstream(upstream); normalized != "" {
			upstreams = append(upstreams, normalized)
		}
	}
	route.Upstreams = uniqueSorted(upstreams)
	if len(route.Upstreams) > 16 {
		route.Upstreams = route.Upstreams[:16]
	}
	return discoveredProxyRoute{WorkloadKey: workloadKey, Route: route}, true
}

func validRouteKind(kind shared.RouteKind) bool {
	return kind == shared.RouteKindUnknown || kind == shared.RouteKindProxy || kind == shared.RouteKindStatic || kind == shared.RouteKindSite
}

func strongerRouteKind(left, right shared.RouteKind) shared.RouteKind {
	rank := map[shared.RouteKind]int{
		shared.RouteKindUnknown: 0,
		shared.RouteKindSite:    1,
		shared.RouteKindStatic:  2,
		shared.RouteKindProxy:   3,
	}
	if rank[right] > rank[left] {
		return right
	}
	return left
}

func validProxyWorkloadKey(value string) bool {
	if len(value) < 8 || len(value) > 512 || (!strings.HasPrefix(value, "docker:") && !strings.HasPrefix(value, "systemd:")) {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validProxyWarning(value string) bool {
	if value == "" || len(value) > 240 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validProxyHost(host string, allowEmpty bool) bool {
	if host == "" {
		return allowEmpty
	}
	if len(host) > 253 || strings.HasSuffix(host, ".") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.ContainsAny(host, "/:@?#[]") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || !asciiAlphaNumeric(label[0]) || !asciiAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for index := 1; index < len(label)-1; index++ {
			if !asciiAlphaNumeric(label[index]) && label[index] != '-' {
				return false
			}
		}
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func validRoutePath(path string) bool {
	if path == "" || len(path) > 512 || path[0] != '/' || !utf8.ValidString(path) || strings.ContainsAny(path, "?#") {
		return false
	}
	for _, character := range path {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func sanitizeUpstream(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 256 || strings.ContainsAny(raw, "$@?#") {
		return ""
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "h2c") {
			return ""
		}
		host := strings.ToLower(parsed.Hostname())
		port := parsed.Port()
		if port == "" {
			if parsed.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		return normalizedAuthority(host, port)
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return ""
	}
	return normalizedAuthority(strings.ToLower(host), port)
}

func normalizedAuthority(host, portText string) string {
	if !validProxyHost(host, false) {
		return ""
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type caddyAddress struct {
	Scheme string
	Host   string
	Port   int
}

func parseCaddyRoutes(content []byte, workloadKey string) ([]discoveredProxyRoute, error) {
	if len(content) > maxProxyConfigBytes {
		return nil, errors.New("Caddy config exceeds the route discovery limit")
	}
	var routes []discoveredProxyRoute
	depth := 0
	var addresses []caddyAddress
	siteDepth := 0
	siteRouteCount := 0
	paths := make(map[int]string)
	finishSite := func() {
		if len(addresses) > 0 && siteRouteCount == 0 {
			for _, address := range addresses {
				routes = append(routes, discoveredProxyRoute{WorkloadKey: workloadKey, Route: shared.ProxyRoute{
					Kind:   shared.RouteKindSite,
					Scheme: address.Scheme, Host: address.Host, Port: address.Port, Path: "/",
				}})
			}
		}
		addresses = nil
		siteDepth = 0
		siteRouteCount = 0
		paths = make(map[int]string)
	}
	for _, rawLine := range strings.Split(string(content), "\n") {
		tokens := lexConfig(rawLine)
		if len(tokens) == 0 {
			continue
		}
		for len(tokens) > 0 && tokens[0] == "}" {
			depth--
			delete(paths, depth+1)
			tokens = tokens[1:]
			if len(addresses) > 0 && depth < siteDepth {
				finishSite()
			}
		}
		if len(tokens) == 0 {
			continue
		}
		open := tokenIndex(tokens, "{")
		if len(addresses) == 0 && depth == 0 && open >= 0 && open > 0 {
			for _, token := range tokens[:open] {
				for _, part := range strings.Split(token, ",") {
					if address, ok := parseCaddyAddress(strings.TrimSpace(part)); ok {
						addresses = append(addresses, address)
					}
				}
			}
			if len(addresses) > 0 {
				siteDepth = depth + 1
			}
		}
		if len(addresses) > 0 && depth >= siteDepth {
			directive := tokens[0]
			currentPath := "/"
			for pathDepth := siteDepth; pathDepth <= depth; pathDepth++ {
				if path := paths[pathDepth]; path != "" {
					currentPath = path
				}
			}
			if (directive == "handle" || directive == "handle_path" || directive == "route") && open >= 0 {
				for _, token := range tokens[1:open] {
					if path := normalizeMatcherPath(token); path != "" {
						paths[depth+1] = path
						break
					}
				}
			}
			if directive == "reverse_proxy" {
				args := tokens[1:]
				if open >= 0 {
					args = tokens[1:open]
				}
				if len(args) > 0 {
					if path := normalizeMatcherPath(args[0]); path != "" {
						currentPath = path
						args = args[1:]
					} else if strings.HasPrefix(args[0], "@") {
						args = args[1:]
					}
				}
				upstreams := make([]string, 0, len(args))
				for _, argument := range args {
					if argument == "{" {
						break
					}
					if upstream := sanitizeUpstream(argument); upstream != "" {
						upstreams = append(upstreams, upstream)
					}
				}
				for _, address := range addresses {
					routes = append(routes, discoveredProxyRoute{WorkloadKey: workloadKey, Route: shared.ProxyRoute{
						Kind:   shared.RouteKindProxy,
						Scheme: address.Scheme, Host: address.Host, Port: address.Port,
						Path: currentPath, Upstreams: upstreams,
					}})
					siteRouteCount++
				}
			}
		}
		for _, token := range tokens {
			switch token {
			case "{":
				depth++
			case "}":
				depth--
				delete(paths, depth+1)
				if len(addresses) > 0 && depth < siteDepth {
					finishSite()
				}
			}
		}
	}
	if len(addresses) > 0 {
		finishSite()
	}
	return mergeProxyRoutes(routes), nil
}

func parseCaddyAddress(raw string) (caddyAddress, bool) {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ","))
	if raw == "" || strings.HasPrefix(raw, "(") || strings.ContainsAny(raw, "{}$@") {
		return caddyAddress{}, false
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Path != "" {
			return caddyAddress{}, false
		}
		host := strings.ToLower(parsed.Hostname())
		portText := parsed.Port()
		if portText == "" {
			if parsed.Scheme == "http" {
				portText = "80"
			} else {
				portText = "443"
			}
		}
		port, err := strconv.Atoi(portText)
		return caddyAddress{Scheme: parsed.Scheme, Host: host, Port: port}, err == nil && port >= 1 && port <= 65535 && validProxyHost(host, true)
	}
	if strings.HasPrefix(raw, ":") {
		port, err := strconv.Atoi(strings.TrimPrefix(raw, ":"))
		if err != nil || port < 1 || port > 65535 {
			return caddyAddress{}, false
		}
		scheme := "https"
		if port == 80 {
			scheme = "http"
		}
		return caddyAddress{Scheme: scheme, Port: port}, true
	}
	host := raw
	port := 443
	if parsedHost, parsedPort, err := net.SplitHostPort(raw); err == nil {
		host = parsedHost
		parsed, parseErr := strconv.Atoi(parsedPort)
		if parseErr != nil {
			return caddyAddress{}, false
		}
		port = parsed
	} else if strings.Count(raw, ":") == 1 {
		candidateHost, candidatePort, found := strings.Cut(raw, ":")
		parsed, parseErr := strconv.Atoi(candidatePort)
		if found && parseErr == nil {
			host, port = candidateHost, parsed
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if port < 1 || port > 65535 || !validProxyHost(host, false) {
		return caddyAddress{}, false
	}
	scheme := "https"
	if port == 80 {
		scheme = "http"
	}
	return caddyAddress{Scheme: scheme, Host: host, Port: port}, true
}

func normalizeMatcherPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return ""
	}
	if !validRoutePath(raw) {
		return ""
	}
	return raw
}

func tokenIndex(tokens []string, target string) int {
	for index, token := range tokens {
		if token == target {
			return index
		}
	}
	return -1
}

type configNode struct {
	Name     string
	Args     []string
	Children []configNode
}

func parseNginxRoutes(content []byte, workloadKey string) ([]discoveredProxyRoute, error) {
	if len(content) > maxProxyConfigBytes {
		return nil, errors.New("Nginx config exceeds the route discovery limit")
	}
	tokens := lexConfig(string(content))
	position := 0
	nodes, err := parseConfigNodes(tokens, &position, false)
	if err != nil {
		return nil, errors.New("Nginx config could not be parsed safely")
	}
	var routes []discoveredProxyRoute
	for _, server := range findConfigNodes(nodes, "server") {
		hosts := []string{""}
		var discoveredHosts []string
		for _, child := range server.Children {
			if child.Name != "server_name" {
				continue
			}
			for _, candidate := range child.Args {
				candidate = strings.ToLower(strings.TrimSuffix(candidate, "."))
				if validProxyHost(candidate, false) && candidate != "_" {
					discoveredHosts = append(discoveredHosts, candidate)
				}
			}
		}
		if len(discoveredHosts) > 0 {
			hosts = uniqueSorted(discoveredHosts)
		}
		listeners := nginxListeners(server.Children)
		if len(listeners) == 0 {
			listeners = []caddyAddress{{Scheme: "http", Port: 80}}
		}
		proxyPaths := collectNginxProxyPass(server.Children, "/")
		staticPaths := collectNginxStaticPaths(server.Children, "/")
		for _, listener := range listeners {
			for _, host := range hosts {
				for path, upstreams := range proxyPaths {
					routes = append(routes, discoveredProxyRoute{WorkloadKey: workloadKey, Route: shared.ProxyRoute{
						Kind:   shared.RouteKindProxy,
						Scheme: listener.Scheme, Host: host, Port: listener.Port, Path: path,
						Upstreams: uniqueSorted(upstreams),
					}})
				}
				if host == "" {
					continue
				}
				for path := range staticPaths {
					if _, proxied := proxyPaths[path]; proxied {
						continue
					}
					routes = append(routes, discoveredProxyRoute{WorkloadKey: workloadKey, Route: shared.ProxyRoute{
						Kind:   shared.RouteKindStatic,
						Scheme: listener.Scheme, Host: host, Port: listener.Port, Path: path,
					}})
				}
			}
		}
	}
	return mergeProxyRoutes(routes), nil
}

func lexConfig(content string) []string {
	var tokens []string
	for index := 0; index < len(content); {
		character := content[index]
		if character == '#' {
			for index < len(content) && content[index] != '\n' {
				index++
			}
			continue
		}
		if character == ' ' || character == '\t' || character == '\r' || character == '\n' {
			index++
			continue
		}
		if character == '{' || character == '}' || character == ';' {
			tokens = append(tokens, string(character))
			index++
			continue
		}
		if character == '\'' || character == '"' {
			quote := character
			index++
			var value strings.Builder
			for index < len(content) && content[index] != quote {
				if content[index] == '\\' && index+1 < len(content) {
					index++
				}
				value.WriteByte(content[index])
				index++
			}
			if index < len(content) {
				index++
			}
			tokens = append(tokens, value.String())
			continue
		}
		start := index
		for index < len(content) && !strings.ContainsRune(" \t\r\n{};#", rune(content[index])) {
			index++
		}
		if index > start {
			tokens = append(tokens, content[start:index])
		}
	}
	return tokens
}

func parseConfigNodes(tokens []string, position *int, nested bool) ([]configNode, error) {
	var nodes []configNode
	for *position < len(tokens) {
		if tokens[*position] == "}" {
			if !nested {
				return nil, errors.New("unexpected closing brace")
			}
			(*position)++
			return nodes, nil
		}
		name := tokens[*position]
		(*position)++
		var args []string
		for *position < len(tokens) && tokens[*position] != ";" && tokens[*position] != "{" && tokens[*position] != "}" {
			args = append(args, tokens[*position])
			(*position)++
		}
		if *position >= len(tokens) {
			return nil, errors.New("unterminated directive")
		}
		switch tokens[*position] {
		case ";":
			(*position)++
			nodes = append(nodes, configNode{Name: name, Args: args})
		case "{":
			(*position)++
			children, err := parseConfigNodes(tokens, position, true)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, configNode{Name: name, Args: args, Children: children})
		default:
			return nil, errors.New("unexpected closing brace")
		}
	}
	if nested {
		return nil, errors.New("unterminated block")
	}
	return nodes, nil
}

func findConfigNodes(nodes []configNode, name string) []configNode {
	var result []configNode
	for _, node := range nodes {
		if node.Name == name {
			result = append(result, node)
		}
		result = append(result, findConfigNodes(node.Children, name)...)
	}
	return result
}

func nginxListeners(nodes []configNode) []caddyAddress {
	seen := make(map[string]caddyAddress)
	for _, node := range nodes {
		if node.Name != "listen" || len(node.Args) == 0 || strings.HasPrefix(node.Args[0], "unix:") {
			continue
		}
		portText := node.Args[0]
		if _, port, err := net.SplitHostPort(portText); err == nil {
			portText = port
		} else if colon := strings.LastIndex(portText, ":"); colon >= 0 {
			portText = strings.Trim(portText[colon+1:], "[]")
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		scheme := "http"
		for _, argument := range node.Args[1:] {
			if argument == "ssl" {
				scheme = "https"
			}
		}
		if port == 443 || port == 8443 || port == 9443 {
			scheme = "https"
		}
		seen[scheme+":"+strconv.Itoa(port)] = caddyAddress{Scheme: scheme, Port: port}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]caddyAddress, 0, len(keys))
	for _, key := range keys {
		result = append(result, seen[key])
	}
	return result
}

func collectNginxProxyPass(nodes []configNode, inheritedPath string) map[string][]string {
	result := make(map[string][]string)
	for _, node := range nodes {
		path := inheritedPath
		if node.Name == "location" {
			for _, argument := range node.Args {
				if candidate := normalizeMatcherPath(argument); candidate != "" {
					path = candidate
					break
				}
			}
		}
		if node.Name == "proxy_pass" && len(node.Args) > 0 {
			if upstream := sanitizeUpstream(node.Args[0]); upstream != "" {
				result[path] = append(result[path], upstream)
			} else {
				// Preserve the external route even when an upstream group or
				// variable cannot be represented without ambiguity.
				result[path] = result[path]
			}
		}
		for childPath, upstreams := range collectNginxProxyPass(node.Children, path) {
			result[childPath] = append(result[childPath], upstreams...)
		}
	}
	return result
}

func collectNginxStaticPaths(nodes []configNode, inheritedPath string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, node := range nodes {
		path := inheritedPath
		if node.Name == "location" {
			for _, argument := range node.Args {
				if candidate := normalizeMatcherPath(argument); candidate != "" {
					path = candidate
					break
				}
			}
			if hasNginxServingDirective(node.Children) && !hasNginxDirective(node.Children, "proxy_pass") {
				result[path] = struct{}{}
			}
		}
		for childPath := range collectNginxStaticPaths(node.Children, path) {
			result[childPath] = struct{}{}
		}
	}
	if inheritedPath == "/" && hasDirectNginxServingDirective(nodes) {
		result["/"] = struct{}{}
	}
	return result
}

func hasDirectNginxServingDirective(nodes []configNode) bool {
	for _, node := range nodes {
		if node.Name == "root" || node.Name == "alias" || node.Name == "try_files" || node.Name == "index" {
			return true
		}
	}
	return false
}

func hasNginxServingDirective(nodes []configNode) bool {
	if hasDirectNginxServingDirective(nodes) {
		return true
	}
	for _, node := range nodes {
		if hasNginxServingDirective(node.Children) {
			return true
		}
	}
	return false
}

func hasNginxDirective(nodes []configNode, name string) bool {
	for _, node := range nodes {
		if node.Name == name || hasNginxDirective(node.Children, name) {
			return true
		}
	}
	return false
}

func proxyParseWarning(proxy, source string) string {
	return fmt.Sprintf("%s %s config could not be parsed without exposing raw content", source, proxy)
}

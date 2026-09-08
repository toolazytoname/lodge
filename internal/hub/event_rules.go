package hub

import (
	"fmt"
	"sort"
	"strings"

	"github.com/toolazytoname/lodge/internal/domain"
)

const (
	memoryOpenPercent  = 85
	memoryClearPercent = 80
	diskOpenPercent    = 90
	diskClearPercent   = 85
	loadOpenRatio      = 1.5
	loadClearRatio     = 1.0
	sshOpenTotal       = 30
	sshOpenSource      = 10
	sshClearTotal      = 10
	sshClearSource     = 3
	sshCriticalTotal   = 100
	sshCriticalSource  = 50

	warningStatusCollectionFailed = "主机状态采集失败"
	warningServiceDiscoveryFailed = "服务发现采集失败"
)

// evaluateEventSignals turns the latest observation into current rule truth.
// Wildcard-listener novelty is compared against an independently stored
// complete baseline. An unestablished baseline is distinct from an established
// empty set: partial or first-seen ports are not novelty until a complete
// collection establishes the host, after which a newly observed wildcard
// still opens an event. Existing non-host events are carried while a host is
// offline because missing telemetry is not proof of recovery.
func evaluateEventSignals(previous *domain.Observation, current domain.Observation, active []domain.Event, completeListeners map[string]struct{}, baselineEstablished bool) []domain.EventSignal {
	hostPrefix := string(current.HostID) + ":"
	activeByKey := make(map[string]domain.Event, len(active))
	for _, event := range active {
		if event.HostID == current.HostID && event.State != domain.EventResolved {
			activeByKey[event.DedupeKey] = event
		}
	}
	var signals []domain.EventSignal
	emit := func(signal domain.EventSignal) {
		signals = append(signals, signal)
	}
	carry := func(event domain.Event) {
		emit(signalFromEvent(event))
	}
	if !current.Online {
		for _, event := range activeByKey {
			if event.Kind != "host.offline" {
				carry(event)
			}
		}
		emit(domain.EventSignal{
			HostID: current.HostID, Kind: "host.offline", Severity: domain.SeverityCritical,
			DedupeKey: hostPrefix + "host:offline", Title: "主机离线",
			Detail: boundedEventDetail(current.LastError, "Hub 无法取得 Agent 实时状态"),
		})
		return sortedEventSignals(signals)
	}

	if current.Resources == nil {
		for key, event := range activeByKey {
			if strings.HasPrefix(key, hostPrefix+"resource:") {
				carry(event)
			}
		}
	} else {
		memoryKey := hostPrefix + "resource:memory"
		if memoryTelemetryMissing(current) {
			if event := activeByKey[memoryKey]; event.ID != "" {
				carry(event)
			}
		} else {
			memory := domain.UsagePercent(current.Resources.Memory.UsedBytes, current.Resources.Memory.TotalBytes)
			if memory >= memoryOpenPercent || (activeByKey[memoryKey].ID != "" && memory >= memoryClearPercent) {
				severity := domain.SeverityWarning
				if memory >= 95 {
					severity = domain.SeverityCritical
				}
				emit(domain.EventSignal{
					HostID: current.HostID, Kind: "resource.memory", Severity: severity,
					DedupeKey: memoryKey, Title: "内存压力", Detail: fmt.Sprintf("内存使用率 %d%%", memory),
				})
			}
		}
		diskKey := hostPrefix + "resource:disk:/"
		if diskTelemetryMissing(current) {
			if event := activeByKey[diskKey]; event.ID != "" {
				carry(event)
			}
		} else {
			for _, disk := range current.Resources.Disks {
				if disk.Mount != "/" {
					continue
				}
				used := domain.UsagePercent(disk.UsedBytes, disk.TotalBytes)
				if used >= diskOpenPercent || (activeByKey[diskKey].ID != "" && used >= diskClearPercent) {
					severity := domain.SeverityWarning
					if used >= 95 {
						severity = domain.SeverityCritical
					}
					emit(domain.EventSignal{
						HostID: current.HostID, Kind: "resource.disk", Severity: severity,
						DedupeKey: diskKey, Title: "根磁盘空间不足", Detail: fmt.Sprintf("根文件系统使用率 %d%%", used),
					})
				}
				break
			}
		}
		loadKey := hostPrefix + "resource:load"
		if loadTelemetryMissing(current) {
			if event := activeByKey[loadKey]; event.ID != "" {
				carry(event)
			}
		} else if current.Resources.CPUs > 0 {
			ratio := current.Resources.Load1 / float64(current.Resources.CPUs)
			if ratio >= loadOpenRatio || (activeByKey[loadKey].ID != "" && ratio >= loadClearRatio) {
				severity := domain.SeverityWarning
				if ratio >= 2 {
					severity = domain.SeverityCritical
				}
				emit(domain.EventSignal{
					HostID: current.HostID, Kind: "resource.load", Severity: severity,
					DedupeKey: loadKey, Title: "系统负载持续偏高",
					Detail: fmt.Sprintf("1 分钟负载 %.2f / %d CPU", current.Resources.Load1, current.Resources.CPUs),
				})
			}
		}
	}

	sshKey := hostPrefix + "ssh:authentication-failures"
	if current.SSH == nil {
		if event := activeByKey[sshKey]; event.ID != "" {
			signals = append(signals, signalFromEvent(event))
		}
	} else {
		topSource := 0
		for _, source := range current.SSH.Sources {
			if source.Count > topSource {
				topSource = source.Count
			}
		}
		active := activeByKey[sshKey].ID != ""
		aboveOpen := current.SSH.FailedTotal >= sshOpenTotal || topSource >= sshOpenSource
		aboveClear := current.SSH.FailedTotal >= sshClearTotal || topSource >= sshClearSource
		if aboveOpen || (active && aboveClear) {
			severity := domain.SeverityWarning
			if current.SSH.FailedTotal >= sshCriticalTotal || topSource >= sshCriticalSource {
				severity = domain.SeverityCritical
			}
			signals = append(signals, domain.EventSignal{
				HostID: current.HostID, Kind: "ssh.bruteforce", Severity: severity,
				DedupeKey: sshKey, Title: "SSH 认证失败突增", Detail: sshFailureDetail(current.SSH),
			})
		}
	}

	seenWorkload := make(map[string]struct{})
	if current.Workloads == nil {
		for key, event := range activeByKey {
			if strings.HasPrefix(key, hostPrefix+"workload:") {
				carry(event)
				seenWorkload[key] = struct{}{}
			}
		}
	} else {
		for key, event := range activeByKey {
			if strings.HasPrefix(key, hostPrefix+"workload:") && workloadCategoryMissing(current, event.DedupeKey, hostPrefix) {
				carry(event)
				seenWorkload[key] = struct{}{}
			}
		}
		for _, workload := range current.Workloads {
			if !workloadFailed(workload) {
				continue
			}
			key := hostPrefix + "workload:" + workload.Key + ":failed"
			if _, exists := seenWorkload[key]; exists {
				continue
			}
			emit(domain.EventSignal{
				HostID: current.HostID, Kind: "workload.failed", Severity: domain.SeverityCritical,
				DedupeKey: key, Title: "服务失败：" + workload.Name,
				Detail: boundedEventDetail(workload.State, "工作负载处于失败状态"),
			})
			seenWorkload[key] = struct{}{}
		}
	}

	currentWildcard := listenerWildcardEndpoints(current)
	completeTelemetry := current.Online && !listenerTelemetryMissing(current)
	establishingListeners := completeTelemetry && (!baselineEstablished || previous == nil || !previous.Online)
	if listenerTelemetryMissing(current) {
		emitted := make(map[string]struct{})
		for key, event := range activeByKey {
			if strings.HasPrefix(key, hostPrefix+"listener:") {
				carry(event)
				emitted[key] = struct{}{}
			}
		}
		if baselineEstablished {
			for key, endpoint := range currentWildcard {
				if _, known := completeListeners[key]; known {
					continue
				}
				if _, exists := emitted[key]; exists {
					continue
				}
				emit(listenerAddedSignal(current.HostID, key, endpoint))
			}
		}
	} else if establishingListeners {
		for key, endpoint := range currentWildcard {
			if event := activeByKey[key]; event.ID != "" {
				emit(listenerAddedSignal(current.HostID, key, endpoint))
			}
		}
	} else {
		for key, endpoint := range currentWildcard {
			_, known := completeListeners[key]
			_, alreadyActive := activeByKey[key]
			if !alreadyActive && known {
				continue
			}
			emit(listenerAddedSignal(current.HostID, key, endpoint))
		}
	}
	return sortedEventSignals(signals)
}

func listenerWildcardEndpoints(observation domain.Observation) map[string]domain.Endpoint {
	wildcards := make(map[string]domain.Endpoint)
	if !observation.Online {
		return wildcards
	}
	prefix := string(observation.HostID) + ":listener:"
	for _, endpoint := range observation.Endpoints {
		if endpoint.Binding == domain.BindingWildcard {
			wildcards[prefix+endpoint.Key] = endpoint
		}
	}
	return wildcards
}

func listenerWildcardKeys(observation domain.Observation) map[string]struct{} {
	keys := make(map[string]struct{}, len(observation.Endpoints))
	for key := range listenerWildcardEndpoints(observation) {
		keys[key] = struct{}{}
	}
	return keys
}

func listenerAddedSignal(hostID domain.HostID, key string, endpoint domain.Endpoint) domain.EventSignal {
	return domain.EventSignal{
		HostID: hostID, Kind: "listener.added", Severity: domain.SeverityWarning,
		DedupeKey: key, Title: fmt.Sprintf("新增公网绑定：%d/%s", endpoint.Port, endpoint.Protocol),
		Detail: fmt.Sprintf("%s · %s", endpoint.WorkloadKey, endpoint.Bind),
	}
}

func memoryTelemetryMissing(observation domain.Observation) bool {
	if observation.Resources == nil {
		return true
	}
	return observation.Resources.Memory.TotalBytes <= 0 || observationHasWarning(observation, "meminfo")
}

func diskTelemetryMissing(observation domain.Observation) bool {
	if observation.Resources == nil || observationHasWarning(observation, "采集磁盘失败") {
		return true
	}
	for _, disk := range observation.Resources.Disks {
		if disk.Mount == "/" {
			return false
		}
	}
	return true
}

func loadTelemetryMissing(observation domain.Observation) bool {
	if observation.Resources == nil {
		return true
	}
	return observation.Resources.CPUs <= 0 || observationHasWarning(observation, "loadavg")
}

func workloadCategoryMissing(observation domain.Observation, dedupeKey, hostPrefix string) bool {
	if observation.Workloads == nil || observationHasWarning(observation, warningServiceDiscoveryFailed) {
		return true
	}
	identity := strings.TrimSuffix(strings.TrimPrefix(dedupeKey, hostPrefix+"workload:"), ":failed")
	switch {
	case strings.HasPrefix(identity, "docker:"):
		return observationHasWarning(observation, "docker ps 失败")
	case strings.HasPrefix(identity, "systemd:"):
		return observationHasWarning(observation, "systemd unit 采集失败")
	default:
		return observationHasWarning(observation, "进程来源采集失败")
	}
}

func listenerTelemetryMissing(observation domain.Observation) bool {
	if observation.Workloads == nil {
		return true
	}
	return observationHasWarning(observation, warningServiceDiscoveryFailed, "docker ps 失败", "ss 采集失败")
}

func observationHasWarning(observation domain.Observation, needles ...string) bool {
	for _, warning := range observation.Warnings {
		for _, needle := range needles {
			if strings.Contains(warning, needle) {
				return true
			}
		}
	}
	return false
}

func sshFailureDetail(summary *domain.SSHAuthObservation) string {
	windowMinutes := int(summary.WindowEnd.Sub(summary.WindowStart).Minutes())
	if windowMinutes < 1 {
		windowMinutes = 1
	}
	detail := fmt.Sprintf("%d 分钟内 SSH 认证失败 %d 次", windowMinutes, summary.FailedTotal)
	if len(summary.Sources) == 0 {
		return detail
	}
	sources := append([]domain.SSHAuthSource(nil), summary.Sources...)
	sort.Slice(sources, func(left, right int) bool {
		if sources[left].Count != sources[right].Count {
			return sources[left].Count > sources[right].Count
		}
		return sources[left].Address < sources[right].Address
	})
	if len(sources) > 3 {
		sources = sources[:3]
	}
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, fmt.Sprintf("%s × %d", source.Address, source.Count))
	}
	return detail + "；主要来源 " + strings.Join(parts, "、")
}

func workloadFailed(workload domain.Workload) bool {
	return strings.EqualFold(strings.TrimSpace(workload.State), "failed") ||
		strings.EqualFold(strings.TrimSpace(workload.Health), "unhealthy")
}

func signalFromEvent(event domain.Event) domain.EventSignal {
	return domain.EventSignal{
		HostID: event.HostID, Kind: event.Kind, Severity: event.Severity,
		DedupeKey: event.DedupeKey, Title: event.Title, Detail: event.Detail,
	}
}

func boundedEventDetail(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	runes := []rune(value)
	if len(runes) > 1000 {
		value = string(runes[:1000])
	}
	return value
}

func sortedEventSignals(signals []domain.EventSignal) []domain.EventSignal {
	sort.Slice(signals, func(left, right int) bool {
		return signals[left].DedupeKey < signals[right].DedupeKey
	})
	return signals
}

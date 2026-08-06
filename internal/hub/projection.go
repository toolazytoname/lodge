package hub

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

// projectObservation is the anti-corruption boundary between the Agent v1 wire
// contract and the durable Hub domain. The legacy Exposure value is interpreted
// as a binding hint only; reachability always starts unknown.
func projectObservation(agent AgentConfig, online bool, lastError string, ping shared.Ping, status *shared.Status, services []shared.Service, observedAt time.Time) (domain.Observation, error) {
	observation := domain.Observation{
		HostID:       domain.HostID(agent.ID),
		ObservedAt:   observedAt.UTC(),
		Online:       online,
		LastError:    lastError,
		Hostname:     ping.Hostname,
		AgentVersion: ping.AgentVer,
		Workloads:    make([]domain.Workload, 0, len(services)),
		Endpoints:    make([]domain.Endpoint, 0),
	}
	if status != nil {
		if status.Hostname != "" {
			observation.Hostname = status.Hostname
		}
		observation.Warnings = append(observation.Warnings, status.Warnings...)
		observation.Resources = projectResources(status)
	}

	for _, service := range services {
		workload := domain.Workload{
			HostID:       domain.HostID(agent.ID),
			Key:          service.Key,
			Kind:         projectWorkloadKind(service.Kind),
			Name:         service.Name,
			State:        service.Status,
			Image:        service.Image,
			Unit:         service.Unit,
			Health:       service.Health,
			PID:          service.PID,
			Unidentified: service.Unidentified,
		}
		if service.Since != "" {
			if startedAt, err := time.Parse(time.RFC3339, service.Since); err == nil {
				startedAt = startedAt.UTC()
				workload.StartedAt = &startedAt
			} else {
				observation.Warnings = append(observation.Warnings, fmt.Sprintf("workload %s has invalid start time", service.Key))
			}
		}
		observation.Workloads = append(observation.Workloads, workload)

		seenEndpoints := make(map[string]struct{}, len(service.Ports))
		for _, port := range service.Ports {
			protocol := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(port.Proto)), "6")
			key := protocol + "://" + net.JoinHostPort(port.Bind, strconv.Itoa(port.Port))
			if _, duplicate := seenEndpoints[key]; duplicate {
				continue
			}
			seenEndpoints[key] = struct{}{}
			observation.Endpoints = append(observation.Endpoints, domain.Endpoint{
				HostID:       domain.HostID(agent.ID),
				WorkloadKey:  service.Key,
				Key:          key,
				Protocol:     protocol,
				Bind:         port.Bind,
				Port:         port.Port,
				Binding:      projectBinding(shared.ClassifyBind(port.Bind)),
				Reachability: domain.ReachabilityUnknown,
			})
		}
	}
	if err := observation.Validate(); err != nil {
		return domain.Observation{}, err
	}
	return observation, nil
}

func projectWorkloadKind(kind shared.Kind) domain.WorkloadKind {
	switch kind {
	case shared.KindDocker:
		return domain.WorkloadDocker
	case shared.KindSystemd:
		return domain.WorkloadSystemd
	default:
		return domain.WorkloadProcess
	}
}

func projectBinding(exposure shared.Exposure) domain.BindingScope {
	switch exposure {
	case shared.ExposureLocal:
		return domain.BindingLocal
	case shared.ExposureTailnet:
		return domain.BindingTailnet
	case shared.ExposurePublic:
		return domain.BindingWildcard
	case shared.ExposureOther:
		return domain.BindingInterface
	default:
		return domain.BindingUnknown
	}
}

func projectResources(status *shared.Status) *domain.Resources {
	resources := &domain.Resources{
		CPUs:   status.Load.CPUs,
		Load1:  status.Load.One,
		Load5:  status.Load.Five,
		Load15: status.Load.Fifteen,
		Memory: domain.MemoryResources{
			TotalBytes:     status.Memory.TotalBytes,
			AvailableBytes: status.Memory.AvailableBytes,
			UsedBytes:      status.Memory.UsedBytes,
			SwapTotalBytes: status.Memory.SwapTotalBytes,
			SwapUsedBytes:  status.Memory.SwapUsedBytes,
		},
		Disks: make([]domain.DiskResources, 0, len(status.Disks)),
	}
	for _, disk := range status.Disks {
		resources.Disks = append(resources.Disks, domain.DiskResources{
			Mount: disk.Mount, Filesystem: disk.Filesystem, TotalBytes: disk.TotalBytes,
			FreeBytes: disk.FreeBytes, UsedBytes: disk.UsedBytes,
		})
	}
	if status.Docker != nil {
		resources.Docker = &domain.DockerResources{
			Containers: status.Docker.Containers, ContainersRunning: status.Docker.ContainersRunning,
			Images: status.Docker.Images, Volumes: status.Docker.Volumes,
			ReclaimableBytes: status.Docker.ReclaimableBytes, TotalBytes: status.Docker.TotalBytes,
		}
	}
	return resources
}

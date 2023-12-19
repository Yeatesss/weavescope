package kubernetes

import (
	"fmt"
	"github.com/weaveworks/scope/tools/vars"
	"strconv"
	"strings"

	"github.com/weaveworks/scope/report"

	apiv1 "k8s.io/api/core/v1"
)

// These constants are keys used in node metadata
const (
	State            = report.KubernetesState
	IsInHostNetwork  = report.KubernetesIsInHostNetwork
	IsStaticPod      = report.KubernetesIsStaticPod
	RestartCount     = report.KubernetesRestartCount
	HostIP           = report.KubernetesHostIP
	NodeName         = report.KubernetesHostName
	ResourceLimitCpu = report.KubernetesLimitCpu
	ResourceLimitMem = report.KubernetesLimitMem
)

// Pod represents a Kubernetes pod
type Pod interface {
	Meta
	AddParent(topology, id string)
	NodeName() string
	GetNode(probeID string) report.Node
	RestartCount() uint
	ContainerNames() []string
	VolumeClaimNames() []string
}

type pod struct {
	*apiv1.Pod
	Meta
	parents report.Sets
	Node    *apiv1.Node
}

// NewPod creates a new Pod
func NewPod(p *apiv1.Pod) Pod {
	return &pod{
		Pod:     p,
		Meta:    meta{p.ObjectMeta},
		parents: report.MakeSets(),
	}
}

func (p *pod) UID() string {
	// Work around for master pod not reporting the right UID.
	if hash, ok := p.ObjectMeta.Annotations["kubernetes.io/config.hash"]; ok {
		return hash
	}
	return p.Meta.UID()
}

func (p *pod) AddParent(topology, id string) {
	p.parents = p.parents.AddString(topology, id)
}

func (p *pod) State() string {
	if p.ObjectMeta.DeletionTimestamp != nil {
		return "Terminating"
	}

	return string(p.Status.Phase)
}

func (p *pod) NodeName() string {
	return p.Spec.NodeName
}

func (p *pod) RestartCount() uint {
	count := uint(0)
	for _, cs := range p.Status.ContainerStatuses {
		count += uint(cs.RestartCount)
	}
	return count
}

func (p *pod) VolumeClaimNames() []string {
	var claimNames []string
	for _, volume := range p.Spec.Volumes {
		if volume.VolumeSource.PersistentVolumeClaim != nil {
			claimNames = append(claimNames, volume.VolumeSource.PersistentVolumeClaim.ClaimName)
		}
	}
	return claimNames
}

func (p *pod) GetNode(probeID string) report.Node {
	var limits []string // container_name,cpu,mem
	latests := map[string]string{
		State:                 p.State(),
		ClusterUUID:           vars.ClusterUUID,
		IP:                    p.Status.PodIP,
		report.ControlProbeID: probeID,
		RestartCount:          strconv.FormatUint(uint64(p.RestartCount()), 10),
		HostIP:                p.Status.HostIP,
		NodeName:              p.Spec.NodeName,
	}

	if len(p.VolumeClaimNames()) > 0 {
		// PVC name consist of lower case alphanumeric characters, "-" or "."
		// and must start and end with an alphanumeric character.
		latests[VolumeClaim] = strings.Join(p.VolumeClaimNames(), report.ScopeDelim)
	}
	for _, container := range p.Spec.Containers {
		var (
			cpu string
			mem string
		)
		if container.Resources.Limits.Cpu().String() != "0" {
			cpu = container.Resources.Limits.Cpu().String()
		}
		if container.Resources.Limits.Memory().String() != "0" {
			mem = container.Resources.Limits.Memory().String()
		}
		limits = append(limits, fmt.Sprintf("%s,%s,%s", container.Name, cpu, mem))
	}
	if p.Pod.Spec.HostNetwork {
		latests[IsInHostNetwork] = "true"
	}
	for annotationKey, annotationValue := range p.Pod.Annotations {
		if annotationKey == "kubernetes.io/config.source" && (annotationValue == "file" || annotationValue == "http") {
			latests[report.KubernetesIsStaticPod] = "true"
		}
	}

	return p.MetaNode(report.MakePodNodeID(p.UID())).WithLatests(latests).
		WithParents(p.parents).
		WithSet("limit", limits).
		WithLatestActiveControls(GetLogs, DeletePod, Describe)
}

func (p *pod) ContainerNames() []string {
	containerNames := make([]string, 0, len(p.Pod.Spec.Containers))
	for _, c := range p.Pod.Spec.Containers {
		containerNames = append(containerNames, c.Name)
	}
	return containerNames
}

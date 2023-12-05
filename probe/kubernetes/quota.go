package kubernetes

import (
	"fmt"
	"github.com/weaveworks/scope/report"
	"github.com/weaveworks/scope/tools/vars"
	apiv1 "k8s.io/api/core/v1"
)

// Quota represents a Kubernetes quota
type ResourceQuota interface {
	Meta
	GetNode() report.Node
	//Selector() labels.Selector
}

type resourceQuota struct {
	*apiv1.ResourceQuota
	Meta
}

// NewResourceQuota creates a new Quota
func NewResourceQuota(s *apiv1.ResourceQuota) ResourceQuota {
	return &resourceQuota{ResourceQuota: s, Meta: meta{s.ObjectMeta}}
}

/*func (s *quota) Selector() labels.Selector {
	if s.Spec.Selector == nil {
		return labels.Nothing()
	}
	return labels.SelectorFromSet(labels.Set(s.Spec.Selector))
}*/

// human-readable version of a Kubernetes QuotaPort

func (s *resourceQuota) GetNode() report.Node {
	latest := map[string]string{
		ClusterUUID: vars.ClusterUUID,
	}
	for name, quantity := range s.Status.Hard {
		if string(name) == "cpu" {
			latest[fmt.Sprintf("kubernetes_requests.cpu")] = quantity.String()
			continue
		}
		if string(name) == "memory" {
			latest[fmt.Sprintf("kubernetes_requests.memory")] = quantity.String()
			continue
		}
		latest[fmt.Sprintf("kubernetes_%s", string(name))] = quantity.String()
	}
	latest[report.KubernetesName] = s.Name()
	return s.MetaNode(report.MakeResourceQuotaNodeID(s.UID())).WithLatests(latest)
}

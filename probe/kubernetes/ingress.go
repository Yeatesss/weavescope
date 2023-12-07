package kubernetes

import (
	"github.com/weaveworks/scope/report"
	"github.com/weaveworks/scope/tools/vars"
	v1 "k8s.io/api/networking/v1"
)

// Quota represents a Kubernetes quota
type Ingress interface {
	Meta
	GetNode() report.Node
	//Selector() labels.Selector
}

type ingress struct {
	*v1.Ingress
	Meta
}

// NewIngress creates a new Quota
func NewIngress(s *v1.Ingress) Ingress {
	return &ingress{Ingress: s, Meta: meta{s.ObjectMeta}}
}

/*func (s *quota) Selector() labels.Selector {
	if s.Spec.Selector == nil {
		return labels.Nothing()
	}
	return labels.SelectorFromSet(labels.Set(s.Spec.Selector))
}*/

// human-readable version of a Kubernetes QuotaPort

func (s *ingress) GetNode() report.Node {
	set := report.MakeSets()
	latest := map[string]string{
		ClusterUUID:  vars.ClusterUUID,
		"created_at": s.CreationTimestamp.Local().Format("2006-01-02 15:04:05"),
		Name:         s.Name(),
		Namespace:    s.Namespace(),
	}
	for _, lb := range s.Status.LoadBalancer.Ingress {
		set.AddString("load_balancer_ip", lb.IP)
	}
	for annotationKey, annotationVal := range s.Annotations {
		latest["kubernetes_label_annotation."+annotationKey] = annotationVal
	}
	for _, rule := range s.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			switch {
			case path.Backend.Service != nil:
				set.AddString("backend_service_name", path.Backend.Service.Name)

			case path.Backend.Resource != nil:
				set.AddString("backend_resource_name", path.Backend.Resource.Name)
			}
		}
	}
	latest[report.KubernetesName] = s.Name()
	return s.MetaNode(report.MakeIngressID(s.UID())).WithLatests(latest).WithSets(set)
}

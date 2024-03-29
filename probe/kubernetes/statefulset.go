package kubernetes

import (
	"fmt"
	"github.com/weaveworks/scope/tools/vars"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/weaveworks/scope/report"
)

// StatefulSet represents a Kubernetes statefulset
type StatefulSet interface {
	Meta
	Selector() (labels.Selector, error)
	GetNode(probeID string) report.Node
}

type statefulSet struct {
	*appsv1.StatefulSet
	Meta
}

// NewStatefulSet creates a new statefulset
func NewStatefulSet(s *appsv1.StatefulSet) StatefulSet {
	return &statefulSet{
		StatefulSet: s,
		Meta:        meta{s.ObjectMeta},
	}
}

func (s *statefulSet) Selector() (labels.Selector, error) {
	selector, err := metav1.LabelSelectorAsSelector(s.Spec.Selector)
	if err != nil {
		return nil, err
	}
	return selector, nil
}

func (s *statefulSet) GetNode(probeID string) report.Node {
	desiredReplicas := 1
	if s.Spec.Replicas != nil {
		desiredReplicas = int(*s.Spec.Replicas)
	}
	latests := map[string]string{
		NodeType:              "StatefulSet",
		DesiredReplicas:       fmt.Sprint(desiredReplicas),
		ClusterUUID:           vars.ClusterUUID,
		Replicas:              fmt.Sprint(s.Status.Replicas),
		report.ControlProbeID: probeID,
		ObservedGeneration:    fmt.Sprint(s.Status.ObservedGeneration),
	}
	return s.MetaNode(report.MakeStatefulSetNodeID(s.UID())).
		WithLatests(latests).
		WithLatestActiveControls(Describe)
}

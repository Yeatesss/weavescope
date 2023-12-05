package kubernetes

import (
	"bytes"
	jsoniter "github.com/json-iterator/go"
	"github.com/klauspost/compress/zstd"
	"github.com/weaveworks/scope/common/logger"
	"github.com/weaveworks/scope/report"
	"github.com/weaveworks/scope/tools/vars"
	"io"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strconv"
)

// NamespaceResource represents a Kubernetes namespace
// `Namespace` is already taken in meta.go
type NamespaceResource interface {
	Meta
	GetNode() report.Node
}

type namespace struct {
	ns *apiv1.Namespace
	Meta
}

// NewNamespace creates a new Namespace
func NewNamespace(ns *apiv1.Namespace) NamespaceResource {
	return &namespace{ns: ns, Meta: namespaceMeta{ns.ObjectMeta}}
}
func Compress(in io.Reader, out io.Writer) error {
	enc, err := zstd.NewWriter(out)
	if err != nil {
		return err
	}
	_, err = io.Copy(enc, in)
	if err != nil {
		enc.Close()
		return err
	}
	return enc.Close()
}
func (ns *namespace) GetNode() report.Node {
	var (
		applyConfigurationJson bytes.Buffer
	)
	err := jsoniter.NewEncoder(&applyConfigurationJson).Encode(struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	}{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              ns.ns.Name,
			Namespace:         ns.ns.Namespace,
			Labels:            ns.ns.Labels,
			CreationTimestamp: metav1.Time{},
			Annotations:       ns.ns.Annotations,
		},
	})
	if err != nil {
		logger.Logger.Errorf("json namespace apply configuration failed: %v", err)
	}
	latests := map[string]string{
		ClusterUUID: vars.ClusterUUID,
	}
	if applyConfigurationJson.Len() > 0 {
		latests[ApplyConfiguration] = applyConfigurationJson.String()
	}
	latests[Created] = strconv.FormatInt(ns.ns.ObjectMeta.CreationTimestamp.Unix(), 10)
	latests[Status] = string(ns.ns.Status.Phase)
	return ns.MetaNode(report.MakeNamespaceNodeID(ns.UID())).WithLatests(latests)
}

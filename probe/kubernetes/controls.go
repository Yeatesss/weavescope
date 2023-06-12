package kubernetes

import (
	"io"
	"io/ioutil"

	"github.com/weaveworks/scope/common/xfer"
	"github.com/weaveworks/scope/probe/controls"
	"github.com/weaveworks/scope/report"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Control IDs used by the kubernetes integration.
const (
	GetLogs      = report.KubernetesGetLogs
	Describe     = report.KubernetesDescribe
	DeletePod    = report.KubernetesDeletePod
	ScaleUp      = report.KubernetesScaleUp
	ScaleDown    = report.KubernetesScaleDown
	CordonNode   = report.KubernetesCordonNode
	UncordonNode = report.KubernetesUncordonNode
)

// GroupName and version used by CRDs
const (
	SnapshotGroupName = "volumesnapshot.external-storage.k8s.io"
	SnapshotVersion   = "v1"
)

// GetLogs is the control to get the logs for a kubernetes pod
func (r *Reporter) GetLogs(req xfer.Request, namespaceID, podID string, containerNames []string) xfer.Response {
	readCloser, err := r.client.GetLogs(namespaceID, podID, containerNames)
	if err != nil {
		return xfer.ResponseError(err)
	}

	readWriter := struct {
		io.Reader
		io.Writer
	}{
		readCloser,
		ioutil.Discard,
	}
	id, pipe, err := controls.NewPipeFromEnds(nil, readWriter, r.pipes, req.AppID)
	if err != nil {
		return xfer.ResponseError(err)
	}
	pipe.OnClose(func() {
		readCloser.Close()
	})
	return xfer.Response{
		Pipe: id,
	}
}

func (r *Reporter) describePod(req xfer.Request, namespaceID, podID string, _ []string) xfer.Response {
	return r.describe(req, namespaceID, podID, ResourceMap["Pod"], apimeta.RESTMapping{})
}

func (r *Reporter) describeDeployment(req xfer.Request, namespaceID, deploymentID string) xfer.Response {
	return r.describe(req, namespaceID, deploymentID, ResourceMap["Deployment"], apimeta.RESTMapping{})
}

func (r *Reporter) describeService(req xfer.Request, namespaceID, serviceID string) xfer.Response {
	return r.describe(req, namespaceID, serviceID, ResourceMap["Service"], apimeta.RESTMapping{})
}

func (r *Reporter) describeCronJob(req xfer.Request, namespaceID, cronJobID string) xfer.Response {
	return r.describe(req, namespaceID, cronJobID, ResourceMap["CronJob"], apimeta.RESTMapping{})
}

func (r *Reporter) describePV(req xfer.Request, PVID string) xfer.Response {
	return r.describe(req, "", PVID, ResourceMap["PersistentVolume"], apimeta.RESTMapping{})
}

func (r *Reporter) describeDaemonSet(req xfer.Request, namespaceID, daemonSetID string) xfer.Response {
	return r.describe(req, namespaceID, daemonSetID, ResourceMap["DaemonSet"], apimeta.RESTMapping{})
}

func (r *Reporter) describeStatefulSet(req xfer.Request, namespaceID, statefulSetID string) xfer.Response {
	return r.describe(req, namespaceID, statefulSetID, ResourceMap["StatefulSet"], apimeta.RESTMapping{})
}

func (r *Reporter) describeStoragelass(req xfer.Request, storageClassID string) xfer.Response {
	return r.describe(req, "", storageClassID, ResourceMap["StorageClass"], apimeta.RESTMapping{})
}

func (r *Reporter) describeJob(req xfer.Request, namespaceID, jobID string) xfer.Response {
	return r.describe(req, namespaceID, jobID, ResourceMap["Job"], apimeta.RESTMapping{})
}

func (r *Reporter) describeVolumeSnapshot(req xfer.Request, namespaceID, volumeSnapshotID, _, _ string) xfer.Response {
	restMapping := apimeta.RESTMapping{
		Resource: schema.GroupVersionResource{
			Group:    SnapshotGroupName,
			Version:  SnapshotVersion,
			Resource: "volumesnapshots",
		},
	}
	return r.describe(req, namespaceID, volumeSnapshotID, schema.GroupKind{}, restMapping)
}

func (r *Reporter) describeVolumeSnapshotData(req xfer.Request, volumeSnapshotID string) xfer.Response {
	restMapping := apimeta.RESTMapping{
		Resource: schema.GroupVersionResource{
			Group:    SnapshotGroupName,
			Version:  SnapshotVersion,
			Resource: "volumesnapshotdatas",
		},
	}
	return r.describe(req, "", volumeSnapshotID, schema.GroupKind{}, restMapping)
}

// GetLogs is the control to get the logs for a kubernetes pod
func (r *Reporter) describe(req xfer.Request, namespaceID, resourceID string, groupKind schema.GroupKind, restMapping apimeta.RESTMapping) xfer.Response {
	readCloser, err := r.client.Describe(namespaceID, resourceID, groupKind, restMapping)
	if err != nil {
		return xfer.ResponseError(err)
	}

	readWriter := struct {
		io.Reader
		io.Writer
	}{
		readCloser,
		ioutil.Discard,
	}
	id, pipe, err := controls.NewPipeFromEnds(nil, readWriter, r.pipes, req.AppID)
	if err != nil {
		return xfer.ResponseError(err)
	}
	pipe.OnClose(func() {
		readCloser.Close()
	})
	return xfer.Response{
		Pipe: id,
	}
}

func (r *Reporter) deletePod(req xfer.Request, namespaceID, podID string, _ []string) xfer.Response {
	if err := r.client.DeletePod(namespaceID, podID); err != nil {
		return xfer.ResponseError(err)
	}
	return xfer.Response{
		RemovedNode: req.NodeID,
	}
}

// Describe will parse the nodeID and return response according to the node (resource) type.
func (r *Reporter) Describe() func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		var f func(req xfer.Request) xfer.Response
		_, tag, ok := report.ParseNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		switch tag {
		case "<pod>":
			f = r.CapturePod(r.describePod)
		case "<cronjob>":
			f = r.CaptureCronJob(r.describeCronJob)
		case "<deployment>":
			f = r.CaptureDeployment(r.describeDeployment)
		case "<daemonset>":
			f = r.CaptureDaemonSet(r.describeDaemonSet)
		case "<persistent_volume>":
			f = r.CapturePersistentVolume(r.describePV)
		case "<storage_class>":
			f = r.CaptureStorageClass(r.describeStoragelass)
		case "<statefulset>":
			f = r.CaptureStatefulSet(r.describeStatefulSet)
		case "<job>":
			f = r.CaptureJob(r.describeJob)
		default:
			return xfer.ResponseErrorf("Node not found: %s", req.NodeID)
		}
		return f(req)
	}
}

// CapturePod is exported for testing
func (r *Reporter) CapturePod(f func(xfer.Request, string, string, []string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParsePodNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		// find pod by UID
		var pod Pod
		r.client.WalkPods(func(p Pod) error {
			if p.UID() == uid {
				pod = p
			}
			return nil
		})
		if pod == nil {
			return xfer.ResponseErrorf("Pod not found: %s", uid)
		}
		return f(req, pod.Namespace(), pod.Name(), pod.ContainerNames())
	}
}

// CaptureDeployment is exported for testing
func (r *Reporter) CaptureDeployment(f func(xfer.Request, string, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParseDeploymentNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		var deployment Deployment
		r.client.WalkDeployments(func(d Deployment) error {
			if d.UID() == uid {
				deployment = d
			}
			return nil
		})
		if deployment == nil {
			return xfer.ResponseErrorf("Deployment not found: %s", uid)
		}
		return f(req, deployment.Namespace(), deployment.Name())
	}
}

// CaptureDaemonSet is exported for testing
func (r *Reporter) CaptureDaemonSet(f func(xfer.Request, string, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParseDaemonSetNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		var daemonSet DaemonSet
		r.client.WalkDaemonSets(func(d DaemonSet) error {
			if d.UID() == uid {
				daemonSet = d
			}
			return nil
		})
		if daemonSet == nil {
			return xfer.ResponseErrorf("Daemon Set not found: %s", uid)
		}
		return f(req, daemonSet.Namespace(), daemonSet.Name())
	}
}

// CaptureCronJob is exported for testing
func (r *Reporter) CaptureCronJob(f func(xfer.Request, string, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParseCronJobNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		var cronJob CronJob
		r.client.WalkCronJobs(func(c CronJob) error {
			if c.UID() == uid {
				cronJob = c
			}
			return nil
		})
		if cronJob == nil {
			return xfer.ResponseErrorf("Cron Job not found: %s", uid)
		}
		return f(req, cronJob.Namespace(), cronJob.Name())
	}
}

// CaptureStatefulSet is exported for testing
func (r *Reporter) CaptureStatefulSet(f func(xfer.Request, string, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParseStatefulSetNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		var statefulSet StatefulSet
		r.client.WalkStatefulSets(func(s StatefulSet) error {
			if s.UID() == uid {
				statefulSet = s
			}
			return nil
		})
		if statefulSet == nil {
			return xfer.ResponseErrorf("Stateful Set not found: %s", uid)
		}
		return f(req, statefulSet.Namespace(), statefulSet.Name())
	}
}

// CaptureStorageClass is exported for testing
func (r *Reporter) CaptureStorageClass(f func(xfer.Request, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParseStorageClassNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		var storageClass StorageClass
		r.client.WalkStorageClasses(func(s StorageClass) error {
			if s.UID() == uid {
				storageClass = s
			}
			return nil
		})
		if storageClass == nil {
			return xfer.ResponseErrorf("StorageClass not found: %s", uid)
		}
		return f(req, storageClass.Name())
	}
}

// CapturePersistentVolume will return name of PV
func (r *Reporter) CapturePersistentVolume(f func(xfer.Request, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParsePersistentVolumeNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		// find persistentVolume by UID
		var persistentVolume PersistentVolume
		r.client.WalkPersistentVolumes(func(p PersistentVolume) error {
			if p.UID() == uid {
				persistentVolume = p
			}
			return nil
		})
		if persistentVolume == nil {
			return xfer.ResponseErrorf("Persistent volume  not found: %s", uid)
		}
		return f(req, persistentVolume.Name())
	}
}

// CaptureJob is exported for testing
func (r *Reporter) CaptureJob(f func(xfer.Request, string, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		uid, ok := report.ParseJobNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		var job Job
		r.client.WalkJobs(func(c Job) error {
			if c.UID() == uid {
				job = c
			}
			return nil
		})
		if job == nil {
			return xfer.ResponseErrorf("Job not found: %s", uid)
		}
		return f(req, job.Namespace(), job.Name())
	}
}

// CaptureNode is exported for testing
func (r *Reporter) CaptureNode(f func(xfer.Request, string) xfer.Response) func(xfer.Request) xfer.Response {
	return func(req xfer.Request) xfer.Response {
		nodeID, ok := report.ParseHostNodeID(req.NodeID)
		if !ok {
			return xfer.ResponseErrorf("Invalid ID: %s", req.NodeID)
		}
		return f(req, nodeID)
	}
}

// ScaleUp is the control to scale up a deployment
func (r *Reporter) ScaleUp(req xfer.Request, namespace, id string) xfer.Response {
	return xfer.ResponseError(r.client.ScaleUp(namespace, id))
}

// ScaleDown is the control to scale up a deployment
func (r *Reporter) ScaleDown(req xfer.Request, namespace, id string) xfer.Response {
	return xfer.ResponseError(r.client.ScaleDown(namespace, id))
}

// CordonNode is the control to cordon a node.
func (r *Reporter) CordonNode(req xfer.Request, name string) xfer.Response {
	return xfer.ResponseError(r.client.CordonNode(name, true))
}

// UncordonNode is the control to un-cordon a node.
func (r *Reporter) UncordonNode(req xfer.Request, name string) xfer.Response {
	return xfer.ResponseError(r.client.CordonNode(name, false))
}

func (r *Reporter) registerControls() {
	controls := map[string]xfer.ControlHandlerFunc{
		GetLogs:      r.CapturePod(r.GetLogs),
		Describe:     r.Describe(),
		DeletePod:    r.CapturePod(r.deletePod),
		ScaleUp:      r.CaptureDeployment(r.ScaleUp),
		ScaleDown:    r.CaptureDeployment(r.ScaleDown),
		CordonNode:   r.CaptureNode(r.CordonNode),
		UncordonNode: r.CaptureNode(r.UncordonNode),
	}
	r.handlerRegistry.Batch(nil, controls)
}

func (r *Reporter) deregisterControls() {
	controls := []string{
		GetLogs,
		Describe,
		DeletePod,
		ScaleUp,
		ScaleDown,
		CordonNode,
		UncordonNode,
	}
	r.handlerRegistry.Batch(controls, nil)
}

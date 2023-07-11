package docker

import (
	"github.com/Yeatesss/container-software/core"
	yprocess "github.com/Yeatesss/container-software/pkg/proc/process"
	"github.com/dolthub/swiss"
	"github.com/weaveworks/common/mtime"
	"github.com/weaveworks/scope/probe/process"
	"github.com/weaveworks/scope/report"
	"strconv"
	"strings"
	"sync"
)

// Node metadata keys.
const (
	ContainerID = report.DockerContainerID
	Name        = report.Name
)

// These vars are exported for testing.
var (
	NewProcessTreeStub = process.NewTree
)

// Tagger is a tagger that tags Docker container information to process
// nodes that have a PID.
// It also populates the SwarmService topology if any of the associated docker labels are present.
type Tagger struct {
	registry   Registry
	procWalker process.Walker
}

// NewTagger returns a usable Tagger.
func NewTagger(registry Registry, procWalker process.Walker) *Tagger {
	return &Tagger{
		registry:   registry,
		procWalker: procWalker,
	}
}

// Name of this tagger, for metrics gathering
func (Tagger) Name() string { return "Docker" }

// Tag implements Tagger.
func (t *Tagger) Tag(r report.Report) (report.Report, error) {
	tree, err := NewProcessTreeStub(t.procWalker)
	if err != nil {
		return report.MakeReport(), err
	}
	t.tag(tree, &r.Process, &r.Container)

	// Scan for Swarm service info
	for containerID, container := range r.Container.Nodes {
		serviceID, ok := container.Latest.Lookup(LabelPrefix + "com.docker.swarm.service.id")
		if !ok {
			continue
		}
		serviceName, ok := container.Latest.Lookup(LabelPrefix + "com.docker.swarm.service.name")
		if !ok {
			continue
		}
		stackNamespace, ok := container.Latest.Lookup(LabelPrefix + "com.docker.stack.namespace")
		if !ok {
			stackNamespace = DefaultNamespace
		} else {
			prefix := stackNamespace + "_"
			if strings.HasPrefix(serviceName, prefix) {
				serviceName = serviceName[len(prefix):]
			}
		}

		nodeID := report.MakeSwarmServiceNodeID(serviceID)
		node := report.MakeNodeWith(nodeID, map[string]string{
			ServiceName:    serviceName,
			StackNamespace: stackNamespace,
		})
		r.SwarmService.AddNode(node)

		r.Container.Nodes[containerID] = container.WithParent(report.SwarmService, nodeID)
	}

	return r, nil
}

func (t *Tagger) tag(tree process.Tree, topology *report.Topology, containerTopology *report.Topology) {
	var (
		ctrProcess = swiss.NewMap[string, core.Processes](42)
		pses       = swiss.NewMap[int64, yprocess.Process](42)
	)
	for _, node := range topology.Nodes {
		var (
			ok      bool
			ppidStr string
			ps      yprocess.Process
		)
		pidStr, ok := node.Latest.Lookup(process.PID)
		if !ok {
			continue
		}

		pid, err := strconv.ParseUint(pidStr, 10, 64)
		if err != nil {
			continue
		}

		var (
			c         Container
			candidate = int(pid)
		)

		t.registry.LockedPIDLookup(func(lookup func(int) Container) {
			for {
				c = lookup(candidate)
				if c != nil {
					break
				}

				candidate, err = tree.GetParent(candidate)
				if err != nil {
					break
				}
			}
		})
		if c == nil || ContainerIsStopped(c) || c.PID() == 1 {
			continue
		}
		var containerID = c.ID()

		for _, env := range c.Container().Config.Env {
			if strings.Contains(env, "PATH=") {
				SoftFinder.EnvPath.Set([]byte(containerID), []byte(env), 0)
				break
			}
		}
		var processes core.Processes
		processes, ok = ctrProcess.Get(containerID)
		if !ok {
			processes = core.Processes{}

		}
		//维护容器进程
		if ps, ok = pses.Get(int64(pid)); !ok {
			ps = yprocess.NewProcess(int64(pid), []int64{})
			pses.Put(int64(pid), ps)
		}
		//_ = processes

		ctrProcess.Put(containerID, append(processes, &core.Process{Process: ps}))
		if ppidStr, ok = node.Latest.Lookup(process.PPID); ok && c.Container().State.Pid != int(pid) {
			//维护父进程的childPid数据
			var ppidInt64 int64
			ppidInt64, err = strconv.ParseInt(ppidStr, 10, 64)
			if err == nil {
				var pps yprocess.Process
				if pps, ok = pses.Get(ppidInt64); !ok {
					pps = yprocess.NewProcess(ppidInt64, []int64{int64(pid)})
				} else {
					pps.SetChildPids(append(pps.ChildPids(), int64(pid)))
				}
				pses.Put(ppidInt64, pps)

			}
		}
		node = node.WithLatest(ContainerID, mtime.Now(), containerID)
		node = node.WithParent(report.Container, report.MakeContainerNodeID(containerID))

		// If we can work out the image name, add a parent tag for it
		image, ok := t.registry.GetContainerImage(c.Image())
		if ok && len(image.RepoTags) > 0 {
			imageName := ImageNameWithoutTag(image.RepoTags[0])
			node = node.WithParent(report.ContainerImage, report.MakeContainerImageNodeID(imageName))
		}

		topology.ReplaceNode(node)
	}
	var containerLocker sync.RWMutex
	var getContainerTopology = func(containerID string) (node report.Node, exists bool) {
		containerLocker.RLock()
		defer containerLocker.RUnlock()
		node, exists = containerTopology.Nodes[report.MakeContainerNodeID(containerID)]
		return
	}
	var replaceContainerTopology = func(containerID string, node report.Node) {
		containerLocker.RLock()
		containerTopology.Nodes[report.MakeContainerNodeID(containerID)] = node
		return
	}
	ctrProcess.Iter(func(id string, ps core.Processes) (stop bool) {

		envPath, _ := SoftFinder.EnvPath.Get([]byte(id))
		container := &core.Container{
			Id:        id,
			EnvPath:   string(envPath),
			Processes: ps,
		}
		if node, exists := getContainerTopology(id); exists {
			replaceContainerTopology(id, SoftFinder.ParseNodeSet(node, container))
		}
		return false
	})

}

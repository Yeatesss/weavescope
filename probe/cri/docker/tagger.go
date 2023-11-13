package docker

import (
	"bytes"
	"context"
	"fmt"
	"github.com/weaveworks/common/mtime"
	"github.com/weaveworks/scope/probe/process"
	"github.com/weaveworks/scope/report"

	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Yeatesss/container-software/core"
	"github.com/Yeatesss/container-software/pkg/command"
	yprocess "github.com/Yeatesss/container-software/pkg/proc/process"
	"github.com/coocood/freecache"
	"github.com/dolthub/swiss"
	docker_client "github.com/fsouza/go-dockerclient"
	jsoniter "github.com/json-iterator/go"
	log "github.com/sirupsen/logrus"
	"github.com/weaveworks/scope/probe/cri"
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
	registry   cri.Registry[*docker_client.Container]
	procWalker process.Walker
}

// NewTagger returns a usable Tagger.
func NewTagger(registry cri.Registry[*docker_client.Container], procWalker process.Walker) *Tagger {
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
	//fmt.Println("[time]loopcontainer", time.Now().Sub(now))

	return r, nil
}

var bindPorts = freecache.NewCache(1024 * 64)
var nsPids = freecache.NewCache(1024 * 64)

func SetSuspectMap(containerID, imageName string) {
	if strings.Contains(imageName, "mongo") ||
		strings.Contains(imageName, "postgre") ||
		strings.Contains(imageName, "redis") ||
		strings.Contains(imageName, "sqlserver") ||
		strings.Contains(imageName, "jboss") ||
		strings.Contains(imageName, "nginx") {
		cri.SuspectMap.Set([]byte(containerID), []byte("1"), 60)
	} else {
		cri.SuspectMap.Set([]byte(containerID), []byte("0"), 60)
	}
}

func (t *Tagger) tag(tree process.Tree, topology *report.Topology, containerTopology *report.Topology) {
	var (
		ctrProcess = swiss.NewMap[string, core.Processes](42)
		pidMap     = swiss.NewMap[string, string](42)
		pses       = swiss.NewMap[int64, yprocess.Process](42)
		ctrs       []*core.Container
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
			c         cri.Container[*docker_client.Container]
			candidate = int(pid)
		)

		t.registry.LockedPIDLookup(func(lookup func(int) cri.Container[*docker_client.Container]) {
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

		pidMap.Put(pidStr, node.ID)
		//以下操作在当前进程存在对应容器的基础上
		var containerID = c.ID()
		node = node.WithLatest(ContainerID, mtime.Now(), containerID)
		node = node.WithParent(report.Container, report.MakeContainerNodeID(containerID))

		// If we can work out the image name, add a parent tag for it
		image, ok := t.registry.GetContainerImage(c.Image())
		if ok && len(image.RepoTags) > 0 {
			imageName := ImageNameWithoutTag(image.RepoTags[0])
			SetSuspectMap(containerID, imageName)
			node = node.WithParent(report.ContainerImage, report.MakeContainerImageNodeID(imageName))
		}
		if c.Container().Config.Labels["io.kubernetes.docker.type"] != "podsandbox" {
			for _, env := range c.Container().Config.Env {
				if strings.Contains(env, "PATH=") {
					cri.SoftFinder.EnvPath.Set([]byte(containerID), []byte(env), 0)
					break
				}
			}
			var labels []string
			for key, label := range c.Container().Config.Labels {
				labels = append(labels, fmt.Sprintf("%s:%s", key, label))
			}
			//新增master pid，用来验证容器唯一性，避免重启
			labels = append(labels, fmt.Sprintf("master_pid:%d", c.Container().State.Pid))
			if len(labels) > 0 {
				cri.SoftFinder.Labels.Set([]byte(containerID), []byte(strings.Join(labels, "[,]")), 0)
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

			//获取当前进程在容器内绑定端口信息
			var (
				endpoints       []Endpoint
				portsBindingSet report.StringSet
			)
			process := yprocess.NewProcess(int64(pid), nil)

			if endpointByte, err := bindPorts.Get([]byte(containerID + ":" + strconv.FormatInt(process.Pid(), 10))); err == nil {
				_ = jsoniter.Unmarshal(endpointByte, &endpoints)

			} else {
				endpoints, err = getBindingPorts(int64(pid))
				if err != nil {
					log.Errorf("Cannot get container process endpoint fail : %v, error: %v", candidate, err)
				} else {
					tmpEps, _ := jsoniter.Marshal(endpoints)
					if !bytes.Contains(tmpEps, []byte("udp")) {
						_ = bindPorts.Set([]byte(containerID+":"+strconv.FormatInt(process.Pid(), 10)), tmpEps, 60*60+rand.Intn(60))
					}
				}
			}
			var (
				nspid    string
				tmpNspid []byte
			)
			if tmpNspid, err = nsPids.Get([]byte(containerID + ":" + strconv.FormatInt(process.Pid(), 10))); err == nil {
				nspid = string(tmpNspid)
			} else {
				nspid = getNsPid(process)
				nsPids.Set([]byte(containerID+":"+strconv.FormatInt(process.Pid(), 10)), []byte(nspid), 0)
			}
			node = node.WithLatests(map[string]string{
				"inside_pid": nspid,
				"exe":        getExe(process),
				"user":       getUser(process, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
			})

			for _, endpoint := range endpoints {
				var tmpData = make([]string, 4, 4)
				portBinding := getBindingPortsSet(t.registry, containerID, endpoint.Port)
				tmpData[0] = endpoint.Port
				tmpData[1] = endpoint.Protocols
				tmpData[2] = portBinding.HostIP
				tmpData[3] = portBinding.HostPort
				portsBindingSet = portsBindingSet.Add(strings.Join(tmpData, ","))
			}
			if len(portsBindingSet) > 0 {
				node = node.WithSet("bind_ports", portsBindingSet)
			}

		} else {
			process := yprocess.NewProcess(int64(pid), nil)
			nspid := getNsPid(process)
			node = node.WithLatest("inside_pid", time.Now(), nspid)
			node = node.WithLatest("exe", time.Now(), getExe(process))
			node = node.WithLatest("user", time.Now(), getUser(process, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"))
		}

		node = node.WithLatest("is_container", time.Now(), "1")
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
		defer containerLocker.RUnlock()
		containerTopology.Nodes[report.MakeContainerNodeID(containerID)] = node
		return
	}

	var wg sync.WaitGroup
	ctrProcess.Iter(func(id string, ps core.Processes) (stop bool) {
		envPath, _ := cri.SoftFinder.EnvPath.Get([]byte(id))
		labels, _ := cri.SoftFinder.Labels.Get([]byte(id))
		labelMap := make(map[string]string)
		if len(labels) > 0 {
			exlabels := strings.Split(string(labels), "[,]")
			for _, exlabel := range exlabels {
				labelMap[strings.Split(exlabel, ":")[0]] = strings.Split(exlabel, ":")[1]
			}
		}
		container := &core.Container{
			Id:        id,
			EnvPath:   string(envPath),
			Labels:    labelMap,
			Processes: ps,
		}
		ctrs = append(ctrs, container)
		wg.Add(1)
		go func() {
			defer wg.Done()
			container.SetHypotheticalNspid()
		}()

		return false
	})

	wg.Wait()

	for _, ctr := range ctrs {
		for _, ps := range ctr.Processes {
			if ps.NsPid() > 0 {
				if s, ok := pidMap.Get(strconv.FormatInt(ps.Pid(), 10)); ok {
					topology.ReplaceNode(topology.Nodes[s].WithLatest("inside_pid", time.Now(), strconv.FormatInt(ps.NsPid(), 10)))
				}
			}
		}
		if node, exists := getContainerTopology(ctr.Id); exists {
			replaceContainerTopology(ctr.Id, cri.SoftFinder.ParseNodeSet(node, ctr))
		}
	}

}

type Endpoint struct {
	Protocols string
	Port      string
}

func getBindingPorts(pid int64) ([]Endpoint, error) {
	var endpoints []Endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ps := yprocess.NewProcess(pid, nil, command.DisableCache)

	eps, err := core.GetEndpoint(ctx, ps)
	if err != nil {
		return endpoints, err
	}
	for _, endpoint := range eps {
		ports := strings.Split(endpoint, "/")
		if len(ports) == 2 {
			idx := strings.LastIndex(ports[1], ":")

			endpoints = append(endpoints, Endpoint{
				Protocols: ports[0],
				Port:      ports[1][idx+1:],
			})

		}
	}
	return endpoints, nil
}
func getNsPid(ps yprocess.Process) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nsPids, err := ps.NsPids(ctx)
	if err != nil {
		log.Errorf("Get process nspid fail:%v", ps.Pid())
		return ""
	}
	if len(nsPids) > 0 {
		return nsPids[len(nsPids)-1]
	}
	return ""
}

var execCache = freecache.NewCache(1024 * 1024)

func getExe(ps yprocess.Process) string {
	var exeByte []byte
	pidStr := strconv.FormatInt(ps.Pid(), 10)
	defer func() {
		if len(exeByte) > 0 {
			execCache.Set([]byte(pidStr), exeByte, 60*2)

		}
	}()
	exeByte, _ = execCache.Get([]byte(pidStr))
	if len(exeByte) > 0 {
		return string(exeByte)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exe, err := ps.Exe(ctx)
	if err != nil {
		log.Errorf("Get process exe fail:%v", ps.Pid())
		return ""
	}
	if exe.Len() > 0 {
		exeByte, _ = command.ReadField(exe.Bytes(), 11)
		return string(exeByte)
	}
	return ""
}

var userCache = freecache.NewCache(1024 * 1024)

func getUser(ps yprocess.Process, envPath string) string {
	var userByte []byte
	pidStr := strconv.FormatInt(ps.Pid(), 10)
	defer func() {
		if len(userByte) > 0 {
			userCache.Set([]byte(pidStr), userByte, 60*2)

		}
	}()
	userByte, _ = userCache.Get([]byte(pidStr))
	if len(userByte) > 0 {
		return string(userByte)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	user, err := core.GetRunUser(ctx, ps, envPath)
	if err != nil {
		log.Errorf("Get process user fail:%v", ps.Pid())
		return ""
	}
	userByte = []byte(user)
	return user
}
func getBindingPortsSet(registry cri.Registry[*docker_client.Container], containerID, port string) cri.PortBinding {
	portBinding := registry.GetContainerPortsBinding(containerID)
	if portBinding == nil || len(portBinding) == 0 {
		return cri.PortBinding{}
	}
	return portBinding[port]
}

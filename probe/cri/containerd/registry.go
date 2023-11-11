package containerd

import (
	"context"
	"errors"
	"fmt"
	"github.com/coocood/freecache"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd"
	_ "github.com/containerd/containerd/api/events"
	apiEvent "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/typeurl/v2"
	docker_client "github.com/fsouza/go-dockerclient"
	"github.com/weaveworks/scope/probe/cri"

	common_controls "github.com/weaveworks/scope/common/controls"

	"github.com/armon/go-radix"
	log "github.com/sirupsen/logrus"

	"github.com/weaveworks/scope/probe/controls"
	"github.com/weaveworks/scope/report"
)

// NewContainerStub Vars exported for testing.
var (
	NewContainerStub = NewContainer
)

type registry struct {
	sync.RWMutex
	quit                   chan chan struct{}
	interval               time.Duration
	collectStats           bool
	client                 cri.Client
	pipes                  controls.PipeClient
	hostID                 string
	handlerRegistry        *controls.HandlerRegistry
	noCommandLineArguments bool
	noEnvironmentVariables bool

	watchers        []cri.ContainerUpdateWatcher
	containers      *radix.Tree
	ctrBindPorts    map[string]map[string]cri.PortBinding // map[containerID]map[containerPort]hostIP+hostPort
	containersByPID map[int]cri.Container[*docker_client.Container]
	images          map[string]docker_client.APIImages
	usedImages      map[string]docker_client.APIImages
	networks        []docker_client.Network
	pipeIDToexecID  map[string]string
	controlChannel  chan *common_controls.ControlAction
}

// RegistryOptions are used to initialize the Registry
type RegistryOptions struct {
	Interval               time.Duration
	Pipes                  controls.PipeClient
	CollectStats           bool
	HostID                 string
	HandlerRegistry        *controls.HandlerRegistry
	ContainerdEndpoint     string
	NoCommandLineArguments bool
	NoEnvironmentVariables bool
	ControlActions         chan *common_controls.ControlAction
}

var _ cri.Client = &CriClient{}
var _ cri.Registry[*docker_client.Container] = &registry{}

// NewRegistry returns a usable Registry. Don't forget to Stop it.
func NewRegistry(options RegistryOptions) (cri.Registry[*docker_client.Container], error) {
	r := &registry{
		containers:             radix.New(),
		ctrBindPorts:           map[string]map[string]cri.PortBinding{},
		containersByPID:        map[int]cri.Container[*docker_client.Container]{},
		images:                 map[string]docker_client.APIImages{},
		usedImages:             map[string]docker_client.APIImages{},
		pipeIDToexecID:         map[string]string{},
		client:                 NewCriClient(options.ContainerdEndpoint),
		pipes:                  options.Pipes,
		interval:               options.Interval,
		collectStats:           options.CollectStats,
		hostID:                 options.HostID,
		handlerRegistry:        options.HandlerRegistry,
		quit:                   make(chan chan struct{}),
		noCommandLineArguments: options.NoCommandLineArguments,
		noEnvironmentVariables: options.NoEnvironmentVariables,
		controlChannel:         options.ControlActions,
	}
	r.registerControls()
	go r.loop()
	return r, nil
}

// Stop stops the Docker registry's event subscriber.
func (r *registry) Stop() {
	//r.deregisterControls()
	ch := make(chan struct{})
	r.quit <- ch
	<-ch
}

// WatchContainerUpdates registers a callback to be called
// whenever a container is updated.
func (r *registry) WatchContainerUpdates(f cri.ContainerUpdateWatcher) {
	r.Lock()
	defer r.Unlock()
	r.watchers = append(r.watchers, f)
}

func (r *registry) loop() {
	for {
		// NB listenForEvents blocks.
		// Returning false means we should exit.
		if !r.listenForEvents() {
			return
		}

		// Sleep here so we don't hammer the
		// logs if docker is down
		time.Sleep(r.interval)
	}
}

var event = make(<-chan *events.Envelope, 1024)
var checkEvent = make(chan *events.Envelope, 1024)

func (r *registry) listenForEvents() bool {
	// First we empty the store lists.
	// This ensure any containers that went away in between calls to
	// listenForEvents don't hang around.
	r.reset()

	// Next, start listening for event.  We do this before fetching
	// the list of containers so we don't miss containers created
	// after listing but before listening for events.
	// Use a buffered chan so the client library can run ahead of the listener
	// - Docker will drop an event if it is not collected quickly enough.
	errs := make(<-chan error, 1024)
	cancelCtx, cancel := context.WithCancel(context.Background())
	go func() {
		cli := r.client.(*CriClient)
		event, errs = cli.client.EventService().Subscribe(cancelCtx)

	}()

	defer func() {
		cancel()
	}()

	if err := r.updateContainers(); err != nil {
		log.Errorf("docker registry: %s", err)
		return true
	}

	if err := r.updateImages(); err != nil {
		log.Errorf("docker registry: %s", err)
		return true
	}

	if err := r.updateNetworks(); err != nil {
		log.Errorf("docker registry: %s", err)
		return true
	}

	otherUpdates := time.Tick(r.interval)
	for {
		//fmt.Println(66666666, time.Now().Local().Format("2006-01-02 15:04:05"))
		select {
		case eve, ok := <-checkEvent:
			//fmt.Println(1111111, eve.Event)
			if !ok {
				log.Errorf("containerd registry: event listener unexpectedly disconnected")
				return true
			}
			r.handleEvent(eve)

		case eve, ok := <-event:
			//fmt.Println(22222222, eve.Event)
			if !ok {
				log.Errorf("containerd registry: event listener unexpectedly disconnected")
				return true
			}
			r.handleEvent(eve)

		case <-otherUpdates:
			//fmt.Println(333333333)
			if err := r.updateImages(); err != nil {
				log.Errorf("docker registry: %s", err)
				return true
			}
			if err := r.updateNetworks(); err != nil {
				log.Errorf("docker registry: %s", err)
				return true
			}
		case cherr := <-errs:
			//fmt.Println(444444444)
			log.Errorf("containerd event err: %s", cherr)
		case ch := <-r.quit:
			//fmt.Println(5555555)
			run := func() {
				r.Lock()
				defer r.Unlock()
				if r.collectStats {
					r.containers.Walk(func(_ string, c interface{}) bool {
						c.(cri.Container[*docker_client.Container]).StopGatheringStats()
						return false
					})
				}
				close(ch)
			}
			run()
			return false
		}
	}
}

//LockedPIDLookup runs f under a read lock, and gives f a function for
//use doing pid->container lookups.

func (r *registry) LockedPIDLookup(f func(func(int) cri.Container[*docker_client.Container])) {
	r.RLock()
	defer r.RUnlock()

	lookup := func(pid int) cri.Container[*docker_client.Container] {
		return r.containersByPID[pid]
	}

	f(lookup)
}

func (r *registry) GetContainer(id string) (cri.Container[*docker_client.Container], bool) {
	r.RLock()
	defer r.RUnlock()
	c, ok := r.containers.Get(id)
	if ok {
		return c.(cri.Container[*docker_client.Container]), true
	}
	return nil, false
}

func (r *registry) GetContainerByPrefix(prefix string) (cri.Container[*docker_client.Container], bool) {
	r.RLock()
	defer r.RUnlock()
	var out []interface{}
	r.containers.WalkPrefix(prefix, func(_ string, v interface{}) bool {
		out = append(out, v)
		return false
	})
	if len(out) == 1 {
		return out[0].(cri.Container[*docker_client.Container]), true
	}
	return nil, false
}

func (r *registry) reset() {
	r.Lock()
	defer r.Unlock()

	if r.collectStats {
		r.containers.Walk(func(_ string, c interface{}) bool {
			c.(cri.Container[*docker_client.Container]).StopGatheringStats()
			return false
		})
	}

	r.containers = radix.New()
	r.containersByPID = map[int]cri.Container[*docker_client.Container]{}
	r.images = map[string]docker_client.APIImages{}
	r.usedImages = map[string]docker_client.APIImages{}
	//r.networks = r.networks[:0]
}
func RangeNs(client *containerd.Client, f func(ns string) error) error {
	nses, err := client.NamespaceService().List(context.Background())
	if err != nil {
		return err
	}
	for _, ns := range nses {
		err = f(ns)
		if err != nil {
			return err
		}

	}
	return nil
}
func (r *registry) updateContainers() error {
	apiContainers, err := r.client.ListContainers(docker_client.ListContainersOptions{All: true})
	if err != nil {
		return err
	}
	//fmt.Println(len(apiContainers))
	for _, apiContainer := range apiContainers {
		ns := getNsFromAPIContainer(apiContainer)
		nsCtx := namespaces.WithNamespace(context.Background(), ns)
		r.updateContainerState(nsCtx, apiContainer.ID)
	}

	return nil
}

func (r *registry) updateImages() error {
	images, err := r.client.ListImages(docker_client.ListImagesOptions{})
	if err != nil {
		return err
	}
	r.Lock()
	defer r.Unlock()
	r.images = make(map[string]docker_client.APIImages)
	for _, image := range images {
		if strings.Contains(image.RepoTags[0], "@sha256:") || strings.HasPrefix(image.RepoTags[0], "sha256:") {
			continue
		}
		r.images[image.ID] = image
	}
	return nil
}

func (r *registry) updateNetworks() error {
	networks, err := r.client.ListNetworks()
	if err != nil {
		return err
	}

	r.Lock()
	r.networks = networks
	r.Unlock()

	return nil
}

var updateContainerState = func(r *registry, containerID string) error {
	nsCtx, err := r.findContainer(containerID)
	if err != nil {
		return err
	}
	r.updateContainerState(nsCtx, containerID)
	return nil
}

var updateDelContainerState = func(r *registry, containerID string) error {

	r.updateContainerState(namespaces.WithNamespace(context.Background(), "default"), containerID)
	return nil
}

var ctrCreateSign = freecache.NewCache(1024 * 1024)
var ctrTasks = make(map[string]int)

func (r *registry) handleEvent(eventAc *events.Envelope) {
	evt, err := typeurl.UnmarshalAny(eventAc.Event)
	if err != nil {
		log.Errorf("Unable to unmarshal event: %v", err)
	}
	//event.Time = env.Timestamp.Unix()
	switch v := evt.(type) {
	case *apiEvent.ContainerCreate:
		_, err = ctrCreateSign.Get([]byte(v.ID))
		if !errors.Is(err, freecache.ErrNotFound) {
			ctrCreateSign.Del([]byte(v.ID))
			updateContainerState(r, v.ID)
		} else {
			ctrCreateSign.Set([]byte(v.ID), []byte{}, 40)
			go func() {
				time.Sleep(20 * time.Second)
				_, err = ctrCreateSign.Get([]byte(v.ID))
				if !errors.Is(err, freecache.ErrNotFound) {
					checkEvent <- eventAc
				}
			}()
		}

	case *apiEvent.ContainerUpdate:
		updateContainerState(r, v.ID)
	case *apiEvent.TaskPaused:
		updateContainerState(r, v.ContainerID)
	case *apiEvent.TaskResumed:
		updateContainerState(r, v.ContainerID)
	case *apiEvent.TaskCreate:
		_, err = ctrCreateSign.Get([]byte(v.ContainerID))
		ctrTasks[v.ContainerID]++
		if !errors.Is(err, freecache.ErrNotFound) {
			ctrCreateSign.Del([]byte(v.ContainerID))
			updateContainerState(r, v.ContainerID)
		}
	case *apiEvent.TaskExit:
		ctrTasks[v.ContainerID]--
		if ctrTasks[v.ContainerID] <= 0 {
			delete(ctrTasks, v.ContainerID)
			fmt.Println("exit", v.ContainerID)
			updateContainerState(r, v.ContainerID)
		}

	case *apiEvent.ContainerDelete:
		updateDelContainerState(r, v.ID)

	case *apiEvent.NamespaceCreate:
		CtrStatsLister.nsChan <- &NsChan{
			Namespace: v.Name,
			Stats:     nil,
		}
	case *apiEvent.NamespaceDelete:
		CtrStatsLister.nsChan <- &NsChan{
			Namespace: v.Name,
			Remove:    true,
		}
		//	r.sendDeletedUpdate(event.ID)
	}
}

//func (r *registry) handleEvent(event *docker_client.APIEvents) {
//	// TODO: Send shortcut reports on networks being created/destroyed?
//	switch event.Status {
//	case CreateEvent, RenameEvent, StartEvent, DieEvent, PauseEvent, UnpauseEvent, NetworkConnectEvent, NetworkDisconnectEvent:
//		r.updateContainerState(event.ID)
//	case DestroyEvent:
//		r.Lock()
//		r.deleteContainer(event.ID)
//		r.Unlock()
//		r.sendDeletedUpdate(event.ID)
//	}
//}

func (r *registry) updateContainerState(nsCtx context.Context, containerID string) {
	r.Lock()
	defer r.Unlock()

	dockerContainer, err := r.client.InspectContainerWithContext(containerID, nsCtx)
	if err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			// Docker says the container doesn't exist - remove it from our data
			r.deleteContainer(containerID)
			return
		}
		log.Errorf("Unable to get status for container %s: %v", containerID, err)

		return
	}
	dockerContainer.Config.Hostname = r.hostID
	// Container exists, ensure we have it
	o, ok := r.containers.Get(containerID)
	var c cri.Container[*docker_client.Container]
	if !ok {
		c = NewContainerStub(dockerContainer, r.hostID, r.noCommandLineArguments, r.noEnvironmentVariables)
		r.containers.Insert(containerID, c)
	} else {
		c = o.(cri.Container[*docker_client.Container])
		// potentially remove existing pid mapping.
		delete(r.containersByPID, c.PID())
		c.UpdateState(dockerContainer)
	}

	// Update PID index
	if c.PID() > 1 {
		r.containersByPID[c.PID()] = c
	}

	// Trigger anyone watching for updates
	node := c.GetNode()
	for _, f := range r.watchers {
		f(node)
	}

	// And finally, ensure we gather stats for it
	if r.collectStats {
		if dockerContainer.State.Running {
			if err := c.StartGatheringStats(r.client); err != nil {
				log.Errorf("Error gathering stats for container %s: %s", containerID, err)
				return
			}
		} else {
			c.StopGatheringStats()
		}
	}
}

func (r *registry) deleteContainer(containerID string) {
	// Container doesn't exist anymore, so lets stop and remove it
	c, ok := r.containers.Get(containerID)
	if !ok {
		return
	}
	container := c.(cri.Container[*docker_client.Container])

	r.containers.Delete(containerID)
	delete(r.containersByPID, container.PID())
	if r.collectStats {
		container.StopGatheringStats()
	}
}

func (r *registry) sendDeletedUpdate(containerID string) {
	node := report.MakeNodeWith(report.MakeContainerNodeID(containerID), map[string]string{
		ContainerID:    containerID,
		ContainerState: report.StateDeleted,
	})
	// Trigger anyone watching for updates
	for _, f := range r.watchers {
		f(node)
	}
}

// WalkContainers runs f on every running containers the registry knows of.
func (r *registry) WalkContainers(f func(cri.Container[*docker_client.Container])) {
	r.RLock()
	defer r.RUnlock()

	r.containers.Walk(func(_ string, c interface{}) bool {
		f(c.(cri.Container[*docker_client.Container]))
		return false
	})
}

func (r *registry) GetContainerImage(id string) (docker_client.APIImages, bool) {
	r.RLock()
	defer r.RUnlock()
	image, ok := r.images[PauseSha256(id)]
	return image, ok
}

func (r *registry) ReplacePortsBinding(m map[string]map[string]cri.PortBinding) {
	r.ctrBindPorts = m
}

func (r *registry) GetContainerPortsBinding(containerID string) map[string]cri.PortBinding {
	r.RLock()
	defer r.RUnlock()
	return r.ctrBindPorts[containerID]
}

// WalkImages runs f on every image of running containers the registry
// knows of.  f may be run on the same image more than once.
func (r *registry) WalkImages(f func(docker_client.APIImages)) {
	r.RLock()
	defer r.RUnlock()

	r.usedImages = make(map[string]docker_client.APIImages)
	// Loop over containers so we only emit images for running containers.
	r.containers.Walk(func(ctrID string, c interface{}) bool {
		image, ok := r.images[PauseSha256(c.(cri.Container[*docker_client.Container]).Image())]

		if ok {
			if _, exists := r.usedImages[PauseSha256(c.(cri.Container[*docker_client.Container]).Image())]; !exists {
				r.usedImages[PauseSha256(c.(cri.Container[*docker_client.Container]).Image())] = image
				f(image)
			}
		}
		return false
	})
	//fmt.Println(r.usedImages)

}

// WalkUnusedImages Get all unused images
func (r *registry) WalkUnusedImages(f func(docker_client.APIImages)) {
	r.RLock()
	defer r.RUnlock()
	// Loop over containers so we only emit images for running containers.
	for image, val := range r.images {
		_, ok := r.usedImages[PauseSha256(image)]
		if !ok {
			f(val)
		}
	}
}

// WalkNetworks runs f on every network the registry knows of.
func (r *registry) WalkNetworks(f func(docker_client.Network)) {
	r.RLock()
	defer r.RUnlock()

	for _, network := range r.networks {
		f(network)
	}
}

func PauseSha256(h string) string {
	if !strings.Contains(h, "sha256:") {
		return "sha256:" + h
	}
	return h
}

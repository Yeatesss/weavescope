package cri

import (
	docker "github.com/fsouza/go-dockerclient"
	"github.com/weaveworks/scope/report"
)

// Registry keeps track of running docker containers and their images
type Registry[T any] interface {
	Stop()
	LockedPIDLookup(f func(func(int) Container[T]))
	WalkContainers(f func(Container[T]))
	WalkImages(f func(image docker.APIImages))
	WalkUnusedImages(f func(image docker.APIImages))
	WalkNetworks(f func(docker.Network))
	WatchContainerUpdates(ContainerUpdateWatcher)
	GetContainer(string) (Container[T], bool)
	GetContainerByPrefix(string) (Container[T], bool)
	GetContainerImage(string) (docker.APIImages, bool)
	ReplacePortsBinding(map[string]map[string]PortBinding)
	GetContainerPortsBinding(containerID string) map[string]PortBinding
}

// ContainerUpdateWatcher is the type of functions that get called when containers are updated.
type ContainerUpdateWatcher func(report.Node)

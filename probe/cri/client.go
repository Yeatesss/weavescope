package cri

import (
	"context"
	docker_client "github.com/fsouza/go-dockerclient"
)

// Client interface for mocking.
type Client interface {
	//GetClient() interface{}
	ListContainers(docker_client.ListContainersOptions) ([]docker_client.APIContainers, error)
	InspectContainerWithContext(id string, ctx context.Context) (*docker_client.Container, error)
	ListImages(docker_client.ListImagesOptions) ([]docker_client.APIImages, error)
	ListNetworks() ([]docker_client.Network, error)
	AddEventListener(chan<- *docker_client.APIEvents) error
	RemoveEventListener(chan *docker_client.APIEvents) error

	StopContainer(string, uint) error
	StartContainer(string, *docker_client.HostConfig) error
	RestartContainer(string, uint) error
	PauseContainer(string) error
	UnpauseContainer(string) error
	RemoveContainer(docker_client.RemoveContainerOptions) error
	AttachToContainerNonBlocking(docker_client.AttachToContainerOptions) (docker_client.CloseWaiter, error)
	CreateExec(docker_client.CreateExecOptions) (*docker_client.Exec, error)
	StartExecNonBlocking(string, docker_client.StartExecOptions) (docker_client.CloseWaiter, error)
	Stats(docker_client.StatsOptions) error
	ResizeExecTTY(id string, height, width int) error
}

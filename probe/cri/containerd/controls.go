package containerd

import (
	"context"
	"net/http"
	"syscall"

	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	log "github.com/sirupsen/logrus"
	"github.com/weaveworks/scope/report"
)

// Control IDs used by the docker integration.
const (
	StopContainer    = report.DockerStopContainer
	StartContainer   = report.DockerStartContainer
	RestartContainer = report.DockerRestartContainer
	PauseContainer   = report.DockerPauseContainer
	UnpauseContainer = report.DockerUnpauseContainer
	RemoveContainer  = report.DockerRemoveContainer
	AttachContainer  = report.DockerAttachContainer
	ExecContainer    = report.DockerExecContainer
	ResizeExecTTY    = "docker_resize_exec_tty"

	waitTime = 10
)

type ControlHandlerFunc func(id string, r http.Request) error

func (r *registry) registerControls() {
	controls := map[string]ControlHandlerFunc{
		PauseContainer:   r.pauseContainer,
		UnpauseContainer: r.unpauseContainer,
		StopContainer:    r.stopContainer,
		StartContainer:   r.startContainer,
		RemoveContainer:  r.removeContainer,
	}
	go func() {
		for action := range r.controlChannel {
			if handler, ok := controls["containerd_"+action.Action+"_"+action.Type]; ok {
				action.Resp <- handler(action.ID, http.Request{})
			}
		}
	}()
}

// findContainer finds a container in the registry.
//
// It takes a containerID string as a parameter and return a namespace context and an error.
func (r *registry) findContainer(containerID string) (nsCtx context.Context, err error) {
	err = r.client.(*CriClient).RangeNs(func(ns string) (bool, error) {
		nsCtx = namespaces.WithNamespace(context.Background(), ns)
		_, err = r.client.(*CriClient).client.LoadContainer(nsCtx, containerID)
		if err != nil && errdefs.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})
	return
}
func (r *registry) pauseContainer(containerID string, _ http.Request) error {
	log.Infof("Pausing container %s", containerID)
	ns, err := r.findContainer(containerID)
	if err != nil {
		return err
	}
	if ns != nil {
		container, err := r.client.(*CriClient).client.LoadContainer(ns, containerID)

		if err != nil {
			return err
		}
		t, err := container.Task(ns, cio.NewAttach())
		if err == nil {
			// found existing task
			return t.Pause(ns)
		} else if errdefs.IsNotFound(err) {
			// task not found, container not running
			return nil
		} else {
			// probably grpc error
			return err
		}

	}

	return nil
}
func (r *registry) stopContainer(containerID string, _ http.Request) error {
	log.Infof("Stoping container %s", containerID)
	ns, err := r.findContainer(containerID)
	if err != nil {
		return err
	}
	if ns != nil {
		container, err := r.client.(*CriClient).client.LoadContainer(ns, containerID)

		if err != nil {
			return err
		}
		t, err := container.Task(ns, cio.NewAttach())
		if err == nil {
			// found existing task
			return t.Kill(ns, syscall.SIGKILL)
		} else if errdefs.IsNotFound(err) {
			// task not found, container not running
			return nil
		} else {
			// probably grpc error
			return err
		}

	}

	return nil
}
func (r *registry) startContainer(containerID string, _ http.Request) error {
	log.Infof("Start container %s", containerID)
	ns, err := r.findContainer(containerID)
	if err != nil {
		return err
	}
	if ns != nil {
		container, err := r.client.(*CriClient).client.LoadContainer(ns, containerID)

		if err != nil {
			return err
		}
		t, err := container.Task(ns, cio.NewAttach())
		if err == nil {
			// found existing task
			return nil
		} else if errdefs.IsNotFound(err) {
			// task not found, container not running
			t, err = container.NewTask(ns, cio.NewCreator())
			if err != nil {
				return err
			}
			return t.Start(ns)
		} else {
			// probably grpc error
			return err
		}

	}

	return nil
}
func (r *registry) removeContainer(containerID string, _ http.Request) error {
	log.Infof("Remove container %s", containerID)
	ns, err := r.findContainer(containerID)
	if err != nil {
		return err
	}
	if ns != nil {
		container, err := r.client.(*CriClient).client.LoadContainer(ns, containerID)

		if err != nil {
			return err
		}
		return container.Delete(ns)

	}

	return nil
}
func (r *registry) unpauseContainer(containerID string, _ http.Request) error {
	log.Infof("Unpausing container %s", containerID)
	ns, err := r.findContainer(containerID)
	if err != nil {
		return err
	}
	if ns != nil {
		container, err := r.client.(*CriClient).client.LoadContainer(ns, containerID)

		if err != nil {
			return err
		}
		t, err := container.Task(ns, cio.NewAttach())
		if err == nil {
			// found existing task
			return t.Resume(ns)

		} else if errdefs.IsNotFound(err) {
			// task not found, container not running
			return nil
		} else {
			// probably grpc error
			return err
		}

	}

	return nil
}

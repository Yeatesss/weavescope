package containerd

import (
	"context"
	"github.com/containerd/containerd/namespaces"
	"testing"
)

func TestInspectContainer(t *testing.T) {
	client := NewCriClient("/run/containerd/containerd.sock")
	client.InspectContainerWithContext("47aac5d37a994b9130fa417d1206da76dc92477fdd2cd16d0b33f13299086b26", namespaces.WithNamespace(context.Background(), "abc"))
}

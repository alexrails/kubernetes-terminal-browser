package domain

import (
	"fmt"
	core "k8s.io/api/core/v1"
	"strings"
)

type Scope struct {
	Kubeconfig, Context, Namespace string
	Generation                     uint64
}

func (s Scope) String() string { return s.Context + "/" + s.Namespace }

type PodRef struct {
	Scope     Scope
	Name, UID string
}
type ContainerRef struct {
	Pod        PodRef
	Name, Kind string
}
type Container struct {
	Name, Kind, State string
	Ready, Running    bool
	Restarts          int32
}

func Containers(p *core.Pod) []Container {
	var out []Container
	add := func(name, kind string, statuses []core.ContainerStatus) {
		c := Container{Name: name, Kind: kind, State: "Unknown"}
		for _, s := range statuses {
			if s.Name != name {
				continue
			}
			c.Ready = s.Ready
			c.Restarts = s.RestartCount
			switch {
			case s.State.Running != nil:
				c.State = "Running"
				c.Running = true
			case s.State.Waiting != nil:
				c.State = "Waiting: " + s.State.Waiting.Reason
			case s.State.Terminated != nil:
				c.State = fmt.Sprintf("Terminated: %s (%d)", s.State.Terminated.Reason, s.State.Terminated.ExitCode)
			}
		}
		out = append(out, c)
	}
	for _, c := range p.Spec.Containers {
		add(c.Name, "regular", p.Status.ContainerStatuses)
	}
	for _, c := range p.Spec.InitContainers {
		kind := "init"
		if c.RestartPolicy != nil && *c.RestartPolicy == core.ContainerRestartPolicyAlways {
			kind = "sidecar"
		}
		add(c.Name, kind, p.Status.InitContainerStatuses)
	}
	for _, c := range p.Spec.EphemeralContainers {
		add(c.Name, "ephemeral", p.Status.EphemeralContainerStatuses)
	}
	return out
}

type ErrorKind string

const (
	Unknown           ErrorKind = "Unknown"
	Unauthorized      ErrorKind = "Credentials"
	Forbidden         ErrorKind = "Forbidden"
	NotFound          ErrorKind = "NotFound"
	Connection        ErrorKind = "Connection"
	MissingExecutable ErrorKind = "Missing executable"
	Canceled          ErrorKind = "Canceled"
	Replaced          ErrorKind = "Pod replaced"
)

type Error struct {
	Kind      ErrorKind
	Operation string
	Scope     Scope
	Detail    string
	ExitCode  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s [%s], exit %d: %s", e.Kind, e.Operation, e.Scope, e.ExitCode, strings.TrimSpace(e.Detail))
}

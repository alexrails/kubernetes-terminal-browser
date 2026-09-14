package kube

import (
	"context"
	core "k8s.io/api/core/v1"
	"kubernetes-terminal-browser/internal/domain"
)

type LogsOptions struct {
	Tail             int
	Follow, Previous bool
}
type Reader interface {
	Pods(context.Context, domain.Scope) ([]core.Pod, error)
	Namespaces(context.Context, domain.Scope) ([]string, error)
	Pod(context.Context, domain.PodRef) (*core.Pod, error)
	Describe(context.Context, domain.PodRef) (string, error)
	Events(context.Context, domain.PodRef) (string, error)
}

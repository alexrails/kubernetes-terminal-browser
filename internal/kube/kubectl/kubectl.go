package kubectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	core "k8s.io/api/core/v1"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"kubernetes-terminal-browser/internal/kube"
	"kubernetes-terminal-browser/internal/process"
	"kubernetes-terminal-browser/internal/terminal"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Executable string
	Timeout    time.Duration
	Foreground *terminal.IO
}

var _ kube.Reader = (*Client)(nil)

func (c Client) Flags(s domain.Scope, namespaced bool) []string {
	a := []string{"--context=" + s.Context}
	if s.Kubeconfig != "" {
		a = append(a, "--kubeconfig="+s.Kubeconfig)
	}
	if namespaced {
		a = append(a, "--namespace="+s.Namespace)
	}
	return a
}
func (c Client) ReadArgs(s domain.Scope, namespaced bool, args ...string) []string {
	a := c.Flags(s, namespaced)
	a = append(a, "--request-timeout="+c.Timeout.String())
	return append(a, args...)
}
func (c Client) ExecArgs(r domain.ContainerRef, a config.Action) []string {
	v := append(c.Flags(r.Pod.Scope, true), "exec", r.Pod.Name, "--container="+r.Name)
	if a.Stdin {
		v = append(v, "-i")
	}
	if a.TTY {
		v = append(v, "-t")
	}
	v = append(v, "--")
	return append(v, a.Argv...)
}
func (c Client) LogsArgs(r domain.ContainerRef, o kube.LogsOptions) []string {
	return append(c.Flags(r.Pod.Scope, true), "logs", r.Pod.Name, "--container="+r.Name, "--tail="+strconv.Itoa(o.Tail), "--timestamps=true", "--follow="+strconv.FormatBool(o.Follow), "--previous="+strconv.FormatBool(o.Previous))
}

// missingExecutable is true only when a binary could not be found: kubectl
// itself, or a credential plugin ("exec: executable X not found"). A plugin
// that exists and fails ("exec: executable X failed with exit code N") is a
// credentials problem and must reach the Unauthorized case, which offers the
// foreground retry.
func missingExecutable(err error, low string) bool {
	if errors.Is(err, exec.ErrNotFound) || strings.Contains(low, "executable file not found") {
		return true
	}
	for _, line := range strings.Split(low, "\n") {
		if i := strings.Index(line, "exec: executable "); i >= 0 && strings.HasSuffix(strings.TrimSpace(line[i:]), " not found") {
			return true
		}
	}
	return false
}

// Classify maps a finished kubectl process to a domain error. Matching is
// best effort over kubectl's stderr; an unknown message stays Unknown with its
// exit code.
func Classify(op string, s domain.Scope, r process.Result) error {
	detail := detailOf(r)
	return classify(op, s, r, detail, strings.ToLower(detail))
}

// ClassifyExec is Classify for a command that ran inside a container, whose
// stderr is mixed with kubectl's own. kubectl prints "command terminated with
// exit code N" whenever the remote process ran and failed: then nothing on
// stderr is a cluster error and the kind stays Unknown, however the program
// phrased its output ("Error: … not found", "Unauthorized"). Without that
// marker the remote process never started, and only lines kubectl writes
// itself (Forbidden on pods/exec, credential plugin, connection) drive the
// kind. The whole stderr excerpt is the detail either way.
func ClassifyExec(op string, s domain.Scope, r process.Result) error {
	low := kubectlLines(r.Stderr)
	if remoteExited(r.Stderr) {
		low = ""
	}
	return classify(op, s, r, detailOf(r), low)
}

const remoteExitMarker = "command terminated with exit code"

func remoteExited(stderr []byte) bool {
	for _, line := range strings.Split(strings.ToLower(string(stderr)), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), remoteExitMarker) {
			return true
		}
	}
	return false
}

var kubectlPrefixes = []string{"error: ", "error from server", "unable to connect to the server", "the connection to the server"}

func kubectlLines(stderr []byte) string {
	var out []string
	for _, line := range strings.Split(strings.ToLower(string(stderr)), "\n") {
		line = strings.TrimSpace(line)
		for _, p := range kubectlPrefixes {
			if strings.HasPrefix(line, p) {
				out = append(out, line)
				break
			}
		}
	}
	return strings.Join(out, "\n")
}
func detailOf(r process.Result) string {
	detail := strings.TrimSpace(string(r.Stderr))
	if detail == "" && r.Err != nil {
		detail = r.Err.Error()
	}
	return detail
}
func classify(op string, s domain.Scope, r process.Result, detail, low string) error {
	if r.Err == nil && !r.Truncated {
		return nil
	}
	kind := domain.Unknown
	switch {
	case errors.Is(r.Err, context.Canceled):
		kind = domain.Canceled
	case errors.Is(r.Err, context.DeadlineExceeded):
		kind = domain.Connection
	case missingExecutable(r.Err, low):
		kind = domain.MissingExecutable
	case strings.Contains(low, "(forbidden)") || strings.Contains(low, " is forbidden"):
		kind = domain.Forbidden
	case strings.Contains(low, "(notfound)") || strings.Contains(low, " not found"):
		kind = domain.NotFound
	case strings.Contains(low, "unauthorized") || strings.Contains(low, "getting credentials") || strings.Contains(low, "must be logged in") || strings.Contains(low, "provide credentials") || strings.Contains(low, "interactive mode") || strings.Contains(low, "standard input is not a terminal"):
		kind = domain.Unauthorized
	case strings.Contains(low, "unable to connect") || strings.Contains(low, "connection refused") || strings.Contains(low, "no such host") || strings.Contains(low, "i/o timeout") || strings.Contains(low, "x509:"):
		kind = domain.Connection
	}
	if r.Truncated {
		detail += " [output limit exceeded]"
	}
	return &domain.Error{Kind: kind, Operation: op, Scope: s, Detail: detail, ExitCode: r.ExitCode}
}
func (c Client) read(ctx context.Context, s domain.Scope, op string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var r process.Result
	if c.Foreground != nil {
		r = terminal.Run(ctx, c.Executable, args, *c.Foreground, true)
	} else {
		r = process.Run(ctx, c.Executable, args, nil)
	}
	return r.Stdout, Classify(op, s, r)
}
func (c Client) Pods(ctx context.Context, s domain.Scope) ([]core.Pod, error) {
	b, e := c.read(ctx, s, "list pods", c.ReadArgs(s, true, "get", "pods", "-o=json"))
	if e != nil {
		return nil, e
	}
	var p core.PodList
	e = json.Unmarshal(b, &p)
	return p.Items, e
}
func (c Client) Namespaces(ctx context.Context, s domain.Scope) ([]string, error) {
	b, e := c.read(ctx, s, "list namespaces", c.ReadArgs(s, false, "get", "namespaces", "-o=json"))
	if e != nil {
		return nil, e
	}
	var p core.NamespaceList
	if e = json.Unmarshal(b, &p); e != nil {
		return nil, e
	}
	var names []string
	for _, n := range p.Items {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	return names, nil
}
func (c Client) Pod(ctx context.Context, r domain.PodRef) (*core.Pod, error) {
	b, e := c.read(ctx, r.Scope, "get pod", c.ReadArgs(r.Scope, true, "get", "pod", r.Name, "-o=json"))
	if e != nil {
		return nil, e
	}
	var p core.Pod
	if e = json.Unmarshal(b, &p); e != nil {
		return nil, e
	}
	if string(p.UID) != r.UID {
		return nil, &domain.Error{Kind: domain.Replaced, Operation: "check UID", Scope: r.Scope, Detail: "Pod identity changed; select the pod again", ExitCode: 0}
	}
	return &p, nil
}
func (c Client) CheckExec(ctx context.Context, r domain.ContainerRef) error {
	p, e := c.Pod(ctx, r.Pod)
	if e != nil {
		return e
	}
	for _, v := range domain.Containers(p) {
		if v.Name == r.Name && v.Kind == r.Kind {
			if v.Running {
				return nil
			}
			return fmt.Errorf("container %s is %s; logs may still be available", r.Name, v.State)
		}
	}
	return fmt.Errorf("container %s no longer exists; select again", r.Name)
}
func (c Client) Exec(ctx context.Context, r domain.ContainerRef, a config.Action, t terminal.IO) error {
	if e := c.CheckExec(ctx, r); e != nil {
		return e
	}
	result := terminal.Run(ctx, c.Executable, c.ExecArgs(r, a), t, false)
	return ClassifyExec("exec (not retried; choose another action if its executable is unavailable)", r.Pod.Scope, result)
}
func (c Client) Describe(ctx context.Context, r domain.PodRef) (string, error) {
	if _, e := c.Pod(ctx, r); e != nil {
		return "", e
	}
	b, e := c.read(ctx, r.Scope, "describe pod", c.ReadArgs(r.Scope, true, "describe", "pod", r.Name, "--show-events=false"))
	return string(b), e
}
func (c Client) Events(ctx context.Context, r domain.PodRef) (string, error) {
	b, e := c.read(ctx, r.Scope, "list events", c.ReadArgs(r.Scope, true, "get", "events", "--field-selector=involvedObject.uid="+r.UID, "-o=json"))
	if e != nil {
		return "", e
	}
	var events core.EventList
	if e = json.Unmarshal(b, &events); e != nil {
		return "", e
	}
	if len(events.Items) == 0 {
		return "No events for this pod UID.", nil
	}
	var out strings.Builder
	for _, v := range events.Items {
		fmt.Fprintf(&out, "%s  %s  %s (x%d)\n", v.Type, v.Reason, v.Message, v.Count)
	}
	return out.String(), nil
}
func (c Client) Logs(ctx context.Context, r domain.ContainerRef, o kube.LogsOptions, w io.Writer) error {
	if _, e := c.Pod(ctx, r.Pod); e != nil {
		return e
	}
	args := c.LogsArgs(r, o)
	if !o.Follow {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	if c.Foreground != nil {
		return Classify("logs", r.Pod.Scope, terminal.Run(ctx, c.Executable, args, *c.Foreground, false))
	}
	return Classify("logs", r.Pod.Scope, process.Run(ctx, c.Executable, args, w))
}

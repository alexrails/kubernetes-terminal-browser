package kubeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"kubernetes-terminal-browser/internal/process"
	"sort"
)

type Context struct{ Name, Namespace, InteractiveMode string }
type Snapshot struct {
	Current  string
	Contexts []Context
}

func Parse(data []byte) (Snapshot, error) {
	var raw struct {
		Current  string `json:"current-context"`
		Contexts []struct {
			Name    string
			Context struct{ Namespace, User string }
		}
		Users []struct {
			Name string
			User struct {
				Exec *struct {
					InteractiveMode string `json:"interactiveMode"`
				}
			}
		}
	}
	if e := json.Unmarshal(data, &raw); e != nil {
		return Snapshot{}, e
	}
	s := Snapshot{Current: raw.Current}
	modes := map[string]string{}
	for _, u := range raw.Users {
		if u.User.Exec != nil {
			modes[u.Name] = u.User.Exec.InteractiveMode
		}
	}
	for _, c := range raw.Contexts {
		ns := c.Context.Namespace
		if ns == "" {
			ns = "default"
		}
		s.Contexts = append(s.Contexts, Context{Name: c.Name, Namespace: ns, InteractiveMode: modes[c.Context.User]})
	}
	sort.Slice(s.Contexts, func(i, j int) bool { return s.Contexts[i].Name < s.Contexts[j].Name })
	return s, nil
}
func Load(ctx context.Context, executable, path string) (Snapshot, error) {
	args := []string{"config", "view", "-o=json"}
	if path != "" {
		args = append(args, "--kubeconfig="+path)
	}
	r := process.Run(ctx, executable, args, nil)
	if r.Err != nil {
		return Snapshot{}, fmt.Errorf("kubeconfig: %w: %s", r.Err, r.Stderr)
	}
	if r.Truncated {
		return Snapshot{}, fmt.Errorf("kubeconfig output exceeded limit")
	}
	return Parse(r.Stdout)
}

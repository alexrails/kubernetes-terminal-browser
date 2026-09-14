package gcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/process"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxProjects caps a prefix search: each project costs one gcloud call, and an
// account can see thousands of projects.
const MaxProjects = 300

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func ValidPrefix(s string) bool { return prefixPattern.MatchString(s) }

// ConfiguredProjects are the projects of every gcloud configuration.
func (c Client) ConfiguredProjects(ctx context.Context, timeout time.Duration) ([]string, error) {
	out, e := c.read(ctx, timeout, "config", "configurations", "list", "--format=value(properties.core.project)", "--quiet")
	return strings.Fields(string(out)), e
}

// Projects lists the project IDs starting with prefix. A prefix filter is
// evaluated by the server, so it stays fast however many projects exist.
func (c Client) Projects(ctx context.Context, timeout time.Duration, prefix string) ([]string, error) {
	if !ValidPrefix(prefix) {
		return nil, fmt.Errorf("invalid project ID prefix %q: lowercase letters, digits and hyphens", prefix)
	}
	out, e := c.read(ctx, timeout, "projects", "list", "--filter=projectId:"+prefix+"*", "--format=value(projectId)", "--quiet")
	if e != nil {
		return nil, e
	}
	ps := strings.Fields(string(out))
	if len(ps) > MaxProjects {
		return nil, fmt.Errorf("prefix %q matches %d projects (limit %d) • type a longer prefix", prefix, len(ps), MaxProjects)
	}
	return ps, nil
}

// Clusters lists the regional GKE clusters of projects. It runs in the
// background, so --quiet makes an expired login fail instead of prompting. A
// project where listing fails (API disabled, no permission) is skipped; only
// when every project fails is it an error.
func (c Client) Clusters(ctx context.Context, timeout time.Duration, projects []string) ([]config.Cluster, error) {
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		found []config.Cluster
		first error
		ok    int
	)
	sem := make(chan struct{}, 16)
	for _, p := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, e := c.read(ctx, timeout, "container", "clusters", "list", "--project", p, "--format=json", "--quiet")
			var cs []config.Cluster
			if e == nil {
				cs, e = ParseClusters(p, out)
			}
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				if first == nil {
					first = e
				}
				return
			}
			ok++
			found = append(found, cs...)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if ok == 0 && first != nil {
		return nil, first
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Project != found[j].Project {
			return found[i].Project < found[j].Project
		}
		return found[i].Name < found[j].Name
	})
	return found, nil
}

// ParseClusters reads `gcloud container clusters list --format=json` output.
func ParseClusters(project string, data []byte) ([]config.Cluster, error) {
	var raw []struct{ Name, Location string }
	if e := json.Unmarshal(data, &raw); e != nil {
		return nil, fmt.Errorf("gcloud clusters list for %s: %w", project, e)
	}
	var cs []config.Cluster
	for _, r := range raw {
		if r.Name != "" && config.ValidRegion(r.Location) {
			cs = append(cs, config.Cluster{Name: r.Name, Region: r.Location, Project: project})
		}
	}
	return cs, nil
}
func (c Client) read(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r := process.Run(ctx, c.Executable, args, nil)
	if r.Err != nil {
		detail := strings.TrimSpace(string(r.Stderr))
		if detail == "" {
			detail = r.Err.Error()
		}
		return nil, fmt.Errorf("gcloud %s %s failed: %s", args[0], args[1], detail)
	}
	if r.Truncated {
		return nil, fmt.Errorf("gcloud %s %s output exceeded limit", args[0], args[1])
	}
	return r.Stdout, nil
}

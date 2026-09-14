// Package gcloud adds GKE clusters to the user's kubeconfig and lists the
// clusters the account can see.
package gcloud

import (
	"context"
	"fmt"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/terminal"
	"strings"
)

type Client struct {
	Executable string
	Kubeconfig string
}

func Args(c config.Cluster) []string {
	return []string{"container", "clusters", "get-credentials", c.Name, "--region", c.Region, "--project", c.Project}
}

func (c Client) GetCredentials(ctx context.Context, p config.Cluster, t terminal.IO) error {
	env := map[string]string{}
	if c.Kubeconfig != "" {
		env["KUBECONFIG"] = c.Kubeconfig
	}
	r := terminal.RunWithEnv(ctx, c.Executable, Args(p), t, false, env)
	if r.Err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(r.Stderr))
	if detail == "" {
		detail = r.Err.Error()
	}
	return fmt.Errorf("gcloud get-credentials for %s failed (exit %d): %s", p.Name, r.ExitCode, detail)
}

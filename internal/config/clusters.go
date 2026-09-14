package config

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"regexp"
	"strings"
)

// Cluster is one GKE cluster offered on the startup screen. Each entry maps to
// exactly one `gcloud container clusters get-credentials` command.
type Cluster struct {
	Name    string `yaml:"name"`
	Region  string `yaml:"region"`
	Project string `yaml:"project"`
	// Namespace is optional: the initial namespace for this cluster's
	// context. Empty keeps the context's own namespace.
	Namespace string `yaml:"namespace"`
	// Env is "prod" or "dev"; empty derives it from a -prd/-prod suffix of
	// the cluster or project name.
	Env string `yaml:"env"`
}

// ContextName is the kubeconfig context gcloud get-credentials creates.
func (c Cluster) ContextName() string {
	return "gke_" + c.Project + "_" + c.Region + "_" + c.Name
}

// ClusterFromContext recovers the cluster behind a gcloud-made context name
// gke_PROJECT_REGION_NAME. Zonal clusters are skipped: get-credentials here
// always passes --region.
func ClusterFromContext(name string) (Cluster, bool) {
	p := strings.Split(name, "_")
	if len(p) != 4 || p[0] != "gke" || p[1] == "" || p[3] == "" || !ValidRegion(p[2]) {
		return Cluster{}, false
	}
	return Cluster{Name: p[3], Region: p[2], Project: p[1]}, true
}
func ValidRegion(s string) bool { return regionPattern.MatchString(s) }

// Prod reports whether commands in this cluster need confirmation.
func (c Cluster) Prod() bool {
	if c.Env != "" {
		return c.Env == "prod"
	}
	for _, s := range []string{c.Name, c.Project} {
		if strings.HasSuffix(s, "-prd") || strings.HasSuffix(s, "-prod") {
			return true
		}
	}
	return false
}

// Target is a bookmark: cluster, namespace and application, optionally with
// the container and the action to run in the application's current pod.
type Target struct {
	Name      string `yaml:"name"`
	Cluster   string `yaml:"cluster"`
	Namespace string `yaml:"namespace"`
	App       string `yaml:"app"`
	Container string `yaml:"container"`
	Action    string `yaml:"action"`
}

// ClustersFile is the content of clusters.yaml.
type ClustersFile struct {
	Clusters []Cluster `yaml:"clusters"`
	Targets  []Target  `yaml:"targets"`
}

var (
	regionPattern = regexp.MustCompile(`^[a-z]+(?:-[a-z0-9]+)+[0-9]$`)
	targetPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
)

func DefaultClustersPath() string { return defaultFile("clusters.yaml") }

// LoadClusters reads the clusters file. A missing file is empty unless the
// path was given explicitly.
func LoadClusters(path string, required bool) (ClustersFile, error) {
	f, e := os.Open(path)
	if e != nil {
		if !required && errors.Is(e, os.ErrNotExist) {
			return ClustersFile{}, nil
		}
		return ClustersFile{}, e
	}
	defer f.Close()
	cf, e := DecodeClusters(f)
	if e != nil {
		return cf, fmt.Errorf("%s: %w", path, e)
	}
	return cf, nil
}
func DecodeClusters(r io.Reader) (ClustersFile, error) {
	var file ClustersFile
	d := yaml.NewDecoder(io.LimitReader(r, 1<<20))
	d.KnownFields(true)
	if e := d.Decode(&file); e != nil && e != io.EOF {
		return file, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return file, fmt.Errorf("expected one YAML document")
	}
	if e := validateClusters(file.Clusters); e != nil {
		return file, e
	}
	return file, validateTargets(file.Targets, file.Clusters, nil)
}
func validateClusters(cs []Cluster) error {
	seen := map[string]bool{}
	for _, c := range cs {
		if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Region) == "" || strings.TrimSpace(c.Project) == "" {
			return fmt.Errorf("each cluster needs name, region, and project")
		}
		if c.Name != strings.TrimSpace(c.Name) || c.Region != strings.TrimSpace(c.Region) || c.Project != strings.TrimSpace(c.Project) {
			return fmt.Errorf("cluster name, region, and project cannot have surrounding whitespace")
		}
		if strings.ContainsRune(c.Name+c.Region+c.Project, 0) {
			return fmt.Errorf("cluster name, region, and project cannot contain NUL")
		}
		if !regionPattern.MatchString(c.Region) {
			return fmt.Errorf("cluster %q has invalid GCP region %q", c.Name, c.Region)
		}
		if c.Namespace != "" && !ValidNamespace(c.Namespace) {
			return fmt.Errorf("cluster %q has invalid namespace %q", c.Name, c.Namespace)
		}
		if c.Env != "" && c.Env != "prod" && c.Env != "dev" {
			return fmt.Errorf("cluster %q: env must be prod or dev", c.Name)
		}
		key := c.Project + "\x00" + c.Region + "\x00" + c.Name
		if seen[key] {
			return fmt.Errorf("duplicate cluster %q", c.Name)
		}
		seen[key] = true
	}
	return nil
}

// FindCluster returns the only cluster with this name.
func FindCluster(cs []Cluster, name string) (Cluster, error) {
	var found []Cluster
	for _, c := range cs {
		if c.Name == name {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return Cluster{}, fmt.Errorf("unknown cluster %q", name)
	case 1:
		return found[0], nil
	}
	return Cluster{}, fmt.Errorf("cluster name %q is ambiguous", name)
}

// validateTargets checks targets against the clusters and, when actions is
// not nil, against the configured action ids.
func validateTargets(ts []Target, cs []Cluster, actions []Action) error {
	seen := map[string]bool{}
	for _, t := range ts {
		if !targetPattern.MatchString(t.Name) || seen[t.Name] {
			return fmt.Errorf("invalid or duplicate target name %q", t.Name)
		}
		seen[t.Name] = true
		if _, e := FindCluster(cs, t.Cluster); e != nil {
			return fmt.Errorf("target %q: %w", t.Name, e)
		}
		if strings.TrimSpace(t.App) == "" || strings.ContainsRune(t.App+t.Container, 0) {
			return fmt.Errorf("target %q needs app", t.Name)
		}
		if t.Namespace != "" && !ValidNamespace(t.Namespace) {
			return fmt.Errorf("target %q has invalid namespace %q", t.Name, t.Namespace)
		}
		if t.Action == "" || actions == nil {
			continue
		}
		found := false
		for _, a := range actions {
			found = found || a.ID == t.Action
		}
		if !found {
			return fmt.Errorf("target %q: unknown action %q", t.Name, t.Action)
		}
	}
	return nil
}

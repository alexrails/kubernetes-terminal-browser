package config

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Action struct {
	ID    string   `yaml:"id"`
	Label string   `yaml:"label"`
	Argv  []string `yaml:"argv"`
	Stdin bool     `yaml:"stdin"`
	TTY   bool     `yaml:"tty"`
}
type Config struct {
	Refresh time.Duration `yaml:"refresh"`
	Timeout time.Duration `yaml:"timeout"`
	Actions []Action      `yaml:"actions"`
	// Clusters and Targets come from the separate clusters file (LoadClusters).
	Clusters []Cluster `yaml:"-"`
	Targets  []Target  `yaml:"-"`
}

func Defaults() Config {
	return Config{Refresh: 5 * time.Second, Timeout: 15 * time.Second, Actions: []Action{
		{ID: "sh", Label: "Shell (/bin/sh)", Argv: []string{"/bin/sh"}, Stdin: true, TTY: true},
		{ID: "rails-console", Label: "Rails console (bundle exec rails console)", Argv: []string{"bundle", "exec", "rails", "console"}, Stdin: true, TTY: true},
	}}
}
func DefaultPath() string { return defaultFile("config.yaml") }
func defaultFile(name string) string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "ktb", name)
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "ktb", name)
}
func Load(path string, required bool) (Config, error) {
	c := Defaults()
	f, e := os.Open(path)
	if e != nil {
		if !required && errors.Is(e, os.ErrNotExist) {
			return c, nil
		}
		return c, e
	}
	defer f.Close()
	return Decode(f)
}
func Decode(r io.Reader) (Config, error) {
	c := Defaults()
	d := yaml.NewDecoder(io.LimitReader(r, 1<<20))
	d.KnownFields(true)
	if e := d.Decode(&c); e != nil {
		return c, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return c, fmt.Errorf("expected one YAML document")
	}
	return c, c.Validate()
}

// FindTarget returns the target with this name.
func (c Config) FindTarget(name string) (Target, error) {
	for _, t := range c.Targets {
		if t.Name == name {
			return t, nil
		}
	}
	return Target{}, fmt.Errorf("unknown target %q", name)
}
func (c Config) Validate() error {
	if c.Refresh < 0 || (c.Refresh > 0 && c.Refresh < time.Second) {
		return fmt.Errorf("refresh must be 0 or at least 1s")
	}
	if c.Timeout < time.Second || c.Timeout > 5*time.Minute {
		return fmt.Errorf("timeout must be between 1s and 5m")
	}
	seen := map[string]bool{}
	labels := map[string]bool{}
	for _, a := range c.Actions {
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(a.ID) || seen[a.ID] {
			return fmt.Errorf("invalid or duplicate action id %q", a.ID)
		}
		seen[a.ID] = true
		if strings.TrimSpace(a.Label) == "" || len(a.Argv) == 0 || a.Argv[0] == "" {
			return fmt.Errorf("action %q needs label and argv", a.ID)
		}
		if labels[a.Label] {
			return fmt.Errorf("duplicate action label %q", a.Label)
		}
		labels[a.Label] = true
		if a.TTY && !a.Stdin {
			return fmt.Errorf("action %q: tty requires stdin", a.ID)
		}
		for _, v := range a.Argv {
			if strings.ContainsRune(v, 0) {
				return fmt.Errorf("action %q contains NUL", a.ID)
			}
		}
	}
	if e := validateClusters(c.Clusters); e != nil {
		return e
	}
	return validateTargets(c.Targets, c.Clusters, c.Actions)
}

var namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func ValidNamespace(s string) bool {
	return len(s) > 0 && len(s) <= 63 && namespacePattern.MatchString(s)
}

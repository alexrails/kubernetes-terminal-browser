package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"golang.org/x/term"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/gcloud"
	"kubernetes-terminal-browser/internal/kube/kubectl"
	"kubernetes-terminal-browser/internal/pane"
	"kubernetes-terminal-browser/internal/presentation"
	"kubernetes-terminal-browser/internal/process"
	"kubernetes-terminal-browser/internal/terminal"
	"kubernetes-terminal-browser/internal/ui"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
)

// version is set at build time from the git tag (make build, GoReleaser);
// `go install module@vX.Y.Z` builds carry it in the build info instead.
var version = "dev"

func ktbVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

// attach is the ktb that the action menu starts in a new pane: it runs one exec session
// and exits, which closes the pane. A failure stays on screen until Enter,
// because the pane would take the message with it.
func attach(v string) error {
	s, e := pane.Decode(v)
	if e != nil {
		return e
	}
	s.Apply(os.Setenv, os.Unsetenv)
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("ktb requires a terminal on stdin and stdout")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	// Ctrl+C belongs to the command in the container. It is caught rather
	// than ignored: an ignored signal would be inherited by kubectl.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	t := terminal.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	if s.Dir != "" {
		e = os.Chdir(s.Dir)
	}
	if e == nil {
		e = ui.Attach(ctx, s, t)
	}
	signal.Stop(interrupts)
	if e != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "\nktb:", presentation.SafeText(e.Error()))
		terminal.WaitForEnter(ctx, t, "Press Enter to close this pane.\n")
		return nil
	}
	return e
}
func run() error {
	if len(os.Args) > 1 && len(os.Args) < 4 && os.Args[1] == pane.Flag {
		v := os.Getenv(pane.Var)
		os.Unsetenv(pane.Var)
		if len(os.Args) == 3 {
			v = os.Args[2]
		}
		return attach(v)
	}
	source := flag.String("kubeconfig", "", "Explicit kubeconfig file (otherwise kubectl resolves KUBECONFIG/default)")
	ns := flag.String("namespace", "", "Initial namespace override")
	cfgPath := flag.String("config", config.DefaultPath(), "Application YAML config")
	clustersPath := flag.String("clusters", config.DefaultClustersPath(), "YAML list of GKE clusters offered at startup")
	executable := flag.String("kubectl", "kubectl", "kubectl executable")
	gcloudExecutable := flag.String("gcloud", "gcloud", "gcloud executable")
	refresh := flag.Duration("refresh", -1, "Polling interval; 0 disables polling (default 5s/config)")
	timeout := flag.Duration("timeout", 0, "Read deadline (default 15s/config)")
	check := flag.Bool("check-config", false, "Validate configuration and exit")
	showVersion := flag.Bool("version", false, "Print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: ktb [flags] [target]\n\nA target from clusters.yaml opens its application's pod directly.\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("ktb", ktbVersion())
		return nil
	}
	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	c, e := config.Load(*cfgPath, explicit["config"])
	if e != nil {
		return e
	}
	cf, e := config.LoadClusters(*clustersPath, explicit["clusters"])
	if e != nil {
		return e
	}
	c.Clusters, c.Targets = cf.Clusters, cf.Targets
	if *refresh >= 0 {
		c.Refresh = *refresh
	}
	if *timeout != 0 {
		c.Timeout = *timeout
	}
	if e = c.Validate(); e != nil {
		return e
	}
	if *ns != "" && !config.ValidNamespace(*ns) {
		return fmt.Errorf("invalid namespace %q", *ns)
	}
	if flag.NArg() > 1 {
		return fmt.Errorf("expected at most one target, got %d arguments", flag.NArg())
	}
	var start *config.Target
	if flag.NArg() == 1 {
		t, e := c.FindTarget(flag.Arg(0))
		if e != nil {
			return e
		}
		start = &t
	}
	if *check {
		fmt.Printf("Configuration valid: clusters %d, targets %d\n", len(c.Clusters), len(c.Targets))
		return nil
	}
	bin, e := exec.LookPath(*executable)
	if e != nil {
		return fmt.Errorf("kubectl executable unavailable: %w", e)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("ktb requires a terminal on stdin and stdout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); process.Shutdown() }()
	versionCtx, versionCancel := context.WithTimeout(ctx, 5*time.Second)
	r := process.Run(versionCtx, bin, []string{"version", "--client", "-o=json"}, nil)
	versionCancel()
	kv := "unknown (client version check failed)"
	if r.Err == nil {
		var v struct{ ClientVersion struct{ GitVersion string } }
		if json.Unmarshal(r.Stdout, &v) == nil {
			kv = v.ClientVersion.GitVersion
		}
	}
	m := ui.New(ctx, kubectl.Client{Executable: bin, Timeout: c.Timeout}, gcloud.Client{Executable: *gcloudExecutable, Kubeconfig: *source}, c, *source, *ns, "ktb "+ktbVersion()+" · kubectl "+kv)
	if start != nil {
		m.Start(*start)
	}
	if o, ok := pane.Detect(os.Getenv, c.Pane); ok {
		if self, e := os.Executable(); e == nil {
			m.Panes(o, self)
		}
	}
	p := tea.NewProgram(m)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-signals:
			cancel()
			p.Quit()
		case <-done:
		}
	}()
	_, e = p.Run()
	cancel()
	return e
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "ktb:", e)
		os.Exit(1)
	}
}

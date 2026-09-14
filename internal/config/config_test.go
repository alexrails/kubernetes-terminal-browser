package config

import (
	"strings"
	"testing"
)

func TestActionArgvValidation(t *testing.T) {
	_, e := Decode(strings.NewReader("actions:\n  - id: rails\n    label: Rails\n    argv: [bundle, exec, rails, console]\n    stdin: true\n    tty: true\n"))
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"actions:\n  - id: x\n    label: X\n    argv: []\n", "actions:\n  - id: x\n    label: X\n    argv: [sh]\n    tty: true\n", "unknown: value\n"} {
		if _, e = Decode(strings.NewReader(s)); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestNamespace(t *testing.T) {
	for _, s := range []string{"default", "my-ns", "a"} {
		if !ValidNamespace(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "A", "-bad", "bad-", "a.b"} {
		if ValidNamespace(s) {
			t.Fatal(s)
		}
	}
}

func TestClustersValidation(t *testing.T) {
	valid := "clusters:\n  - name: blog-europe-west2-dev\n    region: europe-west2\n    project: acme-blog-dev\n"
	cs, e := DecodeClusters(strings.NewReader(valid + "  - name: shop-europe-west1-dev\n    region: europe-west2\n    project: acme-shop-dev\n    namespace: shop-dev\n"))
	if e != nil || len(cs.Clusters) != 2 || cs.Clusters[1].Namespace != "shop-dev" {
		t.Fatal(cs, e)
	}
	for _, s := range []string{
		"clusters:\n  - name: ''\n    region: europe-west2\n    project: acme-blog-dev\n",
		"clusters:\n  - name: blog-europe-west2-dev\n    region: europe-west2\n    project: ' acme-blog-dev'\n",
		"clusters:\n  - name: zonal\n    region: us-central1-a\n    project: acme-blog-dev\n",
		"clusters:\n  - name: x\n    region: europe-west2\n    project: p\n    namespace: Bad_NS\n",
		"clusters:\n  - name: x\n    region: europe-west2\n    project: p\n    cluster: y\n",
		valid + "  - name: blog-europe-west2-dev\n    region: europe-west2\n    project: acme-blog-dev\n",
	} {
		if _, e := DecodeClusters(strings.NewReader(s)); e == nil {
			t.Fatalf("accepted invalid clusters file %q", s)
		}
	}
	if _, e := Decode(strings.NewReader("projects: []\n")); e == nil {
		t.Fatal("config.yaml still accepts projects; clusters belong in clusters.yaml")
	}
}

func TestExampleClustersFile(t *testing.T) {
	cf, e := LoadClusters("../../clusters.example.yaml", true)
	if e != nil || len(cf.Clusters) == 0 || len(cf.Targets) == 0 {
		t.Fatal(len(cf.Clusters), len(cf.Targets), e)
	}
	c := Defaults()
	c.Clusters, c.Targets = cf.Clusters, cf.Targets
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
}

func TestProdAndContextName(t *testing.T) {
	c := Cluster{Name: "shop-us-central1-prd", Region: "us-central1", Project: "acme-shop-prd"}
	if !c.Prod() || c.ContextName() != "gke_acme-shop-prd_us-central1_shop-us-central1-prd" {
		t.Fatal(c.Prod(), c.ContextName())
	}
	if (Cluster{Name: "shop-europe-west1-dev", Project: "acme-shop-dev"}).Prod() {
		t.Fatal("dev cluster treated as prod")
	}
	if !(Cluster{Name: "x", Project: "y", Env: "prod"}).Prod() {
		t.Fatal("env: prod ignored")
	}
}

func TestClusterFromContext(t *testing.T) {
	for _, c := range []struct {
		name string
		want Cluster
		ok   bool
	}{
		{"gke_acme-shop-prd_us-central1_shop-us-central1-prd", Cluster{Name: "shop-us-central1-prd", Region: "us-central1", Project: "acme-shop-prd"}, true},
		{"gke_p_us-central1-a_zonal", Cluster{}, false},
		{"gke_p__name", Cluster{}, false},
		{"kind-kind", Cluster{}, false},
		{"gke_p_us-central1", Cluster{}, false},
	} {
		got, ok := ClusterFromContext(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %+v %t", c.name, got, ok)
		}
		if ok && got.ContextName() != c.name {
			t.Errorf("%s: round trip gave %s", c.name, got.ContextName())
		}
	}
}

func TestTargetsValidation(t *testing.T) {
	base := "clusters:\n  - name: c1\n    region: europe-west2\n    project: p\ntargets:\n"
	if _, e := DecodeClusters(strings.NewReader(base + "  - name: admin\n    cluster: c1\n    app: admin-web\n    action: rails-console\n")); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{
		"  - name: x\n    cluster: missing\n    app: a\n",
		"  - name: x\n    cluster: c1\n",
		"  - name: 'bad name'\n    cluster: c1\n    app: a\n",
		"  - name: x\n    cluster: c1\n    app: a\n  - name: x\n    cluster: c1\n    app: b\n",
	} {
		if _, e := DecodeClusters(strings.NewReader(base + s)); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	c := Defaults()
	c.Clusters = []Cluster{{Name: "c1", Region: "europe-west2", Project: "p"}}
	c.Targets = []Target{{Name: "x", Cluster: "c1", App: "a", Action: "nope"}}
	if c.Validate() == nil {
		t.Fatal("unknown action accepted")
	}
}

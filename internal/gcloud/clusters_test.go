package gcloud

import (
	"context"
	"kubernetes-terminal-browser/internal/config"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseClustersSkipsZonal(t *testing.T) {
	got, e := ParseClusters("p", []byte(`[{"name":"a","location":"europe-west2","status":"RUNNING"},{"name":"z","location":"us-central1-a"}]`))
	if e != nil || !reflect.DeepEqual(got, []config.Cluster{{Name: "a", Region: "europe-west2", Project: "p"}}) {
		t.Fatal(got, e)
	}
}

func fakeGcloud(t *testing.T, body string) Client {
	bin := filepath.Join(t.TempDir(), "gcloud")
	if e := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0700); e != nil {
		t.Fatal(e)
	}
	return Client{Executable: bin}
}

func TestClustersAcrossProjects(t *testing.T) {
	c := fakeGcloud(t, `case " $* " in
  *' --project p-a '*) echo '[{"name":"a","location":"us-central1"}]' ;;
  *' --project p-b '*) echo '[{"name":"b","location":"europe-west2"}]' ;;
  *) echo 'API not enabled' >&2; exit 1 ;;
esac
`)
	got, e := c.Clusters(context.Background(), 5*time.Second, []string{"p-b", "p-a", "p-off"})
	want := []config.Cluster{{Name: "a", Region: "us-central1", Project: "p-a"}, {Name: "b", Region: "europe-west2", Project: "p-b"}}
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, e)
	}
	if _, e := c.Clusters(context.Background(), 5*time.Second, []string{"p-off"}); e == nil || !strings.Contains(e.Error(), "API not enabled") {
		t.Fatal("every project failing was not an error:", e)
	}
}

func TestProjectsByPrefix(t *testing.T) {
	c := fakeGcloud(t, `case " $* " in
  *' projects list --filter=projectId:acme-* '*) printf 'acme-a\nacme-b\n' ;;
  *' projects list --filter=projectId:a* '*) seq 1 301 ;;
  *) exit 1 ;;
esac
`)
	for _, tc := range []struct {
		prefix string
		want   []string
		err    string
	}{
		{"acme-", []string{"acme-a", "acme-b"}, ""},
		{"a", nil, "type a longer prefix"},
		{"x' --flag", nil, "invalid project ID prefix"},
		{"", nil, "invalid project ID prefix"},
	} {
		got, e := c.Projects(context.Background(), 5*time.Second, tc.prefix)
		if tc.err != "" && (e == nil || !strings.Contains(e.Error(), tc.err)) || tc.err == "" && (e != nil || !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("%q: got %v, %v", tc.prefix, got, e)
		}
	}
}

func TestConfiguredProjects(t *testing.T) {
	c := fakeGcloud(t, "printf 'proj-a\\n\\nproj-b\\n'\n")
	got, e := c.ConfiguredProjects(context.Background(), 5*time.Second)
	if e != nil || !reflect.DeepEqual(got, []string{"proj-a", "proj-b"}) {
		t.Fatal(got, e)
	}
}

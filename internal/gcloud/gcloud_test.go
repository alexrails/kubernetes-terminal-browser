package gcloud

import (
	"kubernetes-terminal-browser/internal/config"
	"reflect"
	"testing"
)

func TestArgs(t *testing.T) {
	p := config.Cluster{Name: "blog-europe-west2-dev", Region: "europe-west2", Project: "acme-blog-dev"}
	want := []string{"container", "clusters", "get-credentials", "blog-europe-west2-dev", "--region", "europe-west2", "--project", "acme-blog-dev"}
	if got := Args(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

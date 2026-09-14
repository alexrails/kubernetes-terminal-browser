package presentation

import (
	api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func owned(name, kind, owner, hash string) api.Pod {
	yes := true
	p := api.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{}, OwnerReferences: []metav1.OwnerReference{{Kind: kind, Name: owner, Controller: &yes}}}}
	if hash != "" {
		p.Labels["pod-template-hash"] = hash
	}
	return p
}

func TestAppNames(t *testing.T) {
	for _, c := range []struct {
		pod        api.Pod
		name, kind string
	}{
		{owned("admin-web-84c6654568-nwfwg", "ReplicaSet", "admin-web-84c6654568", "84c6654568"), "admin-web", "Deployment"},
		{owned("cashman-simple-expirator-29837173-5n9fb", "Job", "cashman-simple-expirator-29837173", ""), "cashman-simple-expirator", "CronJob"},
		{owned("redis-cluster-0", "StatefulSet", "redis-cluster", ""), "redis-cluster", "StatefulSet"},
		{api.Pod{ObjectMeta: metav1.ObjectMeta{Name: "debug"}}, "debug", "Pod"},
	} {
		if n, k := App(&c.pod); n != c.name || k != c.kind {
			t.Errorf("%s: got %s/%s, want %s/%s", c.pod.Name, k, n, c.kind, c.name)
		}
	}
}

func TestBestPodPrefersReadyThenNewest(t *testing.T) {
	now := time.Now()
	mk := func(name string, phase api.PodPhase, ready bool, age time.Duration) api.Pod {
		p := api.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.NewTime(now.Add(-age))}}
		p.Status.Phase = phase
		if ready {
			p.Status.Conditions = []api.PodCondition{{Type: api.PodReady, Status: api.ConditionTrue}}
		}
		return p
	}
	p, ok := BestPod([]api.Pod{mk("new-pending", api.PodPending, false, time.Minute), mk("old-ready", api.PodRunning, true, time.Hour), mk("new-ready", api.PodRunning, true, 2*time.Minute), mk("done", api.PodSucceeded, false, 0)})
	if !ok || p.Name != "new-ready" {
		t.Fatalf("best = %s", p.Name)
	}
}

func TestFuzzyRanksContiguousAndWordStarts(t *testing.T) {
	if _, ok := Fuzzy("admin-web-587dcb6845-2rfq8", "adweb"); !ok {
		t.Fatal("subsequence not matched")
	}
	if _, ok := Fuzzy("billing-api", "xyz"); ok {
		t.Fatal("non-match matched")
	}
	a, _ := Fuzzy("admin-web", "admin")
	b, _ := Fuzzy("report-engine-admin-7b6c", "admin")
	c, _ := Fuzzy("a-d-m-i-n-long-name", "admin")
	if !(a > b && b > c) {
		t.Fatalf("ranking %d %d %d", a, b, c)
	}
}

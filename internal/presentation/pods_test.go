package presentation

import (
	"encoding/json"
	"fmt"
	core "k8s.io/api/core/v1"
	"os"
	"testing"
	"time"
)

func TestPodParityFixtures(t *testing.T) {
	data, e := os.ReadFile("testdata/pods.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Name                         string
		Pod                          core.Pod
		Ready, Status, Restarts, Age string
	}
	if e = json.Unmarshal(data, &cases); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := Pod(&tc.Pod, now)
			if got.Ready != tc.Ready || got.Status != tc.Status || got.Restarts != tc.Restarts || (tc.Age != "" && got.Age != tc.Age) {
				t.Fatalf("got %+v; want READY %s STATUS %s RESTARTS %s AGE %s", got, tc.Ready, tc.Status, tc.Restarts, tc.Age)
			}
		})
	}
}
func TestFilterAndIdentity(t *testing.T) {
	p := []core.Pod{{}, {}}
	p[0].Name = "z-a"
	p[0].UID = "uid-z"
	p[1].Name = "A-b"
	p[1].UID = "uid-a"
	got := Filter(p, "A")
	if len(got) != 2 || got[0].Name != "A-b" || got[1].UID != "uid-z" {
		t.Fatalf("unexpected %+v", got)
	}
}

func BenchmarkThousandPodSearchAndRows(b *testing.B) {
	pods := make([]core.Pod, 1000)
	for i := range pods {
		pods[i].Name = fmt.Sprintf("sample-%04d", i)
		pods[i].Spec.Containers = []core.Container{{Name: "app"}}
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows := Filter(pods, "sample")
		for j := range rows {
			_ = Pod(&rows[j], now)
		}
	}
}

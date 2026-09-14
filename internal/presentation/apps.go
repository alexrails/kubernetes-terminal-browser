package presentation

import (
	api "k8s.io/api/core/v1"
	"sort"
	"strings"
	"time"
	"unicode"
)

// App names the workload a pod belongs to, from its owner: a ReplicaSet
// without the pod-template-hash is its Deployment, a Job with a numeric
// schedule suffix is its CronJob. A pod without an owner is its own app.
func App(p *api.Pod) (name, kind string) {
	for _, o := range p.OwnerReferences {
		if o.Controller == nil || !*o.Controller {
			continue
		}
		switch o.Kind {
		case "ReplicaSet":
			if h := p.Labels["pod-template-hash"]; h != "" && strings.HasSuffix(o.Name, "-"+h) {
				return strings.TrimSuffix(o.Name, "-"+h), "Deployment"
			}
		case "Job":
			if i := strings.LastIndexByte(o.Name, '-'); i > 0 && len(o.Name)-i > 8 && digits(o.Name[i+1:]) {
				return o.Name[:i], "CronJob"
			}
		}
		return o.Name, o.Kind
	}
	return p.Name, "Pod"
}
func digits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) }) < 0
}

// AppGroup is the pods of one application.
type AppGroup struct {
	Name, Kind string
	Pods       []api.Pod
}

// Apps groups pods by application, sorted by name.
func Apps(pods []api.Pod) []AppGroup {
	idx := map[string]int{}
	var out []AppGroup
	for _, p := range pods {
		name, kind := App(&p)
		key := kind + "/" + name
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, AppGroup{Name: name, Kind: kind})
		}
		out[i].Pods = append(out[i].Pods, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for _, g := range out {
		sort.SliceStable(g.Pods, func(i, j int) bool { return g.Pods[i].Name < g.Pods[j].Name })
	}
	return out
}

// Active reports pods that are not finished (Succeeded/Failed) or deleting.
func Active(p *api.Pod) bool {
	return p.DeletionTimestamp == nil && p.Status.Phase != api.PodSucceeded && p.Status.Phase != api.PodFailed
}

// BestPod picks the pod to open for an application: running and ready first,
// then running, then any active pod, newest first within each rank.
func BestPod(pods []api.Pod) (api.Pod, bool) {
	rank := func(p *api.Pod) int {
		switch {
		case !Active(p):
			return 0
		case p.Status.Phase == api.PodRunning && hasCondition(p.Status.Conditions, api.PodReady):
			return 3
		case p.Status.Phase == api.PodRunning:
			return 2
		}
		return 1
	}
	best, found := -1, false
	for i := range pods {
		if !found || rank(&pods[i]) > rank(&pods[best]) ||
			(rank(&pods[i]) == rank(&pods[best]) && pods[i].CreationTimestamp.After(pods[best].CreationTimestamp.Time)) {
			best, found = i, true
		}
	}
	if !found {
		return api.Pod{}, false
	}
	return pods[best], true
}

// GroupRow is the summary line of an application.
type GroupRow struct {
	Name, Kind, Pods, Status, Restarts, Age string
	Worst                                   string
}

// Group summarises an application: pods ready/total among active pods, the
// statuses by count (worst first) and the age of its newest pod.
func Group(g AppGroup, now time.Time) GroupRow {
	counts := map[string]int{}
	var order []string
	active, ready, restarts := 0, 0, 0
	newest := g.Pods[0].CreationTimestamp
	for i := range g.Pods {
		p := &g.Pods[i]
		r := Pod(p, now)
		if counts[r.Status] == 0 {
			order = append(order, r.Status)
		}
		counts[r.Status]++
		if Active(p) {
			active++
			if hasCondition(p.Status.Conditions, api.PodReady) {
				ready++
			}
		}
		for _, c := range p.Status.ContainerStatuses {
			restarts += int(c.RestartCount)
		}
		if p.CreationTimestamp.After(newest.Time) {
			newest = p.CreationTimestamp
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return severity(order[i]) > severity(order[j]) })
	var parts []string
	for _, s := range order {
		if counts[s] > 1 {
			parts = append(parts, s+" ×"+itoa(counts[s]))
		} else {
			parts = append(parts, s)
		}
	}
	pods := itoa(ready) + "/" + itoa(active)
	if active == 0 {
		pods = itoa(len(g.Pods)) + " done"
	}
	return GroupRow{Name: g.Name, Kind: g.Kind, Pods: pods, Status: strings.Join(parts, ", "), Restarts: itoa(restarts), Age: age(newest, now), Worst: order[0]}
}
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// severity orders statuses: failures, then progress, then running, then done.
func severity(s string) int {
	switch {
	case s == "Completed" || s == "Succeeded":
		return 0
	case s == "Running":
		return 1
	case s == "Failed" || s == "Unknown" || s == "Evicted" || s == "NotReady" || strings.Contains(s, "Err") ||
		strings.Contains(s, "BackOff") || strings.Contains(s, "Kill") || strings.Contains(s, "ExitCode") || strings.Contains(s, "Signal"):
		return 3
	}
	return 2
}

// Fuzzy matches query as a case-insensitive subsequence of s. Higher scores
// are better: a contiguous match, a match at a word start and a short
// candidate all rank up.
func Fuzzy(s, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	ls, lq := strings.ToLower(s), strings.ToLower(query)
	if i := strings.Index(ls, lq); i >= 0 {
		score := 1000 - len(ls)
		if i == 0 || strings.ContainsRune("-_./ ", rune(ls[i-1])) {
			score += 500
		}
		return score, true
	}
	score, qi, prev := 0, 0, -2
	rs, rq := []rune(ls), []rune(lq)
	for i, r := range rs {
		if qi < len(rq) && r == rq[qi] {
			switch {
			case i == prev+1:
				score += 15
			case i == 0 || strings.ContainsRune("-_./ ", rs[i-1]):
				score += 10
			default:
				score++
			}
			prev = i
			qi++
		}
	}
	if qi < len(rq) {
		return 0, false
	}
	return score - len(rs)/4, true
}

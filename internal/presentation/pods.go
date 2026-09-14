/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Adapted from Kubernetes v1.35.0 pkg/printers/internalversion/printers.go.
// Changes: public core/v1 types, deterministic clock, five-column Row result.
package presentation

import (
	"fmt"
	api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Row struct{ Name, UID, Ready, Status, Restarts, Age string }

func restartable(c *api.Container) bool {
	return c != nil && c.RestartPolicy != nil && *c.RestartPolicy == api.ContainerRestartPolicyAlways
}
func hasCondition(cs []api.PodCondition, t api.PodConditionType) bool {
	for _, c := range cs {
		if c.Type == t {
			return c.Status == api.ConditionTrue
		}
	}
	return false
}
func age(t metav1.Time, now time.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	return duration.HumanDuration(now.Sub(t.Time))
}
func Pod(pod *api.Pod, now time.Time) Row {
	restarts := 0
	restartableInitContainerRestarts := 0
	totalContainers := len(pod.Spec.Containers)
	readyContainers := 0
	lastRestartDate := metav1.NewTime(time.Time{})
	lastRestartableInitContainerRestartDate := metav1.NewTime(time.Time{})

	podPhase := pod.Status.Phase
	reason := string(podPhase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}

	// If the Pod carries {type:PodScheduled, reason:SchedulingGated}, set reason to 'SchedulingGated'.
	for _, condition := range pod.Status.Conditions {
		if condition.Type == api.PodScheduled && condition.Reason == api.PodReasonSchedulingGated {
			reason = api.PodReasonSchedulingGated
		}
	}

	initContainers := make(map[string]*api.Container)
	for i := range pod.Spec.InitContainers {
		initContainers[pod.Spec.InitContainers[i].Name] = &pod.Spec.InitContainers[i]
		if restartable(&pod.Spec.InitContainers[i]) {
			totalContainers++
		}
	}

	initializing := false
	for i := range pod.Status.InitContainerStatuses {
		container := pod.Status.InitContainerStatuses[i]
		restarts += int(container.RestartCount)
		if container.LastTerminationState.Terminated != nil {
			terminatedDate := container.LastTerminationState.Terminated.FinishedAt
			if lastRestartDate.Before(&terminatedDate) {
				lastRestartDate = terminatedDate
			}
		}
		if restartable(initContainers[container.Name]) {
			restartableInitContainerRestarts += int(container.RestartCount)
			if container.LastTerminationState.Terminated != nil {
				terminatedDate := container.LastTerminationState.Terminated.FinishedAt
				if lastRestartableInitContainerRestartDate.Before(&terminatedDate) {
					lastRestartableInitContainerRestartDate = terminatedDate
				}
			}
		}
		switch {
		case container.State.Terminated != nil && container.State.Terminated.ExitCode == 0:
			continue
		case restartable(initContainers[container.Name]) &&
			container.Started != nil && *container.Started:
			if container.Ready {
				readyContainers++
			}
			continue
		case container.State.Terminated != nil:
			// initialization is failed
			if len(container.State.Terminated.Reason) == 0 {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Init:Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("Init:ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else {
				reason = "Init:" + container.State.Terminated.Reason
			}
			initializing = true
		case container.State.Waiting != nil && len(container.State.Waiting.Reason) > 0 && container.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + container.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}

	if !initializing || hasCondition(pod.Status.Conditions, api.PodInitialized) {
		restarts = restartableInitContainerRestarts
		lastRestartDate = lastRestartableInitContainerRestartDate
		hasRunning := false
		errorReason := ""
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			container := pod.Status.ContainerStatuses[i]

			restarts += int(container.RestartCount)
			if container.LastTerminationState.Terminated != nil {
				terminatedDate := container.LastTerminationState.Terminated.FinishedAt
				if lastRestartDate.Before(&terminatedDate) {
					lastRestartDate = terminatedDate
				}
			}
			switch {
			case container.State.Waiting != nil && container.State.Waiting.Reason != "":
				reason = container.State.Waiting.Reason
			case container.State.Terminated != nil:
				if len(container.State.Terminated.Reason) > 0 {
					reason = container.State.Terminated.Reason
				} else if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", container.State.Terminated.ExitCode)
				}
				if container.State.Terminated.ExitCode != 0 {
					errorReason = reason
				}
			case container.Ready && container.State.Running != nil:
				hasRunning = true
				readyContainers++
			}
		}

		// change pod status back to "Running" if there is at least one container still reporting as "Running" status
		if reason == "Completed" {
			if hasRunning && hasCondition(pod.Status.Conditions, api.PodReady) {
				reason = "Running"
			} else if errorReason != "" {
				reason = errorReason
			} else if hasRunning {
				reason = "NotReady"
			}
		}
	}

	if pod.DeletionTimestamp != nil && pod.Status.Reason == "NodeLost" {
		reason = "Unknown"
	} else if pod.DeletionTimestamp != nil && podPhase != api.PodSucceeded && podPhase != api.PodFailed {
		reason = "Terminating"
	}

	restartsStr := strconv.Itoa(restarts)
	if restarts != 0 && !lastRestartDate.IsZero() {
		restartsStr = fmt.Sprintf("%d (%s ago)", restarts, age(lastRestartDate, now))
	}

	return Row{Name: pod.Name, UID: string(pod.UID), Ready: fmt.Sprintf("%d/%d", readyContainers, totalContainers), Status: reason, Restarts: restartsStr, Age: age(pod.CreationTimestamp, now)}
}
func Filter(pods []api.Pod, query string) []api.Pod {
	var out []api.Pod
	q := strings.ToLower(query)
	for _, p := range pods {
		if strings.Contains(strings.ToLower(p.Name), q) {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

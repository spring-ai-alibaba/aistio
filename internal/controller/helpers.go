package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/prober"
)

// agentWorkloadRefPredicate selects Agents based on whether they are BYO
// workloadRef agents. AgentReconciler wants the non-workloadRef agents
// (Declarative + BYO image); BYOWorkloadReconciler wants workloadRef agents.
func agentWorkloadRefPredicate(wantWorkloadRef bool) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(o client.Object) bool {
		a, ok := o.(*v1alpha1.Agent)
		if !ok {
			return false
		}
		isWorkloadRef := a.Spec.Type == v1alpha1.AgentTypeBYO &&
			a.Spec.BYO != nil && a.Spec.BYO.WorkloadRef != nil
		return isWorkloadRef == wantWorkloadRef
	})
}

// managedDeploymentPredicate selects only Deployments labeled as managed,
// so the DiscoveryController is not enqueued for every Deployment in the cluster.
func managedDeploymentPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(o client.Object) bool {
		return o.GetLabels()[labelManaged] == "true"
	})
}

// toDataPlaneInfo converts a probe result into the CRD status representation.
func toDataPlaneInfo(info *prober.DataPlaneInfo) *v1alpha1.DataPlaneInfo {
	dpi := &v1alpha1.DataPlaneInfo{
		ContractLevel:   info.ContractLevel,
		SDKVersion:      info.SDKVersion,
		Version:         info.Version,
		SessionAffinity: info.SessionAffinity,
		Capabilities:    info.Capabilities,
		LastProbeAt:     time.Now().Format(time.RFC3339),
	}
	if info.AgentConfig != nil {
		dpi.Model = info.AgentConfig.Model
		dpi.ModelProvider = info.AgentConfig.ModelProvider
		dpi.Tools = info.AgentConfig.Tools
	}
	return dpi
}

// countActiveSessions counts non-terminated AgentSessions referencing the agent.
func countActiveSessions(ctx context.Context, c client.Client, agentName, namespace string) int32 {
	var sessions v1alpha1.AgentSessionList
	if err := c.List(ctx, &sessions, client.InNamespace(namespace)); err != nil {
		return 0
	}
	var count int32
	for i := range sessions.Items {
		s := &sessions.Items[i]
		if s.Spec.AgentRef.Name != agentName {
			continue
		}
		if s.Status.Phase == v1alpha1.SessionPhaseTerminated {
			continue
		}
		count++
	}
	return count
}

// findReadyPodIPForDeployment finds a ready pod IP for a given deployment.
func findReadyPodIPForDeployment(ctx context.Context, c client.Client, dep *appsv1.Deployment) (string, error) {
	var podList corev1.PodList
	if err := c.List(ctx, &podList,
		client.InNamespace(dep.Namespace),
		client.MatchingLabels(dep.Spec.Selector.MatchLabels),
	); err != nil {
		return "", err
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					return pod.Status.PodIP, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no ready pods found for deployment %s/%s", dep.Namespace, dep.Name)
}

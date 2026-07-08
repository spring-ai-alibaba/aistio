package controller

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/metrics"
	"github.com/spring-ai-alibaba/aistio/internal/prober"
)

const (
	labelSessionAgent   = "agentscope.io/agent"
	annoSessionID       = "agentscope.io/session-id"
	sessionPollInterval = 15 * time.Second
)

// SessionPollerReconciler periodically polls agents with contractLevel >= 2
// for session data and syncs AgentSession CRDs.
type SessionPollerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Prober   prober.DataPlaneProber
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=agentscope.io,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=agentscope.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agentscope.io,resources=agentsessions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=agentscope.io,resources=agentsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *SessionPollerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agent v1alpha1.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		metrics.RecordReconcileError("session-poller", "get-agent")
		return ctrl.Result{}, err
	}

	// Check contractLevel
	var contractLevel int32
	if agent.Status.DataPlaneInfo != nil {
		contractLevel = agent.Status.DataPlaneInfo.ContractLevel
	}

	if contractLevel < 2 {
		logger.V(1).Info("skipping session polling, contractLevel < 2",
			"agent", agent.Name, "contractLevel", contractLevel)
		return ctrl.Result{RequeueAfter: sessionPollInterval}, r.setPollerCondition(ctx, &agent,
			"SessionPollingUnsupported",
			fmt.Sprintf("contractLevel %d does not support session polling (requires >= 2)", contractLevel))
	}

	// Find a ready pod endpoint
	endpoint, err := r.findAgentEndpoint(ctx, &agent)
	if err != nil {
		logger.Info("cannot find agent endpoint for session polling", "agent", agent.Name, "error", err)
		return ctrl.Result{RequeueAfter: sessionPollInterval}, nil
	}

	// Poll sessions from data plane
	probeStart := time.Now()
	snapshots, err := r.Prober.ProbeSessions(ctx, endpoint)
	metrics.RecordProbeLatency(agent.Namespace, agent.Name, "sessions", time.Since(probeStart))
	if err != nil {
		logger.Info("failed to poll sessions", "agent", agent.Name, "error", err)
		metrics.RecordDataPlaneStatus(agent.Namespace, agent.Name, false, contractLevel)
		return ctrl.Result{RequeueAfter: sessionPollInterval}, nil
	}
	metrics.RecordDataPlaneStatus(agent.Namespace, agent.Name, true, contractLevel)

	// Build a set of data-plane session IDs for orphan detection.
	// Keyed by the raw data-plane ID (tracked on the CRD via annotation),
	// since the CRD object name may be a sanitized/hashed form of the ID.
	dpSessionIDs := make(map[string]struct{}, len(snapshots))
	for _, snap := range snapshots {
		dpSessionIDs[snap.ID] = struct{}{}
	}

	// Sync each session snapshot to a CRD
	for i := range snapshots {
		if err := r.syncSession(ctx, &agent, &snapshots[i]); err != nil {
			logger.Error(err, "failed to sync session", "sessionID", snapshots[i].ID)
		}
	}

	// Mark orphaned CRD sessions as Terminated
	if err := r.markOrphanedSessions(ctx, &agent, dpSessionIDs); err != nil {
		logger.Error(err, "failed to mark orphaned sessions")
	}

	// Update agent's activeSessions count
	activeSessions := countActiveSessions(ctx, r.Client, agent.Name, agent.Namespace)
	metrics.RecordSessionCount(agent.Namespace, agent.Name, activeSessions)
	if err := r.updateAgentSessionCount(ctx, &agent, activeSessions); err != nil {
		logger.Error(err, "failed to update agent session count")
	}

	return ctrl.Result{RequeueAfter: sessionPollInterval}, nil
}

func (r *SessionPollerReconciler) findAgentEndpoint(ctx context.Context, agent *v1alpha1.Agent) (string, error) {
	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}, &dep); err != nil {
		return "", fmt.Errorf("looking up deployment: %w", err)
	}

	podIP, err := findReadyPodIPForDeployment(ctx, r.Client, &dep)
	if err != nil {
		return "", err
	}

	port := int32(8080)
	if agent.Spec.BYO != nil && agent.Spec.BYO.AgentPort > 0 {
		port = agent.Spec.BYO.AgentPort
	}

	return fmt.Sprintf("http://%s:%d", podIP, port), nil
}

func (r *SessionPollerReconciler) syncSession(ctx context.Context, agent *v1alpha1.Agent, snap *prober.SessionSnapshot) error {
	o := ObservedSession{
		ID:              snap.ID,
		Phase:           snap.Phase,
		MessageCount:    snap.MessageCount,
		ContextPressure: snap.ContextPressure,
		StartedAt:       snap.StartedAt,
		LastActiveAt:    snap.LastActiveAt,
	}
	if snap.TokenUsage != nil {
		o.PromptTokens = snap.TokenUsage.PromptTokens
		o.CompletionTokens = snap.TokenUsage.CompletionTokens
	}
	return upsertObservedSession(ctx, r.Client, r.Scheme, agent, o)
}

func (r *SessionPollerReconciler) markOrphanedSessions(ctx context.Context, agent *v1alpha1.Agent, dpSessionIDs map[string]struct{}) error {
	var sessions v1alpha1.AgentSessionList
	if err := r.List(ctx, &sessions,
		client.InNamespace(agent.Namespace),
		client.MatchingLabels{labelSessionAgent: agent.Name},
	); err != nil {
		return err
	}

	for i := range sessions.Items {
		s := &sessions.Items[i]
		if s.Status.Phase == v1alpha1.SessionPhaseTerminated {
			continue
		}
		// Skip sessions created in the last 60 seconds — they may not yet
		// appear in the data plane's response.
		if time.Since(s.CreationTimestamp.Time) < 60*time.Second {
			continue
		}
		// Match on the raw data-plane session ID recorded in the annotation,
		// falling back to the object name for sessions created before the
		// annotation existed.
		dpID := s.Annotations[annoSessionID]
		if dpID == "" {
			dpID = s.Name
		}
		if _, exists := dpSessionIDs[dpID]; exists {
			continue
		}
		// Session exists in CRD but not in data plane — mark as terminated
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var fresh v1alpha1.AgentSession
			if err := r.Get(ctx, client.ObjectKeyFromObject(s), &fresh); err != nil {
				return err
			}
			fresh.Status.Phase = v1alpha1.SessionPhaseTerminated
			setConditionInList(&fresh.Status.Conditions, v1alpha1.Condition{
				Type:               v1alpha1.ConditionReady,
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Now(),
				Reason:             "SessionNotFoundInDataPlane",
				Message:            "Session no longer reported by data plane, marked as terminated",
			})
			return r.Status().Update(ctx, &fresh)
		}); err != nil {
			return fmt.Errorf("marking session %s as terminated: %w", s.Name, err)
		}
	}

	return nil
}

func (r *SessionPollerReconciler) updateAgentSessionCount(ctx context.Context, agent *v1alpha1.Agent, count int32) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh v1alpha1.Agent
		if err := r.Get(ctx, client.ObjectKeyFromObject(agent), &fresh); err != nil {
			return err
		}
		fresh.Status.ActiveSessions = count
		return r.Status().Update(ctx, &fresh)
	})
}

func (r *SessionPollerReconciler) setPollerCondition(ctx context.Context, agent *v1alpha1.Agent, reason, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh v1alpha1.Agent
		if err := r.Get(ctx, client.ObjectKeyFromObject(agent), &fresh); err != nil {
			return err
		}
		setConditionInList(&fresh.Status.Conditions, v1alpha1.Condition{
			Type:               "SessionPolling",
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		})
		return r.Status().Update(ctx, &fresh)
	})
}

func (r *SessionPollerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A distinct name is required: the default name derived from the watched
	// kind ("agent") would collide with AgentReconciler. The predicate scopes
	// polling to control-plane-managed agents (declarative / BYO-image), whose
	// Deployment is named after the Agent; workloadRef agents are skipped.
	return ctrl.NewControllerManagedBy(mgr).
		Named("session-poller").
		For(&v1alpha1.Agent{}, builder.WithPredicates(agentWorkloadRefPredicate(false))).
		Complete(r)
}

// sanitizeSessionName converts an arbitrary data-plane session ID into a valid
// Kubernetes object name. IDs that are already DNS-1123 subdomains are used
// as-is for readability; otherwise a deterministic hash is used.
func sanitizeSessionName(id string) string {
	if errs := validation.IsDNS1123Subdomain(id); len(errs) == 0 {
		return id
	}
	sum := sha1.Sum([]byte(id))
	return "session-" + hex.EncodeToString(sum[:])[:16]
}

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/metrics"
	"github.com/spring-ai-alibaba/aistio/internal/prober"
)

// AgentSessionReconciler watches AgentSession CRDs and executes commands.
type AgentSessionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Prober   prober.DataPlaneProber
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=agentscope.io,resources=agentsessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agentscope.io,resources=agentsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agentscope.io,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AgentSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var session v1alpha1.AgentSession
	if err := r.Get(ctx, req.NamespacedName, &session); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		metrics.RecordReconcileError("session", "get-session")
		return ctrl.Result{}, err
	}

	logger.Info("reconciling AgentSession", "name", session.Name)

	// Process commands
	if session.Spec.Commands == nil {
		return ctrl.Result{}, nil
	}

	if session.Spec.Commands.Compress && session.Status.Phase != v1alpha1.SessionPhaseCompressing {
		return r.handleCompress(ctx, &session)
	}

	if session.Spec.Commands.Terminate && session.Status.Phase != v1alpha1.SessionPhaseTerminated {
		return r.handleTerminate(ctx, &session)
	}

	return ctrl.Result{}, nil
}

func (r *AgentSessionReconciler) handleCompress(ctx context.Context, session *v1alpha1.AgentSession) (ctrl.Result, error) {
	contractLevel, endpoint, err := r.resolveDataPlane(ctx, session)
	if err != nil {
		metrics.RecordReconcileError("session", "resolve-dataplane")
		return ctrl.Result{}, err
	}

	if contractLevel < 2 {
		r.Recorder.Eventf(session, corev1.EventTypeWarning, "SessionObservabilityUnsupported",
			"session observability not supported at contractLevel %d", contractLevel)
		metrics.RecordReconcileError("session", "compress-contract-level")
		return ctrl.Result{}, r.setSessionCondition(ctx, session, "SessionPollingUnsupported",
			fmt.Sprintf("data plane contractLevel %d does not support session observability (requires >= 2)", contractLevel))
	}

	if contractLevel < 3 {
		r.Recorder.Eventf(session, corev1.EventTypeWarning, "ContractLevelInsufficient",
			"data plane does not support compress command at contractLevel %d", contractLevel)
		metrics.RecordReconcileError("session", "compress-contract-level")
		return ctrl.Result{}, r.setSessionCondition(ctx, session, "ContractLevelInsufficient",
			fmt.Sprintf("compress requires contractLevel >= 3, agent has %d", contractLevel))
	}

	agentName := session.Spec.AgentRef.Name
	namespace := session.Namespace

	// Dispatch compress to data plane
	if err := r.Prober.SendCompress(ctx, endpoint, session.Name); err != nil {
		r.Recorder.Eventf(session, corev1.EventTypeWarning, "CompressFailed",
			"failed to send compress command: %v", err)
		metrics.RecordSessionOperation(namespace, agentName, "compress", "error")
		return ctrl.Result{}, err
	}

	metrics.RecordSessionOperation(namespace, agentName, "compress", "success")

	r.Recorder.Eventf(session, corev1.EventTypeNormal, "CompressDispatched",
		"compress command sent to data plane for session %s", session.Name)
	return ctrl.Result{}, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh v1alpha1.AgentSession
		if err := r.Get(ctx, client.ObjectKeyFromObject(session), &fresh); err != nil {
			return err
		}
		fresh.Status.Phase = v1alpha1.SessionPhaseCompressing
		setConditionInList(&fresh.Status.Conditions, v1alpha1.Condition{
			Type:               v1alpha1.ConditionReady,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "CompressDispatched",
			Message:            "Context compression dispatched to data plane",
		})
		return r.Status().Update(ctx, &fresh)
	})
}

func (r *AgentSessionReconciler) handleTerminate(ctx context.Context, session *v1alpha1.AgentSession) (ctrl.Result, error) {
	contractLevel, endpoint, err := r.resolveDataPlane(ctx, session)
	if err != nil {
		metrics.RecordReconcileError("session", "resolve-dataplane")
		return ctrl.Result{}, err
	}

	if contractLevel < 2 {
		r.Recorder.Eventf(session, corev1.EventTypeWarning, "SessionObservabilityUnsupported",
			"session observability not supported at contractLevel %d", contractLevel)
		metrics.RecordReconcileError("session", "terminate-contract-level")
		return ctrl.Result{}, r.setSessionCondition(ctx, session, "SessionPollingUnsupported",
			fmt.Sprintf("data plane contractLevel %d does not support session observability (requires >= 2)", contractLevel))
	}

	if contractLevel < 3 {
		r.Recorder.Eventf(session, corev1.EventTypeWarning, "ContractLevelInsufficient",
			"data plane does not support terminate command at contractLevel %d", contractLevel)
		metrics.RecordReconcileError("session", "terminate-contract-level")
		return ctrl.Result{}, r.setSessionCondition(ctx, session, "ContractLevelInsufficient",
			fmt.Sprintf("terminate requires contractLevel >= 3, agent has %d", contractLevel))
	}

	agentName := session.Spec.AgentRef.Name
	namespace := session.Namespace

	// Dispatch terminate to data plane
	if err := r.Prober.SendTerminate(ctx, endpoint, session.Name); err != nil {
		r.Recorder.Eventf(session, corev1.EventTypeWarning, "TerminateFailed",
			"failed to send terminate command: %v", err)
		metrics.RecordSessionOperation(namespace, agentName, "terminate", "error")
		return ctrl.Result{}, err
	}

	metrics.RecordSessionOperation(namespace, agentName, "terminate", "success")

	r.Recorder.Eventf(session, corev1.EventTypeNormal, "TerminateDispatched",
		"terminate command sent to data plane for session %s", session.Name)
	return ctrl.Result{}, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh v1alpha1.AgentSession
		if err := r.Get(ctx, client.ObjectKeyFromObject(session), &fresh); err != nil {
			return err
		}
		fresh.Status.Phase = v1alpha1.SessionPhaseTerminated
		setConditionInList(&fresh.Status.Conditions, v1alpha1.Condition{
			Type:               v1alpha1.ConditionReady,
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             "TerminateDispatched",
			Message:            "Session termination dispatched to data plane",
		})
		return r.Status().Update(ctx, &fresh)
	})
}

// resolveDataPlane looks up the referenced agent, checks contractLevel, and finds a pod endpoint.
func (r *AgentSessionReconciler) resolveDataPlane(ctx context.Context, session *v1alpha1.AgentSession) (int32, string, error) {
	agentName := session.Spec.AgentRef.Name
	ns := session.Namespace

	var agent v1alpha1.Agent
	if err := r.Get(ctx, types.NamespacedName{Name: agentName, Namespace: ns}, &agent); err != nil {
		return 0, "", fmt.Errorf("looking up agent %s: %w", agentName, err)
	}

	var contractLevel int32
	if agent.Status.DataPlaneInfo != nil {
		contractLevel = agent.Status.DataPlaneInfo.ContractLevel
	}

	// Find a ready pod for the agent's deployment
	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Name: agentName, Namespace: ns}, &dep); err != nil {
		return contractLevel, "", fmt.Errorf("looking up deployment for agent %s: %w", agentName, err)
	}

	podIP, err := findReadyPodIPForDeployment(ctx, r.Client, &dep)
	if err != nil {
		return contractLevel, "", fmt.Errorf("finding ready pod: %w", err)
	}

	// Determine port from agent spec
	port := int32(8080)
	if agent.Spec.BYO != nil && agent.Spec.BYO.AgentPort > 0 {
		port = agent.Spec.BYO.AgentPort
	}

	endpoint := fmt.Sprintf("http://%s:%d", podIP, port)
	return contractLevel, endpoint, nil
}

func (r *AgentSessionReconciler) setSessionCondition(ctx context.Context, session *v1alpha1.AgentSession, reason, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh v1alpha1.AgentSession
		if err := r.Get(ctx, client.ObjectKeyFromObject(session), &fresh); err != nil {
			return err
		}
		setConditionInList(&fresh.Status.Conditions, v1alpha1.Condition{
			Type:               v1alpha1.ConditionReady,
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		})
		return r.Status().Update(ctx, &fresh)
	})
}

func (r *AgentSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AgentSession{}).
		Complete(r)
}

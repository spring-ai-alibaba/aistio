package controller

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelTrace "go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/metrics"
	"github.com/spring-ai-alibaba/aistio/internal/tracing"
)

// TeamEventDeliverer sends team events to connected members.
// Implemented by asdp.Distributor wrapper.
type TeamEventDeliverer interface {
	DeliverTeamEvent(namespace, instanceID, teamID, eventType, memberName, content string) error
	GetConnectedInstance(namespace, agentName string) (instanceID string, ok bool)
}

// TeamEventWatcher watches TeamMessage outbox and delivers to local connections.
// Runs on ALL replicas (NeedLeaderElection = false).
type TeamEventWatcher struct {
	client.Client
	Scheme    *runtime.Scheme
	Deliverer TeamEventDeliverer
}

func (w *TeamEventWatcher) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, span := tracing.Tracer().Start(ctx, "teamOutbox.Deliver",
		otelTrace.WithAttributes(attribute.String("message", req.Name)))
	defer span.End()

	logger := log.FromContext(ctx)

	var msg v1alpha1.TeamMessage
	if err := w.Get(ctx, req.NamespacedName, &msg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip already delivered
	if msg.Status.Delivered {
		return ctrl.Result{}, nil
	}

	// Skip if too many attempts
	if msg.Status.Attempts >= 5 {
		logger.Info("message exceeded max attempts", "message", msg.Name)
		metrics.RecordTeamMessage(msg.Namespace, msg.Spec.TeamRef, "dropped")
		span.SetStatus(codes.Error, "max attempts exceeded")
		return ctrl.Result{}, nil
	}

	if w.Deliverer == nil {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	targetMember := msg.Spec.To
	if targetMember == "" {
		return ctrl.Result{}, nil
	}

	// Look up which agent/instance hosts this member
	var sessions v1alpha1.AgentSessionList
	if err := w.List(ctx, &sessions, client.InNamespace(msg.Namespace),
		client.MatchingLabels{
			"agentscope.io/team":      msg.Spec.TeamRef,
			"agentscope.io/team-role": targetMember,
		}); err != nil {
		metrics.RecordReconcileError("teamevent-watcher", "list-sessions")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}

	if len(sessions.Items) == 0 {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	sess := sessions.Items[0]
	agentName := sess.Labels["agentscope.io/agent"]

	instanceID, connected := w.Deliverer.GetConnectedInstance(msg.Namespace, agentName)
	if !connected {
		// Not on this replica — another replica will deliver
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Deliver via gRPC
	if err := w.Deliverer.DeliverTeamEvent(msg.Namespace, instanceID,
		msg.Spec.TeamRef, msg.Spec.Kind, targetMember, msg.Spec.Content); err != nil {
		logger.Error(err, "delivery failed", "message", msg.Name)
		metrics.RecordTeamMessage(msg.Namespace, msg.Spec.TeamRef, "failed")
		msg.Status.Attempts++
		_ = w.Status().Update(ctx, &msg)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Mark delivered
	msg.Status.Delivered = true
	msg.Status.DeliveredAt = time.Now().Format(time.RFC3339)
	msg.Status.Attempts++
	if err := w.Status().Update(ctx, &msg); err != nil {
		logger.Error(err, "failed to update delivery status", "message", msg.Name)
		metrics.RecordReconcileError("teamevent-watcher", "status-update")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	metrics.RecordTeamMessage(msg.Namespace, msg.Spec.TeamRef, "delivered")
	logger.Info("message delivered", "message", msg.Name, "to", targetMember)
	return ctrl.Result{}, nil
}

func (w *TeamEventWatcher) SetupWithManager(mgr ctrl.Manager) error {
	// The outbox watcher must run on every replica (not just the leader) so a
	// message can be delivered by whichever replica holds the recipient's live
	// gRPC connection. NeedLeaderElection=false opts this controller out of
	// leader gating.
	needLeaderElection := false
	return ctrl.NewControllerManagedBy(mgr).
		Named("team-event-watcher").
		For(&v1alpha1.TeamMessage{}).
		WithOptions(controller.Options{NeedLeaderElection: &needLeaderElection}).
		Complete(w)
}

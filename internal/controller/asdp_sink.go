package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
)

// ObservedSession is a neutral, transport-agnostic session snapshot reported by
// the data plane (via the HTTP prober or the ASDP gRPC stream). It decouples the
// controller package from the asdp/prober concrete types.
type ObservedSession struct {
	ID               string
	Phase            string
	MessageCount     int32
	PromptTokens     int64
	CompletionTokens int64
	ContextPressure  float64
	StartedAt        string
	LastActiveAt     string
}

// SessionEventSink applies data-plane session reports (delivered over the ASDP
// gRPC stream) to AgentSession CRDs. It implements the bridge that the asdp
// package's EventSink adapter delegates to, so the gRPC upstream path actually
// reaches the Kubernetes API in production.
type SessionEventSink struct {
	client.Client
	Scheme *runtime.Scheme
}

// ApplySessionReport upserts each reported session into an AgentSession CRD.
func (s *SessionEventSink) ApplySessionReport(ctx context.Context, namespace, agentName, instanceID string, sessions []ObservedSession) {
	logger := log.FromContext(ctx).WithName("asdp-session-sink")

	var agent v1alpha1.Agent
	if err := s.Get(ctx, types.NamespacedName{Name: agentName, Namespace: namespace}, &agent); err != nil {
		logger.V(1).Info("agent not found for session report; skipping",
			"agent", agentName, "namespace", namespace, "error", err.Error())
		return
	}

	for i := range sessions {
		if err := upsertObservedSession(ctx, s.Client, s.Scheme, &agent, sessions[i]); err != nil {
			logger.Error(err, "failed to upsert reported session", "sessionID", sessions[i].ID)
		}
	}
}

// upsertObservedSession creates or updates an AgentSession CRD from an observed
// session snapshot. It is shared by the SessionPoller (HTTP pull) and the ASDP
// gRPC sink (push) so both paths produce identical CRD state.
func upsertObservedSession(ctx context.Context, c client.Client, scheme *runtime.Scheme, agent *v1alpha1.Agent, o ObservedSession) error {
	sessionName := sanitizeSessionName(o.ID)
	ns := agent.Namespace

	var existing v1alpha1.AgentSession
	err := c.Get(ctx, types.NamespacedName{Name: sessionName, Namespace: ns}, &existing)
	if errors.IsNotFound(err) {
		session := &v1alpha1.AgentSession{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sessionName,
				Namespace: ns,
				Labels: map[string]string{
					labelSessionAgent: agent.Name,
				},
				Annotations: map[string]string{
					annoSessionID: o.ID,
				},
			},
			Spec: v1alpha1.AgentSessionSpec{
				AgentRef: v1alpha1.ObjectReference{Name: agent.Name},
			},
		}
		if err := controllerutil.SetControllerReference(agent, session, scheme); err != nil {
			return fmt.Errorf("setting owner reference on AgentSession %s: %w", sessionName, err)
		}
		if err := c.Create(ctx, session); err != nil {
			return fmt.Errorf("creating AgentSession %s: %w", sessionName, err)
		}
	} else if err != nil {
		return fmt.Errorf("getting AgentSession %s: %w", sessionName, err)
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh v1alpha1.AgentSession
		if err := c.Get(ctx, types.NamespacedName{Name: sessionName, Namespace: ns}, &fresh); err != nil {
			return err
		}
		fresh.Status.Phase = v1alpha1.SessionPhase(o.Phase)
		fresh.Status.MessageCount = o.MessageCount
		if o.StartedAt != "" {
			fresh.Status.StartedAt = o.StartedAt
		}
		if o.LastActiveAt != "" {
			fresh.Status.LastActiveAt = o.LastActiveAt
		}
		if o.PromptTokens > 0 || o.CompletionTokens > 0 {
			fresh.Status.TokenUsage = &v1alpha1.TokenUsage{
				PromptTokens:     o.PromptTokens,
				CompletionTokens: o.CompletionTokens,
				TotalTokens:      o.PromptTokens + o.CompletionTokens,
			}
		}
		if o.ContextPressure > 0 {
			fresh.Status.State = &v1alpha1.SessionState{
				ContextPressure: &v1alpha1.ContextPressure{
					Ratio: fmt.Sprintf("%.2f", o.ContextPressure),
				},
			}
		}
		return c.Status().Update(ctx, &fresh)
	})
}

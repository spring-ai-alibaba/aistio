package discovery

import (
	"context"
	"fmt"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
)

// +kubebuilder:webhook:path=/validate-agentscope-io-v1alpha1-agentsession,mutating=false,failurePolicy=fail,sideEffects=None,groups=agentscope.io,resources=agentsessions,verbs=create;update,versions=v1alpha1,name=vagentsession.agentscope.io,admissionReviewVersions=v1

// AgentSessionValidator implements a ValidatingWebhook for AgentSession CRD admission.
type AgentSessionValidator struct {
	decoder admission.Decoder
}

// NewAgentSessionValidator creates the webhook handler.
func NewAgentSessionValidator(decoder admission.Decoder) *AgentSessionValidator {
	return &AgentSessionValidator{decoder: decoder}
}

// Handle validates AgentSession CRD create/update requests.
func (v *AgentSessionValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	session := &v1alpha1.AgentSession{}
	if err := v.decoder.Decode(req, session); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if err := v.validate(session); err != nil {
		return admission.Denied(err.Error())
	}

	return admission.Allowed("")
}

func (v *AgentSessionValidator) validate(session *v1alpha1.AgentSession) error {
	// agentRef.name must be non-empty.
	if session.Spec.AgentRef.Name == "" {
		return fmt.Errorf("spec.agentRef.name is required")
	}

	// Team labels must be set together.
	labels := session.Labels
	_, hasTeam := labels["agentscope.io/team"]
	_, hasRole := labels["agentscope.io/team-role"]
	if hasTeam != hasRole {
		return fmt.Errorf("labels agentscope.io/team and agentscope.io/team-role must be set together")
	}

	return nil
}

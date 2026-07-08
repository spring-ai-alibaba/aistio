package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SessionPhase represents the lifecycle phase of a session.
// +kubebuilder:validation:Enum=Active;Idle;Compressing;Terminated
type SessionPhase string

const (
	SessionPhaseActive      SessionPhase = "Active"
	SessionPhaseIdle        SessionPhase = "Idle"
	SessionPhaseCompressing SessionPhase = "Compressing"
	SessionPhaseTerminated  SessionPhase = "Terminated"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=as
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Messages",type=integer,JSONPath=`.status.messageCount`
// +kubebuilder:printcolumn:name="Instance",type=string,JSONPath=`.status.instanceRef`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentSession represents a session on a managed agent.
type AgentSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSessionSpec   `json:"spec,omitempty"`
	Status AgentSessionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentSessionList contains a list of AgentSession.
type AgentSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentSession `json:"items"`
}

// AgentSessionSpec defines the desired state of AgentSession.
type AgentSessionSpec struct {
	// +kubebuilder:validation:Required
	AgentRef ObjectReference `json:"agentRef"`

	// Commands for control plane to execute on this session.
	// +optional
	Commands *SessionCommands `json:"commands,omitempty"`
}

// SessionCommands defines control plane commands for a session.
type SessionCommands struct {
	// Set to true to trigger context compression.
	Compress bool `json:"compress,omitempty"`
	// Set to true to trigger session termination.
	Terminate bool `json:"terminate,omitempty"`
}

// AgentSessionStatus defines the observed state of AgentSession.
type AgentSessionStatus struct {
	Phase        SessionPhase  `json:"phase,omitempty"`
	InstanceRef  string        `json:"instanceRef,omitempty"`
	InstanceIP   string        `json:"instanceIP,omitempty"`
	StartedAt    string        `json:"startedAt,omitempty"`
	LastActiveAt string        `json:"lastActiveAt,omitempty"`
	MessageCount int32         `json:"messageCount,omitempty"`
	TokenUsage   *TokenUsage   `json:"tokenUsage,omitempty"`
	State        *SessionState `json:"state,omitempty"`
	Conditions   []Condition   `json:"conditions,omitempty"`
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	PromptTokens     int64 `json:"promptTokens,omitempty"`
	CompletionTokens int64 `json:"completionTokens,omitempty"`
	TotalTokens      int64 `json:"totalTokens,omitempty"`
}

// SessionState holds the detailed session state snapshot.
type SessionState struct {
	Summary         string           `json:"summary,omitempty"`
	CurrentIter     int32            `json:"currentIter,omitempty"`
	ContextPressure *ContextPressure `json:"contextPressure,omitempty"`
	Tasks           []TaskState      `json:"tasks,omitempty"`
}

// ContextPressure tracks context window usage.
type ContextPressure struct {
	UsedTokens int64 `json:"usedTokens,omitempty"`
	MaxTokens  int64 `json:"maxTokens,omitempty"`
	// Ratio is usedTokens/maxTokens serialized as string (e.g. "0.56").
	Ratio string `json:"ratio,omitempty"`
}

// TaskState represents a task within a session.
type TaskState struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	// +kubebuilder:validation:Enum=pending;in_progress;completed
	State string `json:"state"`
}

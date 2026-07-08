package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// TeamTaskState is the status of a team task.
// +kubebuilder:validation:Enum=pending;in_progress;completed
type TeamTaskState string

const (
	TeamTaskStatePending    TeamTaskState = "pending"
	TeamTaskStateInProgress TeamTaskState = "in_progress"
	TeamTaskStateCompleted  TeamTaskState = "completed"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tt
// +kubebuilder:printcolumn:name="Team",type=string,JSONPath=`.spec.teamRef`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.status.owner`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TeamTask is a task in a team's distributed task list.
type TeamTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TeamTaskSpec   `json:"spec,omitempty"`
	Status            TeamTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TeamTaskList contains a list of TeamTask.
type TeamTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TeamTask `json:"items"`
}

// TeamTaskSpec defines the desired state of a TeamTask.
type TeamTaskSpec struct {
	// +kubebuilder:validation:Required
	TeamRef     string   `json:"teamRef"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	BlockedBy   []string `json:"blockedBy,omitempty"`
}

// TeamTaskStatus defines the observed state of a TeamTask.
type TeamTaskStatus struct {
	State       TeamTaskState `json:"state,omitempty"`
	Owner       string        `json:"owner,omitempty"`
	CompletedAt string        `json:"completedAt,omitempty"`
	Result      string        `json:"result,omitempty"`
}

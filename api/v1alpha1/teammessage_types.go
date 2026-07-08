package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tm
// +kubebuilder:printcolumn:name="Team",type=string,JSONPath=`.spec.teamRef`
// +kubebuilder:printcolumn:name="From",type=string,JSONPath=`.spec.from`
// +kubebuilder:printcolumn:name="To",type=string,JSONPath=`.spec.to`
// +kubebuilder:printcolumn:name="Delivered",type=boolean,JSONPath=`.status.delivered`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TeamMessage is a message in a team's distributed outbox.
type TeamMessage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TeamMessageSpec   `json:"spec,omitempty"`
	Status            TeamMessageStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TeamMessageList contains a list of TeamMessage.
type TeamMessageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TeamMessage `json:"items"`
}

// TeamMessageSpec defines the desired state of a TeamMessage.
type TeamMessageSpec struct {
	// +kubebuilder:validation:Required
	TeamRef string `json:"teamRef"`
	From    string `json:"from"`
	To      string `json:"to,omitempty"` // empty = broadcast
	Content string `json:"content"`
	Kind    string `json:"kind,omitempty"` // message, task_event, member_event
	Nonce   string `json:"nonce,omitempty"`
}

// TeamMessageStatus defines the observed state of a TeamMessage.
type TeamMessageStatus struct {
	Delivered   bool   `json:"delivered,omitempty"`
	DeliveredAt string `json:"deliveredAt,omitempty"`
	Attempts    int32  `json:"attempts,omitempty"`
}

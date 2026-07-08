package team

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/metrics"
)

const teamRoleLabel = "agentscope.io/team-role"

// TeamMessage represents a message between team members.
type TeamMessage struct {
	ID        string `json:"id"`
	TeamName  string `json:"teamName"`
	From      string `json:"from"`
	To        string `json:"to"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Delivered bool   `json:"delivered"`
}

// MemberLocation holds the routing information for a team member.
type MemberLocation struct {
	MemberName  string
	AgentName   string
	InstanceRef string
	InstanceIP  string
	SessionID   string
	Connected   bool
}

// MessageRouter routes messages between team members through a CRD-based
// outbox (TeamMessage), which the TeamEventWatcher delivers to whichever
// replica holds the recipient's live gRPC connection. It also keeps an
// in-memory registry purely for informational REST listing — routing never
// depends on it, so it is safe across replicas and restarts.
type MessageRouter struct {
	mu        sync.RWMutex
	locations map[string]map[string]*MemberLocation // teamName -> memberName -> location
	client    client.Client
}

// NewMessageRouter creates a new CRD-backed MessageRouter.
func NewMessageRouter(c client.Client) *MessageRouter {
	return &MessageRouter{
		locations: make(map[string]map[string]*MemberLocation),
		client:    c,
	}
}

// RegisterMember records a member's location for informational listing.
func (r *MessageRouter) RegisterMember(teamName string, loc *MemberLocation) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.locations[teamName] == nil {
		r.locations[teamName] = make(map[string]*MemberLocation)
	}
	r.locations[teamName][loc.MemberName] = loc
}

// UnregisterMember removes a member from the informational registry.
func (r *MessageRouter) UnregisterMember(teamName, memberName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if locs := r.locations[teamName]; locs != nil {
		delete(locs, memberName)
	}
}

// GetMemberLocation returns the last known location of a team member.
func (r *MessageRouter) GetMemberLocation(teamName, memberName string) (*MemberLocation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	locs := r.locations[teamName]
	if locs == nil {
		return nil, fmt.Errorf("team %s not found in router", teamName)
	}
	loc, ok := locs[memberName]
	if !ok {
		return nil, fmt.Errorf("member %s not found in team %s", memberName, teamName)
	}
	return loc, nil
}

// ListMembers returns all registered members for a team.
func (r *MessageRouter) ListMembers(teamName string) []*MemberLocation {
	r.mu.RLock()
	defer r.mu.RUnlock()

	locs := r.locations[teamName]
	result := make([]*MemberLocation, 0, len(locs))
	for _, loc := range locs {
		result = append(result, loc)
	}
	return result
}

// RouteMessage routes a message from one member to another by creating an
// undelivered TeamMessage CRD (outbox pattern). Delivery and connectivity are
// handled asynchronously by the TeamEventWatcher, so this does not require the
// recipient to be connected to this replica.
func (r *MessageRouter) RouteMessage(namespace, teamName, from, to, content string) (*TeamMessage, error) {
	msg := r.newMessageObject(namespace, teamName, from, to, content)
	if err := r.client.Create(context.Background(), msg); err != nil {
		return nil, fmt.Errorf("creating team message CRD: %w", err)
	}
	metrics.RecordTeamMessage(namespace, teamName, "enqueued")
	return &TeamMessage{
		ID:        msg.Name,
		TeamName:  teamName,
		From:      from,
		To:        to,
		Content:   content,
		Timestamp: time.Now().Format(time.RFC3339),
	}, nil
}

// BroadcastMessage sends a message to every team member (except the sender) by
// creating one point-to-point TeamMessage CRD per recipient. Recipients are
// derived from the team's AgentSession objects (the persistent source of
// truth), not the in-memory registry.
func (r *MessageRouter) BroadcastMessage(namespace, teamName, from, content string) ([]*TeamMessage, error) {
	recipients, err := r.teamMemberRoles(namespace, teamName)
	if err != nil {
		return nil, err
	}

	var msgs []*TeamMessage
	for _, to := range recipients {
		if to == from {
			continue
		}
		crdMsg := r.newMessageObject(namespace, teamName, from, to, content)
		if err := r.client.Create(context.Background(), crdMsg); err != nil {
			return msgs, fmt.Errorf("creating broadcast message for %s: %w", to, err)
		}
		metrics.RecordTeamMessage(namespace, teamName, "enqueued")
		msgs = append(msgs, &TeamMessage{
			ID:        crdMsg.Name,
			TeamName:  teamName,
			From:      from,
			To:        to,
			Content:   content,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
	return msgs, nil
}

// newMessageObject builds a TeamMessage CRD with an owner reference to the
// parent AgentTeam so it is garbage-collected when the team is deleted.
func (r *MessageRouter) newMessageObject(namespace, teamName, from, to, content string) *v1alpha1.TeamMessage {
	return &v1alpha1.TeamMessage{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName:    fmt.Sprintf("%s-msg-", teamName),
			Namespace:       namespace,
			Labels:          map[string]string{teamLabel: teamName},
			OwnerReferences: ownerRefFor(context.Background(), r.client, namespace, teamName),
		},
		Spec: v1alpha1.TeamMessageSpec{
			TeamRef: teamName,
			From:    from,
			To:      to,
			Content: content,
			Kind:    "message",
		},
	}
}

// teamMemberRoles returns the distinct team-role values for the team's
// AgentSession objects (e.g. "lead" and each member name).
func (r *MessageRouter) teamMemberRoles(namespace, teamName string) ([]string, error) {
	var sessions v1alpha1.AgentSessionList
	if err := r.client.List(context.Background(), &sessions,
		client.InNamespace(namespace),
		client.MatchingLabels{teamLabel: teamName},
	); err != nil {
		return nil, fmt.Errorf("listing team sessions: %w", err)
	}
	seen := make(map[string]struct{})
	var roles []string
	for i := range sessions.Items {
		role := sessions.Items[i].Labels[teamRoleLabel]
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	return roles, nil
}

// GetMessageHistory returns recent messages for a team from the CRD store.
func (r *MessageRouter) GetMessageHistory(namespace, teamName string, limit int) []*TeamMessage {
	var list v1alpha1.TeamMessageList
	if err := r.client.List(context.Background(), &list,
		client.InNamespace(namespace),
		client.MatchingLabels{teamLabel: teamName},
	); err != nil {
		return nil
	}

	msgs := make([]*TeamMessage, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		msgs = append(msgs, &TeamMessage{
			ID:        item.Name,
			TeamName:  item.Spec.TeamRef,
			From:      item.Spec.From,
			To:        item.Spec.To,
			Content:   item.Spec.Content,
			Timestamp: item.CreationTimestamp.Format(time.RFC3339),
			Delivered: item.Status.Delivered,
		})
	}

	if limit > 0 && limit < len(msgs) {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs
}

// DeleteTeam clears in-memory routing state for a team. TeamMessage CRDs are
// garbage-collected via owner references on the AgentTeam.
func (r *MessageRouter) DeleteTeam(teamName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.locations, teamName)
}

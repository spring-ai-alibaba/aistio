package team

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
)

// TeamContext is injected into each teammate session at startup.
type TeamContext struct {
	TeamName         string           `json:"teamName"`
	Objective        string           `json:"objective"`
	MyRole           string           `json:"myRole"`
	IsLead           bool             `json:"isLead"`
	Members          []MemberInfo     `json:"members"`
	AvailableActions []string         `json:"availableActions"`
	RecoveryContext  *RecoveryContext `json:"recoveryContext,omitempty"`
}

// MemberInfo describes a team member visible to all participants.
type MemberInfo struct {
	Name     string `json:"name"`
	AgentRef string `json:"agentRef"`
	Status   string `json:"status"`
}

// RecoveryContext provides context when a session is recovering from a crash.
type RecoveryContext struct {
	PreviousSessionID string           `json:"previousSessionId"`
	RestartCount      int32            `json:"restartCount"`
	CompletedTasks    []CompletedTask  `json:"completedTasks,omitempty"`
	InterruptedTask   *InterruptedTask `json:"interruptedTask,omitempty"`
	RecentMessages    []RecentMessage  `json:"recentMessages,omitempty"`
}

// CompletedTask records a task finished by the predecessor session.
type CompletedTask struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Result  string `json:"result"`
}

// InterruptedTask records a task that was in-progress when the session died.
type InterruptedTask struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Note    string `json:"note"`
}

// RecentMessage is a message from the team history injected for context.
type RecentMessage struct {
	From      string `json:"from"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// SessionSpawner creates AgentSession CRDs for team members with injected team context.
type SessionSpawner struct {
	client client.Client
	router *MessageRouter
}

// NewSessionSpawner creates a new SessionSpawner.
func NewSessionSpawner(c client.Client, router *MessageRouter) *SessionSpawner {
	return &SessionSpawner{
		client: c,
		router: router,
	}
}

// SpawnLeadSession creates the lead's AgentSession with team context.
func (s *SessionSpawner) SpawnLeadSession(ctx context.Context, team *v1alpha1.AgentTeam) (*v1alpha1.AgentSession, error) {
	teamCtx := s.buildTeamContext(team, "lead", true, nil)
	return s.createSession(ctx, team, team.Spec.Lead.AgentRef.Name, "lead", teamCtx)
}

// SpawnMemberSession creates a member's AgentSession with team context.
func (s *SessionSpawner) SpawnMemberSession(ctx context.Context, team *v1alpha1.AgentTeam, member v1alpha1.TeamMemberSpec) (*v1alpha1.AgentSession, error) {
	teamCtx := s.buildTeamContext(team, member.Name, false, nil)
	return s.createSession(ctx, team, member.AgentRef.Name, member.Name, teamCtx)
}

// SpawnRecoverySession creates a replacement session with recovery context.
func (s *SessionSpawner) SpawnRecoverySession(
	ctx context.Context,
	team *v1alpha1.AgentTeam,
	memberName, agentRef string,
	recovery *RecoveryContext,
) (*v1alpha1.AgentSession, error) {
	teamCtx := s.buildTeamContext(team, memberName, false, recovery)
	return s.createSession(ctx, team, agentRef, memberName, teamCtx)
}

func (s *SessionSpawner) buildTeamContext(
	team *v1alpha1.AgentTeam,
	myRole string,
	isLead bool,
	recovery *RecoveryContext,
) *TeamContext {
	members := make([]MemberInfo, 0)

	// Add lead
	members = append(members, MemberInfo{
		Name:     "lead",
		AgentRef: team.Spec.Lead.AgentRef.Name,
		Status:   "working",
	})

	// Add static members
	for _, m := range team.Spec.Members {
		status := "joining"
		if team.Status.Members != nil {
			for _, ms := range team.Status.Members {
				if ms.Name == m.Name {
					status = string(ms.Phase)
					break
				}
			}
		}
		members = append(members, MemberInfo{
			Name:     m.Name,
			AgentRef: m.AgentRef.Name,
			Status:   status,
		})
	}

	actions := []string{
		"listTasks", "claimTask", "completeTask",
		"sendMessage", "broadcastMessage", "listMembers",
	}
	if isLead {
		actions = append(actions,
			"createTask", "spawnMember", "shutdownMember",
			"approvePlan", "rejectPlan", "completeTeam",
		)
	}

	return &TeamContext{
		TeamName:         team.Name,
		Objective:        team.Spec.Objective,
		MyRole:           myRole,
		IsLead:           isLead,
		Members:          members,
		AvailableActions: actions,
		RecoveryContext:  recovery,
	}
}

func (s *SessionSpawner) createSession(
	ctx context.Context,
	team *v1alpha1.AgentTeam,
	agentRef, memberName string,
	teamCtx *TeamContext,
) (*v1alpha1.AgentSession, error) {
	contextJSON, err := json.Marshal(teamCtx)
	if err != nil {
		return nil, fmt.Errorf("marshaling team context: %w", err)
	}

	session := &v1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-%s-", team.Name, memberName),
			Namespace:    team.Namespace,
			Labels: map[string]string{
				"agentscope.io/agent":     agentRef,
				"agentscope.io/team":      team.Name,
				"agentscope.io/team-role": memberName,
			},
			Annotations: map[string]string{
				"agentscope.io/team-context": string(contextJSON),
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(team, v1alpha1.GroupVersion.WithKind("AgentTeam")),
			},
		},
		Spec: v1alpha1.AgentSessionSpec{
			AgentRef: v1alpha1.ObjectReference{Name: agentRef},
		},
	}

	if err := s.client.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("creating session for %s: %w", memberName, err)
	}

	// Register in message router
	s.router.RegisterMember(team.Name, &MemberLocation{
		MemberName: memberName,
		AgentName:  agentRef,
		SessionID:  session.Name,
		Connected:  true,
	})

	// Update session status
	session.Status.Phase = v1alpha1.SessionPhaseActive
	session.Status.StartedAt = time.Now().Format(time.RFC3339)
	if err := s.client.Status().Update(ctx, session); err != nil {
		return session, nil // non-fatal, session was created
	}

	return session, nil
}

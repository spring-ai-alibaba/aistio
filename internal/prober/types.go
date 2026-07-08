package prober

// DataPlaneInfo holds metadata returned by GET /agentscope/info.
type DataPlaneInfo struct {
	Name            string            `json:"name"`
	DisplayName     string            `json:"displayName,omitempty"`
	Description     string            `json:"description,omitempty"`
	Runtime         string            `json:"runtime"`
	Version         string            `json:"version,omitempty"`
	SDKVersion      string            `json:"sdkVersion,omitempty"`
	ContractLevel   int32             `json:"contractLevel"`
	Capabilities    []string          `json:"capabilities,omitempty"`
	Port            int32             `json:"port,omitempty"`
	SessionAffinity string            `json:"sessionAffinity,omitempty"`
	AgentConfig     *ProbeAgentConfig `json:"agentConfig,omitempty"`
}

// ProbeAgentConfig holds agent configuration reported by the data plane.
type ProbeAgentConfig struct {
	ModelProvider string   `json:"modelProvider,omitempty"`
	Model         string   `json:"model,omitempty"`
	Tools         []string `json:"tools,omitempty"`
	MaxTurns      int32    `json:"maxTurns,omitempty"`
}

// SessionSnapshot represents a session as reported by the data plane.
type SessionSnapshot struct {
	ID              string       `json:"id"`
	Phase           string       `json:"phase"`
	StartedAt       string       `json:"startedAt,omitempty"`
	LastActiveAt    string       `json:"lastActiveAt,omitempty"`
	MessageCount    int32        `json:"messageCount,omitempty"`
	TokenUsage      *TokenUsage  `json:"tokenUsage,omitempty"`
	ContextPressure float64      `json:"contextPressure,omitempty"`
	TaskSummary     *TaskSummary `json:"taskSummary,omitempty"`
}

// TokenUsage tracks token counts.
type TokenUsage struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
}

// TaskSummary holds aggregate task counts.
type TaskSummary struct {
	Total      int32 `json:"total"`
	Pending    int32 `json:"pending"`
	InProgress int32 `json:"inProgress"`
	Completed  int32 `json:"completed"`
}

// SessionState holds detailed session state returned by GET /agentscope/sessions/{id}/state.
type SessionState struct {
	SessionID       string               `json:"sessionId"`
	Summary         string               `json:"summary,omitempty"`
	CurrentIter     int32                `json:"currentIter,omitempty"`
	ContextPressure *ContextPressureInfo `json:"contextPressure,omitempty"`
	Tasks           []TaskInfo           `json:"tasks,omitempty"`
}

// ContextPressureInfo holds context window pressure metrics.
type ContextPressureInfo struct {
	UsedTokens int64   `json:"usedTokens"`
	MaxTokens  int64   `json:"maxTokens"`
	Ratio      float64 `json:"ratio"`
}

// TaskInfo represents a task within a session state response.
type TaskInfo struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	State   string `json:"state"`
}

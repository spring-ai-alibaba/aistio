package prober

import "context"

// DataPlaneProber encapsulates calls to the data plane contract HTTP API.
// Used by DiscoveryController for initial probing and periodic health checks.
type DataPlaneProber interface {
	// ProbeInfo calls GET /agentscope/info to get data plane metadata.
	ProbeInfo(ctx context.Context, endpoint string) (*DataPlaneInfo, error)

	// ProbeHealth calls GET /agentscope/health.
	ProbeHealth(ctx context.Context, endpoint string) (bool, error)

	// ProbeSessions calls GET /agentscope/sessions (Level 2+).
	ProbeSessions(ctx context.Context, endpoint string) ([]SessionSnapshot, error)

	// SendCompress calls POST /agentscope/sessions/{id}/compress (Level 3+).
	SendCompress(ctx context.Context, endpoint string, sessionID string) error

	// SendTerminate calls POST /agentscope/sessions/{id}/terminate (Level 3+).
	SendTerminate(ctx context.Context, endpoint string, sessionID string) error

	// FetchSessionState calls GET /agentscope/sessions/{id}/state (Level 2+).
	FetchSessionState(ctx context.Context, endpoint string, sessionID string) (*SessionState, error)
}

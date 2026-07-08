package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spring-ai-alibaba/aistio/internal/team"
	"github.com/spring-ai-alibaba/aistio/internal/version"
)

// ServerOptions configures the REST API server.
type ServerOptions struct {
	Client       client.Client
	Addr         string
	Experimental bool
	// AuthToken, when non-empty, requires a matching bearer token on all
	// /api/v1 requests.
	AuthToken string
	// TLSCertFile and TLSKeyFile enable HTTPS when both are provided.
	TLSCertFile string
	TLSKeyFile  string
	// KubeClient, when non-nil, enables Kubernetes TokenReview authentication
	// for bearer tokens, taking precedence over static AuthToken.
	KubeClient kubernetes.Interface
}

// Server is the REST API server for the control plane.
type Server struct {
	client       client.Client
	router       *gin.Engine
	httpServer   *http.Server
	experimental bool
	authToken    string
	tlsCertFile  string
	tlsKeyFile   string
	kubeClient   kubernetes.Interface

	// Experimental team coordination state (only initialized when experimental
	// features are enabled). CRD-backed and HA-safe across replicas.
	taskStore     team.TaskStoreInterface
	messageRouter *team.MessageRouter
}

// NewServer creates a new API server.
func NewServer(opts ServerOptions) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	s := &Server{
		client:       opts.Client,
		router:       router,
		experimental: opts.Experimental,
		authToken:    opts.AuthToken,
		tlsCertFile:  opts.TLSCertFile,
		tlsKeyFile:   opts.TLSKeyFile,
		kubeClient:   opts.KubeClient,
		httpServer: &http.Server{
			Addr:         opts.Addr,
			Handler:      router,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
	}

	if opts.KubeClient == nil {
		ctrl.Log.WithName("httpapi").Info("authorization disabled: no kube client configured (static token mode does not support authorization)")
	}

	if opts.Experimental {
		s.taskStore = team.NewK8sTaskStore(opts.Client)
		s.messageRouter = team.NewMessageRouter(opts.Client)
	}

	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// System
	s.router.GET("/healthz", s.healthz)
	s.router.GET("/readyz", s.readyz)
	s.router.GET("/api/v1/version", s.version)

	v1 := s.router.Group("/api/v1")
	v1.Use(s.authMiddleware())
	v1.Use(s.authzMiddleware())
	{
		// Agent lifecycle
		agents := v1.Group("/agents")
		{
			agents.GET("", s.listAgents)
			agents.GET("/:name", s.getAgent)
			agents.POST("/:name/push", s.pushAgent)
			agents.PATCH("/:name", s.patchAgent)
			agents.DELETE("/:name", s.deleteAgent)
			agents.GET("/:name/health", s.agentHealth)
			agents.GET("/:name/revisions", s.listRevisions)
			agents.GET("/:name/revisions/:rev", s.getRevision)
			agents.POST("/:name/rollback", s.rollbackAgent)
			agents.POST("/:name/adopt", s.adoptAgent)

			// Sessions under agent
			agents.GET("/:name/sessions", s.listSessions)
			agents.POST("/:name/sessions", s.createSession)
			agents.GET("/:name/sessions/:sessionId", s.getSession)
			agents.GET("/:name/sessions/:sessionId/state", s.getSessionState)
			agents.POST("/:name/sessions/:sessionId/compress", s.compressSession)
			agents.POST("/:name/sessions/:sessionId/terminate", s.terminateSession)
			agents.DELETE("/:name/sessions/:sessionId", s.deleteSession)
		}

		// ModelConfig
		modelconfigs := v1.Group("/modelconfigs")
		{
			modelconfigs.POST("", s.createModelConfig)
			modelconfigs.GET("", s.listModelConfigs)
			modelconfigs.GET("/:name", s.getModelConfig)
			modelconfigs.PATCH("/:name", s.patchModelConfig)
			modelconfigs.DELETE("/:name", s.deleteModelConfig)
		}

		// MCPServer
		mcpservers := v1.Group("/mcpservers")
		{
			mcpservers.POST("", s.createMCPServer)
			mcpservers.GET("", s.listMCPServers)
			mcpservers.GET("/:name", s.getMCPServer)
			mcpservers.PATCH("/:name", s.patchMCPServer)
			mcpservers.DELETE("/:name", s.deleteMCPServer)
			mcpservers.GET("/:name/tools", s.listMCPTools)
		}

		// Experimental: Teams + Sandbox (only when enabled)
		if s.experimental {
			teams := v1.Group("/teams")
			{
				teams.POST("", s.createTeam)
				teams.GET("", s.listTeams)
				teams.GET("/:team", s.getTeam)
				teams.DELETE("/:team", s.deleteTeam)
				teams.POST("/:team/members", s.addTeamMember)
				teams.DELETE("/:team/members/:memberName", s.removeTeamMember)
				teams.GET("/:team/members", s.listTeamMembers)
				teams.POST("/:team/tasks", s.createTeamTask)
				teams.GET("/:team/tasks", s.listTeamTasks)
				teams.POST("/:team/tasks/:taskId/claim", s.claimTeamTask)
				teams.POST("/:team/tasks/:taskId/complete", s.completeTeamTask)
				teams.POST("/:team/messages", s.sendTeamMessage)
			}

			sandboxes := v1.Group("/sandboxes")
			{
				sandboxes.POST("", s.createSandbox)
				sandboxes.GET("", s.listSandboxes)
				sandboxes.GET("/:name", s.getSandbox)
				sandboxes.DELETE("/:name", s.deleteSandbox)
			}
		}
	}
}

// authMiddleware enforces authentication. When a KubeClient is configured,
// bearer tokens are validated via Kubernetes TokenReview (takes precedence).
// Otherwise, a static authToken is checked. No-op when neither is configured.
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// K8s TokenReview auth takes precedence over static token.
		if s.kubeClient != nil {
			s.kubeAuth(c)
			return
		}
		if s.authToken == "" {
			c.Next()
			return
		}
		auth := c.GetHeader("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != s.authToken || auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: "unauthorized"})
			return
		}
		c.Next()
	}
}

// kubeAuth validates a bearer token via the Kubernetes TokenReview API.
func (s *Server) kubeAuth(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" || token == auth {
		c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: "missing bearer token"})
		return
	}

	tr := &authv1.TokenReview{
		Spec: authv1.TokenReviewSpec{Token: token},
	}
	result, err := s.kubeClient.AuthenticationV1().TokenReviews().Create(
		c.Request.Context(), tr, metav1.CreateOptions{},
	)
	if err != nil || !result.Status.Authenticated {
		c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: "token authentication failed"})
		return
	}

	c.Set("username", result.Status.User.Username)
	c.Set("groups", result.Status.User.Groups)
	c.Next()
}

// Start begins serving HTTP (or HTTPS when TLS cert/key are configured).
func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutdownCtx)
	}()

	if s.tlsCertFile != "" && s.tlsKeyFile != "" {
		if err := s.httpServer.ListenAndServeTLS(s.tlsCertFile, s.tlsKeyFile); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("HTTPS server error: %w", err)
		}
	} else {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("HTTP server error: %w", err)
		}
	}
	return nil
}

func (s *Server) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) readyz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) version(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":      version.Version,
		"apiVersion":   version.APIVersion,
		"component":    version.Component,
		"experimental": s.experimental,
	})
}

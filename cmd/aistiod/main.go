package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/asdp"
	"github.com/spring-ai-alibaba/aistio/internal/controller"
	"github.com/spring-ai-alibaba/aistio/internal/discovery"
	"github.com/spring-ai-alibaba/aistio/internal/httpapi"
	"github.com/spring-ai-alibaba/aistio/internal/prober"
	"github.com/spring-ai-alibaba/aistio/internal/team"
	"github.com/spring-ai-alibaba/aistio/internal/tracing"
)

var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
)

var (
	scheme = runtime.NewScheme()
)

// distributorAdapter adapts asdp.Distributor (proto ConfigType) to the
// controller.ConfigDistributor interface (int32 configType).
type distributorAdapter struct {
	dist *asdp.Distributor
}

func (a *distributorAdapter) PushConfig(namespace, agentName string, configType int32, resources interface{}) error {
	return a.dist.PushConfig(namespace, agentName, asdp.ConfigType(configType), resources)
}

func (a *distributorAdapter) ForgetAgent(namespace, agentName string) {
	a.dist.ForgetAgent(namespace, agentName)
}

// sessionSinkAdapter adapts the asdp.EventSink interface (proto types) to the
// controller.SessionEventSink (neutral types), so upstream gRPC reports actually
// reach AgentSession CRDs without coupling the controller package to asdp.
type sessionSinkAdapter struct {
	sink     *controller.SessionEventSink
	teamSink *controller.TeamEventSink
}

func (a *sessionSinkAdapter) HandleSessionReport(namespace, agentName, instanceID string, report *asdp.SessionReport) {
	if report == nil {
		return
	}
	observed := make([]controller.ObservedSession, 0, len(report.Sessions))
	for _, s := range report.Sessions {
		if s == nil {
			continue
		}
		observed = append(observed, controller.ObservedSession{
			ID:               s.GetSessionId(),
			Phase:            s.GetPhase(),
			MessageCount:     s.GetMessageCount(),
			PromptTokens:     s.GetPromptTokens(),
			CompletionTokens: s.GetCompletionTokens(),
			ContextPressure:  s.GetContextPressure(),
		})
	}
	a.sink.ApplySessionReport(context.Background(), namespace, agentName, instanceID, observed)
}

func (a *sessionSinkAdapter) HandleTeamEventReport(namespace, agentName string, report *asdp.TeamEventReport) {
	if a.teamSink == nil || report == nil {
		return
	}
	a.teamSink.HandleEvent(context.Background(), namespace, &controller.TeamEventReport{
		TeamID:     report.GetTeamId(),
		EventType:  report.GetEventType(),
		MemberName: report.GetMemberName(),
		TaskID:     report.GetTaskId(),
		Detail:     controller.ParseDetail(report.GetDetail()),
	})
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		httpAddr             string
		grpcAddr             string
		enableLeaderElection bool
		enableASDP           bool
		enableExperimental   bool
		enableWebhook        bool
		showVersion          bool
		apiAuthToken         string
		apiTLSCert           string
		apiTLSKey            string
		enableKubeAuth       bool
		logFormat            string
		otelEndpoint         string
		traceSampling        float64
		grpcTLSCert          string
		grpcTLSKey           string
		grpcTLSCA            string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8081", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8082", "The address the probe endpoint binds to.")
	flag.StringVar(&httpAddr, "http-bind-address", ":8080", "The address the REST API server binds to.")
	flag.StringVar(&grpcAddr, "grpc-bind-address", ":15010", "The address the ASDP gRPC server binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election.")
	flag.BoolVar(&enableASDP, "enable-asdp", true,
		"Enable the ASDP data plane protocol (gRPC coordination, config push). On by default.")
	flag.BoolVar(&enableExperimental, "enable-experimental", false,
		"Enable experimental features (distributed AgentTeam, sandbox provisioning). Off by default.")
	flag.BoolVar(&enableWebhook, "enable-webhook", false, "Enable the Agent validating admission webhook (requires serving certs).")
	flag.BoolVar(&showVersion, "version", false, "Print version information and exit.")
	flag.StringVar(&apiAuthToken, "api-auth-token", os.Getenv("AGENTSCOPE_API_TOKEN"),
		"Optional bearer token required for REST API access. Empty disables auth.")
	flag.StringVar(&apiTLSCert, "api-tls-cert", "", "TLS certificate file for REST API server.")
	flag.StringVar(&apiTLSKey, "api-tls-key", "", "TLS key file for REST API server.")
	flag.BoolVar(&enableKubeAuth, "enable-kube-auth", false, "Enable Kubernetes TokenReview authentication for REST API.")
	flag.StringVar(&logFormat, "log-format", "json", "Log output format: json (default) or console (human-readable).")
	flag.StringVar(&otelEndpoint, "otel-endpoint", "", "OpenTelemetry collector endpoint (empty disables tracing).")
	flag.Float64Var(&traceSampling, "trace-sampling", 1.0, "Trace sampling rate (0.0-1.0).")
	flag.StringVar(&grpcTLSCert, "grpc-tls-cert", "", "TLS certificate for gRPC server.")
	flag.StringVar(&grpcTLSKey, "grpc-tls-key", "", "TLS key for gRPC server.")
	flag.StringVar(&grpcTLSCA, "grpc-tls-ca", "", "CA certificate for client verification (enables mTLS).")
	flag.Parse()

	if showVersion {
		fmt.Printf("aistiod %s (commit: %s, built: %s)\n", version, gitCommit, buildDate)
		os.Exit(0)
	}

	// Structured logging: default to production JSON; use console mode for
	// local development via --log-format=console or AGENTSCOPE_DEV_LOG=true.
	opts := zap.Options{
		Development: false,
	}
	if logFormat == "console" || os.Getenv("AGENTSCOPE_DEV_LOG") == "true" {
		opts.Development = true
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := ctrl.Log.WithName("setup")
	logger.Info("starting aistio", "version", version, "commit", gitCommit, "buildDate", buildDate)

	if otelEndpoint != "" {
		shutdownTracing, err := tracing.Init(context.Background(), otelEndpoint, traceSampling)
		if err != nil {
			logger.Error(err, "failed to initialize tracing")
		} else {
			defer shutdownTracing()
			logger.Info("OpenTelemetry tracing initialized", "endpoint", otelEndpoint)
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "aistio.agentscope.io",
	})
	if err != nil {
		logger.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Shared components
	httpProber := prober.NewHTTPProber()

	// Build ASDP server for data plane coordination.
	// Created early so core controllers can receive the distributor.
	var dist controller.ConfigDistributor
	var asdpServer *asdp.Server
	var sinkAdapter *sessionSinkAdapter
	if enableASDP {
		srv, err := asdp.NewServer(asdp.ServerConfig{
			Addr:      grpcAddr,
			TLSCert:   grpcTLSCert,
			TLSKey:    grpcTLSKey,
			TLSCACert: grpcTLSCA,
		})
		if err != nil {
			logger.Error(err, "unable to create ASDP gRPC server")
			os.Exit(1)
		}
		asdpServer = srv
		dist = &distributorAdapter{dist: asdpServer.Distributor()}
		// Wire upstream session reports through to AgentSession CRDs.
		// teamSink is set later once taskStore is available (if experimental is enabled).
		sinkAdapter = &sessionSinkAdapter{
			sink: &controller.SessionEventSink{Client: mgr.GetClient(), Scheme: mgr.GetScheme()},
		}
		asdpServer.SetEventSink(sinkAdapter)
		logger.Info("ASDP data plane protocol enabled")
	}

	// ===== v0.1 core controllers (always registered) =====
	if err := (&controller.AgentReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Prober:   httpProber,
		Recorder: mgr.GetEventRecorderFor("agent-controller"),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Agent")
		os.Exit(1)
	}

	if err := (&controller.DiscoveryReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Prober:   httpProber,
		Recorder: mgr.GetEventRecorderFor("discovery-controller"),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Discovery")
		os.Exit(1)
	}

	if err := (&controller.BYOWorkloadReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Prober:   httpProber,
		Recorder: mgr.GetEventRecorderFor("byoworkload-controller"),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "BYOWorkload")
		os.Exit(1)
	}

	if err := (&controller.ModelConfigReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("modelconfig-controller"),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "ModelConfig")
		os.Exit(1)
	}

	if err := (&controller.MCPServerReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("mcpserver-controller"),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "MCPServer")
		os.Exit(1)
	}

	if err := (&controller.AgentSessionReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Prober:   httpProber,
		Recorder: mgr.GetEventRecorderFor("session-controller"),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "AgentSession")
		os.Exit(1)
	}

	if err := (&controller.SessionPollerReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Prober:   httpProber,
		Recorder: mgr.GetEventRecorderFor("session-poller"),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "SessionPoller")
		os.Exit(1)
	}

	// ===== Config delivery (GA) =====
	// ConfigPushWatcher drives ASDP config push from every replica's informer
	// cache (NOT leader-gated), so config reaches data plane connections
	// regardless of which replica owns them. This is a core GA capability and is
	// registered whenever the ASDP server is enabled, independent of experimental.
	if dist != nil {
		if err := mgr.Add(&controller.ConfigPushWatcher{
			Client: mgr.GetClient(),
			Cache:  mgr.GetCache(),
			Dist:   dist,
		}); err != nil {
			logger.Error(err, "unable to add config push watcher")
			os.Exit(1)
		}
		logger.Info("ASDP config delivery enabled (agent/model/tool/skill hot-reload)")
	}

	// ===== Experimental controllers (gated) =====
	if enableExperimental {
		logger.Info("experimental features enabled (AgentTeam, SandboxBroker)")

		if err := (&controller.SandboxBrokerReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor("sandboxbroker-controller"),
		}).SetupWithManager(mgr); err != nil {
			logger.Error(err, "unable to create controller", "controller", "SandboxBroker")
			os.Exit(1)
		}

		taskStore := team.NewK8sTaskStore(mgr.GetClient())
		msgRouter := team.NewMessageRouter(mgr.GetClient())
		spawner := team.NewSessionSpawner(mgr.GetClient(), msgRouter)
		lifecycle := team.NewLifecycle(mgr.GetClient(), taskStore, msgRouter, spawner)

		// Wire team event processing now that taskStore is available.
		if sinkAdapter != nil {
			sinkAdapter.teamSink = controller.NewTeamEventSink(
				mgr.GetClient(), taskStore, mgr.GetEventRecorderFor("agentscope-controller"))
		}

		if err := (&controller.AgentTeamReconciler{
			Client:    mgr.GetClient(),
			Scheme:    mgr.GetScheme(),
			Recorder:  mgr.GetEventRecorderFor("agentscope-controller"),
			Lifecycle: lifecycle,
		}).SetupWithManager(mgr); err != nil {
			logger.Error(err, "unable to create controller", "controller", "AgentTeam")
			os.Exit(1)
		}

		// The outbox watcher delivers TeamMessages over the live gRPC channel.
		// It registers itself as non-leader (runs on every replica) so it can
		// reach connections held by any replica. The ASDP Distributor satisfies
		// the TeamEventDeliverer interface directly.
		var deliverer controller.TeamEventDeliverer
		if asdpServer != nil {
			deliverer = asdpServer.Distributor()
		}
		teamWatcher := &controller.TeamEventWatcher{
			Client:    mgr.GetClient(),
			Scheme:    mgr.GetScheme(),
			Deliverer: deliverer,
		}
		if err := teamWatcher.SetupWithManager(mgr); err != nil {
			logger.Error(err, "unable to create controller", "controller", "TeamEventWatcher")
			os.Exit(1)
		}
	}

	// ===== Admission webhooks (gated; requires serving certs) =====
	if enableWebhook {
		decoder := admission.NewDecoder(mgr.GetScheme())
		mgr.GetWebhookServer().Register("/validate-agentscope-io-v1alpha1-agent",
			&admission.Webhook{Handler: discovery.NewAgentValidator(decoder)})
		mgr.GetWebhookServer().Register("/mutate-agentscope-io-v1alpha1-agent",
			&admission.Webhook{Handler: discovery.NewAgentDefaulter(decoder)})
		mgr.GetWebhookServer().Register("/validate-agentscope-io-v1alpha1-agentteam",
			&admission.Webhook{Handler: discovery.NewAgentTeamValidator(decoder)})
		mgr.GetWebhookServer().Register("/validate-agentscope-io-v1alpha1-agentsession",
			&admission.Webhook{Handler: discovery.NewAgentSessionValidator(decoder)})
		mgr.GetWebhookServer().Register("/validate-agentscope-io-v1alpha1-modelconfig",
			&admission.Webhook{Handler: discovery.NewModelConfigValidator(decoder)})
		mgr.GetWebhookServer().Register("/validate-agentscope-io-v1alpha1-mcpserver",
			&admission.Webhook{Handler: discovery.NewMCPServerValidator(decoder)})
		logger.Info("admission webhooks registered")
	}

	// Health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("received shutdown signal")
		cancel()
	}()

	// Start experimental ASDP gRPC server (if enabled).
	// Multi-replica note: the gRPC server runs on ALL replicas, not just the
	// leader. Each replica accepts data plane connections and pushes config to
	// its own connections via the local informer cache. Controller reconcile
	// loops remain leader-gated via the manager's leader election.
	if asdpServer != nil {
		go func() {
			logger.Info("starting experimental ASDP gRPC server", "addr", grpcAddr)
			if err := asdpServer.Start(); err != nil {
				logger.Error(err, "ASDP gRPC server error")
			}
		}()
		go func() {
			<-ctx.Done()
			asdpServer.Stop()
		}()
	}

	// Build REST API server options
	apiOpts := httpapi.ServerOptions{
		Client:       mgr.GetClient(),
		Addr:         httpAddr,
		Experimental: enableExperimental,
		AuthToken:    apiAuthToken,
		TLSCertFile:  apiTLSCert,
		TLSKeyFile:   apiTLSKey,
	}
	if enableKubeAuth {
		kubeClient, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
		if err != nil {
			logger.Error(err, "unable to create Kubernetes clientset for API auth")
			os.Exit(1)
		}
		apiOpts.KubeClient = kubeClient
	}

	// Start REST API server
	apiServer := httpapi.NewServer(apiOpts)
	go func() {
		logger.Info("starting REST API server", "addr", httpAddr, "experimental", enableExperimental,
			"tls", apiTLSCert != "", "kubeAuth", enableKubeAuth)
		if err := apiServer.Start(ctx); err != nil {
			logger.Error(err, "REST API server error")
		}
	}()

	// Start controller manager (blocking)
	logger.Info("starting controller manager")
	if err := mgr.Start(ctx); err != nil {
		logger.Error(err, "controller manager error")
		os.Exit(1)
	}
}

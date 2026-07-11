//go:build integration

package controller_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/controller"
)

var (
	k8sClient     client.Client
	managerClient client.Client
	agentProber   = &recordingProber{}
)

const (
	testTimeout                    = 30 * time.Second
	managerGracefulShutdownTimeout = 10 * time.Second
	managerStopTimeout             = 15 * time.Second
	cleanupTimeout                 = 5 * time.Second
)

func TestMain(m *testing.M) {
	os.Exit(runSuite(m))
}

// runSuite 启动真实 API Server、etcd 和控制器 manager。显式运行 integration
// suite 时缺少二进制或 CRD 必须失败，不能退化成全部 Skip 的假绿结果。
func runSuite(m *testing.M) (exitCode int) {
	logf.SetLogger(zap.New(zap.WriteTo(os.Stderr)))

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest: failed to start control plane: %v\n", err)
		return 1
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "envtest: failed to stop control plane: %v\n", err)
			exitCode = 1
		}
	}()

	if err := v1alpha1.AddToScheme(scheme.Scheme); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: failed to add Aistio APIs to scheme: %v\n", err)
		return 1
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest: failed to create API client: %v\n", err)
		return 1
	}

	// manager 使用真实缓存和 watch，测试覆盖 API 持久化后的异步协调，而不是
	// 直接调用 Reconcile。关闭监听端口可避免并行 CI 端口冲突。
	gracefulShutdownTimeout := managerGracefulShutdownTimeout
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme.Scheme,
		Metrics:                 metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:  "0",
		GracefulShutdownTimeout: &gracefulShutdownTimeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest: failed to create manager: %v\n", err)
		return 1
	}
	managerClient = mgr.GetClient()

	// AgentTeam 使用无外部依赖的 legacy 生命周期路径。
	if err := (&controller.AgentTeamReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("agentteam-controller"),
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: failed to register AgentTeam controller: %v\n", err)
		return 1
	}

	// agentscope-java adapter 由 controller 依赖包的 init 自动注册。线程安全的
	// recordingProber 用于验证 Ready Pod 到数据面探测状态的闭环；Recorder 与生产装配一致。
	if err := (&controller.AgentReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Prober:   agentProber,
		Recorder: mgr.GetEventRecorderFor("agent-controller"),
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: failed to register Agent controller: %v\n", err)
		return 1
	}

	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	mgrDone := make(chan struct{})
	var mgrRunErr error
	go func() {
		defer close(mgrDone)
		mgrRunErr = mgr.Start(mgrCtx)
	}()
	defer func() {
		mgrCancel()
		stopTimer := time.NewTimer(managerStopTimeout)
		defer stopTimer.Stop()
		select {
		case <-mgrDone:
			if mgrRunErr != nil {
				fmt.Fprintf(os.Stderr, "envtest: manager failed: %v\n", mgrRunErr)
				exitCode = 1
			}
		case <-stopTimer.C:
			fmt.Fprintln(os.Stderr, "envtest: timed out stopping manager")
			exitCode = 1
		}
	}()

	startupTimer := time.NewTimer(testTimeout)
	defer startupTimer.Stop()
	select {
	case <-mgr.Elected():
		// 未启用 leader election 时，该信号在缓存同步且 controller 启动后关闭。
	case <-mgrDone:
		fmt.Fprintf(os.Stderr, "envtest: manager stopped during startup: %v\n", mgrRunErr)
		return 1
	case <-startupTimer.C:
		fmt.Fprintln(os.Stderr, "envtest: timed out waiting for manager startup")
		return 1
	}

	return m.Run()
}

// testContext returns a context with a timeout suitable for test assertions.
func testContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), testTimeout)
}

// createNamespace creates a unique namespace for test isolation and returns its name.
func createNamespace(t *testing.T, prefix string) string {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()

	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if err := k8sClient.Create(ctx, ns); err != nil {
		t.Fatalf("failed to create namespace %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), cleanupTimeout)
		defer cancel()
		if err := k8sClient.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("failed to delete namespace %s: %v", name, err)
		}
	})
	return name
}

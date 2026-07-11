//go:build integration

package controller_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/spring-ai-alibaba/aistio/api/v1alpha1"
	"github.com/spring-ai-alibaba/aistio/internal/prober"
)

func TestAgentControlLoopE2E(t *testing.T) {
	ns := createNamespace(t, "agent-e2e")
	agentProber.reset()

	replicas := int32(1)
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "declarative-agent",
			Namespace: ns,
		},
		Spec: v1alpha1.AgentSpec{
			Type:        v1alpha1.AgentTypeDeclarative,
			Runtime:     "agentscope-java",
			DisplayName: "Envtest Agent",
			Declarative: &v1alpha1.DeclarativeSpec{
				Replicas: &replicas,
				AgentConfig: v1alpha1.AgentConfig{
					SystemMessage:   "verify the complete control loop",
					Stream:          true,
					MaxTurns:        4,
					SessionAffinity: "none",
				},
				Env: []v1alpha1.EnvVar{{Name: "E2E_MARKER", Value: "envtest"}},
			},
		},
	}
	createCtx, createCancel := testContext(t)
	if err := k8sClient.Create(createCtx, agent); err != nil {
		createCancel()
		t.Fatalf("创建 Agent CRD：%v", err)
	}
	createCancel()
	cleanupAgent(t, client.ObjectKeyFromObject(agent))

	key := types.NamespacedName{Name: agent.Name, Namespace: ns}
	var (
		observedAgent v1alpha1.Agent
		configMap     corev1.ConfigMap
		deployment    appsv1.Deployment
		service       corev1.Service
	)
	eventually(t, "Agent 子资源和初始状态完成协调", func(ctx context.Context) (bool, error) {
		if err := k8sClient.Get(ctx, key, &observedAgent); err != nil {
			return retryIfNotFound(err)
		}
		if !slices.Contains(observedAgent.Finalizers, "agentscope.io/agent-finalizer") ||
			observedAgent.Status.ObservedGeneration != observedAgent.Generation ||
			observedAgent.Status.ManagementMode != v1alpha1.ManagementModeCPManaged ||
			!hasAgentCondition(observedAgent.Status.Conditions, v1alpha1.ConditionAccepted, metav1.ConditionTrue, "Reconciled") ||
			!hasAgentCondition(observedAgent.Status.Conditions, v1alpha1.ConditionReady, metav1.ConditionFalse, "DeploymentNotReady") {
			return false, nil
		}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: agent.Name + "-config", Namespace: ns}, &configMap); err != nil {
			return retryIfNotFound(err)
		}
		if err := k8sClient.Get(ctx, key, &deployment); err != nil {
			return retryIfNotFound(err)
		}
		if err := k8sClient.Get(ctx, key, &service); err != nil {
			return retryIfNotFound(err)
		}
		return true, nil
	})

	assertControllerOwner(t, &configMap, &observedAgent, "Agent")
	assertControllerOwner(t, &deployment, &observedAgent, "Agent")
	assertControllerOwner(t, &service, &observedAgent, "Agent")
	assertRenderedConfig(t, &configMap)
	assertDeploymentContract(t, &deployment, replicas)
	assertServiceContract(t, &service, agent.Name)

	if observedAgent.Status.Replicas.Desired != replicas ||
		observedAgent.Status.Replicas.Ready != 0 ||
		observedAgent.Status.Replicas.Available != 0 {
		t.Fatalf("初始副本状态不符合 envtest 边界：%+v", observedAgent.Status.Replicas)
	}

	// envtest 不包含 Deployment/ReplicaSet controller、scheduler 或 kubelet。
	// 这里从真实 Deployment PodTemplate 创建经过 API Server 校验的 Pod，并写入
	// Kubernetes 组件本应上报的状态，用于验证 Agent controller 的状态消费闭环。
	statusCtx, statusCancel := testContext(t)
	defer statusCancel()
	pod := createSimulatedReadyPod(t, statusCtx, &deployment)
	statusCancel()
	eventually(t, "manager cache 观察到 Ready Pod", func(ctx context.Context) (bool, error) {
		var cachedPod corev1.Pod
		if err := managerClient.Get(ctx, client.ObjectKeyFromObject(pod), &cachedPod); err != nil {
			return retryIfNotFound(err)
		}
		return cachedPod.Status.Phase == corev1.PodRunning && cachedPod.Status.PodIP != "" && podReady(&cachedPod), nil
	})
	deploymentStatusCtx, deploymentStatusCancel := testContext(t)
	defer deploymentStatusCancel()
	markDeploymentReady(t, deploymentStatusCtx, key, replicas)
	deploymentStatusCancel()

	eventually(t, "Agent 消费 Pod 与 Deployment Ready 状态", func(ctx context.Context) (bool, error) {
		if err := k8sClient.Get(ctx, key, &observedAgent); err != nil {
			return retryIfNotFound(err)
		}
		return observedAgent.Status.Replicas.Ready == replicas &&
			observedAgent.Status.Replicas.Available == replicas &&
			hasAgentCondition(observedAgent.Status.Conditions, v1alpha1.ConditionReady, metav1.ConditionTrue, "DeploymentReady") &&
			hasAgentCondition(observedAgent.Status.Conditions, v1alpha1.ConditionDataPlaneConnected, metav1.ConditionTrue, "ContractLevel1Verified") &&
			observedAgent.Status.DataPlaneInfo != nil && observedAgent.Status.DataPlaneInfo.ContractLevel == 1, nil
	})

	if pod.Status.Phase != corev1.PodRunning || !podReady(pod) {
		t.Fatalf("模拟 Pod 未进入 Running/Ready：phase=%s conditions=%v", pod.Status.Phase, pod.Status.Conditions)
	}
	selector := labels.SelectorFromSet(service.Spec.Selector)
	if !selector.Matches(labels.Set(pod.Labels)) {
		t.Fatalf("Service selector %v 未选中 Pod labels %v", service.Spec.Selector, pod.Labels)
	}
	if endpoint := agentProber.lastEndpoint(); endpoint != "http://10.0.0.10:8080" {
		t.Fatalf("Agent controller 探测 endpoint=%q，期望 http://10.0.0.10:8080", endpoint)
	}
}

func eventually(t *testing.T, description string, condition wait.ConditionWithContextFunc) {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()
	if err := wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, condition); err != nil {
		t.Fatalf("等待%s：%v", description, err)
	}
}

func cleanupAgent(t *testing.T, key client.ObjectKey) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), testTimeout)
		defer cancel()

		var agent v1alpha1.Agent
		if err := k8sClient.Get(ctx, key, &agent); err != nil {
			if !apierrors.IsNotFound(err) {
				t.Errorf("清理前读取 Agent %s：%v", key, err)
			}
			return
		}
		if err := k8sClient.Delete(ctx, &agent); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("删除 Agent %s：%v", key, err)
			return
		}
		if err := wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
			err := k8sClient.Get(ctx, key, &agent)
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}); err != nil {
			t.Errorf("等待 Agent %s finalizer 清理：%v", key, err)
		}
	})
}

func retryIfNotFound(err error) (bool, error) {
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func hasAgentCondition(conditions []v1alpha1.Condition, conditionType v1alpha1.ConditionType, status metav1.ConditionStatus, reason string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && condition.Status == status && condition.Reason == reason {
			return true
		}
	}
	return false
}

func assertControllerOwner(t *testing.T, object metav1.Object, owner metav1.Object, kind string) {
	t.Helper()
	ownerRef := metav1.GetControllerOf(object)
	if ownerRef == nil {
		t.Fatalf("%s/%s 缺少 controller owner reference", object.GetNamespace(), object.GetName())
	}
	if ownerRef.Kind != kind || ownerRef.Name != owner.GetName() || ownerRef.UID != owner.GetUID() {
		t.Fatalf("%s/%s owner=%+v，期望 %s/%s uid=%s", object.GetNamespace(), object.GetName(), ownerRef, kind, owner.GetName(), owner.GetUID())
	}
}

func assertRenderedConfig(t *testing.T, configMap *corev1.ConfigMap) {
	t.Helper()
	var config struct {
		Name          string `json:"name"`
		Runtime       string `json:"runtime"`
		SystemMessage string `json:"systemMessage"`
		Stream        bool   `json:"stream"`
		MaxTurns      int32  `json:"maxTurns"`
	}
	if err := json.Unmarshal([]byte(configMap.Data["agent-config.json"]), &config); err != nil {
		t.Fatalf("解析 agent-config.json：%v", err)
	}
	if config.Name != "declarative-agent" || config.Runtime != "agentscope-java" ||
		config.SystemMessage != "verify the complete control loop" || !config.Stream || config.MaxTurns != 4 {
		t.Fatalf("ConfigMap 渲染结果不符合 Agent spec：%+v", config)
	}
}

func assertDeploymentContract(t *testing.T, deployment *appsv1.Deployment, replicas int32) {
	t.Helper()
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != replicas {
		t.Fatalf("Deployment replicas=%v，期望 %d", deployment.Spec.Replicas, replicas)
	}
	if deployment.Spec.Selector.MatchLabels["app"] != deployment.Name ||
		deployment.Spec.Template.Labels["app"] != deployment.Name ||
		deployment.Spec.Template.Labels["agentscope.io/managed"] != "true" {
		t.Fatalf("Deployment selector/template labels 不一致：selector=%v labels=%v", deployment.Spec.Selector.MatchLabels, deployment.Spec.Template.Labels)
	}
	if len(deployment.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("Deployment containers=%d，期望 1", len(deployment.Spec.Template.Spec.Containers))
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if container.Name != "agent" || len(container.Ports) != 1 || container.Ports[0].ContainerPort != 8080 {
		t.Fatalf("Agent container 契约错误：name=%s ports=%v", container.Name, container.Ports)
	}
	if container.ReadinessProbe == nil || container.ReadinessProbe.HTTPGet == nil ||
		container.ReadinessProbe.HTTPGet.Path != "/agentscope/health" {
		t.Fatalf("Agent readiness probe 错误：%+v", container.ReadinessProbe)
	}
	if !hasEnv(container.Env, "E2E_MARKER", "envtest") {
		t.Fatalf("Agent container 缺少 E2E_MARKER：%v", container.Env)
	}
	if len(deployment.Spec.Template.Spec.Volumes) != 1 ||
		deployment.Spec.Template.Spec.Volumes[0].ConfigMap == nil ||
		deployment.Spec.Template.Spec.Volumes[0].ConfigMap.Name != deployment.Name+"-config" {
		t.Fatalf("Deployment ConfigMap volume 错误：%v", deployment.Spec.Template.Spec.Volumes)
	}
}

func assertServiceContract(t *testing.T, service *corev1.Service, agentName string) {
	t.Helper()
	if service.Spec.Type != corev1.ServiceTypeClusterIP || service.Spec.Selector["app"] != agentName {
		t.Fatalf("Service 类型或 selector 错误：type=%s selector=%v", service.Spec.Type, service.Spec.Selector)
	}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != 8080 || service.Spec.Ports[0].TargetPort.IntValue() != 8080 {
		t.Fatalf("Service 端口契约错误：%v", service.Spec.Ports)
	}
}

func hasEnv(env []corev1.EnvVar, name, value string) bool {
	for _, variable := range env {
		if variable.Name == name && variable.Value == value {
			return true
		}
	}
	return false
}

func createSimulatedReadyPod(t *testing.T, ctx context.Context, deployment *appsv1.Deployment) *corev1.Pod {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployment.Name + "-envtest",
			Namespace: deployment.Namespace,
			Labels:    maps.Clone(deployment.Spec.Template.Labels),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment")),
			},
		},
		Spec: *deployment.Spec.Template.Spec.DeepCopy(),
	}
	if err := k8sClient.Create(ctx, pod); err != nil {
		t.Fatalf("从 Deployment PodTemplate 创建模拟 Pod：%v", err)
	}
	pod.Status = corev1.PodStatus{
		Phase: corev1.PodRunning,
		PodIP: "10.0.0.10",
		Conditions: []corev1.PodCondition{{
			Type:               corev1.PodReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		}},
	}
	if err := k8sClient.Status().Update(ctx, pod); err != nil {
		t.Fatalf("写入模拟 Pod Ready 状态：%v", err)
	}
	return pod
}

func markDeploymentReady(t *testing.T, ctx context.Context, key client.ObjectKey, replicas int32) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var deployment appsv1.Deployment
		if err := k8sClient.Get(ctx, key, &deployment); err != nil {
			return err
		}
		deployment.Status = appsv1.DeploymentStatus{
			ObservedGeneration: deployment.Generation,
			Replicas:           replicas,
			UpdatedReplicas:    replicas,
			ReadyReplicas:      replicas,
			AvailableReplicas:  replicas,
		}
		return k8sClient.Status().Update(ctx, &deployment)
	}); err != nil {
		t.Fatalf("写入模拟 Deployment Ready 状态：%v", err)
	}
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

type recordingProber struct {
	mu       sync.Mutex
	endpoint string
}

func (p *recordingProber) ProbeInfo(_ context.Context, endpoint string) (*prober.DataPlaneInfo, error) {
	p.mu.Lock()
	p.endpoint = endpoint
	p.mu.Unlock()
	return &prober.DataPlaneInfo{
		Name:          "declarative-agent",
		Runtime:       "agentscope-java",
		Version:       "envtest",
		ContractLevel: 1,
	}, nil
}

func (p *recordingProber) ProbeHealth(context.Context, string) (bool, error) {
	return true, nil
}

func (p *recordingProber) ProbeSessions(context.Context, string) ([]prober.SessionSnapshot, error) {
	return nil, nil
}

func (p *recordingProber) SendCompress(context.Context, string, string) error {
	return nil
}

func (p *recordingProber) SendTerminate(context.Context, string, string) error {
	return nil
}

func (p *recordingProber) FetchSessionState(context.Context, string, string) (*prober.SessionState, error) {
	return nil, nil
}

func (p *recordingProber) reset() {
	p.mu.Lock()
	p.endpoint = ""
	p.mu.Unlock()
}

func (p *recordingProber) lastEndpoint() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.endpoint
}

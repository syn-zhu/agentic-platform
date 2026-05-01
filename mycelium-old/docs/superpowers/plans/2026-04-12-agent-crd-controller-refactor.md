# Agent CRD + Controller Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Define the Agent CRD and refactor all controllers to follow kubebuilder best practices: one Kind per controller, idempotent SSA reconciliation, GenerationChangedPredicate, validating webhooks for deletion protection, and cross-controller coordination via Watches.

**Architecture:** Four controllers (Project, Tool, CredentialProvider, Agent), each watching exactly one Kind. ProjectReconciler additionally watches Agent/Tool events to recompute the tool-access policy. ValidatingWebhooks handle deletion protection at admission time (immediate rejection vs. lingering "terminating" state). All resource application uses Server-Side Apply with `FieldOwner("mycelium-controller")`.

**Tech Stack:** Go, controller-runtime v0.23.3, AgentGateway v1alpha1 types, Knative Serving, agent-sandbox CRDs

**Existing code:** `/Users/siyanzhu/agentic-platform/mycelium/`

---

## File Structure (changes only)

```
mycelium/
├── api/v1alpha1/
│   ├── agent_types.go              # NEW: Agent CRD
│   ├── agent_types_test.go         # NEW: Agent CRD tests
│   └── (existing types unchanged)
│
├── internal/
│   ├── controller/
│   │   ├── project_reconciler.go       # MODIFY: add Watches for Agent/Tool, syncToolAccessPolicy()
│   │   ├── project_reconciler_test.go  # MODIFY: add tool-access policy tests
│   │   ├── tool_reconciler.go          # MODIFY: add GenerationChangedPredicate, validate CP ref
│   │   ├── tool_reconciler_test.go     # MODIFY: add CP validation test
│   │   ├── credentialprovider_reconciler.go      # MODIFY: add GenerationChangedPredicate, remove finalizer-based deletion blocking
│   │   ├── credentialprovider_reconciler_test.go  # MODIFY: update deletion tests
│   │   ├── agent_reconciler.go         # NEW
│   │   ├── agent_reconciler_test.go    # NEW
│   │   └── suite_test.go              # MODIFY: no changes needed (scheme already covers all types)
│   │
│   ├── generate/
│   │   ├── cel.go                  # EXISTING: ToolAccessCEL (no changes)
│   │   └── cel_test.go             # EXISTING: (no changes)
│   │
│   └── webhook/
│       ├── project_webhook.go      # NEW: ValidatingWebhook for Project DELETE
│       ├── project_webhook_test.go # NEW
│       ├── credentialprovider_webhook.go      # NEW: ValidatingWebhook for CP DELETE
│       └── credentialprovider_webhook_test.go # NEW
│
├── cmd/controller/main.go          # MODIFY: register AgentReconciler + webhooks
└── config/
    └── crd/bases/
        └── mycelium.io_agents.yaml # GENERATED
```

---

## Task 1: Agent CRD Type

**Files:**
- Create: `mycelium/api/v1alpha1/agent_types.go`
- Create: `mycelium/api/v1alpha1/agent_types_test.go`

- [ ] **Step 1: Write the type test**

```go
// mycelium/api/v1alpha1/agent_types_test.go
package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAgent_HasExpectedFields(t *testing.T) {
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github-assistant",
			Namespace: "acme",
		},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub integration agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
				{Ref: corev1.LocalObjectReference{Name: "create-issue"}},
			},
			Container: v1alpha1.AgentContainer{
				Image: "acme/github-assistant:latest",
			},
			Sandbox: &v1alpha1.SandboxConfig{
				ShutdownTimeout: "30m",
				WarmPool: &v1alpha1.WarmPoolConfig{
					Replicas: 2,
				},
			},
		},
	}

	assert.Equal(t, "GitHub integration agent", agent.Spec.Description)
	require.Len(t, agent.Spec.Tools, 2)
	assert.Equal(t, "list-repos", agent.Spec.Tools[0].Ref.Name)
	assert.Equal(t, "create-issue", agent.Spec.Tools[1].Ref.Name)
	assert.Equal(t, "acme/github-assistant:latest", agent.Spec.Container.Image)
	require.NotNil(t, agent.Spec.Sandbox)
	assert.Equal(t, "30m", agent.Spec.Sandbox.ShutdownTimeout)
	assert.Equal(t, int32(2), agent.Spec.Sandbox.WarmPool.Replicas)
}

func TestAgent_ToolsRequired(t *testing.T) {
	agent := &v1alpha1.Agent{
		Spec: v1alpha1.AgentSpec{
			Description: "Minimal agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "echo"}},
			},
			Container: v1alpha1.AgentContainer{Image: "tools/echo:latest"},
		},
	}
	require.Len(t, agent.Spec.Tools, 1)
	assert.Nil(t, agent.Spec.Sandbox)
}

func TestAgent_StatusConditions(t *testing.T) {
	agent := &v1alpha1.Agent{
		Status: v1alpha1.AgentStatus{
			ServiceAccount: "github-assistant",
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled"},
				{Type: "ToolsValid", Status: metav1.ConditionTrue, Reason: "AllToolsExist"},
			},
		},
	}
	assert.Equal(t, "github-assistant", agent.Status.ServiceAccount)
	assert.Len(t, agent.Status.Conditions, 2)
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/siyanzhu/agentic-platform/mycelium && go test ./api/v1alpha1/ -v -run TestAgent
```
Expected: FAIL — types not defined.

- [ ] **Step 3: Implement Agent types**

```go
// mycelium/api/v1alpha1/agent_types.go
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ToolRef is a reference to a Tool in the same namespace.
type ToolRef struct {
	// Ref references a Tool by name in the same namespace.
	// +kubebuilder:validation:Required
	Ref corev1.LocalObjectReference `json:"ref"`
}

// AgentContainer defines the container spec for the agent sandbox.
type AgentContainer struct {
	// Image is the container image for the agent.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Image string `json:"image"`
}

// WarmPoolConfig defines the warm pool settings for agent sandboxes.
type WarmPoolConfig struct {
	// Replicas is the number of pre-warmed sandbox pods to maintain.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas"`
}

// SandboxConfig defines the sandbox lifecycle settings.
type SandboxConfig struct {
	// ShutdownTimeout is the duration after which an idle sandbox is released.
	// Format: Go duration string (e.g., "30m", "1h").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[0-9]+(s|m|h)$`
	ShutdownTimeout string `json:"shutdownTimeout"`
	// WarmPool configures pre-warmed sandbox pods for this agent.
	// +optional
	WarmPool *WarmPoolConfig `json:"warmPool,omitempty"`
}

// AgentSpec defines the desired state of Agent.
type AgentSpec struct {
	// Description is the human-readable agent description.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description"`
	// Tools are the tools this agent can access, as typed references to Tool resources
	// in the same namespace. The Mycelium controller uses this to generate the
	// AGW tool-access policy CEL expressions.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	Tools []ToolRef `json:"tools"`
	// Container defines the agent sandbox container.
	// +kubebuilder:validation:Required
	Container AgentContainer `json:"container"`
	// Sandbox configures the agent sandbox lifecycle. If nil, defaults are used.
	// +optional
	Sandbox *SandboxConfig `json:"sandbox,omitempty"`
}

// AgentStatus defines the observed state of Agent.
type AgentStatus struct {
	// ServiceAccount is the K8s service account name derived from this agent.
	// Used in tool-access policy CEL expressions for identity resolution.
	// +optional
	ServiceAccount string `json:"serviceAccount,omitempty"`
	// Conditions represent the latest observations of the Agent's state.
	// Known condition types: "Ready", "ToolsValid"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ag,categories=mycelium
// +kubebuilder:printcolumn:name="Tools",type=integer,JSONPath=".spec.tools",description="Number of tools",priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the agent is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Agent is the Schema for the agents API. Each Agent defines which Tools it
// can access and how its sandbox is configured.
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   AgentSpec   `json:"spec"`
	Status AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentList contains a list of Agent.
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Agent{}, &AgentList{})
}
```

- [ ] **Step 4: Generate deepcopy**

```bash
make generate
```

- [ ] **Step 5: Run test — expect PASS**

```bash
go test ./api/v1alpha1/ -v -run TestAgent
```

- [ ] **Step 6: Generate CRD manifests and verify**

```bash
make manifests
ls config/crd/bases/mycelium.io_agents.yaml
```

- [ ] **Step 7: Commit**

```bash
git add mycelium/api/v1alpha1/agent_types.go mycelium/api/v1alpha1/agent_types_test.go mycelium/api/v1alpha1/zz_generated.deepcopy.go mycelium/config/crd/bases/mycelium.io_agents.yaml
git commit -m "feat(mycelium): add Agent CRD type with tool refs and sandbox config"
```

---

## Task 2: ProjectReconciler — Add Watches + syncToolAccessPolicy

**Files:**
- Modify: `mycelium/internal/controller/project_reconciler.go`
- Modify: `mycelium/internal/controller/project_reconciler_test.go`

- [ ] **Step 1: Write test for tool-access policy sync**

Add to `project_reconciler_test.go`:

```go
func TestProjectReconciler_SyncsToolAccessPolicy(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()

	// Agent referencing two tools
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
				{Ref: corev1.LocalObjectReference{Name: "create-issue"}},
			},
			Container: v1alpha1.AgentContainer{Image: "acme/gh:latest"},
		},
	}

	// Tools in namespace
	tool1 := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			ToolName: "list_repos", Description: "List repos",
			Container: v1alpha1.ToolContainer{Image: "tools/lr:latest"},
		},
	}
	tool2 := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "create-issue", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			ToolName: "create_issue", Description: "Create issue",
			Container: v1alpha1.ToolContainer{Image: "tools/ci:latest"},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(proj, agent, tool1, tool2).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	// Verify tool-access policy was created
	var policy agwv1alpha1.AgentgatewayPolicy
	err = cl.Get(context.Background(), types.NamespacedName{
		Name: "mcp-tool-access", Namespace: "acme",
	}, &policy)
	require.NoError(t, err)
	require.NotNil(t, policy.Spec.Backend)
	require.NotNil(t, policy.Spec.Backend.MCP)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Len(t, policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions, 1)
	assert.Contains(t, string(policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0]), "github-assistant")
}

func TestProjectReconciler_NoAgents_NoToolAccessPolicy(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	// No agents → no tool-access policy created
	var policy agwv1alpha1.AgentgatewayPolicy
	err = cl.Get(context.Background(), types.NamespacedName{
		Name: "mcp-tool-access", Namespace: "acme",
	}, &policy)
	assert.True(t, errors.IsNotFound(err))
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
go test ./internal/controller/ -v -run TestProjectReconciler_SyncsToolAccessPolicy
```

- [ ] **Step 3: Add syncToolAccessPolicy sub-handler + Watches**

Update `project_reconciler.go`:

```go
// Add to imports:
// "sigs.k8s.io/controller-runtime/pkg/handler"
// "sigs.k8s.io/controller-runtime/pkg/predicate"
// "sigs.k8s.io/controller-runtime/pkg/reconcile"

// Add syncToolAccessPolicy as a new sub-handler called from Reconcile():
func (r *ProjectReconciler) syncToolAccessPolicy(ctx context.Context, proj *v1alpha1.Project) error {
	ns := proj.Name

	// List all agents in the project namespace
	var agents v1alpha1.AgentList
	if err := r.List(ctx, &agents, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("listing agents: %w", err)
	}

	if len(agents.Items) == 0 {
		// No agents — delete the policy if it exists
		existing := generate.ToolAccessPolicy(ns, nil)
		if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("deleting orphaned tool-access policy: %w", err)
		}
		return nil
	}

	// List all tools in the project namespace
	var tools v1alpha1.ToolList
	if err := r.List(ctx, &tools, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}

	// Build tool name lookup (Tool.metadata.name → Tool.spec.toolName)
	toolNameMap := make(map[string]string, len(tools.Items))
	for _, t := range tools.Items {
		toolNameMap[t.Name] = t.Spec.ToolName
	}

	// Build agent→tools mapping
	agentTools := make(map[string][]string)
	for _, agent := range agents.Items {
		var toolNames []string
		for _, ref := range agent.Spec.Tools {
			if mcpName, ok := toolNameMap[ref.Ref.Name]; ok {
				toolNames = append(toolNames, mcpName)
			}
		}
		if len(toolNames) > 0 {
			agentTools[agent.Name] = toolNames
		}
	}

	celExprs := generate.ToolAccessCEL(agentTools)
	policy := generate.ToolAccessPolicy(ns, celExprs)
	if err := controllerutil.SetControllerReference(proj, policy, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on tool-access policy: %w", err)
	}
	if err := r.Patch(ctx, policy, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying tool-access policy: %w", err)
	}
	return nil
}

// Update Reconcile() to call syncToolAccessPolicy after applyGeneratedResources:
// ... (insert after applyGeneratedResources call)
// if err := r.syncToolAccessPolicy(ctx, &proj); err != nil { ... }

// Update SetupWithManager to add Watches + predicate:
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.Agent{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject)).
		Watches(&v1alpha1.Tool{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject)).
		Complete(r)
}

// mapToProject maps a namespace-scoped resource to its owning Project reconcile request.
func (r *ProjectReconciler) mapToProject(ctx context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: obj.GetNamespace()},
	}}
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./internal/controller/ -v -run TestProjectReconciler
```

- [ ] **Step 5: Commit**

```bash
git add mycelium/internal/controller/project_reconciler.go mycelium/internal/controller/project_reconciler_test.go
git commit -m "feat(mycelium): add tool-access policy sync + Agent/Tool watches to ProjectReconciler"
```

---

## Task 3: AgentReconciler

**Files:**
- Create: `mycelium/internal/controller/agent_reconciler.go`
- Create: `mycelium/internal/controller/agent_reconciler_test.go`

- [ ] **Step 1: Write agent reconciler tests**

```go
// mycelium/internal/controller/agent_reconciler_test.go
package controller_test

import (
	"context"
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/controller"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newAgent() *v1alpha1.Agent {
	return &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
			},
			Container: v1alpha1.AgentContainer{Image: "acme/gh:latest"},
		},
	}
}

func TestAgentReconciler_SetsServiceAccount(t *testing.T) {
	scheme := newScheme(t)
	agent := newAgent()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	require.NoError(t, err)
	assert.Equal(t, "github-assistant", updated.Status.ServiceAccount)
}

func TestAgentReconciler_ValidatesToolRefs(t *testing.T) {
	scheme := newScheme(t)
	agent := newAgent()
	// Tool "list-repos" does NOT exist
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	require.NoError(t, err)

	// Should have ToolsValid=False condition
	var toolsValid bool
	for _, c := range updated.Status.Conditions {
		if c.Type == "ToolsValid" && c.Status == metav1.ConditionFalse {
			toolsValid = true
			assert.Contains(t, c.Message, "list-repos")
		}
	}
	assert.True(t, toolsValid, "expected ToolsValid=False condition")
}

func TestAgentReconciler_AllToolsExist_SetsReady(t *testing.T) {
	scheme := newScheme(t)
	agent := newAgent()
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			ToolName: "list_repos", Description: "List repos",
			Container: v1alpha1.ToolContainer{Image: "tools/lr:latest"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent, tool).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	require.NoError(t, err)

	var ready, toolsValid bool
	for _, c := range updated.Status.Conditions {
		if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
			ready = true
		}
		if c.Type == "ToolsValid" && c.Status == metav1.ConditionTrue {
			toolsValid = true
		}
	}
	assert.True(t, ready)
	assert.True(t, toolsValid)
}

func TestAgentReconciler_AddsFinalizer(t *testing.T) {
	scheme := newScheme(t)
	agent := newAgent()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.AgentFinalizer)
}

func TestAgentReconciler_NotFound(t *testing.T) {
	scheme := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "gone", Namespace: "acme"},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
go test ./internal/controller/ -v -run TestAgentReconciler
```

- [ ] **Step 3: Implement AgentReconciler**

```go
// mycelium/internal/controller/agent_reconciler.go
package controller

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const AgentFinalizer = "mycelium.io/agent-cleanup"

type AgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=agents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=agents/finalizers,verbs=update
// +kubebuilder:rbac:groups=mycelium.io,resources=tools,verbs=get;list;watch

func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agent v1alpha1.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !agent.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &agent)
	}

	if !controllerutil.ContainsFinalizer(&agent, AgentFinalizer) {
		controllerutil.AddFinalizer(&agent, AgentFinalizer)
		if err := r.Update(ctx, &agent); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.Info("Reconciling Agent", "agent", agent.Name)

	// Set service account name (derived from agent name)
	agent.Status.ServiceAccount = agent.Name

	// Validate tool references
	missingTools := r.validateToolRefs(ctx, &agent)
	if len(missingTools) > 0 {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               "ToolsValid",
			Status:             metav1.ConditionFalse,
			Reason:             "ToolsNotFound",
			Message:            fmt.Sprintf("Missing tools: %v", missingTools),
			LastTransitionTime: metav1.Now(),
		})
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ToolsNotFound",
			Message:            fmt.Sprintf("Missing tools: %v", missingTools),
			LastTransitionTime: metav1.Now(),
		})
	} else {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               "ToolsValid",
			Status:             metav1.ConditionTrue,
			Reason:             "AllToolsExist",
			Message:            "All referenced tools exist",
			LastTransitionTime: metav1.Now(),
		})
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Reconciled",
			Message:            fmt.Sprintf("Agent %s reconciled", agent.Name),
			LastTransitionTime: metav1.Now(),
		})
	}

	// TODO(mycelium): Generate SandboxTemplate + WarmPool from agent.Spec.Sandbox
	// when agent-sandbox integration is implemented.

	if err := r.Status().Update(ctx, &agent); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AgentReconciler) validateToolRefs(ctx context.Context, agent *v1alpha1.Agent) []string {
	var missing []string
	for _, toolRef := range agent.Spec.Tools {
		var tool v1alpha1.Tool
		err := r.Get(ctx, types.NamespacedName{
			Name:      toolRef.Ref.Name,
			Namespace: agent.Namespace,
		}, &tool)
		if errors.IsNotFound(err) {
			missing = append(missing, toolRef.Ref.Name)
		}
	}
	return missing
}

func (r *AgentReconciler) reconcileDelete(ctx context.Context, agent *v1alpha1.Agent) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up Agent", "agent", agent.Name)

	// Sandbox resources are cleaned up via ownerReference GC
	// Tool-access policy recomputation is triggered by the ProjectReconciler
	// watching Agent events.

	controllerutil.RemoveFinalizer(agent, AgentFinalizer)
	if err := r.Update(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Agent{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./internal/controller/ -v -run TestAgentReconciler
```

- [ ] **Step 5: Commit**

```bash
git add mycelium/internal/controller/agent_reconciler.go mycelium/internal/controller/agent_reconciler_test.go
git commit -m "feat(mycelium): add AgentReconciler with tool ref validation"
```

---

## Task 4: Add GenerationChangedPredicate to Tool + CredentialProvider Reconcilers

**Files:**
- Modify: `mycelium/internal/controller/tool_reconciler.go`
- Modify: `mycelium/internal/controller/credentialprovider_reconciler.go`

- [ ] **Step 1: Update ToolReconciler.SetupWithManager**

In `tool_reconciler.go`, change:
```go
func (r *ToolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Tool{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
```

Add imports: `"sigs.k8s.io/controller-runtime/pkg/builder"` and `"sigs.k8s.io/controller-runtime/pkg/predicate"`.

- [ ] **Step 2: Update CredentialProviderReconciler.SetupWithManager**

In `credentialprovider_reconciler.go`, change:
```go
func (r *CredentialProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CredentialProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
```

Add imports: `"sigs.k8s.io/controller-runtime/pkg/builder"` and `"sigs.k8s.io/controller-runtime/pkg/predicate"`.

- [ ] **Step 3: Remove finalizer-based deletion blocking from CredentialProviderReconciler**

Replace the `reconcileDelete` method — deletion protection is now handled by the webhook (Task 6). The reconciler just cleans up:

```go
func (r *CredentialProviderReconciler) reconcileDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up CredentialProvider", "name", cp.Name)

	controllerutil.RemoveFinalizer(cp, CredentialProviderFinalizer)
	if err := r.Update(ctx, cp); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}
```

Remove the `findDependentTools` method and the `time` import.

- [ ] **Step 4: Update CredentialProvider tests**

Remove `TestCredentialProviderReconciler_BlocksDeletionWithDependentTools` — deletion blocking moves to the webhook. Update `TestCredentialProviderReconciler_AllowsDeletionWithNoDependents` to just verify finalizer removal.

- [ ] **Step 5: Run all tests**

```bash
go test ./internal/controller/ -v -count=1
```
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add mycelium/internal/controller/
git commit -m "refactor(mycelium): add GenerationChangedPredicate, move deletion protection to webhooks"
```

---

## Task 5: ValidatingWebhook — Project Deletion

**Files:**
- Create: `mycelium/internal/webhook/project_webhook.go`
- Create: `mycelium/internal/webhook/project_webhook_test.go`

- [ ] **Step 1: Write webhook test**

```go
// mycelium/internal/webhook/project_webhook_test.go
package webhook_test

import (
	"context"
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

func TestProjectDeletionValidator_AllowsWhenEmpty(t *testing.T) {
	scheme := newScheme(t)
	proj := &v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	v := &webhook.ProjectDeletionValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), proj)
	assert.NoError(t, err)
}

func TestProjectDeletionValidator_RejectsWithTools(t *testing.T) {
	scheme := newScheme(t)
	proj := &v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			ToolName: "list_repos", Description: "d",
			Container: v1alpha1.ToolContainer{Image: "i"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).Build()

	v := &webhook.ProjectDeletionValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), proj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Tools")
}

func TestProjectDeletionValidator_RejectsWithAgents(t *testing.T) {
	scheme := newScheme(t)
	proj := &v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "gh", Namespace: "acme"},
		Spec: v1alpha1.AgentSpec{
			Description: "d",
			Tools:       []v1alpha1.ToolRef{{Ref: corev1.LocalObjectReference{Name: "t"}}},
			Container:   v1alpha1.AgentContainer{Image: "i"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).Build()

	v := &webhook.ProjectDeletionValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), proj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Agents")
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
go test ./internal/webhook/ -v -run TestProjectDeletion
```

- [ ] **Step 3: Implement ProjectDeletionValidator**

```go
// mycelium/internal/webhook/project_webhook.go
package webhook

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProjectDeletionValidator validates that a Project can be deleted
// (no dependent Tools, CredentialProviders, or Agents in its namespace).
type ProjectDeletionValidator struct {
	client.Client
}

func (v *ProjectDeletionValidator) ValidateDelete(ctx context.Context, proj *v1alpha1.Project) error {
	ns := proj.Name // Project name == namespace name

	var tools v1alpha1.ToolList
	if err := v.List(ctx, &tools, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}
	if len(tools.Items) > 0 {
		return fmt.Errorf("cannot delete Project %s: %d Tools still exist in namespace", proj.Name, len(tools.Items))
	}

	var cps v1alpha1.CredentialProviderList
	if err := v.List(ctx, &cps, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("listing credential providers: %w", err)
	}
	if len(cps.Items) > 0 {
		return fmt.Errorf("cannot delete Project %s: %d CredentialProviders still exist in namespace", proj.Name, len(cps.Items))
	}

	var agents v1alpha1.AgentList
	if err := v.List(ctx, &agents, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("listing agents: %w", err)
	}
	if len(agents.Items) > 0 {
		return fmt.Errorf("cannot delete Project %s: %d Agents still exist in namespace", proj.Name, len(agents.Items))
	}

	return nil
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./internal/webhook/ -v -run TestProjectDeletion
```

- [ ] **Step 5: Commit**

```bash
git add mycelium/internal/webhook/
git commit -m "feat(mycelium): add Project deletion validating webhook"
```

---

## Task 6: ValidatingWebhook — CredentialProvider Deletion

**Files:**
- Create: `mycelium/internal/webhook/credentialprovider_webhook.go`
- Create: `mycelium/internal/webhook/credentialprovider_webhook_test.go`

- [ ] **Step 1: Write webhook test**

```go
// mycelium/internal/webhook/credentialprovider_webhook_test.go
package webhook_test

import (
	"context"
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCredentialProviderDeletionValidator_AllowsWhenNoDependents(t *testing.T) {
	scheme := newScheme(t)
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	v := &webhook.CredentialProviderDeletionValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), cp)
	assert.NoError(t, err)
}

func TestCredentialProviderDeletionValidator_RejectsWithDependentOAuth(t *testing.T) {
	scheme := newScheme(t)
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			ToolName: "list_repos", Description: "d",
			Container: v1alpha1.ToolContainer{Image: "i"},
			Credentials: &v1alpha1.ToolCredentials{
				OAuth: &v1alpha1.OAuthCredentialRef{
					ProviderRef: corev1.LocalObjectReference{Name: "github"},
					Scopes:      []string{"repo"},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).Build()

	v := &webhook.CredentialProviderDeletionValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), cp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list-repos")
}

func TestCredentialProviderDeletionValidator_RejectsWithDependentAPIKey(t *testing.T) {
	scheme := newScheme(t)
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "stripe", Namespace: "acme"},
	}
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "charge", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			ToolName: "charge", Description: "d",
			Container: v1alpha1.ToolContainer{Image: "i"},
			Credentials: &v1alpha1.ToolCredentials{
				APIKeys: []v1alpha1.APIKeyCredentialRef{
					{ProviderRef: corev1.LocalObjectReference{Name: "stripe"}},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).Build()

	v := &webhook.CredentialProviderDeletionValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), cp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "charge")
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
go test ./internal/webhook/ -v -run TestCredentialProviderDeletion
```

- [ ] **Step 3: Implement CredentialProviderDeletionValidator**

```go
// mycelium/internal/webhook/credentialprovider_webhook.go
package webhook

import (
	"context"
	"fmt"
	"strings"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CredentialProviderDeletionValidator validates that a CredentialProvider can be deleted
// (no dependent Tools reference it).
type CredentialProviderDeletionValidator struct {
	client.Client
}

func (v *CredentialProviderDeletionValidator) ValidateDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	var toolList v1alpha1.ToolList
	if err := v.List(ctx, &toolList, client.InNamespace(cp.Namespace)); err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}

	var dependents []string
	for _, tool := range toolList.Items {
		if tool.Spec.Credentials == nil {
			continue
		}
		if tool.Spec.Credentials.OAuth != nil && tool.Spec.Credentials.OAuth.ProviderRef.Name == cp.Name {
			dependents = append(dependents, tool.Name)
			continue
		}
		for _, apiKey := range tool.Spec.Credentials.APIKeys {
			if apiKey.ProviderRef.Name == cp.Name {
				dependents = append(dependents, tool.Name)
				break
			}
		}
	}

	if len(dependents) > 0 {
		return fmt.Errorf("cannot delete CredentialProvider %s: referenced by Tools: %s",
			cp.Name, strings.Join(dependents, ", "))
	}
	return nil
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./internal/webhook/ -v -run TestCredentialProviderDeletion
```

- [ ] **Step 5: Commit**

```bash
git add mycelium/internal/webhook/
git commit -m "feat(mycelium): add CredentialProvider deletion validating webhook"
```

---

## Task 7: Update Controller Main + Remove Stale Deletion Logic

**Files:**
- Modify: `mycelium/cmd/controller/main.go`
- Modify: `mycelium/internal/controller/project_reconciler.go` (remove dependency check from reconcileDelete)

- [ ] **Step 1: Update controller main to register Agent + webhooks**

```go
// cmd/controller/main.go — add AgentReconciler registration after CredentialProviderReconciler:
if err := (&controller.AgentReconciler{
    Client: mgr.GetClient(),
    Scheme: mgr.GetScheme(),
}).SetupWithManager(mgr); err != nil {
    ctrl.Log.Error(err, "unable to create Agent controller")
    os.Exit(1)
}

// TODO(mycelium): Register webhooks with the manager when webhook server
// infrastructure is set up. The webhook validators are implemented in
// internal/webhook/ but require cert-manager and webhook server configuration
// to run in-cluster. For now, deletion protection is enforced by the
// reconciler finalizers as a fallback.
```

- [ ] **Step 2: Simplify ProjectReconciler.reconcileDelete**

Remove the dependency check from reconcileDelete (webhook handles it now). Keep the finalizer for MongoDB cleanup:

```go
func (r *ProjectReconciler) reconcileDelete(ctx context.Context, proj *v1alpha1.Project) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up Project", "name", proj.Name)

	// TODO(mycelium): Clean up MongoDB project database here

	// Delete the namespace (owned resources cascade)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: proj.Name}}
	if err := r.Delete(ctx, ns); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(proj, ProjectFinalizer)
	if err := r.Update(ctx, proj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}
```

- [ ] **Step 3: Update project_reconciler_test.go — remove dependency check test, add check for Agents**

Update `newProject()` and test imports as needed for the new Agent type in tests that need it.

- [ ] **Step 4: Build and run full test suite**

```bash
cd /Users/siyanzhu/agentic-platform/mycelium
go mod tidy
make generate manifests
go build ./...
go test ./... -count=1
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add mycelium/
git commit -m "feat(mycelium): register AgentReconciler, simplify deletion to webhook-based protection"
```

---

## Summary

| Task | What it produces | Tests added |
| --- | --- | --- |
| 1. Agent CRD | Agent type with tool refs, sandbox config, validations | 3 type tests |
| 2. ProjectReconciler refactor | syncToolAccessPolicy + Watches for Agent/Tool | 2 policy tests |
| 3. AgentReconciler | Tool ref validation, service account, finalizer | 5 reconciler tests |
| 4. Predicate + CP cleanup | GenerationChangedPredicate on all reconcilers, remove CP deletion blocking | Updated existing tests |
| 5. Project webhook | ValidateDelete — reject if dependents | 3 webhook tests |
| 6. CP webhook | ValidateDelete — reject if tools reference it | 3 webhook tests |
| 7. Tool webhook | ValidateDelete — reject if agents reference it | 2 webhook tests |
| 8. Agent webhook | ValidateCreate/Update — reject if tool refs don't exist (+ check deletionTimestamp) | 2 webhook tests |
| 8b. Defaulting webhooks | Mutating webhooks to set defaults (Tool scaling, etc.) so generators don't handle defaults | TBD |
| 9. Wire up + cleanup | Controller main, simplified deletion | Build + full suite |

**Best practices applied:**
- One Kind per controller (kubebuilder)
- GenerationChangedPredicate (only reconcile on spec changes)
- SSA with FieldOwner (idempotent reconciliation)
- Webhooks for deletion protection (immediate rejection)
- Finalizers only for external cleanup (MongoDB)
- Cross-controller coordination via Watches (Agent/Tool → Project re-reconcile)

**Critical: DeletionTimestamp check in all reference-validating webhooks**

When a webhook validates that a referenced resource exists (e.g., Agent webhook checks Tool refs exist), it MUST also check `deletionTimestamp.IsZero()`. Without this, finalizers create a race:

1. Resource DELETE allowed by its webhook (no dependents)
2. K8s sets `deletionTimestamp` — resource is "terminating" but still visible via `r.Get()`
3. New dependent created — its webhook sees the terminating resource as existing → allows
4. Finalizer runs → resource deleted → dangling reference

Fix: treat a resource with `!deletionTimestamp.IsZero()` as non-existent in validation webhooks.

This applies to ALL reference validation webhooks:
- **Agent create/update webhook**: tool refs must exist AND not be terminating
- **Tool create/update webhook** (if added): credential provider refs must exist AND not be terminating
- **Any future webhook** that validates cross-resource references

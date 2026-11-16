package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/prometheus/client_golang/prometheus/testutil"

	platformv1beta1 "github.com/HariBoddapati01/kubtenant/api/v1beta1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add clientgo scheme: %v", err)
	}
	if err := platformv1beta1.AddToScheme(s); err != nil {
		t.Fatalf("add platform scheme: %v", err)
	}
	return s
}

func newTenant() *platformv1beta1.Tenant {
	return &platformv1beta1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Generation: 1},
		Spec: platformv1beta1.TenantSpec{
			DisplayName:      "ACME Corp",
			Namespaces:       []string{"acme-dev", "acme-prod"},
			Owners:           []platformv1beta1.Subject{{Kind: "Group", Name: "acme-admins"}},
			OwnerRole:        "admin",
			NetworkIsolation: true,
			ResourceQuota:    &platformv1beta1.ResourceQuotaSpec{CPU: "8", Memory: "16Gi", Pods: "50"},
			LimitRange: &platformv1beta1.LimitRangeSpec{
				DefaultCPU: "500m", DefaultMemory: "512Mi",
				DefaultRequestCPU: "100m", DefaultRequestMemory: "128Mi",
			},
		},
	}
}

func reconcileOnce(t *testing.T, objs ...client.Object) (*TenantReconciler, client.Client) {
	t.Helper()
	s := testScheme(t)
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&platformv1beta1.Tenant{}).
		Build()
	r := &TenantReconciler{Client: cl, Scheme: s}
	return r, cl
}

func TestReconcileProvisionsGuardrails(t *testing.T) {
	tenant := newTenant()
	r, cl := reconcileOnce(t, tenant)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "acme"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	for _, nsName := range []string{"acme-dev", "acme-prod"} {
		var ns corev1.Namespace
		if err := cl.Get(ctx, types.NamespacedName{Name: nsName}, &ns); err != nil {
			t.Fatalf("namespace %s not created: %v", nsName, err)
		}
		if got := ns.Labels[LabelTenant]; got != "acme" {
			t.Errorf("namespace %s tenant label = %q, want acme", nsName, got)
		}
		if got := ns.Labels[LabelManagedBy]; got != managerName {
			t.Errorf("namespace %s managed-by = %q, want %s", nsName, got, managerName)
		}
		if len(ns.OwnerReferences) != 1 || ns.OwnerReferences[0].Name != "acme" {
			t.Errorf("namespace %s missing owner reference to tenant", nsName)
		}

		var rq corev1.ResourceQuota
		if err := cl.Get(ctx, types.NamespacedName{Name: managerName + "-quota", Namespace: nsName}, &rq); err != nil {
			t.Fatalf("resourcequota not created in %s: %v", nsName, err)
		}
		if rq.Spec.Hard.Pods().Value() != 50 {
			t.Errorf("quota pods = %v, want 50", rq.Spec.Hard.Pods())
		}

		var lr corev1.LimitRange
		if err := cl.Get(ctx, types.NamespacedName{Name: managerName + "-limits", Namespace: nsName}, &lr); err != nil {
			t.Fatalf("limitrange not created in %s: %v", nsName, err)
		}
		if len(lr.Spec.Limits) != 1 || lr.Spec.Limits[0].Type != corev1.LimitTypeContainer {
			t.Errorf("limitrange in %s malformed: %+v", nsName, lr.Spec.Limits)
		}

		var deny networkingv1.NetworkPolicy
		if err := cl.Get(ctx, types.NamespacedName{Name: managerName + "-default-deny-ingress", Namespace: nsName}, &deny); err != nil {
			t.Fatalf("default-deny netpol not created in %s: %v", nsName, err)
		}
		var allow networkingv1.NetworkPolicy
		if err := cl.Get(ctx, types.NamespacedName{Name: managerName + "-allow-same-tenant", Namespace: nsName}, &allow); err != nil {
			t.Fatalf("allow-same-tenant netpol not created in %s: %v", nsName, err)
		}
		if sel := allow.Spec.Ingress[0].From[0].NamespaceSelector; sel == nil || sel.MatchLabels[LabelTenant] != "acme" {
			t.Errorf("allow-same-tenant selector wrong in %s: %+v", nsName, allow.Spec.Ingress)
		}

		var rb rbacv1.RoleBinding
		if err := cl.Get(ctx, types.NamespacedName{Name: managerName + "-owners", Namespace: nsName}, &rb); err != nil {
			t.Fatalf("rolebinding not created in %s: %v", nsName, err)
		}
		if rb.RoleRef.Name != "admin" || rb.RoleRef.Kind != "ClusterRole" {
			t.Errorf("rolebinding roleRef wrong in %s: %+v", nsName, rb.RoleRef)
		}
		if len(rb.Subjects) != 1 || rb.Subjects[0].Name != "acme-admins" {
			t.Errorf("rolebinding subjects wrong in %s: %+v", nsName, rb.Subjects)
		}
	}

	var got platformv1beta1.Tenant
	if err := cl.Get(ctx, types.NamespacedName{Name: "acme"}, &got); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if got.Status.Phase != platformv1beta1.TenantPhaseActive {
		t.Errorf("phase = %q, want Active", got.Status.Phase)
	}
	if len(got.Status.Namespaces) != 2 {
		t.Errorf("status namespaces = %v, want 2", got.Status.Namespaces)
	}
	if c := meta.FindStatusCondition(got.Status.Conditions, conditionReady); c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition not True: %+v", got.Status.Conditions)
	}
	if got.Status.ObservedGeneration != 1 {
		t.Errorf("observedGeneration = %d, want 1", got.Status.ObservedGeneration)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	tenant := newTenant()
	r, cl := reconcileOnce(t, tenant)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "acme"}}

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("reconcile pass %d: %v", i, err)
		}
	}

	var nsList corev1.NamespaceList
	if err := cl.List(ctx, &nsList); err != nil {
		t.Fatalf("list namespaces: %v", err)
	}
	if len(nsList.Items) != 2 {
		t.Errorf("expected 2 namespaces after repeated reconciles, got %d", len(nsList.Items))
	}
}

func TestReconcilePrunesRemovedNamespaces(t *testing.T) {
	tenant := newTenant()
	tenant.Spec.Namespaces = []string{"acme-dev", "acme-prod"}
	r, cl := reconcileOnce(t, tenant)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "acme"}}

	// First reconcile provisions both namespaces and records them in status.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	for _, nsName := range []string{"acme-dev", "acme-prod"} {
		var ns corev1.Namespace
		if err := cl.Get(ctx, types.NamespacedName{Name: nsName}, &ns); err != nil {
			t.Fatalf("namespace %s not created: %v", nsName, err)
		}
	}

	// Drop acme-prod from the spec and reconcile again.
	var current platformv1beta1.Tenant
	if err := cl.Get(ctx, types.NamespacedName{Name: "acme"}, &current); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	current.Spec.Namespaces = []string{"acme-dev"}
	if err := cl.Update(ctx, &current); err != nil {
		t.Fatalf("update tenant spec: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	// The kept namespace still exists.
	var kept corev1.Namespace
	if err := cl.Get(ctx, types.NamespacedName{Name: "acme-dev"}, &kept); err != nil {
		t.Fatalf("kept namespace acme-dev missing: %v", err)
	}

	// The dropped namespace was deleted.
	var dropped corev1.Namespace
	err := cl.Get(ctx, types.NamespacedName{Name: "acme-prod"}, &dropped)
	if err == nil {
		t.Fatalf("dropped namespace acme-prod still exists, want deleted")
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected error getting dropped namespace: %v", err)
	}

	// Status reflects only the remaining namespace.
	var got platformv1beta1.Tenant
	if err := cl.Get(ctx, types.NamespacedName{Name: "acme"}, &got); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if len(got.Status.Namespaces) != 1 || got.Status.Namespaces[0] != "acme-dev" {
		t.Errorf("status namespaces = %v, want [acme-dev]", got.Status.Namespaces)
	}
}

func TestPruneSkipsUnownedNamespace(t *testing.T) {
	// A namespace recorded in status but not labeled for this tenant must not be
	// deleted by the prune, guarding against collateral damage.
	tenant := newTenant()
	tenant.Spec.Namespaces = []string{"acme-dev"}
	tenant.Status.Namespaces = []string{"acme-dev", "foreign"}

	foreign := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "foreign",
		Labels: map[string]string{LabelTenant: "someone-else"},
	}}

	r, cl := reconcileOnce(t, tenant, foreign)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "acme"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got corev1.Namespace
	if err := cl.Get(ctx, types.NamespacedName{Name: "foreign"}, &got); err != nil {
		t.Fatalf("unowned namespace was wrongly deleted or errored: %v", err)
	}
}

func TestReconcileRecordsMetrics(t *testing.T) {
	tenant := newTenant()
	tenant.Name = "metrics-tenant"
	tenant.Spec.Namespaces = []string{"mt-dev", "mt-prod", "mt-staging"}
	r, _ := reconcileOnce(t, tenant)
	ctx := context.Background()

	before := testutil.ToFloat64(reconcileTotal.WithLabelValues(resultSuccess))
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "metrics-tenant"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got := testutil.ToFloat64(reconcileTotal.WithLabelValues(resultSuccess)); got != before+1 {
		t.Errorf("success counter = %v, want %v", got, before+1)
	}
	if got := testutil.ToFloat64(tenantNamespaces.WithLabelValues("metrics-tenant")); got != 3 {
		t.Errorf("tenant namespaces gauge = %v, want 3", got)
	}
}

func TestReconcileMissingTenantIsNoOp(t *testing.T) {
	r, _ := reconcileOnce(t)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "ghost"}}); err != nil {
		t.Fatalf("reconcile of missing tenant should be a no-op, got: %v", err)
	}
}

func TestQuotaHardList(t *testing.T) {
	hard, err := quotaHardList(&platformv1beta1.ResourceQuotaSpec{CPU: "4", Memory: "8Gi", Pods: "20"})
	if err != nil {
		t.Fatalf("quotaHardList: %v", err)
	}
	if q := hard[corev1.ResourceLimitsCPU]; q.String() != "4" {
		t.Errorf("limits.cpu = %s, want 4", q.String())
	}
	if q := hard[corev1.ResourcePods]; q.String() != "20" {
		t.Errorf("pods = %s, want 20", q.String())
	}

	if _, err := quotaHardList(&platformv1beta1.ResourceQuotaSpec{CPU: "not-a-quantity"}); err == nil {
		t.Error("expected error for invalid quantity, got nil")
	}
}

func TestParseResourceList(t *testing.T) {
	rl, err := parseResourceList("250m", "256Mi")
	if err != nil {
		t.Fatalf("parseResourceList: %v", err)
	}
	if c := rl[corev1.ResourceCPU]; c.String() != "250m" {
		t.Errorf("cpu = %s, want 250m", c.String())
	}
	if _, err := parseResourceList("bad", ""); err == nil {
		t.Error("expected error for invalid cpu, got nil")
	}
}

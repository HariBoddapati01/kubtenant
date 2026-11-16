//go:build integration

package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	platformv1alpha1 "github.com/HariBoddapati01/kubtenant/api/v1alpha1"
)

// TestIntegration runs the real controller against a real kube-apiserver
// (envtest): CRD CEL validation, provisioning of all guardrails, watch-driven
// self-healing, and namespace pruning — the behaviors the fake client cannot
// exercise. (Owner-reference GC and namespace finalization are NOT active in
// envtest; those paths are covered by the kind e2e job.)
func TestIntegration(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("starting envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  newScheme(t),
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("creating manager: %v", err)
	}
	if err := (&TenantReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		t.Fatalf("setting up reconciler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Errorf("manager exited: %v", err)
		}
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache never synced")
	}
	c := mgr.GetClient()

	t.Run("CELValidation", func(t *testing.T) { testCELValidation(t, ctx, c) })
	t.Run("ProvisionsGuardrails", func(t *testing.T) { testProvision(t, ctx, c) })
	t.Run("SelfHeals", func(t *testing.T) { testSelfHeal(t, ctx, c) })
	t.Run("PrunesRemovedNamespaces", func(t *testing.T) { testPrune(t, ctx, c) })
}

func newScheme(t *testing.T) *runtime.Scheme {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := platformv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func eventually(t *testing.T, what string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if last = fn(); last == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %v", what, last)
}

func fullTenant(name string) *platformv1alpha1.Tenant {
	return &platformv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: platformv1alpha1.TenantSpec{
			DisplayName: "Integration Co",
			Namespaces:  []string{name + "-dev", name + "-prod"},
			Owners:      []platformv1alpha1.Subject{{Kind: "Group", Name: name + "-admins"}},
			OwnerRole:   "edit",
			PodSecurity: "restricted",
			ResourceQuota: &platformv1alpha1.ResourceQuotaSpec{
				CPU: "8", Memory: "16Gi", Pods: "40",
			},
			LimitRange: &platformv1alpha1.LimitRangeSpec{
				DefaultCPU: "500m", DefaultMemory: "512Mi",
				DefaultRequestCPU: "100m", DefaultRequestMemory: "128Mi",
				MaxCPU: "4", MaxMemory: "8Gi",
				MinPVCStorage: "1Gi", MaxPVCStorage: "100Gi",
			},
			NetworkIsolation: true,
		},
	}
}

func testCELValidation(t *testing.T, ctx context.Context, c client.Client) {
	cases := []struct {
		name   string
		mutate func(*platformv1alpha1.Tenant)
	}{
		{"BadQuotaQuantity", func(tn *platformv1alpha1.Tenant) { tn.Spec.ResourceQuota.CPU = "lots" }},
		{"KubePrefixedNamespace", func(tn *platformv1alpha1.Tenant) { tn.Spec.Namespaces = []string{"kube-sneaky"} }},
		{"DefaultNamespace", func(tn *platformv1alpha1.Tenant) { tn.Spec.Namespaces = []string{"default"} }},
		{"UppercaseNamespace", func(tn *platformv1alpha1.Tenant) { tn.Spec.Namespaces = []string{"NotADNSLabel"} }},
		{"UnboundOwnerRole", func(tn *platformv1alpha1.Tenant) { tn.Spec.OwnerRole = "cluster-admin" }},
		{"BadPVCQuantity", func(tn *platformv1alpha1.Tenant) { tn.Spec.LimitRange.MaxPVCStorage = "ten gigs" }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tn := fullTenant(fmt.Sprintf("cel-%d", i))
			tc.mutate(tn)
			err := c.Create(ctx, tn)
			if err == nil {
				_ = c.Delete(ctx, tn)
				t.Fatalf("expected the API server to reject the Tenant, but it was admitted")
			}
			if !apierrors.IsInvalid(err) {
				t.Fatalf("expected Invalid error, got: %v", err)
			}
		})
	}
}

func testProvision(t *testing.T, ctx context.Context, c client.Client) {
	tn := fullTenant("acme")
	if err := c.Create(ctx, tn); err != nil {
		t.Fatalf("creating tenant: %v", err)
	}

	eventually(t, "tenant Active", func() error {
		var got platformv1alpha1.Tenant
		if err := c.Get(ctx, types.NamespacedName{Name: "acme"}, &got); err != nil {
			return err
		}
		if got.Status.Phase != platformv1alpha1.TenantPhaseActive {
			return fmt.Errorf("phase=%s", got.Status.Phase)
		}
		return nil
	})

	for _, nsName := range []string{"acme-dev", "acme-prod"} {
		var ns corev1.Namespace
		if err := c.Get(ctx, types.NamespacedName{Name: nsName}, &ns); err != nil {
			t.Fatalf("namespace %s: %v", nsName, err)
		}
		if ns.Labels[LabelTenant] != "acme" {
			t.Errorf("%s: tenant label = %q", nsName, ns.Labels[LabelTenant])
		}
		if ns.Labels[labelPSSEnforce] != "restricted" {
			t.Errorf("%s: PSS enforce label = %q, want restricted", nsName, ns.Labels[labelPSSEnforce])
		}
	}

	var lr corev1.LimitRange
	if err := c.Get(ctx, types.NamespacedName{Name: "kubtenant-limits", Namespace: "acme-dev"}, &lr); err != nil {
		t.Fatalf("limitrange: %v", err)
	}
	if len(lr.Spec.Limits) != 2 {
		t.Fatalf("limitrange items = %d, want 2 (container + PVC)", len(lr.Spec.Limits))
	}
	if lr.Spec.Limits[0].Max.Cpu().String() != "4" {
		t.Errorf("container max cpu = %s, want 4", lr.Spec.Limits[0].Max.Cpu())
	}
	if lr.Spec.Limits[1].Type != corev1.LimitTypePersistentVolumeClaim {
		t.Errorf("second item type = %s, want PersistentVolumeClaim", lr.Spec.Limits[1].Type)
	}

	var deny networkingv1.NetworkPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: "kubtenant-default-deny-ingress", Namespace: "acme-dev"}, &deny); err != nil {
		t.Fatalf("deny policy: %v", err)
	}
	if len(deny.Spec.PolicyTypes) != 2 {
		t.Errorf("deny policyTypes = %v, want [Ingress Egress]", deny.Spec.PolicyTypes)
	}
	var dns networkingv1.NetworkPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: "kubtenant-allow-dns", Namespace: "acme-dev"}, &dns); err != nil {
		t.Fatalf("dns policy: %v", err)
	}

	var rb rbacv1.RoleBinding
	if err := c.Get(ctx, types.NamespacedName{Name: "kubtenant-owners", Namespace: "acme-prod"}, &rb); err != nil {
		t.Fatalf("owners rolebinding: %v", err)
	}
	if rb.RoleRef.Name != "edit" {
		t.Errorf("roleRef = %s, want edit", rb.RoleRef.Name)
	}
}

func testSelfHeal(t *testing.T, ctx context.Context, c client.Client) {
	var rq corev1.ResourceQuota
	if err := c.Get(ctx, types.NamespacedName{Name: "kubtenant-quota", Namespace: "acme-dev"}, &rq); err != nil {
		t.Fatalf("precondition quota: %v", err)
	}
	if err := c.Delete(ctx, &rq); err != nil {
		t.Fatalf("deleting quota: %v", err)
	}
	eventually(t, "quota recreated by watch-driven reconcile", func() error {
		var got corev1.ResourceQuota
		if err := c.Get(ctx, types.NamespacedName{Name: "kubtenant-quota", Namespace: "acme-dev"}, &got); err != nil {
			return err
		}
		if got.UID == rq.UID {
			return fmt.Errorf("still the old object")
		}
		return nil
	})
}

func testPrune(t *testing.T, ctx context.Context, c client.Client) {
	var tn platformv1alpha1.Tenant
	if err := c.Get(ctx, types.NamespacedName{Name: "acme"}, &tn); err != nil {
		t.Fatalf("getting tenant: %v", err)
	}
	tn.Spec.Namespaces = []string{"acme-dev"}
	if err := c.Update(ctx, &tn); err != nil {
		t.Fatalf("updating tenant: %v", err)
	}

	// envtest has no namespace finalization controller, so a pruned namespace
	// parks in Terminating; a set DeletionTimestamp proves the delete happened.
	eventually(t, "acme-prod deletion", func() error {
		var ns corev1.Namespace
		if err := c.Get(ctx, types.NamespacedName{Name: "acme-prod"}, &ns); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if ns.DeletionTimestamp.IsZero() {
			return fmt.Errorf("deletionTimestamp not set")
		}
		return nil
	})
}

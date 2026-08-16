// Package controller implements the Tenant reconciler, which provisions and
// continuously reconciles guardrailed namespaces for each Tenant custom resource.
package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/HariBoddapati01/kubtenant/api/v1alpha1"
)

const (
	// LabelTenant marks every managed object with the owning tenant's name and
	// is also the selector used by the allow-same-tenant NetworkPolicy.
	LabelTenant = "platform.hariboddapati.dev/tenant"
	// LabelManagedBy is the standard recommended label identifying the operator.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// managerName is the value used for LabelManagedBy and field ownership.
	managerName = "kubtenant"

	conditionReady = "Ready"
)

// TenantReconciler reconciles a Tenant object into a set of guardrailed namespaces.
type TenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=platform.hariboddapati.dev,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.hariboddapati.dev,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.hariboddapati.dev,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas;limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives the observed state of a Tenant toward its spec. It is
// idempotent: every managed object is created-or-updated, so repeated calls
// converge without side effects.
func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tenant platformv1alpha1.Tenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		// Not found: owned namespaces are garbage-collected via owner references.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	provisioned := make([]string, 0, len(tenant.Spec.Namespaces))
	for _, ns := range tenant.Spec.Namespaces {
		if err := r.reconcileNamespace(ctx, &tenant, ns); err != nil {
			return r.fail(ctx, &tenant, fmt.Errorf("namespace %q: %w", ns, err))
		}
		if tenant.Spec.ResourceQuota != nil {
			if err := r.reconcileResourceQuota(ctx, &tenant, ns); err != nil {
				return r.fail(ctx, &tenant, fmt.Errorf("resourcequota in %q: %w", ns, err))
			}
		}
		if tenant.Spec.LimitRange != nil {
			if err := r.reconcileLimitRange(ctx, &tenant, ns); err != nil {
				return r.fail(ctx, &tenant, fmt.Errorf("limitrange in %q: %w", ns, err))
			}
		}
		if tenant.Spec.NetworkIsolation {
			if err := r.reconcileNetworkPolicies(ctx, &tenant, ns); err != nil {
				return r.fail(ctx, &tenant, fmt.Errorf("networkpolicy in %q: %w", ns, err))
			}
		}
		if len(tenant.Spec.Owners) > 0 {
			if err := r.reconcileOwnerRBAC(ctx, &tenant, ns); err != nil {
				return r.fail(ctx, &tenant, fmt.Errorf("rbac in %q: %w", ns, err))
			}
		}
		provisioned = append(provisioned, ns)
	}

	// Prune namespaces that were previously provisioned for this tenant but are
	// no longer desired. This must run before succeed overwrites status, since
	// it compares the previous status.Namespaces against the desired set.
	if err := r.pruneNamespaces(ctx, &tenant, provisioned); err != nil {
		return r.fail(ctx, &tenant, fmt.Errorf("pruning stale namespaces: %w", err))
	}

	logger.Info("tenant reconciled", "tenant", tenant.Name, "namespaces", provisioned)
	return r.succeed(ctx, &tenant, provisioned)
}

// pruneNamespaces deletes namespaces that this operator previously provisioned
// for the tenant (recorded in status.Namespaces) but that are no longer present
// in the desired set. To avoid deleting namespaces the operator does not own, it
// verifies the tenant-ownership label before issuing the delete. Deletion of an
// already-absent namespace is treated as success.
func (r *TenantReconciler) pruneNamespaces(ctx context.Context, t *platformv1alpha1.Tenant, desired []string) error {
	desiredSet := make(map[string]struct{}, len(desired))
	for _, ns := range desired {
		desiredSet[ns] = struct{}{}
	}

	logger := log.FromContext(ctx)
	for _, prev := range t.Status.Namespaces {
		if _, ok := desiredSet[prev]; ok {
			continue
		}

		var ns corev1.Namespace
		if err := r.Get(ctx, client.ObjectKey{Name: prev}, &ns); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("namespace %q: %w", prev, err)
		}

		// Only delete namespaces this operator owns, identified by the tenant
		// label. A namespace re-created out from under us, or one that happens to
		// share the name, is left untouched.
		if ns.Labels[LabelTenant] != t.Name {
			logger.Info("skipping prune of unowned namespace", "tenant", t.Name, "namespace", prev)
			continue
		}

		logger.Info("pruning stale namespace", "tenant", t.Name, "namespace", prev)
		if err := client.IgnoreNotFound(r.Delete(ctx, &ns)); err != nil {
			return fmt.Errorf("deleting namespace %q: %w", prev, err)
		}
	}
	return nil
}

func (r *TenantReconciler) reconcileNamespace(ctx context.Context, t *platformv1alpha1.Tenant, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		applyLabels(&ns.ObjectMeta, t)
		if t.Spec.DisplayName != "" {
			if ns.Annotations == nil {
				ns.Annotations = map[string]string{}
			}
			ns.Annotations["platform.hariboddapati.dev/display-name"] = t.Spec.DisplayName
		}
		return controllerutil.SetControllerReference(t, ns, r.Scheme)
	})
	return err
}

func (r *TenantReconciler) reconcileResourceQuota(ctx context.Context, t *platformv1alpha1.Tenant, ns string) error {
	hard, err := quotaHardList(t.Spec.ResourceQuota)
	if err != nil {
		return err
	}
	rq := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: managerName + "-quota", Namespace: ns}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, rq, func() error {
		applyLabels(&rq.ObjectMeta, t)
		rq.Spec.Hard = hard
		return controllerutil.SetControllerReference(t, rq, r.Scheme)
	})
	return err
}

func (r *TenantReconciler) reconcileLimitRange(ctx context.Context, t *platformv1alpha1.Tenant, ns string) error {
	spec := t.Spec.LimitRange
	def, err := parseResourceList(spec.DefaultCPU, spec.DefaultMemory)
	if err != nil {
		return fmt.Errorf("default limits: %w", err)
	}
	req, err := parseResourceList(spec.DefaultRequestCPU, spec.DefaultRequestMemory)
	if err != nil {
		return fmt.Errorf("default requests: %w", err)
	}
	lr := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: managerName + "-limits", Namespace: ns}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, lr, func() error {
		applyLabels(&lr.ObjectMeta, t)
		lr.Spec.Limits = []corev1.LimitRangeItem{{
			Type:           corev1.LimitTypeContainer,
			Default:        def,
			DefaultRequest: req,
		}}
		return controllerutil.SetControllerReference(t, lr, r.Scheme)
	})
	return err
}

func (r *TenantReconciler) reconcileNetworkPolicies(ctx context.Context, t *platformv1alpha1.Tenant, ns string) error {
	// 1. Default-deny all ingress.
	deny := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: managerName + "-default-deny-ingress", Namespace: ns}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, deny, func() error {
		applyLabels(&deny.ObjectMeta, t)
		deny.Spec = networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		}
		return controllerutil.SetControllerReference(t, deny, r.Scheme)
	}); err != nil {
		return err
	}

	// 2. Allow ingress from any namespace belonging to the same tenant.
	allow := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: managerName + "-allow-same-tenant", Namespace: ns}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, allow, func() error {
		applyLabels(&allow.ObjectMeta, t)
		allow.Spec = networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{LabelTenant: t.Name},
					},
				}},
			}},
		}
		return controllerutil.SetControllerReference(t, allow, r.Scheme)
	})
	return err
}

func (r *TenantReconciler) reconcileOwnerRBAC(ctx context.Context, t *platformv1alpha1.Tenant, ns string) error {
	role := t.Spec.OwnerRole
	if role == "" {
		role = "admin"
	}
	subjects := make([]rbacv1.Subject, 0, len(t.Spec.Owners))
	for _, o := range t.Spec.Owners {
		subjects = append(subjects, rbacv1.Subject{
			APIGroup: rbacv1.GroupName,
			Kind:     o.Kind,
			Name:     o.Name,
		})
	}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: managerName + "-owners", Namespace: ns}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		applyLabels(&rb.ObjectMeta, t)
		rb.Subjects = subjects
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     role,
		}
		return controllerutil.SetControllerReference(t, rb, r.Scheme)
	})
	return err
}

// --- status helpers ---

func (r *TenantReconciler) succeed(ctx context.Context, t *platformv1alpha1.Tenant, namespaces []string) (ctrl.Result, error) {
	t.Status.ObservedGeneration = t.Generation
	t.Status.Phase = platformv1alpha1.TenantPhaseActive
	t.Status.Namespaces = namespaces
	meta.SetStatusCondition(&t.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Provisioned",
		Message:            fmt.Sprintf("%d namespace(s) reconciled", len(namespaces)),
		ObservedGeneration: t.Generation,
	})
	reconcileTotal.WithLabelValues(resultSuccess).Inc()
	tenantNamespaces.WithLabelValues(t.Name).Set(float64(len(namespaces)))
	return ctrl.Result{}, r.Status().Update(ctx, t)
}

func (r *TenantReconciler) fail(ctx context.Context, t *platformv1alpha1.Tenant, cause error) (ctrl.Result, error) {
	t.Status.ObservedGeneration = t.Generation
	t.Status.Phase = platformv1alpha1.TenantPhaseError
	meta.SetStatusCondition(&t.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             "ReconcileFailed",
		Message:            cause.Error(),
		ObservedGeneration: t.Generation,
	})
	reconcileTotal.WithLabelValues(resultError).Inc()
	// Best-effort status update; return the original cause so the item requeues.
	if err := r.Status().Update(ctx, t); err != nil && !apierrors.IsConflict(err) {
		log.FromContext(ctx).Error(err, "failed to update status after error")
	}
	return ctrl.Result{}, cause
}

// SetupWithManager registers the controller and the resources it owns.
func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.Tenant{}).
		Owns(&corev1.Namespace{}).
		Owns(&corev1.ResourceQuota{}).
		Owns(&corev1.LimitRange{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&rbacv1.RoleBinding{}).
		Named(managerName).
		Complete(r)
}

// --- pure helpers (unit-tested directly) ---

func applyLabels(m *metav1.ObjectMeta, t *platformv1alpha1.Tenant) {
	if m.Labels == nil {
		m.Labels = map[string]string{}
	}
	m.Labels[LabelTenant] = t.Name
	m.Labels[LabelManagedBy] = managerName
}

func quotaHardList(q *platformv1alpha1.ResourceQuotaSpec) (corev1.ResourceList, error) {
	hard := corev1.ResourceList{}
	add := func(name corev1.ResourceName, val string) error {
		if val == "" {
			return nil
		}
		qty, err := resource.ParseQuantity(val)
		if err != nil {
			return fmt.Errorf("invalid quantity %q for %s: %w", val, name, err)
		}
		hard[name] = qty
		return nil
	}
	if err := add(corev1.ResourceLimitsCPU, q.CPU); err != nil {
		return nil, err
	}
	if err := add(corev1.ResourceLimitsMemory, q.Memory); err != nil {
		return nil, err
	}
	if err := add(corev1.ResourcePods, q.Pods); err != nil {
		return nil, err
	}
	return hard, nil
}

func parseResourceList(cpu, mem string) (corev1.ResourceList, error) {
	out := corev1.ResourceList{}
	if cpu != "" {
		qty, err := resource.ParseQuantity(cpu)
		if err != nil {
			return nil, fmt.Errorf("invalid cpu %q: %w", cpu, err)
		}
		out[corev1.ResourceCPU] = qty
	}
	if mem != "" {
		qty, err := resource.ParseQuantity(mem)
		if err != nil {
			return nil, fmt.Errorf("invalid memory %q: %w", mem, err)
		}
		out[corev1.ResourceMemory] = qty
	}
	return out, nil
}

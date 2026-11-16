package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TenantSpec defines the desired state of a Tenant.
//
// A Tenant is a self-service unit of soft multi-tenancy. The operator reconciles
// a Tenant into a set of guardrailed namespaces: each namespace gets a
// ResourceQuota, a LimitRange with sane container defaults, a default-deny
// NetworkPolicy (plus an allow-same-tenant rule), and RBAC RoleBindings that
// grant the tenant owners the configured role.
type TenantSpec struct {
	// DisplayName is a human-friendly name for the tenant, surfaced in labels
	// and annotations on managed resources.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	DisplayName string `json:"displayName,omitempty"`

	// Namespaces is the list of namespaces to provision for this tenant. Every
	// namespace is labeled with the tenant name and owned by the Tenant object,
	// so deleting the Tenant garbage-collects them. Names must be DNS labels
	// and may not collide with system namespaces.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="self.all(ns, !ns.startsWith('kube-') && ns != 'default')",message="namespaces may not be 'default' or start with 'kube-'"
	Namespaces []string `json:"namespaces"`

	// Owners are the RBAC subjects (users and/or groups) that receive the
	// OwnerRole within every tenant namespace via a RoleBinding.
	// +optional
	Owners []Subject `json:"owners,omitempty"`

	// OwnerRole is the ClusterRole that owners are bound to inside each tenant
	// namespace. Restricted to the built-in aggregate roles, which are the only
	// ones the operator holds `bind` on — anything else would fail Kubernetes'
	// RBAC escalation check at reconcile time.
	// +optional
	// +kubebuilder:default=admin
	// +kubebuilder:validation:Enum=admin;edit;view
	OwnerRole string `json:"ownerRole,omitempty"`

	// ResourceQuota, when set, is applied to every tenant namespace to cap
	// aggregate resource consumption.
	// +optional
	ResourceQuota *ResourceQuotaSpec `json:"resourceQuota,omitempty"`

	// LimitRange, when set, installs default container requests/limits in every
	// tenant namespace so unbounded pods can't starve the cluster.
	// +optional
	LimitRange *LimitRangeSpec `json:"limitRange,omitempty"`

	// NetworkIsolation, when true, installs default-deny ingress AND egress
	// NetworkPolicies in each namespace, plus allow-same-tenant rules in both
	// directions and an egress allowance for DNS to kube-system.
	// +optional
	// +kubebuilder:default=true
	NetworkIsolation bool `json:"networkIsolation,omitempty"`

	// PodSecurity, when set, applies the given Pod Security Standards profile
	// to every tenant namespace via the pod-security.kubernetes.io/enforce
	// label. Unset means no PSS label is managed.
	// +optional
	// +kubebuilder:validation:Enum=privileged;baseline;restricted
	PodSecurity string `json:"podSecurity,omitempty"`

	// PriorityClass, when set, provisions a cluster-scoped PriorityClass named
	// "kubtenant-<tenant>" for this tenant's workloads, and can optionally
	// restrict tenant namespaces to it.
	// +optional
	PriorityClass *PriorityClassSpec `json:"priorityClass,omitempty"`
}

// PriorityClassSpec configures the tenant's PriorityClass.
type PriorityClassSpec struct {
	// Value is the scheduling priority assigned to the tenant's class. Kept
	// below 1e9 so tenants can never outrank system-critical classes.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=999999999
	Value int32 `json:"value"`
	// Description is surfaced on the created PriorityClass.
	// +optional
	Description string `json:"description,omitempty"`
	// Restrict, when true, installs a scoped ResourceQuota in every tenant
	// namespace that forbids pods from using any OTHER explicit PriorityClass.
	// Pods with no priorityClassName are unaffected.
	// +optional
	Restrict bool `json:"restrict,omitempty"`
}

// Subject identifies an RBAC subject to bind within tenant namespaces.
type Subject struct {
	// Kind is the subject kind: "User" or "Group".
	// +kubebuilder:validation:Enum=User;Group
	Kind string `json:"kind"`
	// Name is the subject name.
	Name string `json:"name"`
}

// ResourceQuotaSpec is a thin, validated projection of the fields we expose for
// per-namespace quotas.
type ResourceQuotaSpec struct {
	// CPU is the aggregate limits.cpu allowed across the namespace (e.g. "8").
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="must be a valid resource quantity"
	CPU string `json:"cpu,omitempty"`
	// Memory is the aggregate limits.memory allowed across the namespace (e.g. "16Gi").
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="must be a valid resource quantity"
	Memory string `json:"memory,omitempty"`
	// Pods is the maximum number of pods allowed in the namespace.
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="must be a valid resource quantity"
	Pods string `json:"pods,omitempty"`
}

// LimitRangeSpec configures default container-level requests and limits.
type LimitRangeSpec struct {
	// DefaultCPU is the default CPU limit applied to containers (e.g. "500m").
	// +optional
	// +kubebuilder:default="500m"
	DefaultCPU string `json:"defaultCPU,omitempty"`
	// DefaultMemory is the default memory limit applied to containers (e.g. "512Mi").
	// +optional
	// +kubebuilder:default="512Mi"
	DefaultMemory string `json:"defaultMemory,omitempty"`
	// DefaultRequestCPU is the default CPU request applied to containers (e.g. "100m").
	// +optional
	// +kubebuilder:default="100m"
	DefaultRequestCPU string `json:"defaultRequestCPU,omitempty"`
	// DefaultRequestMemory is the default memory request applied to containers (e.g. "128Mi").
	// +optional
	// +kubebuilder:default="128Mi"
	DefaultRequestMemory string `json:"defaultRequestMemory,omitempty"`
	// MaxCPU, when set, is the maximum CPU limit any single container may
	// request (e.g. "4").
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="must be a valid resource quantity"
	MaxCPU string `json:"maxCPU,omitempty"`
	// MaxMemory, when set, is the maximum memory limit any single container may
	// request (e.g. "8Gi").
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="must be a valid resource quantity"
	MaxMemory string `json:"maxMemory,omitempty"`
	// MaxPVCStorage, when set, caps the storage request of any single
	// PersistentVolumeClaim (e.g. "100Gi").
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="must be a valid resource quantity"
	MaxPVCStorage string `json:"maxPVCStorage,omitempty"`
	// MinPVCStorage, when set, is the minimum storage request of any single
	// PersistentVolumeClaim (e.g. "1Gi").
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="must be a valid resource quantity"
	MinPVCStorage string `json:"minPVCStorage,omitempty"`
}

// TenantPhase is a coarse, human-readable summary of reconciliation state.
type TenantPhase string

const (
	// TenantPhasePending indicates the tenant has not yet been fully provisioned.
	TenantPhasePending TenantPhase = "Pending"
	// TenantPhaseActive indicates all namespaces and guardrails are in place.
	TenantPhaseActive TenantPhase = "Active"
	// TenantPhaseError indicates the last reconcile failed.
	TenantPhaseError TenantPhase = "Error"
)

// TenantStatus defines the observed state of a Tenant.
type TenantStatus struct {
	// ObservedGeneration is the .metadata.generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase is a coarse summary of the tenant's state.
	// +optional
	Phase TenantPhase `json:"phase,omitempty"`

	// Namespaces lists the namespaces currently reconciled for this tenant.
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`

	// Conditions represent the latest available observations of the tenant's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Ensure the corev1 import is retained for downstream typed helpers.
var _ = corev1.ResourceCPU

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:deprecatedversion:warning="platform.hariboddapati.dev/v1alpha1 Tenant is deprecated; use v1beta1 (identical schema)"
// +kubebuilder:resource:scope=Cluster,shortName=tn
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Namespaces",type=string,JSONPath=`.status.namespaces`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Tenant is the Schema for the tenants API. It is cluster-scoped because it
// provisions and owns namespaces.
type Tenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantSpec   `json:"spec,omitempty"`
	Status TenantStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TenantList contains a list of Tenant.
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tenant{}, &TenantList{})
}

var _ runtime.Object = &Tenant{}

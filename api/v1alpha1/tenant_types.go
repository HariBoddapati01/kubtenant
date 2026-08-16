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
	// so deleting the Tenant garbage-collects them.
	// +kubebuilder:validation:MinItems=1
	Namespaces []string `json:"namespaces"`

	// Owners are the RBAC subjects (users and/or groups) that receive the
	// OwnerRole within every tenant namespace via a RoleBinding.
	// +optional
	Owners []Subject `json:"owners,omitempty"`

	// OwnerRole is the ClusterRole that owners are bound to inside each tenant
	// namespace. Defaults to the built-in "admin" ClusterRole, which is scoped
	// to the namespace by the RoleBinding.
	// +optional
	// +kubebuilder:default=admin
	OwnerRole string `json:"ownerRole,omitempty"`

	// ResourceQuota, when set, is applied to every tenant namespace to cap
	// aggregate resource consumption.
	// +optional
	ResourceQuota *ResourceQuotaSpec `json:"resourceQuota,omitempty"`

	// LimitRange, when set, installs default container requests/limits in every
	// tenant namespace so unbounded pods can't starve the cluster.
	// +optional
	LimitRange *LimitRangeSpec `json:"limitRange,omitempty"`

	// NetworkIsolation, when true, installs a default-deny ingress NetworkPolicy
	// plus an allow-same-tenant rule in each namespace, isolating tenant traffic.
	// +optional
	// +kubebuilder:default=true
	NetworkIsolation bool `json:"networkIsolation,omitempty"`
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
	CPU string `json:"cpu,omitempty"`
	// Memory is the aggregate limits.memory allowed across the namespace (e.g. "16Gi").
	// +optional
	Memory string `json:"memory,omitempty"`
	// Pods is the maximum number of pods allowed in the namespace.
	// +optional
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

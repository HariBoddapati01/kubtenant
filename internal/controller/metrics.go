package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// resultSuccess and resultError are the values of the "result" label on
	// the reconcile counter.
	resultSuccess = "success"
	resultError   = "error"
)

var (
	// reconcileTotal counts Tenant reconciliations, partitioned by outcome.
	reconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubtenant_reconcile_total",
			Help: "Total number of Tenant reconciliations, partitioned by result.",
		},
		[]string{"result"},
	)

	// tenantNamespaces reports the number of namespaces currently provisioned
	// for each Tenant.
	tenantNamespaces = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kubtenant_tenant_namespaces",
			Help: "Number of namespaces currently provisioned for a Tenant.",
		},
		[]string{"tenant"},
	)
)

func init() {
	metrics.Registry.MustRegister(reconcileTotal, tenantNamespaces)
}

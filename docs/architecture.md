# Architecture

## Overview

`kubtenant` follows the standard Kubernetes controller pattern built on
[controller-runtime](https://github.com/kubernetes-sigs/controller-runtime): a
single `Tenant` custom resource is the desired state, and a level-triggered
reconcile loop drives the cluster toward it.

```
┌────────────┐     watch      ┌─────────────────────┐
│  Tenant CR │───────────────▶│  TenantReconciler   │
└────────────┘                │  (controller-runtime)│
      ▲                       └──────────┬──────────┘
      │ ownerReferences                  │ create-or-update
      │                                  ▼
      │        ┌───────────────────────────────────────────────┐
      └────────┤ Namespace · ResourceQuota · LimitRange ·        │
               │ NetworkPolicy ×2 · RoleBinding                  │
               └───────────────────────────────────────────────┘
```

## Reconciliation model

The controller is **level-triggered**, not edge-triggered. It never reasons
about *what changed*; on every event it recomputes the complete desired state
for a `Tenant` and applies it. This is what makes the loop robust:

- **Idempotent.** Every managed object is applied with
  `controllerutil.CreateOrUpdate`, which GETs the object, runs a mutate function
  to set the desired fields, and PUTs only if something changed. Re-running the
  loop is a no-op once converged.
- **Self-healing.** The controller `Owns()` every resource type it manages, so
  the manager's cache watches them. A human editing a managed `ResourceQuota`
  enqueues the parent `Tenant` and the edit is reverted on the next loop.
- **Crash-safe.** State lives entirely in the API server. A controller restart
  simply re-lists and re-reconciles.

## Ownership & garbage collection

Every child object sets an `ownerReference` to the `Tenant` via
`controllerutil.SetControllerReference`. Because the `Tenant` is cluster-scoped,
it can legally own both cluster-scoped `Namespace` objects and the namespaced
objects inside them.

Consequences:

1. `kubectl delete tenant acme` triggers cascading deletion of every namespace
   (and, transitively, everything in them) via the garbage collector — **no
   finalizer required**.
2. Removing a namespace from `spec.namespaces` does *not* currently delete it
   (see "Known limitations"), but deleting the whole tenant always cleans up.

Avoiding finalizers is a deliberate simplicity choice: finalizers introduce
"stuck terminating" failure modes and require careful ordering. Owner references
push that responsibility to the built-in garbage collector.

## The guardrails

For each namespace listed in `spec.namespaces`, the controller ensures:

| Object | Name | Purpose |
| --- | --- | --- |
| `Namespace` | *(the name)* | Labeled `platform.hariboddapati.dev/tenant=<tenant>` so policies and selectors can target it. |
| `ResourceQuota` | `kubtenant-quota` | Aggregate ceiling on `limits.cpu`, `limits.memory`, `pods`. |
| `LimitRange` | `kubtenant-limits` | Default container requests/limits so unbounded pods can't break scheduling. |
| `NetworkPolicy` | `kubtenant-default-deny-ingress` | Denies all ingress by default. |
| `NetworkPolicy` | `kubtenant-allow-same-tenant` | Re-allows ingress from namespaces sharing the tenant label. |
| `RoleBinding` | `kubtenant-owners` | Binds `spec.owners` to `spec.ownerRole` (default `admin`), namespace-scoped. |

## Multi-tenancy threat model

`kubtenant` implements **soft multi-tenancy** — appropriate for tenants within a
single organization (teams, environments), *not* for mutually hostile tenants.
Explicitly:

- **Network.** Default-deny ingress means a tenant's pods are unreachable from
  other tenants unless they share the tenant label. This is namespace-level
  isolation, enforced by the cluster's CNI; it assumes a `NetworkPolicy`-capable
  CNI (Calico, Cilium, etc.).
- **Resources.** Quotas and limit ranges bound the blast radius of a noisy
  neighbor but do not provide hardware isolation. Node-level isolation
  (taints, separate node pools) is out of scope and composable on top.
- **RBAC.** Owners receive the built-in `admin` role *bound at namespace scope*,
  never `cluster-admin`. They cannot see or touch other tenants' namespaces.
- **Not covered.** Kernel/container-escape isolation (use gVisor/Kata),
  API-server priority & fairness, and admission policy (compose with Kyverno or
  Gatekeeper) are intentionally left to dedicated tools.

## Status & observability

`Tenant.status` carries:

- `phase`: `Pending` → `Active` → `Error`.
- `namespaces`: the namespaces reconciled on the last successful pass.
- `observedGeneration`: the `.metadata.generation` the status reflects, so
  clients can tell whether the controller has caught up.
- `conditions`: a standard `Ready` condition with a machine-readable `reason`
  and human-readable `message`.

## Known limitations / future work

- Namespaces removed from `spec.namespaces` are not pruned (only full-tenant
  deletion cascades). A reconcile-time diff against previously-owned namespaces
  is the planned fix.
- No admission webhook yet; spec validation relies on CRD OpenAPI schema
  (enums, min-items, defaults) generated from kubebuilder markers.
- Integration tests currently use the fake client; an `envtest` suite against a
  real API server is on the roadmap.

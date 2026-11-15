package controller

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

// The controller grants admin/edit/view to tenant owners via RoleBindings.
// Kubernetes' RBAC escalation check rejects those grants unless the manager
// role itself holds `bind` on the referenced ClusterRoles, so losing this
// rule (e.g. a marker edit followed by `make manifests`) silently breaks
// owner RBAC on real clusters. Pin it here.
func TestManagerRoleGrantsBindOnOwnerClusterRoles(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	if err != nil {
		t.Fatalf("reading generated role.yaml: %v", err)
	}

	var role rbacv1.ClusterRole
	if err := yaml.Unmarshal(raw, &role); err != nil {
		t.Fatalf("unmarshalling role.yaml: %v", err)
	}

	for _, rule := range role.Rules {
		if !slices.Contains(rule.APIGroups, rbacv1.GroupName) ||
			!slices.Contains(rule.Resources, "clusterroles") ||
			!slices.Contains(rule.Verbs, "bind") {
			continue
		}
		for _, want := range []string{"admin", "edit", "view"} {
			if !slices.Contains(rule.ResourceNames, want) {
				t.Errorf("bind rule is missing resourceName %q (got %v)", want, rule.ResourceNames)
			}
		}
		return
	}
	t.Fatalf("role.yaml has no rule granting bind on clusterroles; owner RoleBindings will fail the RBAC escalation check")
}

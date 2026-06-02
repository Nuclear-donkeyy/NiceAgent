package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"niceagent/control-plane/internal/app"
)

func TestActionPolicyAuthorizesConfiguredActions(t *testing.T) {
	policy := defaultActionPolicy()

	viewer := app.ActorContext{UserID: "viewer", ProjectID: app.DemoProjectID, Roles: []string{"viewer"}}
	member := app.ActorContext{UserID: "member", ProjectID: app.DemoProjectID, Roles: []string{"member"}}
	admin := app.ActorContext{UserID: "admin", ProjectID: app.DemoProjectID, Roles: []string{"admin"}}

	if decision := policy.Authorize(viewer, "chat.create", actionRequirementWrite); decision.Allowed {
		t.Fatalf("viewer chat.create allowed = true, want false")
	}
	if decision := policy.Authorize(member, "chat.create", actionRequirementWrite); !decision.Allowed {
		t.Fatalf("member chat.create allowed = false, want true: %#v", decision)
	}
	if decision := policy.Authorize(member, "project.quota.update", actionRequirementWrite); decision.Allowed {
		t.Fatalf("member project.quota.update allowed = true, want false")
	}
	if decision := policy.Authorize(admin, "project.quota.update", actionRequirementWrite); !decision.Allowed {
		t.Fatalf("admin project.quota.update allowed = false, want true: %#v", decision)
	}
	if decision := policy.Authorize(member, "organization.member.remove", actionRequirementWrite); decision.Allowed {
		t.Fatalf("member organization.member.remove allowed = true, want false")
	}
	if decision := policy.Authorize(admin, "organization.member.remove", actionRequirementWrite); !decision.Allowed {
		t.Fatalf("admin organization.member.remove allowed = false, want true: %#v", decision)
	}
}

func TestActionPolicyUsesFallbackForUnknownActions(t *testing.T) {
	policy := defaultActionPolicy()
	member := app.ActorContext{UserID: "member", ProjectID: app.DemoProjectID, Roles: []string{"member"}}

	if decision := policy.Authorize(member, "future.write.action", actionRequirementWrite); !decision.Allowed {
		t.Fatalf("fallback write decision = %#v, want allow", decision)
	}
	if decision := policy.Authorize(member, "future.admin.action", actionRequirementProjectAdmin); decision.Allowed {
		t.Fatalf("fallback admin decision allowed = true, want false")
	}
}

func TestLoadActionPolicyFileOverridesDefaultRequirements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action-policy.json")
	if err := os.WriteFile(path, []byte(`{
		"actions": {
			"chat.create": "project_admin",
			"future.read_sensitive": "organization_admin"
		}
	}`), 0o600); err != nil {
		t.Fatalf("write policy file: %v", err)
	}

	policy, err := loadActionPolicyFile(path)
	if err != nil {
		t.Fatalf("load action policy file: %v", err)
	}
	member := app.ActorContext{UserID: "member", ProjectID: app.DemoProjectID, Roles: []string{"member"}}
	admin := app.ActorContext{UserID: "admin", ProjectID: app.DemoProjectID, Roles: []string{"admin"}}

	if decision := policy.Authorize(member, "chat.create", actionRequirementWrite); decision.Allowed {
		t.Fatalf("member chat.create allowed = true after override, want false")
	}
	if decision := policy.Authorize(admin, "chat.create", actionRequirementWrite); !decision.Allowed {
		t.Fatalf("admin chat.create allowed = false after override, want true: %#v", decision)
	}
	if decision := policy.Authorize(member, "message.create", actionRequirementWrite); !decision.Allowed {
		t.Fatalf("default message.create decision = %#v, want allow", decision)
	}
	if decision := policy.Authorize(member, "future.read_sensitive", actionRequirementWrite); decision.Allowed {
		t.Fatalf("member future.read_sensitive allowed = true after custom policy, want false")
	}
}

func TestLoadActionPolicyFileRejectsInvalidRequirement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action-policy.json")
	if err := os.WriteFile(path, []byte(`{"actions":{"chat.create":"super_admin"}}`), 0o600); err != nil {
		t.Fatalf("write policy file: %v", err)
	}

	if err := ValidateActionPolicyFile(path); err == nil {
		t.Fatal("expected invalid policy requirement to fail validation")
	}
}

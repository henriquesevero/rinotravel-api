package trip

import (
	"errors"
	"testing"

	"rinotravel-api/internal/apperror"
)

var allRoles = []Role{RoleOwner, RoleAdmin, RoleMember, RoleViewer}

func isForbidden(err error) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr) && appErr.Kind == apperror.KindForbidden
}

func TestCan_PermissionMatrix(t *testing.T) {
	tests := []struct {
		action Action
		name   string
		want   map[Role]bool
	}{
		{ActionRead, "read", map[Role]bool{RoleOwner: true, RoleAdmin: true, RoleMember: true, RoleViewer: true}},
		{ActionWriteContent, "write content", map[Role]bool{RoleOwner: true, RoleAdmin: true, RoleMember: true, RoleViewer: false}},
		{ActionUpdateTrip, "update trip", map[Role]bool{RoleOwner: true, RoleAdmin: true, RoleMember: false, RoleViewer: false}},
		{ActionManageMembers, "manage members", map[Role]bool{RoleOwner: true, RoleAdmin: true, RoleMember: false, RoleViewer: false}},
		{ActionDeleteTrip, "delete trip", map[Role]bool{RoleOwner: true, RoleAdmin: false, RoleMember: false, RoleViewer: false}},
		{ActionTransferOwnership, "transfer ownership", map[Role]bool{RoleOwner: true, RoleAdmin: false, RoleMember: false, RoleViewer: false}},
	}

	for _, tt := range tests {
		for _, role := range allRoles {
			if got := Can(role, tt.action); got != tt.want[role] {
				t.Errorf("Can(%s, %s) = %v, want %v", role, tt.name, got, tt.want[role])
			}
		}
	}
}

func TestCan_DeniesUnknownRolesAndActions(t *testing.T) {
	if Can("", ActionRead) || Can("SUPERUSER", ActionRead) {
		t.Error("an unknown role was allowed to read")
	}
	if Can(RoleOwner, Action(0)) || Can(RoleOwner, Action(999)) {
		t.Error("an unknown action was allowed")
	}
}

func TestCheckAddMember(t *testing.T) {
	tests := []struct {
		actor, newRole Role
		allowed        bool
	}{
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleMember, true},
		{RoleOwner, RoleViewer, true},
		{RoleAdmin, RoleAdmin, false},
		{RoleAdmin, RoleMember, true},
		{RoleAdmin, RoleViewer, true},
		{RoleMember, RoleViewer, false},
		{RoleMember, RoleMember, false},
		{RoleViewer, RoleViewer, false},
	}

	for _, tt := range tests {
		err := checkAddMember(tt.actor, tt.newRole)
		if tt.allowed != (err == nil) || (err != nil && !isForbidden(err)) {
			t.Errorf("checkAddMember(%s adds %s) = %v, allowed %v", tt.actor, tt.newRole, err, tt.allowed)
		}
	}
}

func TestCheckChangeRole(t *testing.T) {
	tests := []struct {
		actor, target, newRole Role
		allowed                bool
	}{
		{RoleOwner, RoleMember, RoleAdmin, true},
		{RoleOwner, RoleAdmin, RoleMember, true},
		{RoleOwner, RoleViewer, RoleMember, true},
		{RoleAdmin, RoleMember, RoleViewer, true},
		{RoleAdmin, RoleViewer, RoleMember, true},
		{RoleAdmin, RoleMember, RoleAdmin, false},
		{RoleAdmin, RoleAdmin, RoleMember, false},
		{RoleAdmin, RoleOwner, RoleMember, false},
		{RoleMember, RoleViewer, RoleMember, false},
		{RoleViewer, RoleMember, RoleViewer, false},
	}

	for _, tt := range tests {
		err := checkChangeRole(tt.actor, tt.target, tt.newRole)
		if tt.allowed != (err == nil) || (err != nil && !isForbidden(err)) {
			t.Errorf("checkChangeRole(%s changes %s to %s) = %v, allowed %v", tt.actor, tt.target, tt.newRole, err, tt.allowed)
		}
	}
}

func TestCheckRemoveMember(t *testing.T) {
	tests := []struct {
		actor, target Role
		self          bool
		allowed       bool
	}{
		{RoleOwner, RoleAdmin, false, true},
		{RoleOwner, RoleMember, false, true},
		{RoleAdmin, RoleMember, false, true},
		{RoleAdmin, RoleViewer, false, true},
		{RoleAdmin, RoleAdmin, false, false},
		{RoleAdmin, RoleOwner, false, false},
		{RoleMember, RoleViewer, false, false},
		{RoleViewer, RoleMember, false, false},
		{RoleMember, RoleMember, true, true},
		{RoleViewer, RoleViewer, true, true},
		{RoleAdmin, RoleAdmin, true, true},
	}

	for _, tt := range tests {
		err := checkRemoveMember(tt.actor, tt.target, tt.self)
		if tt.allowed != (err == nil) || (err != nil && !isForbidden(err)) {
			t.Errorf("checkRemoveMember(%s removes %s, self=%v) = %v, allowed %v", tt.actor, tt.target, tt.self, err, tt.allowed)
		}
	}
}

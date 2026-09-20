package trip

import (
	"errors"
	"reflect"
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

func TestCapabilitiesOf(t *testing.T) {
	tests := []struct {
		role Role
		want Capabilities
	}{
		{RoleOwner, Capabilities{UpdateTrip: true, ManageMembers: true, WriteContent: true, DeleteTrip: true, TransferOwnership: true, Leave: false, AddableRoles: []Role{RoleAdmin, RoleMember, RoleViewer}}},
		{RoleAdmin, Capabilities{UpdateTrip: true, ManageMembers: true, WriteContent: true, Leave: true, AddableRoles: []Role{RoleMember, RoleViewer}}},
		{RoleMember, Capabilities{WriteContent: true, Leave: true, AddableRoles: []Role{}}},
		{RoleViewer, Capabilities{Leave: true, AddableRoles: []Role{}}},
		{Role("GUEST"), Capabilities{AddableRoles: []Role{}}},
	}

	for _, tt := range tests {
		if got := CapabilitiesOf(tt.role); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("CapabilitiesOf(%s) = %+v, want %+v", tt.role, got, tt.want)
		}
	}
}

func TestCapabilitiesAgreeWithCan(t *testing.T) {
	for _, role := range allRoles {
		c := CapabilitiesOf(role)
		for _, addable := range c.AddableRoles {
			if err := checkAddMember(role, addable); err != nil {
				t.Errorf("CapabilitiesOf(%s) offers %s but the policy forbids adding it", role, addable)
			}
		}
		if c.UpdateTrip != Can(role, ActionUpdateTrip) || c.ManageMembers != Can(role, ActionManageMembers) ||
			c.WriteContent != Can(role, ActionWriteContent) || c.DeleteTrip != Can(role, ActionDeleteTrip) ||
			c.TransferOwnership != Can(role, ActionTransferOwnership) {
			t.Errorf("CapabilitiesOf(%s) disagrees with Can: %+v", role, c)
		}
	}
}

func TestMemberCapabilitiesOf(t *testing.T) {
	tests := []struct {
		name        string
		actor       Role
		target      Role
		isSelf      bool
		wantAssign  []Role
		wantRemoval bool
	}{
		{"owner over admin", RoleOwner, RoleAdmin, false, []Role{RoleMember, RoleViewer}, true},
		{"owner over member", RoleOwner, RoleMember, false, []Role{RoleAdmin, RoleViewer}, true},
		{"owner over viewer", RoleOwner, RoleViewer, false, []Role{RoleAdmin, RoleMember}, true},
		{"admin over member", RoleAdmin, RoleMember, false, []Role{RoleViewer}, true},
		{"admin over viewer", RoleAdmin, RoleViewer, false, []Role{RoleMember}, true},
		{"admin over admin", RoleAdmin, RoleAdmin, false, []Role{}, false},
		{"admin over owner", RoleAdmin, RoleOwner, false, []Role{}, false},
		{"owner over self", RoleOwner, RoleOwner, true, []Role{}, false},
		{"member over viewer", RoleMember, RoleViewer, false, []Role{}, false},
		{"viewer over member", RoleViewer, RoleMember, false, []Role{}, false},
		{"member leaving", RoleMember, RoleMember, true, []Role{}, true},
		{"viewer leaving", RoleViewer, RoleViewer, true, []Role{}, true},
		{"admin leaving", RoleAdmin, RoleAdmin, true, []Role{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MemberCapabilitiesOf(tt.actor, tt.target, tt.isSelf)

			if got.AssignableRoles == nil {
				t.Error("AssignableRoles is nil, want an empty slice so it serializes as []")
			}
			if len(got.AssignableRoles) != len(tt.wantAssign) {
				t.Fatalf("AssignableRoles = %v, want %v", got.AssignableRoles, tt.wantAssign)
			}
			for i, role := range tt.wantAssign {
				if got.AssignableRoles[i] != role {
					t.Errorf("AssignableRoles = %v, want %v", got.AssignableRoles, tt.wantAssign)
				}
			}
			if got.CanRemove != tt.wantRemoval {
				t.Errorf("CanRemove = %v, want %v", got.CanRemove, tt.wantRemoval)
			}
		})
	}
}

func TestMemberCapabilitiesNeverOfferOwnershipAndMatchTheEnforcedRules(t *testing.T) {
	for _, actor := range allRoles {
		for _, target := range allRoles {
			caps := MemberCapabilitiesOf(actor, target, false)
			for _, role := range caps.AssignableRoles {
				if role == RoleOwner {
					t.Errorf("%s may assign OWNER to %s", actor, target)
				}
				if err := checkChangeRole(actor, target, role); err != nil {
					t.Errorf("%s is offered %s for %s but the policy forbids it", actor, role, target)
				}
			}
		}
	}
}

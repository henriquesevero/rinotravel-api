package trip

import "rinotravel-api/internal/apperror"

type Action int

const (
	ActionRead Action = iota + 1
	ActionWriteContent
	ActionUpdateTrip
	ActionManageMembers
	ActionDeleteTrip
	ActionTransferOwnership
)

func Can(role Role, action Action) bool {
	switch action {
	case ActionRead:
		return role == RoleOwner || role == RoleAdmin || role == RoleMember || role == RoleViewer
	case ActionWriteContent:
		return role == RoleOwner || role == RoleAdmin || role == RoleMember
	case ActionUpdateTrip, ActionManageMembers:
		return role == RoleOwner || role == RoleAdmin
	case ActionDeleteTrip, ActionTransferOwnership:
		return role == RoleOwner
	}
	return false
}

func errTripNotFound() *apperror.Error {
	return apperror.NotFound("trip_not_found", "Trip not found.")
}

func errForbidden() *apperror.Error {
	return apperror.Forbidden("forbidden", "You do not have permission to perform this action.")
}

func checkAction(role Role, action Action) error {
	if !Can(role, action) {
		return errForbidden()
	}
	return nil
}

func checkAddMember(actor, newRole Role) error {
	if err := checkAction(actor, ActionManageMembers); err != nil {
		return err
	}
	if newRole == RoleAdmin && actor != RoleOwner {
		return errForbidden()
	}
	return nil
}

func checkChangeRole(actor, target, newRole Role) error {
	if err := checkAction(actor, ActionManageMembers); err != nil {
		return err
	}
	touchesPrivileged := target == RoleOwner || target == RoleAdmin || newRole == RoleAdmin
	if touchesPrivileged && actor != RoleOwner {
		return errForbidden()
	}
	return nil
}

func checkRemoveMember(actor, target Role, isSelf bool) error {
	if isSelf {
		return nil
	}
	if err := checkAction(actor, ActionManageMembers); err != nil {
		return err
	}
	if (target == RoleOwner || target == RoleAdmin) && actor != RoleOwner {
		return errForbidden()
	}
	return nil
}

type Capabilities struct {
	UpdateTrip        bool
	ManageMembers     bool
	WriteContent      bool
	DeleteTrip        bool
	TransferOwnership bool
	Leave             bool
	AddableRoles      []Role
}

func CapabilitiesOf(role Role) Capabilities {
	addable := []Role{}
	for _, candidate := range []Role{RoleAdmin, RoleMember, RoleViewer} {
		if checkAddMember(role, candidate) == nil {
			addable = append(addable, candidate)
		}
	}
	return Capabilities{
		UpdateTrip:        Can(role, ActionUpdateTrip),
		ManageMembers:     Can(role, ActionManageMembers),
		WriteContent:      Can(role, ActionWriteContent),
		DeleteTrip:        Can(role, ActionDeleteTrip),
		TransferOwnership: Can(role, ActionTransferOwnership),
		Leave:             Can(role, ActionRead) && role != RoleOwner,
		AddableRoles:      addable,
	}
}

type MemberCapabilities struct {
	AssignableRoles []Role
	CanRemove       bool
}

func MemberCapabilitiesOf(actor, target Role, isSelf bool) MemberCapabilities {
	caps := MemberCapabilities{AssignableRoles: []Role{}}
	if target == RoleOwner {
		return caps
	}
	for _, role := range []Role{RoleAdmin, RoleMember, RoleViewer} {
		if role != target && checkChangeRole(actor, target, role) == nil {
			caps.AssignableRoles = append(caps.AssignableRoles, role)
		}
	}
	caps.CanRemove = checkRemoveMember(actor, target, isSelf) == nil
	return caps
}

package trip

import (
	"context"
	"fmt"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/user"
)

type ChangeMemberRole struct {
	trips Repository
	users Users
}

type ChangeMemberRoleInput struct {
	TripID   ID
	ActorID  user.ID
	TargetID user.ID
	Role     string
}

func NewChangeMemberRole(trips Repository, users Users) *ChangeMemberRole {
	return &ChangeMemberRole{trips: trips, users: users}
}

func (c *ChangeMemberRole) Execute(ctx context.Context, in ChangeMemberRoleInput) (MemberView, error) {
	t, actorRole, err := loadAsMember(ctx, c.trips, in.TripID, in.ActorID)
	if err != nil {
		return MemberView{}, err
	}
	if err := checkAction(actorRole, ActionManageMembers); err != nil {
		return MemberView{}, err
	}

	newRole, err := ParseRole(in.Role)
	if err != nil {
		return MemberView{}, apperror.Validation(apperror.FieldError{Field: "role", Message: err.Error()})
	}
	targetRole, ok := t.RoleOf(in.TargetID)
	if !ok {
		return MemberView{}, apperror.NotFound("member_not_found", "The user is not a member of this trip.")
	}
	if err := checkChangeRole(actorRole, targetRole, newRole); err != nil {
		return MemberView{}, err
	}

	previousVersion := t.Version
	if err := t.ChangeMemberRole(in.TargetID, newRole, clock.Now()); err != nil {
		return MemberView{}, err
	}
	if t.Version != previousVersion {
		if err := save(ctx, c.trips, t); err != nil {
			return MemberView{}, err
		}
	}

	users, err := c.users.FindByIDs(ctx, []user.ID{in.TargetID})
	if err != nil {
		return MemberView{}, fmt.Errorf("find user: %w", err)
	}
	target := user.User{ID: in.TargetID}
	if len(users) > 0 {
		target = users[0]
	}
	return memberViewOf(t, target, in.ActorID, actorRole), nil
}

package trip

import (
	"context"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/user"
)

type RemoveMember struct {
	trips Repository
}

type RemoveMemberInput struct {
	TripID   ID
	ActorID  user.ID
	TargetID user.ID
}

func NewRemoveMember(trips Repository) *RemoveMember {
	return &RemoveMember{trips: trips}
}

func (r *RemoveMember) Execute(ctx context.Context, in RemoveMemberInput) error {
	t, actorRole, err := loadAsMember(ctx, r.trips, in.TripID, in.ActorID)
	if err != nil {
		return err
	}

	isSelf := in.TargetID == in.ActorID
	targetRole, ok := t.RoleOf(in.TargetID)
	if !ok {
		if err := checkAction(actorRole, ActionManageMembers); err != nil {
			return err
		}
		return apperror.NotFound("member_not_found", "The user is not a member of this trip.")
	}
	if err := checkRemoveMember(actorRole, targetRole, isSelf); err != nil {
		return err
	}

	if err := t.RemoveMember(in.TargetID, clock.Now()); err != nil {
		return err
	}
	return save(ctx, r.trips, t)
}

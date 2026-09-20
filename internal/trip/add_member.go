package trip

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/user"
)

type AddMember struct {
	trips Repository
	users Users
}

type AddMemberInput struct {
	TripID  ID
	ActorID user.ID
	Email   string
	Role    string
}

func NewAddMember(trips Repository, users Users) *AddMember {
	return &AddMember{trips: trips, users: users}
}

func (a *AddMember) Execute(ctx context.Context, in AddMemberInput) (MemberView, error) {
	t, actorRole, err := loadAsMember(ctx, a.trips, in.TripID, in.ActorID)
	if err != nil {
		return MemberView{}, err
	}

	role, err := ParseRole(in.Role)
	if err != nil {
		return MemberView{}, apperror.Validation(apperror.FieldError{Field: "role", Message: err.Error()})
	}
	if err := checkAddMember(actorRole, role); err != nil {
		return MemberView{}, err
	}

	target, err := a.users.FindByEmail(ctx, strings.ToLower(strings.TrimSpace(in.Email)))
	if errors.Is(err, user.ErrNotFound) {
		return MemberView{}, apperror.NotFound("user_not_found", "No user is registered with this email.")
	}
	if err != nil {
		return MemberView{}, fmt.Errorf("find user: %w", err)
	}

	if err := t.AddMember(target.ID, role, clock.Now()); err != nil {
		return MemberView{}, err
	}
	if err := save(ctx, a.trips, t); err != nil {
		return MemberView{}, err
	}
	return memberViewOf(t, target, in.ActorID, actorRole), nil
}

func memberViewOf(t Trip, u user.User, actorID user.ID, actorRole Role) MemberView {
	for _, m := range t.Members {
		if m.UserID == u.ID {
			return MemberView{Member: m, User: u, Capabilities: MemberCapabilitiesOf(actorRole, m.Role, m.UserID == actorID)}
		}
	}
	return MemberView{User: u}
}

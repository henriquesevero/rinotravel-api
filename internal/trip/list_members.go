package trip

import (
	"context"
	"fmt"
	"slices"

	"rinotravel-api/internal/user"
)

type ListMembers struct {
	trips Repository
	users Users
}

func NewListMembers(trips Repository, users Users) *ListMembers {
	return &ListMembers{trips: trips, users: users}
}

func (l *ListMembers) Execute(ctx context.Context, id ID, actor user.ID) ([]MemberView, error) {
	t, role, err := loadAsMember(ctx, l.trips, id, actor)
	if err != nil {
		return nil, err
	}
	if err := checkAction(role, ActionRead); err != nil {
		return nil, err
	}

	userIDs := make([]user.ID, 0, len(t.Members))
	for _, m := range t.Members {
		userIDs = append(userIDs, m.UserID)
	}
	users, err := l.users.FindByIDs(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("find users: %w", err)
	}
	byID := make(map[user.ID]user.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}

	views := make([]MemberView, 0, len(t.Members))
	for _, m := range t.Members {
		views = append(views, MemberView{Member: m, User: byID[m.UserID]})
	}
	slices.SortStableFunc(views, func(a, b MemberView) int {
		if rank := roleRank(a.Member.Role) - roleRank(b.Member.Role); rank != 0 {
			return rank
		}
		return a.Member.CreatedAt.Compare(b.Member.CreatedAt)
	})
	return views, nil
}

func roleRank(r Role) int {
	switch r {
	case RoleOwner:
		return 0
	case RoleAdmin:
		return 1
	case RoleMember:
		return 2
	default:
		return 3
	}
}

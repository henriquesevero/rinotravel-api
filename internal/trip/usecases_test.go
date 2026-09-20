package trip_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/auth/authtest"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/trip/triptest"
	"rinotravel-api/internal/user"
)

type env struct {
	repo     *triptest.Repository
	users    *authtest.Users
	create   *trip.CreateTrip
	get      *trip.GetTrip
	list     *trip.ListTrips
	update   *trip.UpdateTrip
	delete   *trip.DeleteTrip
	add      *trip.AddMember
	members  *trip.ListMembers
	change   *trip.ChangeMemberRole
	remove   *trip.RemoveMember
	transfer *trip.TransferOwnership
}

func newEnv() env {
	repo, users := triptest.NewRepository(), authtest.NewUsers()
	return env{
		repo:     repo,
		users:    users,
		create:   trip.NewCreateTrip(repo),
		get:      trip.NewGetTrip(repo),
		list:     trip.NewListTrips(repo),
		update:   trip.NewUpdateTrip(repo),
		delete:   trip.NewDeleteTrip(repo),
		add:      trip.NewAddMember(repo, users),
		members:  trip.NewListMembers(repo, users),
		change:   trip.NewChangeMemberRole(repo, users),
		remove:   trip.NewRemoveMember(repo),
		transfer: trip.NewTransferOwnership(repo),
	}
}

func (e env) newUser(t *testing.T, name string) user.User {
	t.Helper()
	u := user.User{ID: user.ID(ids.New()), Email: name + "@example.com", Name: strings.ToUpper(name[:1]) + name[1:], CreatedAt: now}
	if err := e.users.Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func (e env) newTrip(t *testing.T, owner user.User) trip.Trip {
	t.Helper()
	view, err := e.create.Execute(context.Background(), trip.CreateTripInput{ActorID: owner.ID, Details: validInput()})
	if err != nil {
		t.Fatal(err)
	}
	return view.Trip
}

func (e env) seedMember(t *testing.T, id trip.ID, member user.User, role trip.Role) {
	t.Helper()
	stored, _ := e.repo.Stored(id)
	if err := stored.AddMember(member.ID, role, now); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
}

func (e env) roleOf(id trip.ID, u user.User) (trip.Role, bool) {
	stored, _ := e.repo.Stored(id)
	return stored.RoleOf(u.ID)
}

func (e env) tripWithAllRoles(t *testing.T) (id trip.ID, owner, admin, member, viewer, outsider user.User) {
	t.Helper()
	owner, admin, member, viewer, outsider = e.newUser(t, "ana"), e.newUser(t, "bia"), e.newUser(t, "caio"), e.newUser(t, "davi"), e.newUser(t, "eli")
	id = e.newTrip(t, owner).ID
	e.seedMember(t, id, admin, trip.RoleAdmin)
	e.seedMember(t, id, member, trip.RoleMember)
	e.seedMember(t, id, viewer, trip.RoleViewer)
	return id, owner, admin, member, viewer, outsider
}

func TestCreateTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("makes the actor the owner and persists the trip", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")

		view, err := e.create.Execute(ctx, trip.CreateTripInput{ActorID: ana.ID, Details: validInput()})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if view.Role != trip.RoleOwner || view.Trip.OwnerID() != ana.ID || view.Trip.Version != 1 || !ids.IsValid(string(view.Trip.ID)) {
			t.Errorf("unexpected view: %+v", view)
		}
		if _, ok := e.repo.Stored(view.Trip.ID); !ok {
			t.Error("trip was not persisted")
		}
	})

	t.Run("accepts a client generated id", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		id := ids.New()

		view, err := e.create.Execute(ctx, trip.CreateTripInput{ActorID: ana.ID, ID: id, Details: validInput()})

		if err != nil || string(view.Trip.ID) != id {
			t.Errorf("Execute() = %+v, %v; want id %s", view.Trip.ID, err, id)
		}
	})

	t.Run("a taken id is a conflict and does not touch the existing trip", func(t *testing.T) {
		e := newEnv()
		ana, bia := e.newUser(t, "ana"), e.newUser(t, "bia")
		existing := e.newTrip(t, ana)

		_, err := e.create.Execute(ctx, trip.CreateTripInput{ActorID: bia.ID, ID: string(existing.ID), Details: validInput()})

		requireCode(t, err, apperror.KindConflict, "trip_id_taken")
		if stored, _ := e.repo.Stored(existing.ID); stored.OwnerID() != ana.ID {
			t.Error("the existing trip was modified")
		}
	})

	t.Run("rejects a malformed client id", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")

		_, err := e.create.Execute(ctx, trip.CreateTripInput{ActorID: ana.ID, ID: "not-a-uuid", Details: validInput()})

		if _, ok := fieldsOf(t, err)["id"]; !ok {
			t.Errorf("Execute() error = %v, want an id error", err)
		}
	})

	t.Run("validates the details", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		in := validInput()
		in.EndDate = "2027-03-01"

		_, err := e.create.Execute(ctx, trip.CreateTripInput{ActorID: ana.ID, Details: in})

		if _, ok := fieldsOf(t, err)["endDate"]; !ok {
			t.Errorf("Execute() error = %v, want an endDate error", err)
		}
	})
}

func TestGetTrip_AnyMemberCanReadAndOutsidersGetNotFound(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	id, owner, admin, member, viewer, outsider := e.tripWithAllRoles(t)

	for name, want := range map[string]struct {
		who  user.User
		role trip.Role
	}{
		"owner": {owner, trip.RoleOwner}, "admin": {admin, trip.RoleAdmin},
		"member": {member, trip.RoleMember}, "viewer": {viewer, trip.RoleViewer},
	} {
		view, err := e.get.Execute(ctx, id, want.who.ID)
		if err != nil || view.Role != want.role || view.Trip.ID != id {
			t.Errorf("%s: Execute() = %+v, %v; want role %s", name, view, err, want.role)
		}
	}

	_, err := e.get.Execute(ctx, id, outsider.ID)
	requireCode(t, err, apperror.KindNotFound, "trip_not_found")

	_, err = e.get.Execute(ctx, trip.ID(ids.New()), owner.ID)
	requireCode(t, err, apperror.KindNotFound, "trip_not_found")
}

func TestGetTrip_OutsiderAndMissingTripAreIndistinguishable(t *testing.T) {
	e := newEnv()
	id, _, _, _, _, outsider := e.tripWithAllRoles(t)

	_, existing := e.get.Execute(context.Background(), id, outsider.ID)
	_, missing := e.get.Execute(context.Background(), trip.ID(ids.New()), outsider.ID)

	if existing.Error() != missing.Error() {
		t.Errorf("responses differ, leaking existence: %q vs %q", existing, missing)
	}
}

func TestListTrips(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	ana, bia := e.newUser(t, "ana"), e.newUser(t, "bia")
	own := e.newTrip(t, ana)
	shared := e.newTrip(t, bia)
	e.seedMember(t, shared.ID, ana, trip.RoleViewer)
	e.newTrip(t, bia)

	views, err := e.list.Execute(ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}

	roles := map[trip.ID]trip.Role{}
	for _, v := range views {
		roles[v.Trip.ID] = v.Role
	}
	if len(views) != 2 || roles[own.ID] != trip.RoleOwner || roles[shared.ID] != trip.RoleViewer {
		t.Errorf("ListTrips() = %+v, want only the two trips ana belongs to", roles)
	}

	empty, err := e.list.Execute(ctx, e.newUser(t, "caio").ID)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("ListTrips() for a user with no trips = %v, %v; want an empty non-nil slice", empty, err)
	}
}

func TestListTrips_ExcludesDeletedTrips(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	ana := e.newUser(t, "ana")
	tr := e.newTrip(t, ana)
	if err := e.delete.Execute(ctx, tr.ID, ana.ID); err != nil {
		t.Fatal(err)
	}

	views, err := e.list.Execute(ctx, ana.ID)

	if err != nil || len(views) != 0 {
		t.Errorf("ListTrips() = %v, %v; want none", views, err)
	}
}

func str(s string) *string { return &s }

func TestUpdateTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("owner and admin can update, member and viewer cannot", func(t *testing.T) {
		for _, tt := range []struct {
			name    string
			pick    func(owner, admin, member, viewer user.User) user.User
			allowed bool
		}{
			{"owner", func(o, _, _, _ user.User) user.User { return o }, true},
			{"admin", func(_, a, _, _ user.User) user.User { return a }, true},
			{"member", func(_, _, m, _ user.User) user.User { return m }, false},
			{"viewer", func(_, _, _, v user.User) user.User { return v }, false},
		} {
			e := newEnv()
			id, owner, admin, member, viewer, _ := e.tripWithAllRoles(t)
			actor := tt.pick(owner, admin, member, viewer)
			stored, _ := e.repo.Stored(id)

			view, err := e.update.Execute(ctx, trip.UpdateTripInput{TripID: id, ActorID: actor.ID, BaseVersion: stored.Version, Patch: trip.DetailsPatch{Name: str("Renamed")}})

			if tt.allowed {
				if err != nil || view.Trip.Name != "Renamed" || view.Trip.Version != stored.Version+1 {
					t.Errorf("%s: Execute() = %+v, %v", tt.name, view.Trip, err)
				}
			} else {
				requireCode(t, err, apperror.KindForbidden, "forbidden")
				if after, _ := e.repo.Stored(id); after.Name == "Renamed" {
					t.Errorf("%s: a forbidden update was persisted", tt.name)
				}
			}
		}
	})

	t.Run("outsider gets not found", func(t *testing.T) {
		e := newEnv()
		id, _, _, _, _, outsider := e.tripWithAllRoles(t)

		_, err := e.update.Execute(ctx, trip.UpdateTripInput{TripID: id, ActorID: outsider.ID, BaseVersion: 1, Patch: trip.DetailsPatch{Name: str("x")}})

		requireCode(t, err, apperror.KindNotFound, "trip_not_found")
	})

	t.Run("stale base version is a conflict", func(t *testing.T) {
		e := newEnv()
		id, owner, _, _, _, _ := e.tripWithAllRoles(t)
		stored, _ := e.repo.Stored(id)

		_, err := e.update.Execute(ctx, trip.UpdateTripInput{TripID: id, ActorID: owner.ID, BaseVersion: stored.Version - 1, Patch: trip.DetailsPatch{Name: str("x")}})

		requireCode(t, err, apperror.KindConflict, "version_conflict")
		if after, _ := e.repo.Stored(id); after.Name != stored.Name {
			t.Error("a conflicting update was persisted")
		}
	})

	t.Run("two updates from the same base version: the second conflicts", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		tr := e.newTrip(t, ana)
		in := func(name string) trip.UpdateTripInput {
			return trip.UpdateTripInput{TripID: tr.ID, ActorID: ana.ID, BaseVersion: tr.Version, Patch: trip.DetailsPatch{Name: str(name)}}
		}

		if _, err := e.update.Execute(ctx, in("first")); err != nil {
			t.Fatal(err)
		}
		_, err := e.update.Execute(ctx, in("second"))

		requireCode(t, err, apperror.KindConflict, "version_conflict")
		if after, _ := e.repo.Stored(tr.ID); after.Name != "first" {
			t.Errorf("Name = %q, want the first write to win", after.Name)
		}
	})

	t.Run("an update without changes keeps the version", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		tr := e.newTrip(t, ana)

		view, err := e.update.Execute(ctx, trip.UpdateTripInput{TripID: tr.ID, ActorID: ana.ID, BaseVersion: tr.Version, Patch: trip.DetailsPatch{Name: str(tr.Name)}})

		if err != nil || view.Trip.Version != tr.Version {
			t.Errorf("Execute() version = %d, %v; want unchanged %d", view.Trip.Version, err, tr.Version)
		}
	})

	t.Run("an empty patch is rejected", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		tr := e.newTrip(t, ana)

		_, err := e.update.Execute(ctx, trip.UpdateTripInput{TripID: tr.ID, ActorID: ana.ID, BaseVersion: tr.Version})

		requireCode(t, err, apperror.KindValidation, "empty_patch")
	})

	t.Run("an invalid merged result is rejected and not persisted", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		tr := e.newTrip(t, ana)

		_, err := e.update.Execute(ctx, trip.UpdateTripInput{TripID: tr.ID, ActorID: ana.ID, BaseVersion: tr.Version, Patch: trip.DetailsPatch{StartDate: str("2028-01-01")}})

		if _, ok := fieldsOf(t, err)["endDate"]; !ok {
			t.Errorf("Execute() error = %v, want an endDate error", err)
		}
		if after, _ := e.repo.Stored(tr.ID); after.StartDate != tr.StartDate {
			t.Error("an invalid update was persisted")
		}
	})
}

func TestDeleteTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("only the owner can delete", func(t *testing.T) {
		e := newEnv()
		id, _, admin, member, viewer, outsider := e.tripWithAllRoles(t)

		for name, actor := range map[string]user.User{"admin": admin, "member": member, "viewer": viewer} {
			requireCode(t, e.delete.Execute(ctx, id, actor.ID), apperror.KindForbidden, "forbidden")
			if stored, _ := e.repo.Stored(id); stored.DeletedAt != nil {
				t.Fatalf("%s deleted the trip", name)
			}
		}
		requireCode(t, e.delete.Execute(ctx, id, outsider.ID), apperror.KindNotFound, "trip_not_found")
	})

	t.Run("owner soft deletes and the trip disappears", func(t *testing.T) {
		e := newEnv()
		id, owner, admin, _, _, _ := e.tripWithAllRoles(t)
		before, _ := e.repo.Stored(id)

		if err := e.delete.Execute(ctx, id, owner.ID); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		stored, ok := e.repo.Stored(id)
		if !ok || stored.DeletedAt == nil || stored.Version != before.Version+1 {
			t.Errorf("stored = %+v, want a tombstone with a bumped version", stored)
		}
		for _, u := range []user.User{owner, admin} {
			if _, err := e.get.Execute(ctx, id, u.ID); err == nil {
				t.Errorf("%s can still read a deleted trip", u.Name)
			}
		}
		requireCode(t, e.delete.Execute(ctx, id, owner.ID), apperror.KindNotFound, "trip_not_found")
	})
}

func TestAddMemberUseCase(t *testing.T) {
	ctx := context.Background()

	t.Run("owner adds by email, case insensitively", func(t *testing.T) {
		e := newEnv()
		ana, bia := e.newUser(t, "ana"), e.newUser(t, "bia")
		tr := e.newTrip(t, ana)

		view, err := e.add.Execute(ctx, trip.AddMemberInput{TripID: tr.ID, ActorID: ana.ID, Email: "  BIA@Example.com ", Role: "MEMBER"})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if view.Member.UserID != bia.ID || view.Member.Role != trip.RoleMember || view.User.Name != "Bia" {
			t.Errorf("unexpected view: %+v", view)
		}
		if role, ok := e.roleOf(tr.ID, bia); !ok || role != trip.RoleMember {
			t.Errorf("bia role = %q, %v", role, ok)
		}
	})

	t.Run("permission matrix", func(t *testing.T) {
		tests := []struct {
			actor   string
			newRole string
			allowed bool
		}{
			{"owner", "ADMIN", true}, {"owner", "MEMBER", true}, {"owner", "VIEWER", true},
			{"admin", "ADMIN", false}, {"admin", "MEMBER", true}, {"admin", "VIEWER", true},
			{"member", "VIEWER", false}, {"viewer", "VIEWER", false},
		}
		for _, tt := range tests {
			e := newEnv()
			id, owner, admin, member, viewer, _ := e.tripWithAllRoles(t)
			newcomer := e.newUser(t, "frida")
			actor := map[string]user.User{"owner": owner, "admin": admin, "member": member, "viewer": viewer}[tt.actor]

			_, err := e.add.Execute(ctx, trip.AddMemberInput{TripID: id, ActorID: actor.ID, Email: newcomer.Email, Role: tt.newRole})

			_, joined := e.roleOf(id, newcomer)
			if tt.allowed && (err != nil || !joined) {
				t.Errorf("%s adding %s: error = %v, joined = %v", tt.actor, tt.newRole, err, joined)
			}
			if !tt.allowed {
				requireCode(t, err, apperror.KindForbidden, "forbidden")
				if joined {
					t.Errorf("%s adding %s: the member was added despite the error", tt.actor, tt.newRole)
				}
			}
		}
	})

	t.Run("cannot add an owner", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		bia := e.newUser(t, "bia")
		tr := e.newTrip(t, ana)

		_, err := e.add.Execute(ctx, trip.AddMemberInput{TripID: tr.ID, ActorID: ana.ID, Email: bia.Email, Role: "OWNER"})

		requireCode(t, err, apperror.KindValidation, "owner_assignment")
	})

	t.Run("invalid role", func(t *testing.T) {
		e := newEnv()
		ana, bia := e.newUser(t, "ana"), e.newUser(t, "bia")
		tr := e.newTrip(t, ana)

		_, err := e.add.Execute(ctx, trip.AddMemberInput{TripID: tr.ID, ActorID: ana.ID, Email: bia.Email, Role: "GUEST"})

		if _, ok := fieldsOf(t, err)["role"]; !ok {
			t.Errorf("Execute() error = %v, want a role error", err)
		}
	})

	t.Run("unknown email", func(t *testing.T) {
		e := newEnv()
		ana := e.newUser(t, "ana")
		tr := e.newTrip(t, ana)

		_, err := e.add.Execute(ctx, trip.AddMemberInput{TripID: tr.ID, ActorID: ana.ID, Email: "nobody@example.com", Role: "MEMBER"})

		requireCode(t, err, apperror.KindNotFound, "user_not_found")
	})

	t.Run("already a member", func(t *testing.T) {
		e := newEnv()
		id, owner, _, member, _, _ := e.tripWithAllRoles(t)

		_, err := e.add.Execute(ctx, trip.AddMemberInput{TripID: id, ActorID: owner.ID, Email: member.Email, Role: "VIEWER"})

		requireCode(t, err, apperror.KindConflict, "member_exists")
		if role, _ := e.roleOf(id, member); role != trip.RoleMember {
			t.Errorf("existing member role changed to %s", role)
		}
	})

	t.Run("outsider gets not found before anything else", func(t *testing.T) {
		e := newEnv()
		id, _, _, _, _, outsider := e.tripWithAllRoles(t)

		_, err := e.add.Execute(ctx, trip.AddMemberInput{TripID: id, ActorID: outsider.ID, Email: outsider.Email, Role: "OWNER"})

		requireCode(t, err, apperror.KindNotFound, "trip_not_found")
	})
}

func TestListMembers(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	id, owner, admin, member, viewer, outsider := e.tripWithAllRoles(t)

	views, err := e.members.Execute(ctx, id, viewer.ID)
	if err != nil {
		t.Fatalf("a viewer must be able to list members: %v", err)
	}

	wantOrder := []user.User{owner, admin, member, viewer}
	if len(views) != len(wantOrder) {
		t.Fatalf("got %d members, want %d", len(views), len(wantOrder))
	}
	for i, want := range wantOrder {
		if views[i].Member.UserID != want.ID || views[i].User.Email != want.Email || views[i].User.Name != want.Name {
			t.Errorf("member %d = %+v, want %s (ordered by role) with name and email", i, views[i], want.Name)
		}
	}

	_, err = e.members.Execute(ctx, id, outsider.ID)
	requireCode(t, err, apperror.KindNotFound, "trip_not_found")
}

func TestChangeMemberRoleUseCase(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		actor   string
		target  string
		newRole string
		allowed bool
	}{
		{"owner promotes member to admin", "owner", "member", "ADMIN", true},
		{"owner demotes admin to viewer", "owner", "admin", "VIEWER", true},
		{"admin demotes member to viewer", "admin", "member", "VIEWER", true},
		{"admin promotes viewer to member", "admin", "viewer", "MEMBER", true},
		{"admin cannot promote to admin", "admin", "member", "ADMIN", false},
		{"admin cannot demote an admin", "admin", "admin", "MEMBER", false},
		{"admin cannot touch the owner", "admin", "owner", "MEMBER", false},
		{"member cannot change roles", "member", "viewer", "MEMBER", false},
		{"viewer cannot change roles", "viewer", "member", "VIEWER", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv()
			id, owner, admin, member, viewer, _ := e.tripWithAllRoles(t)
			users := map[string]user.User{"owner": owner, "admin": admin, "member": member, "viewer": viewer}
			before, _ := e.roleOf(id, users[tt.target])

			view, err := e.change.Execute(ctx, trip.ChangeMemberRoleInput{TripID: id, ActorID: users[tt.actor].ID, TargetID: users[tt.target].ID, Role: tt.newRole})

			after, _ := e.roleOf(id, users[tt.target])
			if tt.allowed {
				if err != nil || string(after) != tt.newRole || string(view.Member.Role) != tt.newRole || view.User.Email == "" {
					t.Errorf("Execute() = %+v, %v; role after = %s", view, err, after)
				}
				return
			}
			requireCode(t, err, apperror.KindForbidden, "forbidden")
			if after != before {
				t.Errorf("a forbidden change was persisted: %s -> %s", before, after)
			}
		})
	}

	t.Run("the owner cannot change their own role, and ownership cannot be assigned", func(t *testing.T) {
		e := newEnv()
		id, owner, _, member, _, _ := e.tripWithAllRoles(t)

		_, err := e.change.Execute(ctx, trip.ChangeMemberRoleInput{TripID: id, ActorID: owner.ID, TargetID: owner.ID, Role: "ADMIN"})
		requireCode(t, err, apperror.KindValidation, "owner_locked")

		_, err = e.change.Execute(ctx, trip.ChangeMemberRoleInput{TripID: id, ActorID: owner.ID, TargetID: member.ID, Role: "OWNER"})
		requireCode(t, err, apperror.KindValidation, "owner_assignment")
		if stored, _ := e.repo.Stored(id); stored.OwnerID() != owner.ID {
			t.Error("owner changed")
		}
	})

	t.Run("setting the same role does not bump the version", func(t *testing.T) {
		e := newEnv()
		id, owner, _, member, _, _ := e.tripWithAllRoles(t)
		before, _ := e.repo.Stored(id)

		_, err := e.change.Execute(ctx, trip.ChangeMemberRoleInput{TripID: id, ActorID: owner.ID, TargetID: member.ID, Role: "MEMBER"})

		if after, _ := e.repo.Stored(id); err != nil || after.Version != before.Version {
			t.Errorf("error = %v, version %d -> %d", err, before.Version, after.Version)
		}
	})

	t.Run("unknown target and outsider", func(t *testing.T) {
		e := newEnv()
		id, owner, _, _, _, outsider := e.tripWithAllRoles(t)

		_, err := e.change.Execute(ctx, trip.ChangeMemberRoleInput{TripID: id, ActorID: owner.ID, TargetID: outsider.ID, Role: "MEMBER"})
		requireCode(t, err, apperror.KindNotFound, "member_not_found")

		_, err = e.change.Execute(ctx, trip.ChangeMemberRoleInput{TripID: id, ActorID: outsider.ID, TargetID: owner.ID, Role: "MEMBER"})
		requireCode(t, err, apperror.KindNotFound, "trip_not_found")
	})

	t.Run("a member cannot probe who belongs to the trip", func(t *testing.T) {
		e := newEnv()
		id, _, _, member, _, outsider := e.tripWithAllRoles(t)

		_, err := e.change.Execute(ctx, trip.ChangeMemberRoleInput{TripID: id, ActorID: member.ID, TargetID: outsider.ID, Role: "MEMBER"})

		requireCode(t, err, apperror.KindForbidden, "forbidden")
	})
}

func TestRemoveMemberUseCase(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		actor   string
		target  string
		allowed bool
	}{
		{"owner removes admin", "owner", "admin", true},
		{"owner removes member", "owner", "member", true},
		{"admin removes member", "admin", "member", true},
		{"admin removes viewer", "admin", "viewer", true},
		{"admin cannot remove another admin", "admin", "admin2", false},
		{"admin cannot remove the owner", "admin", "owner", false},
		{"member cannot remove others", "member", "viewer", false},
		{"viewer cannot remove others", "viewer", "member", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv()
			id, owner, admin, member, viewer, _ := e.tripWithAllRoles(t)
			admin2 := e.newUser(t, "gabi")
			e.seedMember(t, id, admin2, trip.RoleAdmin)
			users := map[string]user.User{"owner": owner, "admin": admin, "admin2": admin2, "member": member, "viewer": viewer}

			err := e.remove.Execute(ctx, trip.RemoveMemberInput{TripID: id, ActorID: users[tt.actor].ID, TargetID: users[tt.target].ID})

			_, stillMember := e.roleOf(id, users[tt.target])
			if tt.allowed && (err != nil || stillMember) {
				t.Errorf("Execute() = %v, stillMember = %v", err, stillMember)
			}
			if !tt.allowed {
				requireCode(t, err, apperror.KindForbidden, "forbidden")
				if !stillMember {
					t.Error("a forbidden removal was persisted")
				}
			}
		})
	}

	t.Run("members can leave, the owner cannot", func(t *testing.T) {
		e := newEnv()
		id, owner, admin, member, viewer, _ := e.tripWithAllRoles(t)

		for _, u := range []user.User{admin, member, viewer} {
			if err := e.remove.Execute(ctx, trip.RemoveMemberInput{TripID: id, ActorID: u.ID, TargetID: u.ID}); err != nil {
				t.Errorf("%s could not leave: %v", u.Name, err)
			}
		}
		err := e.remove.Execute(ctx, trip.RemoveMemberInput{TripID: id, ActorID: owner.ID, TargetID: owner.ID})

		requireCode(t, err, apperror.KindValidation, "owner_locked")
		if stored, _ := e.repo.Stored(id); len(stored.Members) != 1 || stored.OwnerID() != owner.ID {
			t.Errorf("members = %+v, want only the owner", stored.Members)
		}
	})

	t.Run("a removed member loses access", func(t *testing.T) {
		e := newEnv()
		id, owner, _, member, _, _ := e.tripWithAllRoles(t)
		if err := e.remove.Execute(ctx, trip.RemoveMemberInput{TripID: id, ActorID: owner.ID, TargetID: member.ID}); err != nil {
			t.Fatal(err)
		}

		_, err := e.get.Execute(ctx, id, member.ID)

		requireCode(t, err, apperror.KindNotFound, "trip_not_found")
	})

	t.Run("unknown target and outsider", func(t *testing.T) {
		e := newEnv()
		id, owner, _, member, _, outsider := e.tripWithAllRoles(t)

		requireCode(t, e.remove.Execute(ctx, trip.RemoveMemberInput{TripID: id, ActorID: owner.ID, TargetID: outsider.ID}), apperror.KindNotFound, "member_not_found")
		requireCode(t, e.remove.Execute(ctx, trip.RemoveMemberInput{TripID: id, ActorID: member.ID, TargetID: outsider.ID}), apperror.KindForbidden, "forbidden")
		requireCode(t, e.remove.Execute(ctx, trip.RemoveMemberInput{TripID: id, ActorID: outsider.ID, TargetID: owner.ID}), apperror.KindNotFound, "trip_not_found")
	})
}

func TestTransferOwnershipUseCase(t *testing.T) {
	ctx := context.Background()

	t.Run("only the owner can transfer", func(t *testing.T) {
		e := newEnv()
		id, _, admin, member, viewer, outsider := e.tripWithAllRoles(t)

		for _, actor := range []user.User{admin, member, viewer} {
			_, err := e.transfer.Execute(ctx, trip.TransferOwnershipInput{TripID: id, ActorID: actor.ID, TargetID: actor.ID})
			requireCode(t, err, apperror.KindForbidden, "forbidden")
		}
		_, err := e.transfer.Execute(ctx, trip.TransferOwnershipInput{TripID: id, ActorID: outsider.ID, TargetID: outsider.ID})
		requireCode(t, err, apperror.KindNotFound, "trip_not_found")
	})

	t.Run("hands the trip over and keeps a single owner", func(t *testing.T) {
		e := newEnv()
		id, owner, _, member, _, _ := e.tripWithAllRoles(t)

		view, err := e.transfer.Execute(ctx, trip.TransferOwnershipInput{TripID: id, ActorID: owner.ID, TargetID: member.ID})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if view.Role != trip.RoleAdmin || view.Trip.OwnerID() != member.ID {
			t.Errorf("view = role %s, owner %s; want the previous owner as admin and %s as owner", view.Role, view.Trip.OwnerID(), member.ID)
		}
		stored, _ := e.repo.Stored(id)
		if ownerCount(stored) != 1 || stored.OwnerID() != member.ID {
			t.Errorf("stored owner = %s, owners = %d", stored.OwnerID(), ownerCount(stored))
		}
		if err := e.delete.Execute(ctx, id, owner.ID); err == nil {
			t.Error("the previous owner can still delete the trip")
		}
		if err := e.delete.Execute(ctx, id, member.ID); err != nil {
			t.Errorf("the new owner cannot delete the trip: %v", err)
		}
	})

	t.Run("target must be a member and not the owner", func(t *testing.T) {
		e := newEnv()
		id, owner, _, _, _, outsider := e.tripWithAllRoles(t)

		_, err := e.transfer.Execute(ctx, trip.TransferOwnershipInput{TripID: id, ActorID: owner.ID, TargetID: outsider.ID})
		requireCode(t, err, apperror.KindNotFound, "member_not_found")

		_, err = e.transfer.Execute(ctx, trip.TransferOwnershipInput{TripID: id, ActorID: owner.ID, TargetID: owner.ID})
		requireCode(t, err, apperror.KindValidation, "already_owner")
	})
}

type failingRepo struct {
	*triptest.Repository
	err error
}

func (f failingRepo) FindByID(context.Context, trip.ID) (trip.Trip, error) {
	return trip.Trip{}, f.err
}

func TestInfrastructureFailuresAreNotAppErrors(t *testing.T) {
	boom := errors.New("connection reset")
	get := trip.NewGetTrip(failingRepo{Repository: triptest.NewRepository(), err: boom})

	_, err := get.Execute(context.Background(), "trip-1", "ana")

	var appErr *apperror.Error
	if errors.As(err, &appErr) || !errors.Is(err, boom) {
		t.Errorf("Execute() error = %v, want the wrapped infrastructure error (mapped to 500 by the transport)", err)
	}
}

func TestUpdateIsAtomicWithRespectToConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	ana := e.newUser(t, "ana")
	tr := e.newTrip(t, ana)

	const writers = 20
	results := make(chan error, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		go func() {
			<-start
			_, err := e.update.Execute(ctx, trip.UpdateTripInput{TripID: tr.ID, ActorID: ana.ID, BaseVersion: tr.Version, Patch: trip.DetailsPatch{Name: str("writer")}})
			results <- err
		}()
	}
	close(start)

	succeeded := 0
	for i := 0; i < writers; i++ {
		if err := <-results; err == nil {
			succeeded++
		}
	}

	if succeeded != 1 {
		t.Errorf("%d concurrent updates from the same base version succeeded, want exactly 1", succeeded)
	}
	if stored, _ := e.repo.Stored(tr.ID); stored.Version != tr.Version+1 {
		t.Errorf("version = %d, want %d", stored.Version, tr.Version+1)
	}
}

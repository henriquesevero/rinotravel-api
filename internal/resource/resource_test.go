package resource_test

import (
	"context"
	"errors"
	"testing"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/resource/resourcetest"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type note struct {
	kernel.Base
	Text string
}

func noteBase(n *note) *kernel.Base { return &n.Base }

type fakeAuthz struct {
	roles map[user.ID]trip.Role
}

func (f fakeAuthz) Authorize(_ context.Context, tripID trip.ID, actor user.ID, action trip.Action) (trip.Access, error) {
	role, ok := f.roles[actor]
	if !ok || tripID == "missing" {
		return trip.Access{}, apperror.NotFound("trip_not_found", "Trip not found.")
	}
	if !trip.Can(role, action) {
		return trip.Access{}, apperror.Forbidden("forbidden", "no")
	}
	return trip.Access{Role: role}, nil
}

const tripID = trip.ID("trip-1")

func newService() *resource.Service[note] {
	repo := resourcetest.New(noteBase)
	authz := fakeAuthz{roles: map[user.ID]trip.Role{"owner": trip.RoleOwner, "member": trip.RoleMember, "viewer": trip.RoleViewer}}
	return resource.NewService[note](repo, authz, resource.Config[note]{Name: "note", Base: noteBase})
}

func create(t *testing.T, s *resource.Service[note], text string) note {
	t.Helper()
	res, err := s.Create(context.Background(), "member", tripID, "", func(trip.Access) (note, error) { return note{Text: text}, nil })
	if err != nil {
		t.Fatal(err)
	}
	return res.Entity
}

func requireCode(t *testing.T, err error, kind apperror.Kind, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error = %v, want kind %d code %s", err, kind, code)
	}
}

func TestCreate_StampsTheBaseAndReturnsTheRole(t *testing.T) {
	s := newService()

	res, err := s.Create(context.Background(), "member", tripID, "", func(trip.Access) (note, error) { return note{Text: "hi"}, nil })
	if err != nil {
		t.Fatal(err)
	}

	b := res.Entity.Base
	if !ids.IsValid(b.ID) || b.TripID != "trip-1" || b.Version != 1 || b.CreatedAt.IsZero() || b.DeletedAt != nil || res.Role != trip.RoleMember {
		t.Errorf("unexpected result: %+v role %s", b, res.Role)
	}
}

func TestCreate_Permissions(t *testing.T) {
	s := newService()
	build := func(trip.Access) (note, error) { return note{Text: "x"}, nil }

	for _, actor := range []user.ID{"owner", "member"} {
		if _, err := s.Create(context.Background(), actor, tripID, "", build); err != nil {
			t.Errorf("%s could not create: %v", actor, err)
		}
	}
	_, err := s.Create(context.Background(), "viewer", tripID, "", build)
	requireCode(t, err, apperror.KindForbidden, "forbidden")
	_, err = s.Create(context.Background(), "stranger", tripID, "", build)
	requireCode(t, err, apperror.KindNotFound, "trip_not_found")
}

func TestCreate_ClientIDs(t *testing.T) {
	s := newService()
	build := func(trip.Access) (note, error) { return note{}, nil }
	id := ids.New()

	if res, err := s.Create(context.Background(), "member", tripID, id, build); err != nil || res.Entity.ID != id {
		t.Fatalf("Create() = %v, %v; want the client id", res.Entity.ID, err)
	}
	_, err := s.Create(context.Background(), "member", tripID, id, build)
	requireCode(t, err, apperror.KindConflict, "note_conflict")

	_, err = s.Create(context.Background(), "member", tripID, "NOT-A-UUID", build)
	requireCode(t, err, apperror.KindValidation, "validation_failed")
}

func TestCreate_BuildErrorsAreReturnedAndNothingIsStored(t *testing.T) {
	s := newService()
	boom := apperror.Unprocessable("bad", "bad")

	_, err := s.Create(context.Background(), "member", tripID, "", func(trip.Access) (note, error) { return note{}, boom })

	requireCode(t, err, apperror.KindValidation, "bad")
	if list, _, _ := s.List(context.Background(), "member", tripID); len(list) != 0 {
		t.Error("a failed build stored an entity")
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	rename := func(text string) func(note, trip.Access) (note, error) {
		return func(n note, _ trip.Access) (note, error) { n.Text = text; return n, nil }
	}

	t.Run("bumps the version and stores the change", func(t *testing.T) {
		s := newService()
		n := create(t, s, "old")

		res, err := s.Update(ctx, "member", tripID, n.ID, 1, rename("new"))

		if err != nil || res.Entity.Text != "new" || res.Entity.Version != 2 {
			t.Errorf("Update() = %+v, %v", res.Entity, err)
		}
		if got, _ := s.Get(ctx, "viewer", tripID, n.ID); got.Entity.Text != "new" {
			t.Error("the change was not persisted")
		}
	})

	t.Run("a stale base version conflicts and changes nothing", func(t *testing.T) {
		s := newService()
		n := create(t, s, "old")
		if _, err := s.Update(ctx, "member", tripID, n.ID, 1, rename("first")); err != nil {
			t.Fatal(err)
		}

		_, err := s.Update(ctx, "member", tripID, n.ID, 1, rename("late"))

		requireCode(t, err, apperror.KindConflict, "version_conflict")
		if got, _ := s.Get(ctx, "member", tripID, n.ID); got.Entity.Text != "first" {
			t.Errorf("text = %q, want the first write to survive", got.Entity.Text)
		}
	})

	t.Run("an unchanged result does not move the version", func(t *testing.T) {
		s := newService()
		n := create(t, s, "same")

		res, err := s.Update(ctx, "member", tripID, n.ID, 1, rename("same"))

		if err != nil || res.Entity.Version != 1 {
			t.Errorf("Update() version = %d, %v; want 1", res.Entity.Version, err)
		}
	})

	t.Run("viewers cannot update and mutate errors abort", func(t *testing.T) {
		s := newService()
		n := create(t, s, "old")

		_, err := s.Update(ctx, "viewer", tripID, n.ID, 1, rename("x"))
		requireCode(t, err, apperror.KindForbidden, "forbidden")

		_, err = s.Update(ctx, "member", tripID, n.ID, 1, func(note, trip.Access) (note, error) {
			return note{}, apperror.Unprocessable("nope", "nope")
		})
		requireCode(t, err, apperror.KindValidation, "nope")
	})

	t.Run("an entity of another trip is not found", func(t *testing.T) {
		s := newService()
		n := create(t, s, "old")

		_, err := s.Update(ctx, "member", "trip-2", n.ID, 1, rename("x"))

		requireCode(t, err, apperror.KindNotFound, "note_not_found")
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("writes a tombstone that hides the entity", func(t *testing.T) {
		s := newService()
		n := create(t, s, "bye")

		if err := s.Delete(ctx, "member", tripID, n.ID, nil); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		_, err := s.Get(ctx, "member", tripID, n.ID)
		requireCode(t, err, apperror.KindNotFound, "note_not_found")
		tomb, err := s.Repo().GetAny(ctx, string(tripID), n.ID)
		if err != nil || !tomb.IsDeleted() || tomb.Version != 2 {
			t.Errorf("tombstone = %+v, %v", tomb.Base, err)
		}
		requireCode(t, s.Delete(ctx, "member", tripID, n.ID, nil), apperror.KindNotFound, "note_not_found")
	})

	t.Run("honors a base version and permissions", func(t *testing.T) {
		s := newService()
		n := create(t, s, "keep")
		stale := int64(7)

		requireCode(t, s.Delete(ctx, "member", tripID, n.ID, &stale), apperror.KindConflict, "version_conflict")
		requireCode(t, s.Delete(ctx, "viewer", tripID, n.ID, nil), apperror.KindForbidden, "forbidden")
		if _, err := s.Get(ctx, "member", tripID, n.ID); err != nil {
			t.Errorf("a rejected delete removed the entity: %v", err)
		}
	})
}

func TestGetAndList(t *testing.T) {
	ctx := context.Background()
	s := newService()
	a, b := create(t, s, "a"), create(t, s, "b")
	if err := s.Delete(ctx, "member", tripID, b.ID, nil); err != nil {
		t.Fatal(err)
	}

	list, role, err := s.List(ctx, "viewer", tripID)
	if err != nil || role != trip.RoleViewer || len(list) != 1 || list[0].ID != a.ID {
		t.Errorf("List() = %d items, role %s, %v; want only the live entity readable by a viewer", len(list), role, err)
	}
	_, _, err = s.List(ctx, "stranger", tripID)
	requireCode(t, err, apperror.KindNotFound, "trip_not_found")
}

func TestChangesIncludeTombstonesOnlyAfterAnInitialSync(t *testing.T) {
	ctx := context.Background()
	s := newService()
	a, b := create(t, s, "a"), create(t, s, "b")
	if err := s.Delete(ctx, "member", tripID, b.ID, nil); err != nil {
		t.Fatal(err)
	}

	initial, _ := s.Repo().Changes(ctx, string(tripID), 0, 100)
	if len(initial) != 1 || initial[0].Entity.ID != a.ID {
		t.Errorf("initial changes = %d, want only the live entity", len(initial))
	}
	incremental, _ := s.Repo().Changes(ctx, string(tripID), 1, 100)
	if len(incremental) != 1 || incremental[0].Entity.ID != b.ID || !incremental[0].Entity.IsDeleted() {
		t.Errorf("incremental changes = %+v, want only the tombstone written after seq 1", incremental)
	}
}

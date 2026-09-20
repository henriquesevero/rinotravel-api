package trip_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

var now = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func validInput() trip.DetailsInput {
	return trip.DetailsInput{
		Name:        "Japan 2027",
		Destination: "Tokyo",
		StartDate:   "2027-04-01",
		EndDate:     "2027-04-15",
		Timezone:    "Asia/Tokyo",
		Currency:    "JPY",
	}
}

func mustDetails(t *testing.T) trip.Details {
	t.Helper()
	d, err := trip.NewDetails(validInput())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindValidation {
		t.Fatalf("error = %v, want a validation error", err)
	}
	fields := make(map[string]string, len(appErr.Fields))
	for _, f := range appErr.Fields {
		fields[f.Field] = f.Message
	}
	return fields
}

func requireCode(t *testing.T, err error, kind apperror.Kind, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %v, want an *apperror.Error", err)
	}
	if appErr.Kind != kind || appErr.Code != code {
		t.Errorf("error = {kind %d, code %q}, want {kind %d, code %q}", appErr.Kind, appErr.Code, kind, code)
	}
}

func TestNewDetails_NormalizesValidInput(t *testing.T) {
	in := validInput()
	in.Name = "  Japan 2027 "
	in.Currency = "jpy"

	got, err := trip.NewDetails(in)
	if err != nil {
		t.Fatalf("NewDetails() error = %v", err)
	}

	if got.Name != "Japan 2027" || got.Currency != "JPY" || got.StartDate != "2027-04-01" || got.Timezone != "Asia/Tokyo" {
		t.Errorf("NewDetails() = %+v", got)
	}
}

func TestNewDetails_AllowsSingleDayTrip(t *testing.T) {
	in := validInput()
	in.EndDate = in.StartDate

	if _, err := trip.NewDetails(in); err != nil {
		t.Errorf("NewDetails() error = %v, want a one-day trip to be valid", err)
	}
}

func TestNewDetails_RejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*trip.DetailsInput)
		wantField string
	}{
		{"missing name", func(in *trip.DetailsInput) { in.Name = "  " }, "name"},
		{"long name", func(in *trip.DetailsInput) { in.Name = strings.Repeat("a", 101) }, "name"},
		{"missing destination", func(in *trip.DetailsInput) { in.Destination = "" }, "destination"},
		{"long destination", func(in *trip.DetailsInput) { in.Destination = strings.Repeat("a", 101) }, "destination"},
		{"malformed start", func(in *trip.DetailsInput) { in.StartDate = "01/04/2027" }, "startDate"},
		{"impossible start", func(in *trip.DetailsInput) { in.StartDate = "2027-02-30" }, "startDate"},
		{"malformed end", func(in *trip.DetailsInput) { in.EndDate = "" }, "endDate"},
		{"end before start", func(in *trip.DetailsInput) { in.EndDate = "2027-03-31" }, "endDate"},
		{"unknown timezone", func(in *trip.DetailsInput) { in.Timezone = "Tokyo" }, "timezone"},
		{"unknown currency", func(in *trip.DetailsInput) { in.Currency = "YEN" }, "currency"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.mutate(&in)

			_, err := trip.NewDetails(in)

			fields := fieldsOf(t, err)
			if _, ok := fields[tt.wantField]; !ok || len(fields) != 1 {
				t.Errorf("invalid fields = %v, want only %s", fields, tt.wantField)
			}
		})
	}
}

func TestNewDetails_ReportsEveryInvalidField(t *testing.T) {
	_, err := trip.NewDetails(trip.DetailsInput{})

	fields := fieldsOf(t, err)
	for _, want := range []string{"name", "destination", "startDate", "endDate", "timezone", "currency"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("missing error for %s in %v", want, fields)
		}
	}
}

func TestDetailsApply(t *testing.T) {
	base := mustDetails(t)
	str := func(s string) *string { return &s }

	t.Run("changes only the provided fields", func(t *testing.T) {
		got, err := base.Apply(trip.DetailsPatch{Name: str("Japan and Korea"), Currency: str("krw")})
		if err != nil {
			t.Fatal(err)
		}

		want := base
		want.Name, want.Currency = "Japan and Korea", "KRW"
		if got != want {
			t.Errorf("Apply() = %+v, want %+v", got, want)
		}
	})

	t.Run("validates the merged result", func(t *testing.T) {
		_, err := base.Apply(trip.DetailsPatch{StartDate: str("2027-05-01")})

		if _, ok := fieldsOf(t, err)["endDate"]; !ok {
			t.Errorf("moving start past the existing end must fail on endDate, got %v", err)
		}
	})

	t.Run("can move both dates together", func(t *testing.T) {
		got, err := base.Apply(trip.DetailsPatch{StartDate: str("2027-05-01"), EndDate: str("2027-05-10")})
		if err != nil || got.StartDate != "2027-05-01" || got.EndDate != "2027-05-10" {
			t.Errorf("Apply() = %+v, %v", got, err)
		}
	})

	t.Run("rejects clearing a required field", func(t *testing.T) {
		_, err := base.Apply(trip.DetailsPatch{Name: str("")})
		if _, ok := fieldsOf(t, err)["name"]; !ok {
			t.Errorf("Apply() error = %v, want a name error", err)
		}
	})

	t.Run("an empty patch changes nothing", func(t *testing.T) {
		got, err := base.Apply(trip.DetailsPatch{})
		if err != nil || got != base {
			t.Errorf("Apply() = %+v, %v", got, err)
		}
	})
}

func TestDetailsPatch_IsEmpty(t *testing.T) {
	name := "x"

	if !(trip.DetailsPatch{}).IsEmpty() || (trip.DetailsPatch{Name: &name}).IsEmpty() {
		t.Error("IsEmpty() does not reflect whether any field is set")
	}
}

func TestParseRole(t *testing.T) {
	for _, valid := range []string{"OWNER", "ADMIN", "MEMBER", "VIEWER"} {
		if role, err := trip.ParseRole(valid); err != nil || string(role) != valid {
			t.Errorf("ParseRole(%q) = %q, %v", valid, role, err)
		}
	}
	for _, invalid := range []string{"", "owner", "Admin", "GUEST", "OWNER "} {
		if _, err := trip.ParseRole(invalid); err == nil {
			t.Errorf("ParseRole(%q) succeeded, want an error", invalid)
		}
	}
}

func TestNew_MakesTheCreatorTheOnlyOwner(t *testing.T) {
	got := trip.New("trip-1", "ana", mustDetails(t), now)

	if got.Version != 1 || got.CreatedAt != now || got.UpdatedAt != now || got.DeletedAt != nil {
		t.Errorf("unexpected metadata: %+v", got)
	}
	if got.OwnerID() != "ana" || len(got.Members) != 1 {
		t.Errorf("members = %+v, want ana as the only member and owner", got.Members)
	}
	if role, ok := got.RoleOf("ana"); !ok || role != trip.RoleOwner {
		t.Errorf("RoleOf(ana) = %q, %v", role, ok)
	}
	if _, ok := got.RoleOf("bia"); ok {
		t.Error("RoleOf reports a non-member")
	}
}

func newTrip(t *testing.T) trip.Trip {
	t.Helper()
	got := trip.New("trip-1", "ana", mustDetails(t), now)
	later := now.Add(time.Hour)
	for id, role := range map[user.ID]trip.Role{"bia": trip.RoleAdmin, "caio": trip.RoleMember, "davi": trip.RoleViewer} {
		if err := got.AddMember(id, role, later); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

func ownerCount(tr trip.Trip) int {
	n := 0
	for _, m := range tr.Members {
		if m.Role == trip.RoleOwner {
			n++
		}
	}
	return n
}

func TestAddMember(t *testing.T) {
	t.Run("adds the member and bumps the version", func(t *testing.T) {
		tr := trip.New("trip-1", "ana", mustDetails(t), now)
		later := now.Add(time.Minute)

		if err := tr.AddMember("bia", trip.RoleMember, later); err != nil {
			t.Fatalf("AddMember() error = %v", err)
		}

		if role, _ := tr.RoleOf("bia"); role != trip.RoleMember || tr.Version != 2 || tr.UpdatedAt != later {
			t.Errorf("unexpected trip after AddMember: %+v", tr)
		}
	})

	t.Run("rejects duplicates without changing the trip", func(t *testing.T) {
		tr := newTrip(t)
		before := tr.Version

		err := tr.AddMember("caio", trip.RoleViewer, now)

		requireCode(t, err, apperror.KindConflict, "member_exists")
		if tr.Version != before {
			t.Error("a failed AddMember changed the version")
		}
	})

	t.Run("never assigns ownership", func(t *testing.T) {
		tr := trip.New("trip-1", "ana", mustDetails(t), now)

		requireCode(t, tr.AddMember("bia", trip.RoleOwner, now), apperror.KindValidation, "owner_assignment")
		if ownerCount(tr) != 1 {
			t.Error("owner invariant broken")
		}
	})
}

func TestChangeMemberRole(t *testing.T) {
	t.Run("changes the role", func(t *testing.T) {
		tr := newTrip(t)
		before := tr.Version

		if err := tr.ChangeMemberRole("caio", trip.RoleViewer, now); err != nil {
			t.Fatal(err)
		}

		if role, _ := tr.RoleOf("caio"); role != trip.RoleViewer || tr.Version != before+1 {
			t.Errorf("role = %s, version = %d", role, tr.Version)
		}
	})

	t.Run("same role is a no-op", func(t *testing.T) {
		tr := newTrip(t)
		before := tr.Version

		if err := tr.ChangeMemberRole("caio", trip.RoleMember, now); err != nil || tr.Version != before {
			t.Errorf("error = %v, version %d -> %d", err, before, tr.Version)
		}
	})

	t.Run("owner is locked", func(t *testing.T) {
		tr := newTrip(t)
		requireCode(t, tr.ChangeMemberRole("ana", trip.RoleAdmin, now), apperror.KindValidation, "owner_locked")
	})

	t.Run("cannot promote to owner", func(t *testing.T) {
		tr := newTrip(t)
		requireCode(t, tr.ChangeMemberRole("bia", trip.RoleOwner, now), apperror.KindValidation, "owner_assignment")
		if ownerCount(tr) != 1 {
			t.Error("owner invariant broken")
		}
	})

	t.Run("unknown member", func(t *testing.T) {
		tr := newTrip(t)
		requireCode(t, tr.ChangeMemberRole("zed", trip.RoleViewer, now), apperror.KindNotFound, "member_not_found")
	})
}

func TestRemoveMember(t *testing.T) {
	t.Run("removes the member", func(t *testing.T) {
		tr := newTrip(t)
		before := tr.Version

		if err := tr.RemoveMember("caio", now); err != nil {
			t.Fatal(err)
		}

		if _, ok := tr.RoleOf("caio"); ok || tr.Version != before+1 || len(tr.Members) != 3 {
			t.Errorf("unexpected trip after RemoveMember: %+v", tr)
		}
	})

	t.Run("the owner cannot be removed", func(t *testing.T) {
		tr := newTrip(t)
		requireCode(t, tr.RemoveMember("ana", now), apperror.KindValidation, "owner_locked")
	})

	t.Run("unknown member", func(t *testing.T) {
		tr := newTrip(t)
		requireCode(t, tr.RemoveMember("zed", now), apperror.KindNotFound, "member_not_found")
	})
}

func TestTransferOwnership(t *testing.T) {
	t.Run("swaps owner and demotes the previous owner to admin", func(t *testing.T) {
		tr := newTrip(t)

		if err := tr.TransferOwnership("ana", "caio", now); err != nil {
			t.Fatal(err)
		}

		ana, _ := tr.RoleOf("ana")
		caio, _ := tr.RoleOf("caio")
		if ana != trip.RoleAdmin || caio != trip.RoleOwner || tr.OwnerID() != "caio" || ownerCount(tr) != 1 {
			t.Errorf("roles after transfer: ana=%s caio=%s owners=%d", ana, caio, ownerCount(tr))
		}
	})

	t.Run("target must be a member", func(t *testing.T) {
		tr := newTrip(t)
		requireCode(t, tr.TransferOwnership("ana", "zed", now), apperror.KindNotFound, "member_not_found")
		if tr.OwnerID() != "ana" {
			t.Error("a failed transfer changed the owner")
		}
	})

	t.Run("cannot transfer to yourself", func(t *testing.T) {
		tr := newTrip(t)
		requireCode(t, tr.TransferOwnership("ana", "ana", now), apperror.KindValidation, "already_owner")
	})
}

func TestMutationsDoNotAliasTheOriginalMembers(t *testing.T) {
	original := newTrip(t)
	copyOfTrip := original

	if err := copyOfTrip.ChangeMemberRole("caio", trip.RoleViewer, now); err != nil {
		t.Fatal(err)
	}
	if err := copyOfTrip.RemoveMember("davi", now); err != nil {
		t.Fatal(err)
	}
	if err := copyOfTrip.TransferOwnership("ana", "bia", now); err != nil {
		t.Fatal(err)
	}

	if role, _ := original.RoleOf("caio"); role != trip.RoleMember || original.OwnerID() != "ana" || len(original.Members) != 4 {
		t.Errorf("mutating a copy changed the original: %+v", original.Members)
	}
}

func TestOwnerInvariantHoldsAcrossOperations(t *testing.T) {
	tr := newTrip(t)
	steps := []func() error{
		func() error { return tr.TransferOwnership("ana", "bia", now) },
		func() error { return tr.ChangeMemberRole("ana", trip.RoleMember, now) },
		func() error { return tr.TransferOwnership("bia", "caio", now) },
		func() error { return tr.RemoveMember("davi", now) },
		func() error { return tr.AddMember("eli", trip.RoleAdmin, now) },
		func() error { return tr.RemoveMember("caio", now) },
	}

	for i, step := range steps {
		_ = step()
		if ownerCount(tr) != 1 {
			t.Fatalf("after step %d there are %d owners, want exactly 1", i+1, ownerCount(tr))
		}
	}
}

func TestMarkDeleted(t *testing.T) {
	tr := newTrip(t)
	before := tr.Version
	deletedAt := now.Add(time.Hour)

	tr.MarkDeleted(deletedAt)

	if tr.DeletedAt == nil || !tr.DeletedAt.Equal(deletedAt) || tr.Version != before+1 {
		t.Errorf("unexpected trip after MarkDeleted: deletedAt=%v version=%d", tr.DeletedAt, tr.Version)
	}
}

package checklist_test

import (
	"context"
	"errors"
	"testing"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/checklist"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/resourcetest"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

const tripID = trip.ID("trip-1")

func newItems() *checklist.Items {
	authz := resourcetest.NewAuthz(map[user.ID]trip.Role{
		"owner": trip.RoleOwner, "member": trip.RoleMember, "viewer": trip.RoleViewer,
	})
	return checklist.NewItems(resourcetest.New(checklist.ItemBase), authz)
}

func requireApp(t *testing.T, err error, kind apperror.Kind, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error = %v, want kind %d code %s", err, kind, code)
	}
}

func invalidFields(t *testing.T, err error) map[string]bool {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindValidation || appErr.Code != "validation_failed" {
		t.Fatalf("error = %v, want a validation error", err)
	}
	out := map[string]bool{}
	for _, f := range appErr.Fields {
		out[f.Field] = true
	}
	return out
}

func TestCreate_DefaultsCategoryAndQuantity(t *testing.T) {
	items := newItems()
	res, err := items.Create(context.Background(), "member", tripID, checklist.ItemCreate{Title: "Passaporte"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Entity.Category != checklist.CategoryOther {
		t.Errorf("category = %q, want OTHER", res.Entity.Category)
	}
	if res.Entity.Quantity != 1 {
		t.Errorf("quantity = %d, want 1", res.Entity.Quantity)
	}
	if res.Entity.Checked {
		t.Error("a new item starts unchecked")
	}
}

func TestCreate_KeepsTheGivenCategoryAndQuantity(t *testing.T) {
	items := newItems()
	res, err := items.Create(context.Background(), "member", tripID, checklist.ItemCreate{
		Title: "Meias", Category: "CLOTHES", Quantity: 5,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Entity.Category != checklist.CategoryClothes {
		t.Errorf("category = %q, want CLOTHES", res.Entity.Category)
	}
	if res.Entity.Quantity != 5 {
		t.Errorf("quantity = %d, want 5", res.Entity.Quantity)
	}
}

func TestCreate_RejectsAnEmptyTitle(t *testing.T) {
	items := newItems()
	_, err := items.Create(context.Background(), "member", tripID, checklist.ItemCreate{})
	fields := invalidFields(t, err)
	if !fields["title"] {
		t.Errorf("fields = %v, want title", fields)
	}
}

func TestCreate_RejectsAnUnknownCategory(t *testing.T) {
	items := newItems()
	_, err := items.Create(context.Background(), "member", tripID, checklist.ItemCreate{
		Title: "X", Category: "SPACESHIP",
	})
	fields := invalidFields(t, err)
	if !fields["category"] {
		t.Errorf("fields = %v, want category", fields)
	}
}

func TestCreate_RejectsANonPositiveQuantity(t *testing.T) {
	items := newItems()
	_, err := items.Create(context.Background(), "member", tripID, checklist.ItemCreate{Title: "X", Quantity: -1})
	fields := invalidFields(t, err)
	if !fields["quantity"] {
		t.Errorf("fields = %v, want quantity", fields)
	}
}

func TestUpdate_TicksAnItemOffWithoutTouchingItsOtherFields(t *testing.T) {
	items := newItems()
	ctx := context.Background()
	created, err := items.Create(ctx, "member", tripID, checklist.ItemCreate{Title: "Carregador", Category: "ELECTRONICS"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	checked := true
	res, err := items.Update(ctx, "member", tripID, created.Entity.ID, checklist.ItemPatch{
		Versioned: kernel.Versioned{BaseVersion: &created.Entity.Version}, Checked: &checked,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.Entity.Checked {
		t.Error("checked = false, want true")
	}
	if res.Entity.Title != "Carregador" || res.Entity.Category != checklist.CategoryElectronics {
		t.Errorf("update touched fields it should not have: %+v", res.Entity)
	}
}

func TestUpdate_RejectsAStaleVersion(t *testing.T) {
	items := newItems()
	ctx := context.Background()
	created, err := items.Create(ctx, "member", tripID, checklist.ItemCreate{Title: "X"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	checked := true
	stale := created.Entity.Version + 1
	_, err = items.Update(ctx, "member", tripID, created.Entity.ID, checklist.ItemPatch{
		Versioned: kernel.Versioned{BaseVersion: &stale}, Checked: &checked,
	})
	requireApp(t, err, apperror.KindConflict, "version_conflict")
}

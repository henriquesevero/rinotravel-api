package syncengine_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/resource/resourcetest"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type fakeSource struct {
	name     string
	changes  []syncengine.Change
	applied  []syncengine.Mutation
	applyErr error
	current  syncengine.Change
	hasCur   bool
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Changes(_ context.Context, _ user.ID, _ string, after int64, limit int, _ trip.Role) ([]syncengine.Change, error) {
	var out []syncengine.Change
	for _, c := range f.changes {
		if c.Seq > after {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeSource) Current(context.Context, user.ID, string, string, trip.Role) (syncengine.Change, bool, error) {
	return f.current, f.hasCur, nil
}

func (f *fakeSource) Apply(_ context.Context, _ user.ID, _ trip.ID, m syncengine.Mutation) (syncengine.Outcome, error) {
	f.applied = append(f.applied, m)
	if f.applyErr != nil {
		return syncengine.Outcome{}, f.applyErr
	}
	return syncengine.Outcome{Version: 1, Record: map[string]any{"ok": true}}, nil
}

type memLog struct {
	mu      sync.Mutex
	records map[string]syncengine.MutationRecord
}

func (l *memLog) Find(_ context.Context, userID, id string) (syncengine.MutationRecord, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.records[userID+"/"+id]
	return r, ok, nil
}

func (l *memLog) Save(_ context.Context, r syncengine.MutationRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := r.UserID + "/" + r.MutationID
	if _, exists := l.records[key]; exists {
		return syncengine.ErrDuplicateMutation
	}
	l.records[key] = r
	return nil
}

type direct struct{}

func (direct) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func newEngine(sources ...syncengine.Source) (*syncengine.Engine, *memLog) {
	authz := resourcetest.NewAuthz(map[user.ID]trip.Role{"owner": trip.RoleOwner, "member": trip.RoleMember, "viewer": trip.RoleViewer})
	log := &memLog{records: map[string]syncengine.MutationRecord{}}
	return syncengine.NewEngine(authz, log, direct{}, sources...), log
}

func upsert(entity, id string, seq int64) syncengine.Change {
	return syncengine.Change{Entity: entity, ID: id, Op: syncengine.OpUpsert, Version: 1, Seq: seq, Record: map[string]any{"id": id}}
}

func requireApp(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestPull_MergesSourcesInSeqOrderAndPaginates(t *testing.T) {
	ctx := context.Background()
	items := &fakeSource{name: "item", changes: []syncengine.Change{upsert("item", "i1", 1), upsert("item", "i3", 3), upsert("item", "i5", 5)}}
	days := &fakeSource{name: "day", changes: []syncengine.Change{upsert("day", "d2", 2), upsert("day", "d4", 4)}}
	e, _ := newEngine(items, days)

	first, err := e.Pull(ctx, "member", "trip-1", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Changes) != 2 || first.Changes[0].ID != "i1" || first.Changes[1].ID != "d2" {
		t.Fatalf("first page = %+v", first.Changes)
	}

	second, _ := e.Pull(ctx, "member", "trip-1", first.Cursor, 2)
	third, _ := e.Pull(ctx, "member", "trip-1", second.Cursor, 2)

	var ids []string
	for _, page := range [][]syncengine.Change{second.Changes, third.Changes} {
		for _, c := range page {
			ids = append(ids, c.ID)
		}
	}
	if fmt.Sprint(ids) != "[i3 d4 i5]" || !second.HasMore || third.HasMore {
		t.Errorf("continuation = %v (hasMore %v/%v), want [i3 d4 i5] without duplicates or gaps", ids, second.HasMore, third.HasMore)
	}
}

func TestPull_EmptyResultKeepsTheCursorPosition(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(&fakeSource{name: "item", changes: []syncengine.Change{upsert("item", "i1", 1)}})
	first, _ := e.Pull(ctx, "member", "trip-1", "", 0)

	next, err := e.Pull(ctx, "member", "trip-1", first.Cursor, 0)

	if err != nil || len(next.Changes) != 0 || next.HasMore || next.Cursor == "" || next.Changes == nil {
		t.Errorf("Pull() = %+v, %v; want an empty non-nil page and a usable cursor", next, err)
	}
	again, _ := e.Pull(ctx, "member", "trip-1", next.Cursor, 0)
	if len(again.Changes) != 0 {
		t.Error("the cursor moved backwards")
	}
}

func TestPull_CursorValidation(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(&fakeSource{name: "item"})

	for _, bad := range []string{"not-base64!!", base64.RawURLEncoding.EncodeToString([]byte("nope")), base64.RawURLEncoding.EncodeToString([]byte(`{"s":-1,"t":1}`))} {
		_, err := e.Pull(ctx, "member", "trip-1", bad, 0)
		requireApp(t, err, "invalid_cursor")
	}

	stale := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"s":5,"t":%d}`, time.Now().Add(-syncengine.CursorMaxAge-time.Hour).Unix())))
	res, err := e.Pull(ctx, "member", "trip-1", stale, 0)
	if err != nil || !res.ResetRequired || len(res.Changes) != 0 {
		t.Errorf("stale cursor: %+v, %v; want resetRequired", res, err)
	}
}

func TestPull_AccessAndLimits(t *testing.T) {
	ctx := context.Background()
	many := make([]syncengine.Change, 0, 700)
	for i := 1; i <= 700; i++ {
		many = append(many, upsert("item", fmt.Sprint(i), int64(i)))
	}
	e, _ := newEngine(&fakeSource{name: "item", changes: many})

	_, err := e.Pull(ctx, "stranger", "trip-1", "", 0)
	requireApp(t, err, "trip_not_found")
	if res, _ := e.Pull(ctx, "viewer", "trip-1", "", 0); len(res.Changes) != syncengine.DefaultLimit {
		t.Errorf("default page = %d, want %d", len(res.Changes), syncengine.DefaultLimit)
	}
	if res, _ := e.Pull(ctx, "viewer", "trip-1", "", 100000); len(res.Changes) != syncengine.MaxLimit {
		t.Errorf("capped page = %d, want %d", len(res.Changes), syncengine.MaxLimit)
	}
}

func mutation(op syncengine.Operation, mutate ...func(*syncengine.Mutation)) syncengine.Mutation {
	base := int64(1)
	m := syncengine.Mutation{MutationID: ids.New(), Entity: "item", EntityID: ids.New(), Operation: op, Payload: json.RawMessage(`{"title":"x"}`)}
	if op != syncengine.OpCreate {
		m.BaseVersion = &base
	}
	for _, f := range mutate {
		f(&m)
	}
	return m
}

func TestPush_AppliesAndRecordsTheOutcome(t *testing.T) {
	ctx := context.Background()
	src := &fakeSource{name: "item"}
	e, log := newEngine(src)
	m := mutation(syncengine.OpCreate)

	results, err := e.Push(ctx, "member", "trip-1", []syncengine.Mutation{m})

	if err != nil || len(results) != 1 || results[0].Status != syncengine.StatusApplied || results[0].Version != 1 || results[0].MutationID != m.MutationID {
		t.Fatalf("Push() = %+v, %v", results, err)
	}
	if len(src.applied) != 1 || len(log.records) != 1 {
		t.Errorf("applied %d, logged %d; want 1 and 1", len(src.applied), len(log.records))
	}
}

func TestPush_ARetriedMutationIsNotAppliedTwice(t *testing.T) {
	ctx := context.Background()
	src := &fakeSource{name: "item"}
	e, _ := newEngine(src)
	m := mutation(syncengine.OpUpdate)

	first, _ := e.Push(ctx, "member", "trip-1", []syncengine.Mutation{m})
	second, err := e.Push(ctx, "member", "trip-1", []syncengine.Mutation{m})

	if err != nil || second[0].Status != syncengine.StatusDuplicate || second[0].Version != first[0].Version {
		t.Errorf("retry = %+v, %v; want a duplicate with the original outcome", second, err)
	}
	if len(src.applied) != 1 {
		t.Errorf("the source applied the mutation %d times, want once", len(src.applied))
	}
}

func TestPush_ARepeatedMutationInsideOneBatchIsAlsoDeduplicated(t *testing.T) {
	src := &fakeSource{name: "item"}
	e, _ := newEngine(src)
	m := mutation(syncengine.OpCreate)

	results, _ := e.Push(context.Background(), "member", "trip-1", []syncengine.Mutation{m, m})

	if results[0].Status != syncengine.StatusApplied || results[1].Status != syncengine.StatusDuplicate || len(src.applied) != 1 {
		t.Errorf("results = %+v, applied %d", results, len(src.applied))
	}
}

func TestPush_ReusingAMutationIdWithDifferentContentIsRefused(t *testing.T) {
	ctx := context.Background()
	src := &fakeSource{name: "item"}
	e, _ := newEngine(src)
	first := mutation(syncengine.OpCreate)
	if _, err := e.Push(ctx, "member", "trip-1", []syncengine.Mutation{first}); err != nil {
		t.Fatal(err)
	}

	tampered := first
	tampered.Payload = json.RawMessage(`{"title":"different"}`)
	results, _ := e.Push(ctx, "member", "trip-1", []syncengine.Mutation{tampered})

	if results[0].Status != syncengine.StatusRejected || results[0].Code != "mutation_id_reused" || len(src.applied) != 1 {
		t.Errorf("result = %+v, applied %d", results[0], len(src.applied))
	}
}

func TestPush_TheSameMutationIdFromAnotherUserIsIndependent(t *testing.T) {
	src := &fakeSource{name: "item"}
	e, _ := newEngine(src)
	m := mutation(syncengine.OpCreate)

	a, _ := e.Push(context.Background(), "member", "trip-1", []syncengine.Mutation{m})
	b, _ := e.Push(context.Background(), "owner", "trip-1", []syncengine.Mutation{m})

	if a[0].Status != syncengine.StatusApplied || b[0].Status != syncengine.StatusApplied || len(src.applied) != 2 {
		t.Errorf("results = %+v / %+v", a[0], b[0])
	}
}

func TestPush_InvalidMutationsAreRejectedWithoutTouchingTheSource(t *testing.T) {
	src := &fakeSource{name: "item"}
	e, log := newEngine(src)
	cases := map[string]func(*syncengine.Mutation){
		"bad mutation id":   func(m *syncengine.Mutation) { m.MutationID = "nope" },
		"unknown entity":    func(m *syncengine.Mutation) { m.Entity = "spaceship" },
		"bad entity id":     func(m *syncengine.Mutation) { m.EntityID = "nope" },
		"unknown operation": func(m *syncengine.Mutation) { m.Operation = "MERGE" },
	}
	for name, mutate := range cases {
		results, err := e.Push(context.Background(), "member", "trip-1", []syncengine.Mutation{mutation(syncengine.OpCreate, mutate)})
		if err != nil || results[0].Status != syncengine.StatusRejected {
			t.Errorf("%s: %+v, %v; want rejected", name, results, err)
		}
	}
	update := mutation(syncengine.OpUpdate, func(m *syncengine.Mutation) { m.BaseVersion = nil })
	results, _ := e.Push(context.Background(), "member", "trip-1", []syncengine.Mutation{update})
	if results[0].Code != "base_version_required" {
		t.Errorf("update without baseVersion: %+v", results[0])
	}
	if len(src.applied) != 0 || len(log.records) != 0 {
		t.Errorf("invalid mutations reached the source (%d) or the log (%d)", len(src.applied), len(log.records))
	}
}

func TestPush_VersionConflictReturnsTheServerCopyAndIsReplayedIdentically(t *testing.T) {
	ctx := context.Background()
	src := &fakeSource{
		name: "item", applyErr: apperror.Conflict("version_conflict", "stale"),
		current: syncengine.Change{Entity: "item", Op: syncengine.OpUpsert, Version: 5, Record: map[string]any{"title": "server"}}, hasCur: true,
	}
	e, _ := newEngine(src)
	m := mutation(syncengine.OpUpdate)

	first, _ := e.Push(ctx, "member", "trip-1", []syncengine.Mutation{m})
	replay, _ := e.Push(ctx, "member", "trip-1", []syncengine.Mutation{m})

	if first[0].Status != syncengine.StatusConflict || first[0].Version != 5 || first[0].Record == nil {
		t.Errorf("conflict = %+v, want the current server record", first[0])
	}
	if replay[0].Status != syncengine.StatusConflict || len(src.applied) != 1 {
		t.Errorf("replayed conflict = %+v, applied %d", replay[0], len(src.applied))
	}
}

func TestPush_UpdatingSomethingDeletedIsAConflictWithTheDeletedMarker(t *testing.T) {
	src := &fakeSource{
		name: "item", applyErr: apperror.NotFound("item_not_found", "gone"),
		current: syncengine.Change{Entity: "item", Op: syncengine.OpDelete, Version: 3}, hasCur: true,
	}
	e, _ := newEngine(src)

	results, _ := e.Push(context.Background(), "member", "trip-1", []syncengine.Mutation{mutation(syncengine.OpUpdate)})

	if results[0].Status != syncengine.StatusConflict || results[0].Code != "entity_deleted" || results[0].Version != 3 || results[0].Record != nil {
		t.Errorf("result = %+v", results[0])
	}
}

func TestPush_PermissionAndValidationErrorsAreRejectedPerMutation(t *testing.T) {
	src := &fakeSource{name: "item", applyErr: apperror.Forbidden("forbidden", "no")}
	e, _ := newEngine(src)
	ok := &fakeSource{name: "day"}
	e2, _ := newEngine(src, ok)

	results, _ := e.Push(context.Background(), "viewer", "trip-1", []syncengine.Mutation{mutation(syncengine.OpCreate)})
	if results[0].Status != syncengine.StatusRejected || results[0].Code != "forbidden" {
		t.Errorf("result = %+v", results[0])
	}

	batch := []syncengine.Mutation{
		mutation(syncengine.OpCreate),
		mutation(syncengine.OpCreate, func(m *syncengine.Mutation) { m.Entity = "day" }),
	}
	mixed, _ := e2.Push(context.Background(), "viewer", "trip-1", batch)
	if mixed[0].Status != syncengine.StatusRejected || mixed[1].Status != syncengine.StatusApplied {
		t.Errorf("one rejected mutation must not stop the others: %+v", mixed)
	}
}

func TestPush_InfrastructureFailuresAbortTheRequest(t *testing.T) {
	e, _ := newEngine(&fakeSource{name: "item", applyErr: errors.New("connection reset")})

	_, err := e.Push(context.Background(), "member", "trip-1", []syncengine.Mutation{mutation(syncengine.OpCreate)})

	var appErr *apperror.Error
	if err == nil || errors.As(err, &appErr) {
		t.Errorf("error = %v, want a plain infrastructure error", err)
	}
}

func TestPush_AccessAndBatchLimit(t *testing.T) {
	e, _ := newEngine(&fakeSource{name: "item"})

	_, err := e.Push(context.Background(), "stranger", "trip-1", []syncengine.Mutation{mutation(syncengine.OpCreate)})
	requireApp(t, err, "trip_not_found")

	tooMany := make([]syncengine.Mutation, syncengine.MaxMutations+1)
	_, err = e.Push(context.Background(), "member", "trip-1", tooMany)
	requireApp(t, err, "too_many_mutations")
}

func TestPush_ClientTimestampNeverInfluencesTheResult(t *testing.T) {
	src := &fakeSource{name: "item"}
	e, _ := newEngine(src)
	future := time.Now().Add(400 * 24 * time.Hour)
	m := mutation(syncengine.OpCreate, func(m *syncengine.Mutation) { m.ClientTimestamp = &future })

	results, _ := e.Push(context.Background(), "member", "trip-1", []syncengine.Mutation{m})

	if results[0].Status != syncengine.StatusApplied {
		t.Errorf("result = %+v; a device clock must not matter", results[0])
	}
}

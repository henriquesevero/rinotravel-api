package kernel_test

import (
	"strings"
	"testing"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
)

func fieldMap(t *testing.T, err error) map[string]string {
	t.Helper()
	appErr, ok := err.(*apperror.Error)
	if !ok || appErr.Kind != apperror.KindValidation {
		t.Fatalf("error = %v, want a validation error", err)
	}
	out := map[string]string{}
	for _, f := range appErr.Fields {
		out[f.Field] = f.Message
	}
	return out
}

func TestBase_Lifecycle(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := kernel.NewBase("id", "trip", now)

	if b.Version != 1 || b.IsDeleted() || !b.CreatedAt.Equal(now) {
		t.Fatalf("unexpected new base: %+v", b)
	}
	b.Touch(now.Add(time.Hour))
	if b.Version != 2 || !b.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("Touch() = %+v", b)
	}
	b.MarkDeleted(now.Add(2 * time.Hour))
	if !b.IsDeleted() || b.Version != 3 {
		t.Errorf("MarkDeleted() = %+v", b)
	}
}

func TestValidator(t *testing.T) {
	var v kernel.Validator
	v.Text("title", "   ", true, 10)
	v.Text("notes", strings.Repeat("a", 11), false, 10)
	v.HTTPURL("url", "javascript:alert(1)")
	v.HTTPURL("ok", "https://example.com/x")
	v.HTTPURL("empty", "")

	fields := fieldMap(t, v.Err())
	if len(fields) != 3 || fields["title"] == "" || fields["notes"] == "" || fields["url"] == "" {
		t.Errorf("fields = %v", fields)
	}
	if trimmed := new(kernel.Validator).Text("x", "  hi ", true, 10); trimmed != "hi" {
		t.Errorf("Text() = %q, want it trimmed", trimmed)
	}
	if (&kernel.Validator{}).Err() != nil {
		t.Error("an empty validator must have no error")
	}
}

func f(v float64) *float64 { return &v }

func TestValidatorLocation(t *testing.T) {
	tests := []struct {
		name    string
		in      kernel.LocationInput
		wantErr bool
	}{
		{"name only", kernel.LocationInput{Name: "Hotel"}, false},
		{"valid coordinates", kernel.LocationInput{Latitude: f(35.68), Longitude: f(139.76)}, false},
		{"null island is a real point", kernel.LocationInput{Latitude: f(0), Longitude: f(0)}, false},
		{"latitude only", kernel.LocationInput{Latitude: f(10)}, true},
		{"longitude only", kernel.LocationInput{Longitude: f(10)}, true},
		{"latitude out of range", kernel.LocationInput{Latitude: f(91), Longitude: f(0)}, true},
		{"longitude out of range", kernel.LocationInput{Latitude: f(0), Longitude: f(-181)}, true},
	}
	for _, tt := range tests {
		var v kernel.Validator
		loc := v.Location("location", tt.in)
		if v.HasErrors() != tt.wantErr {
			t.Errorf("%s: errors = %v, want %v", tt.name, v.Err(), tt.wantErr)
		}
		if tt.name == "null island is a real point" && (loc.Coordinates == nil) {
			t.Errorf("%s: coordinates were dropped", tt.name)
		}
	}
}

func TestValidatorMoney(t *testing.T) {
	var ok kernel.Validator
	if m := ok.Money("cost", &kernel.MoneyInput{Amount: 1500, Currency: "brl"}); ok.HasErrors() || m == nil || m.Currency != "BRL" || m.Amount != 1500 {
		t.Errorf("valid money rejected: %v %+v", ok.Err(), m)
	}
	if m := ok.Money("cost", nil); m != nil {
		t.Error("nil input must stay nil")
	}

	var bad kernel.Validator
	bad.Money("a", &kernel.MoneyInput{Amount: -1, Currency: "BRL"})
	bad.Money("b", &kernel.MoneyInput{Amount: 1, Currency: "ZZZ"})
	if fields := fieldMap(t, bad.Err()); fields["a.amount"] == "" || fields["b.currency"] == "" {
		t.Errorf("fields = %v", fields)
	}
}

func TestSumMoney(t *testing.T) {
	brl := func(n int64) *kernel.Money { return &kernel.Money{Amount: n, Currency: "BRL"} }

	total, err := kernel.SumMoney([]*kernel.Money{brl(100), nil, brl(250)})
	if err != nil || total == nil || total.Amount != 350 {
		t.Errorf("SumMoney() = %+v, %v", total, err)
	}
	if none, err := kernel.SumMoney(nil); err != nil || none != nil {
		t.Errorf("SumMoney(nil) = %+v, %v", none, err)
	}
	if _, err := kernel.SumMoney([]*kernel.Money{brl(1), {Amount: 1, Currency: "USD"}}); err == nil {
		t.Error("mixing currencies must fail")
	}
}

func TestZonedTime(t *testing.T) {
	tokyo, err := kernel.ParseZonedTime("2027-04-01T09:30", "Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	if got := tokyo.Instant.Format(time.RFC3339); got != "2027-04-01T00:30:00Z" {
		t.Errorf("instant = %s, want 09:30 JST as 00:30Z", got)
	}
	if tokyo.LocalString() != "2027-04-01T09:30" || tokyo.LocalDate() != "2027-04-01" {
		t.Errorf("local = %s %s", tokyo.LocalString(), tokyo.LocalDate())
	}

	late, _ := kernel.ParseZonedTime("2027-04-01T23:30", "America/Sao_Paulo")
	if late.LocalDate() != "2027-04-01" || late.Instant.Format("2006-01-02") != "2027-04-02" {
		t.Errorf("the local day must differ from the UTC day: local %s, utc %s", late.LocalDate(), late.Instant)
	}

	for _, bad := range []string{"", "2027-04-01", "01/04/2027 09:30", "2027-04-01T25:00"} {
		if _, err := kernel.ParseZonedTime(bad, "Asia/Tokyo"); err == nil {
			t.Errorf("ParseZonedTime(%q) succeeded", bad)
		}
	}
	if _, err := kernel.ParseZonedTime("2027-04-01T09:30", "Mars/Base"); err == nil {
		t.Error("an unknown zone must fail")
	}
}

func TestZonedTimeAcrossZonesMeasuresRealDuration(t *testing.T) {
	depart, _ := kernel.ParseZonedTime("2027-04-01T22:00", "America/Sao_Paulo")
	arrive, _ := kernel.ParseZonedTime("2027-04-02T12:00", "Europe/Lisbon")

	if got := arrive.Instant.Sub(depart.Instant); got != 10*time.Hour {
		t.Errorf("duration = %v, want 10h (22:00 BRT to 12:00 WEST)", got)
	}
	if !depart.Before(arrive) {
		t.Error("Before() disagrees with the instants")
	}
}

func TestValidatorZonedTime(t *testing.T) {
	var v kernel.Validator
	got := v.ZonedTime("start", &kernel.ZonedTimeInput{DateTime: "2027-04-01T09:00"}, "Asia/Tokyo")
	if v.HasErrors() || got == nil || got.Zone != "Asia/Tokyo" {
		t.Errorf("fallback zone not applied: %v %+v", v.Err(), got)
	}
	if v.ZonedTime("none", nil, "UTC") != nil {
		t.Error("nil input must stay nil")
	}

	var bad kernel.Validator
	bad.ZonedTime("a", &kernel.ZonedTimeInput{DateTime: "nope"}, "UTC")
	bad.ZonedTime("b", &kernel.ZonedTimeInput{DateTime: "2027-04-01T09:00", Timezone: "Mars/Base"}, "UTC")
	if fields := fieldMap(t, bad.Err()); fields["a.dateTime"] == "" || fields["b.timezone"] == "" {
		t.Errorf("fields = %v", fields)
	}
}

package kernel_test

import (
	"testing"

	"rinotravel-api/internal/kernel"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"2026-09-19", false},
		{"2028-02-29", false},
		{"2026-02-29", true},
		{"2026-13-01", true},
		{"2026-9-1", true},
		{"19/09/2026", true},
		{"2026-09-19T10:00:00Z", true},
		{" 2026-09-19", true},
		{"", true},
	}

	for _, tt := range tests {
		got, err := kernel.ParseDate(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseDate(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if err == nil && string(got) != tt.in {
			t.Errorf("ParseDate(%q) = %q", tt.in, got)
		}
	}
}

func TestDate_BeforeIsChronological(t *testing.T) {
	early, _ := kernel.ParseDate("2026-09-19")
	late, _ := kernel.ParseDate("2026-10-01")
	yearEnd, _ := kernel.ParseDate("2026-12-31")
	nextYear, _ := kernel.ParseDate("2027-01-01")

	if !early.Before(late) || late.Before(early) || early.Before(early) {
		t.Error("Before() is not a strict chronological order within a year")
	}
	if !yearEnd.Before(nextYear) {
		t.Error("Before() does not order across years")
	}
}

func TestParseTimezone(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"America/Sao_Paulo", false},
		{"Europe/Lisbon", false},
		{"Asia/Tokyo", false},
		{"UTC", false},
		{"", true},
		{"Local", true},
		{"Mars/Olympus_Mons", true},
		{"../../etc/passwd", true},
		{"America/Sao_Paulo ", true},
		{"BRT", true},
	}

	for _, tt := range tests {
		got, err := kernel.ParseTimezone(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseTimezone(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if err == nil && string(got) != tt.in {
			t.Errorf("ParseTimezone(%q) = %q", tt.in, got)
		}
	}
}

func TestParseCurrency(t *testing.T) {
	tests := []struct {
		in      string
		want    kernel.Currency
		wantErr bool
	}{
		{"BRL", "BRL", false},
		{"usd", "USD", false},
		{" eur ", "EUR", false},
		{"JPY", "JPY", false},
		{"ZZZ", "", true},
		{"BR", "", true},
		{"BRLL", "", true},
		{"", "", true},
		{"XAU", "", true},
		{"HRK", "", true},
	}

	for _, tt := range tests {
		got, err := kernel.ParseCurrency(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseCurrency(%q) = %q, %v; want %q, wantErr %v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

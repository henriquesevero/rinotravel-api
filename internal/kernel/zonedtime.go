package kernel

import (
	"errors"
	"time"
)

const (
	localLayout        = "2006-01-02T15:04"
	localLayoutSeconds = "2006-01-02T15:04:05"
)

// ZonedTime is an event moment: an absolute instant plus the IANA zone of the place where it
// happens. The zone decides which calendar day the event belongs to.
type ZonedTime struct {
	Instant time.Time
	Zone    Timezone
}

type ZonedTimeInput struct {
	DateTime string `json:"dateTime"`
	Timezone string `json:"timezone"`
}

func ParseZonedTime(dateTime string, zone Timezone) (ZonedTime, error) {
	location, err := time.LoadLocation(string(zone))
	if err != nil {
		return ZonedTime{}, errors.New("timezone must be an IANA time zone")
	}
	for _, layout := range []string{localLayout, localLayoutSeconds} {
		if at, err := time.ParseInLocation(layout, dateTime, location); err == nil {
			return ZonedTime{Instant: at.UTC(), Zone: zone}, nil
		}
	}
	return ZonedTime{}, errors.New("dateTime must be a local date and time such as 2027-04-01T09:30")
}

func (z ZonedTime) Local() time.Time {
	location, err := time.LoadLocation(string(z.Zone))
	if err != nil {
		return z.Instant
	}
	return z.Instant.In(location)
}

func (z ZonedTime) LocalString() string {
	return z.Local().Format(localLayout)
}

func (z ZonedTime) LocalDate() Date {
	return Date(z.Local().Format(dateLayout))
}

func (z ZonedTime) Before(other ZonedTime) bool {
	return z.Instant.Before(other.Instant)
}

// ZonedTime parses the input; an empty timezone falls back to fallback (usually the trip's zone).
func (v *Validator) ZonedTime(field string, in *ZonedTimeInput, fallback Timezone) *ZonedTime {
	if in == nil {
		return nil
	}
	zone := fallback
	if in.Timezone != "" {
		parsed, err := ParseTimezone(in.Timezone)
		if err != nil {
			v.Add(field+".timezone", err.Error())
			return nil
		}
		zone = parsed
	}
	parsed, err := ParseZonedTime(in.DateTime, zone)
	if err != nil {
		v.Add(field+".dateTime", err.Error())
		return nil
	}
	return &parsed
}

// TimelineEntry is one line of a day's timeline, produced by any feature that has a moment in time.
type TimelineEntry struct {
	Kind     string
	ID       string
	Title    string
	Subtitle string
	Status   string
	Start    *ZonedTime
	End      *ZonedTime
	// Day is the local calendar day the entry is shown under.
	Day Date
	// Position orders entries that have no start time.
	Position int
}

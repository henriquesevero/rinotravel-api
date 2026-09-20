package kernel

import (
	"errors"
	"time"
)

const dateLayout = time.DateOnly

// Date is a calendar date (YYYY-MM-DD) with no time zone. The ISO layout makes
// string comparison chronological.
type Date string

func ParseDate(s string) (Date, error) {
	if _, err := time.Parse(dateLayout, s); err != nil {
		return "", errors.New("must be a valid date in YYYY-MM-DD format")
	}
	return Date(s), nil
}

func (d Date) Before(other Date) bool {
	return d < other
}

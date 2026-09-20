package kernel

import (
	"errors"
	"time"
	_ "time/tzdata"
)

type Timezone string

func ParseTimezone(s string) (Timezone, error) {
	if s == "" || s == "Local" {
		return "", errors.New("must be an IANA time zone such as America/Sao_Paulo")
	}
	if _, err := time.LoadLocation(s); err != nil {
		return "", errors.New("must be an IANA time zone such as America/Sao_Paulo")
	}
	return Timezone(s), nil
}

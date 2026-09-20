package kernel

import "errors"

// PlanStatus is how far along something in the plan is. Itinerary items and transfers share it.
type PlanStatus string

const (
	StatusPlanned   PlanStatus = "PLANNED"
	StatusConfirmed PlanStatus = "CONFIRMED"
	StatusCompleted PlanStatus = "COMPLETED"
	StatusSkipped   PlanStatus = "SKIPPED"
)

func ParsePlanStatus(s string) (PlanStatus, error) {
	switch status := PlanStatus(s); status {
	case StatusPlanned, StatusConfirmed, StatusCompleted, StatusSkipped:
		return status, nil
	}
	return "", errors.New("must be one of PLANNED, CONFIRMED, COMPLETED, SKIPPED")
}

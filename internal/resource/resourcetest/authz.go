package resourcetest

import (
	"context"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

// Authz is a resource.Authorizer for tests: users listed in Roles are members of every trip id
// (except "missing"), and Details describe the trip so entities can validate against its dates,
// zone and currency.
type Authz struct {
	Roles   map[user.ID]trip.Role
	Details trip.Details
}

func NewAuthz(roles map[user.ID]trip.Role) Authz {
	return Authz{
		Roles: roles,
		Details: trip.Details{
			Name:        "Japan",
			Destination: "Tokyo",
			StartDate:   "2027-04-01",
			EndDate:     "2027-04-15",
			Timezone:    kernel.Timezone("Asia/Tokyo"),
			Currency:    kernel.Currency("JPY"),
		},
	}
}

func (a Authz) Authorize(_ context.Context, tripID trip.ID, actor user.ID, action trip.Action) (trip.Access, error) {
	role, ok := a.Roles[actor]
	if !ok || tripID == "missing" {
		return trip.Access{}, apperror.NotFound("trip_not_found", "Trip not found.")
	}
	if !trip.Can(role, action) {
		return trip.Access{}, apperror.Forbidden("forbidden", "You do not have permission to perform this action.")
	}
	return trip.Access{Role: role, Trip: trip.Trip{ID: tripID, Details: a.Details}}, nil
}

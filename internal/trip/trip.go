package trip

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/user"
)

const (
	maxNameLength        = 100
	maxDestinationLength = 100
)

var (
	ErrNotFound        = errors.New("trip not found")
	ErrAlreadyExists   = errors.New("trip already exists")
	ErrVersionConflict = errors.New("trip version conflict")
)

type ID string

type Role string

const (
	RoleOwner  Role = "OWNER"
	RoleAdmin  Role = "ADMIN"
	RoleMember Role = "MEMBER"
	RoleViewer Role = "VIEWER"
)

func ParseRole(s string) (Role, error) {
	switch role := Role(s); role {
	case RoleOwner, RoleAdmin, RoleMember, RoleViewer:
		return role, nil
	}
	return "", errors.New("must be one of OWNER, ADMIN, MEMBER, VIEWER")
}

type Member struct {
	UserID    user.ID
	Role      Role
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Details struct {
	Name        string
	Destination string
	StartDate   kernel.Date
	EndDate     kernel.Date
	Timezone    kernel.Timezone
	Currency    kernel.Currency
}

type DetailsInput struct {
	Name        string
	Destination string
	StartDate   string
	EndDate     string
	Timezone    string
	Currency    string
}

type DetailsPatch struct {
	Name        *string
	Destination *string
	StartDate   *string
	EndDate     *string
	Timezone    *string
	Currency    *string
}

func (p DetailsPatch) IsEmpty() bool {
	return p == DetailsPatch{}
}

func NewDetails(in DetailsInput) (Details, error) {
	var fields []apperror.FieldError
	invalid := func(field, message string) {
		fields = append(fields, apperror.FieldError{Field: field, Message: message})
	}

	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		invalid("name", "is required and must have at most 100 characters")
	}

	destination := strings.TrimSpace(in.Destination)
	if destination == "" || utf8.RuneCountInString(destination) > maxDestinationLength {
		invalid("destination", "is required and must have at most 100 characters")
	}

	start, startErr := kernel.ParseDate(in.StartDate)
	if startErr != nil {
		invalid("startDate", startErr.Error())
	}
	end, endErr := kernel.ParseDate(in.EndDate)
	if endErr != nil {
		invalid("endDate", endErr.Error())
	}
	if startErr == nil && endErr == nil && end.Before(start) {
		invalid("endDate", "must not be before startDate")
	}

	timezone, err := kernel.ParseTimezone(in.Timezone)
	if err != nil {
		invalid("timezone", err.Error())
	}

	currency, err := kernel.ParseCurrency(in.Currency)
	if err != nil {
		invalid("currency", err.Error())
	}

	if len(fields) > 0 {
		return Details{}, apperror.Validation(fields...)
	}
	return Details{
		Name:        name,
		Destination: destination,
		StartDate:   start,
		EndDate:     end,
		Timezone:    timezone,
		Currency:    currency,
	}, nil
}

func (d Details) Apply(p DetailsPatch) (Details, error) {
	in := DetailsInput{
		Name:        d.Name,
		Destination: d.Destination,
		StartDate:   string(d.StartDate),
		EndDate:     string(d.EndDate),
		Timezone:    string(d.Timezone),
		Currency:    string(d.Currency),
	}
	override := func(target *string, value *string) {
		if value != nil {
			*target = *value
		}
	}
	override(&in.Name, p.Name)
	override(&in.Destination, p.Destination)
	override(&in.StartDate, p.StartDate)
	override(&in.EndDate, p.EndDate)
	override(&in.Timezone, p.Timezone)
	override(&in.Currency, p.Currency)
	return NewDetails(in)
}

type Trip struct {
	ID ID
	Details
	Members   []Member
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

func New(id ID, ownerID user.ID, details Details, now time.Time) Trip {
	return Trip{
		ID:        id,
		Details:   details,
		Members:   []Member{{UserID: ownerID, Role: RoleOwner, CreatedAt: now, UpdatedAt: now}},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (t Trip) RoleOf(userID user.ID) (Role, bool) {
	for _, m := range t.Members {
		if m.UserID == userID {
			return m.Role, true
		}
	}
	return "", false
}

func (t Trip) OwnerID() user.ID {
	for _, m := range t.Members {
		if m.Role == RoleOwner {
			return m.UserID
		}
	}
	return ""
}

func (t *Trip) UpdateDetails(details Details, now time.Time) {
	t.Details = details
	t.touch(now)
}

func (t *Trip) MarkDeleted(now time.Time) {
	t.DeletedAt = &now
	t.touch(now)
}

func (t *Trip) AddMember(userID user.ID, role Role, now time.Time) error {
	if role == RoleOwner {
		return errOwnershipAssignment()
	}
	if _, exists := t.RoleOf(userID); exists {
		return apperror.Conflict("member_exists", "The user is already a member of this trip.")
	}
	t.Members = append(slices.Clone(t.Members), Member{UserID: userID, Role: role, CreatedAt: now, UpdatedAt: now})
	t.touch(now)
	return nil
}

func (t *Trip) ChangeMemberRole(userID user.ID, role Role, now time.Time) error {
	i, err := t.memberIndex(userID)
	if err != nil {
		return err
	}
	if t.Members[i].Role == RoleOwner {
		return errOwnerLocked("The owner's role cannot be changed. Transfer ownership instead.")
	}
	if role == RoleOwner {
		return errOwnershipAssignment()
	}
	if t.Members[i].Role == role {
		return nil
	}
	t.Members = slices.Clone(t.Members)
	t.Members[i].Role = role
	t.Members[i].UpdatedAt = now
	t.touch(now)
	return nil
}

func (t *Trip) RemoveMember(userID user.ID, now time.Time) error {
	i, err := t.memberIndex(userID)
	if err != nil {
		return err
	}
	if t.Members[i].Role == RoleOwner {
		return errOwnerLocked("The owner cannot leave or be removed. Transfer ownership first.")
	}
	t.Members = slices.Delete(slices.Clone(t.Members), i, i+1)
	t.touch(now)
	return nil
}

func (t *Trip) TransferOwnership(fromID, toID user.ID, now time.Time) error {
	if fromID == toID {
		return apperror.Unprocessable("already_owner", "The user is already the owner of this trip.")
	}
	to, err := t.memberIndex(toID)
	if err != nil {
		return err
	}
	from, err := t.memberIndex(fromID)
	if err != nil {
		return err
	}
	t.Members = slices.Clone(t.Members)
	t.Members[to].Role = RoleOwner
	t.Members[to].UpdatedAt = now
	t.Members[from].Role = RoleAdmin
	t.Members[from].UpdatedAt = now
	t.touch(now)
	return nil
}

func (t *Trip) memberIndex(userID user.ID) (int, error) {
	for i, m := range t.Members {
		if m.UserID == userID {
			return i, nil
		}
	}
	return 0, apperror.NotFound("member_not_found", "The user is not a member of this trip.")
}

func (t *Trip) touch(now time.Time) {
	t.Version++
	t.UpdatedAt = now
}

func errOwnerLocked(message string) *apperror.Error {
	return apperror.Unprocessable("owner_locked", message)
}

func errOwnershipAssignment() *apperror.Error {
	return apperror.Unprocessable("owner_assignment", "Ownership can only be assigned by transferring it.")
}

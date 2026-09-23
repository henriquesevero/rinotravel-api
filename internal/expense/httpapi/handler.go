package httpapi

import (
	"log/slog"
	"net/http"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/expense"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
)

type Deps struct {
	Logger   *slog.Logger
	Guard    authapi.Guard
	Expenses *expense.Expenses
	Limits   *expense.Limits
	Payments *expense.Payments
}

type Handler struct {
	Expenses httpres.Routes[expense.Expense, expense.Create, expense.Patch]
	Limits   httpres.Routes[expense.Limit, expense.LimitCreate, expense.LimitPatch]
	Payments httpres.Routes[expense.Payment, expense.PaymentCreate, expense.PaymentPatch]
}

func New(d Deps) *Handler {
	return &Handler{
		Expenses: httpres.Routes[expense.Expense, expense.Create, expense.Patch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/expenses",
			Service: d.Expenses.Resource(), Create: d.Expenses.Create, Update: d.Expenses.Update,
			Present: func(e expense.Expense, _ trip.Role) any { return presentExpense(e) },
		},
		Limits: httpres.Routes[expense.Limit, expense.LimitCreate, expense.LimitPatch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/budget-limits",
			Service: d.Limits.Resource(), Create: d.Limits.Create, Update: d.Limits.Update,
			Present: func(l expense.Limit, _ trip.Role) any { return presentLimit(l) },
		},
		Payments: httpres.Routes[expense.Payment, expense.PaymentCreate, expense.PaymentPatch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/payments",
			Service: d.Payments.Resource(), Create: d.Payments.Create, Update: d.Payments.Update,
			Present: func(p expense.Payment, _ trip.Role) any { return presentPayment(p) },
		},
	}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	h.Expenses.Mount(mux)
	h.Limits.Mount(mux)
	h.Payments.Mount(mux)
}

func (h *Handler) SyncSources() []syncengine.Source {
	return []syncengine.Source{h.Expenses.SyncSource("expense"), h.Limits.SyncSource("budget_limit"), h.Payments.SyncSource("payment")}
}

type LinkDTO struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type ExpenseResponse struct {
	httpres.Meta
	Name     string            `json:"name"`
	Category string            `json:"category"`
	Status   string            `json:"status"`
	Estimate *httpres.MoneyDTO `json:"estimate,omitempty"`
	Actual   *httpres.MoneyDTO `json:"actual,omitempty"`
	Date     string            `json:"date,omitempty"`
	Link     *LinkDTO          `json:"link,omitempty"`
	Notes    string            `json:"notes,omitempty"`
}

func presentExpense(e expense.Expense) ExpenseResponse {
	resp := ExpenseResponse{
		Meta: httpres.MetaOf(e.Base), Name: e.Name, Category: string(e.Category), Status: string(e.Status),
		Estimate: httpres.MoneyOf(e.Estimate), Actual: httpres.MoneyOf(e.Actual), Date: string(e.Date), Notes: e.Notes,
	}
	if e.LinkType != "" {
		resp.Link = &LinkDTO{Type: e.LinkType, ID: e.LinkID}
	}
	return resp
}

type LimitResponse struct {
	httpres.Meta
	Category string           `json:"category"`
	Amount   httpres.MoneyDTO `json:"amount"`
}

func presentLimit(l expense.Limit) LimitResponse {
	return LimitResponse{Meta: httpres.MetaOf(l.Base), Category: l.Category, Amount: *httpres.MoneyOf(&l.Amount)}
}

type PaymentResponse struct {
	httpres.Meta
	Link LinkDTO `json:"link"`
	Paid bool    `json:"paid"`
}

func presentPayment(p expense.Payment) PaymentResponse {
	return PaymentResponse{Meta: httpres.MetaOf(p.Base), Link: LinkDTO{Type: p.LinkType, ID: p.LinkID}, Paid: p.Paid}
}

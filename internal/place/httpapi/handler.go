package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"

	authapi "rinotravel-api/internal/auth/httpapi"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/place"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
)

type Deps struct {
	Logger      *slog.Logger
	Guard       authapi.Guard
	Places      *place.Places
	Restaurants *place.Restaurants
	// Search and SearchLimiter are optional: without a places provider the search route is not mounted.
	Search        *place.SearchPlaces
	SearchLimiter *httpx.RateLimiter
}

type Handler struct {
	deps        Deps
	Places      httpres.Routes[place.Place, place.PlaceCreate, place.PlacePatch]
	Restaurants httpres.Routes[place.Restaurant, place.RestaurantCreate, place.RestaurantPatch]
}

func New(d Deps) *Handler {
	return &Handler{
		deps: d,
		Places: httpres.Routes[place.Place, place.PlaceCreate, place.PlacePatch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/places",
			Service: d.Places.Resource(), Create: d.Places.Create, Update: d.Places.Update,
			Present: func(p place.Place, _ trip.Role) any { return presentPlace(p) },
		},
		Restaurants: httpres.Routes[place.Restaurant, place.RestaurantCreate, place.RestaurantPatch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/restaurants",
			Service: d.Restaurants.Resource(), Create: d.Restaurants.Create, Update: d.Restaurants.Update,
			Present: func(r place.Restaurant, role trip.Role) any { return presentRestaurant(r, role) },
		},
	}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	h.Places.Mount(mux)
	h.Restaurants.Mount(mux)
	if h.deps.Search != nil {
		limited := h.deps.SearchLimiter.Wrap(h.search)
		mux.Handle("GET /api/v1/places/search", httpx.Handle(h.deps.Logger, h.deps.Guard.Require(limited)))
	}
}

type candidateResponse struct {
	ProviderID string   `json:"providerId"`
	Name       string   `json:"name"`
	Address    string   `json:"address,omitempty"`
	Latitude   *float64 `json:"latitude,omitempty"`
	Longitude  *float64 `json:"longitude,omitempty"`
	Types      []string `json:"types"`
}

// search looks places up in the external directory. Results are suggestions: nothing is stored
// until the user saves one as a place of their trip.
func (h *Handler) search(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	in := place.SearchInput{Query: q.Get("q"), Language: q.Get("language")}
	for key, target := range map[string]**float64{"lat": &in.Latitude, "lng": &in.Longitude} {
		if raw := q.Get(key); raw != "" {
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return apperror.BadRequest("invalid_"+key, key+" must be a number.")
			}
			*target = &value
		}
	}
	found, err := h.deps.Search.Execute(r.Context(), authapi.UserID(r.Context()), in)
	if err != nil {
		return err
	}
	items := make([]candidateResponse, 0, len(found))
	for _, c := range found {
		item := candidateResponse{ProviderID: c.ProviderID, Name: c.Name, Address: c.Address, Types: c.Types}
		if item.Types == nil {
			item.Types = []string{}
		}
		if c.Coordinates != nil {
			lat, lng := c.Coordinates.Lat, c.Coordinates.Lng
			item.Latitude, item.Longitude = &lat, &lng
		}
		items = append(items, item)
	}
	httpx.WriteJSON(w, http.StatusOK, httpres.List[candidateResponse]{Items: items})
	return nil
}

func (h *Handler) SyncSources() []syncengine.Source {
	return []syncengine.Source{h.Places.SyncSource("place"), h.Restaurants.SyncSource("restaurant")}
}

type PlaceResponse struct {
	httpres.Meta
	Name                     string               `json:"name"`
	Description              string               `json:"description,omitempty"`
	Category                 string               `json:"category"`
	Priority                 string               `json:"priority"`
	Location                 *httpres.LocationDTO `json:"location,omitempty"`
	EstimatedDurationMinutes *int                 `json:"estimatedDurationMinutes,omitempty"`
	EstimatedCost            *httpres.MoneyDTO    `json:"estimatedCost,omitempty"`
	Notes                    string               `json:"notes,omitempty"`
	ExternalID               string               `json:"externalId,omitempty"`
}

func presentPlace(p place.Place) PlaceResponse {
	return PlaceResponse{
		Meta: httpres.MetaOf(p.Base), Name: p.Name, Description: p.Description, Category: string(p.Category),
		Priority: string(p.Priority), Location: httpres.LocationOf(p.Location),
		EstimatedDurationMinutes: p.DurationMinutes, EstimatedCost: httpres.MoneyOf(p.Cost),
		Notes: p.Notes, ExternalID: p.ExternalID,
	}
}

type RestaurantResponse struct {
	httpres.Meta
	Name            string                `json:"name"`
	Cuisine         string                `json:"cuisine,omitempty"`
	Status          string                `json:"status"`
	Location        *httpres.LocationDTO  `json:"location,omitempty"`
	EstimatedCost   *httpres.MoneyDTO     `json:"estimatedCost,omitempty"`
	ReservationAt   *httpres.ZonedTimeDTO `json:"reservationAt,omitempty"`
	ReservationCode string                `json:"reservationCode,omitempty"`
	DesiredDishes   []string              `json:"desiredDishes"`
	Notes           string                `json:"notes,omitempty"`
	ExternalID      string                `json:"externalId,omitempty"`
}

// presentRestaurant hides the reservation code from roles that cannot edit the trip. Sync uses the
// same presenter, so an offline copy never carries what a viewer may not see.
func presentRestaurant(r place.Restaurant, role trip.Role) RestaurantResponse {
	resp := RestaurantResponse{
		Meta: httpres.MetaOf(r.Base), Name: r.Name, Cuisine: r.Cuisine, Status: string(r.Status),
		Location: httpres.LocationOf(r.Location), EstimatedCost: httpres.MoneyOf(r.Cost),
		ReservationAt: httpres.ZonedOf(r.ReservationAt), DesiredDishes: r.DesiredDishes,
		Notes: r.Notes, ExternalID: r.ExternalID,
	}
	if resp.DesiredDishes == nil {
		resp.DesiredDishes = []string{}
	}
	if trip.Can(role, trip.ActionWriteContent) {
		resp.ReservationCode = r.ReservationCode
	}
	return resp
}

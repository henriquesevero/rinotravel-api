package kernel

import "math"

type Coordinates struct {
	Lat float64
	Lng float64
}

// Location is a named place. Coordinates are optional and always come as a pair.
type Location struct {
	Name        string
	Address     string
	Coordinates *Coordinates
}

func (l Location) IsZero() bool {
	return l.Name == "" && l.Address == "" && l.Coordinates == nil
}

type LocationInput struct {
	Name      string   `json:"name"`
	Address   string   `json:"address"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

// Location validates the input under the given field prefix (for example "location").
func (v *Validator) Location(field string, in LocationInput) Location {
	loc := Location{
		Name:    v.Text(field+".name", in.Name, false, 200),
		Address: v.Text(field+".address", in.Address, false, 300),
	}
	switch {
	case in.Latitude == nil && in.Longitude == nil:
	case in.Latitude == nil || in.Longitude == nil:
		v.Add(field, "latitude and longitude must be provided together")
	default:
		lat, lng := *in.Latitude, *in.Longitude
		v.Check(!math.IsNaN(lat) && lat >= -90 && lat <= 90, field+".latitude", "must be between -90 and 90")
		v.Check(!math.IsNaN(lng) && lng >= -180 && lng <= 180, field+".longitude", "must be between -180 and 180")
		loc.Coordinates = &Coordinates{Lat: lat, Lng: lng}
	}
	return loc
}

package kernel

// MapImage is a map picture drawn on request. It is handed to the client and never stored: the
// provider's terms do not allow keeping its content.
type MapImage struct {
	Data        []byte
	ContentType string
}

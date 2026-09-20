package google

import "math"

type coord struct{ lat, lng float64 }

// decodePolyline reads Google's encoded polyline format (precision 1e5).
func decodePolyline(encoded string) []coord {
	var points []coord
	var lat, lng, index int
	next := func() (int, bool) {
		var result, shift int
		for {
			if index >= len(encoded) {
				return 0, false
			}
			b := int(encoded[index]) - 63
			index++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 {
				break
			}
		}
		if result&1 != 0 {
			return ^(result >> 1), true
		}
		return result >> 1, true
	}
	for index < len(encoded) {
		dLat, ok := next()
		if !ok {
			break
		}
		dLng, ok := next()
		if !ok {
			break
		}
		lat += dLat
		lng += dLng
		points = append(points, coord{float64(lat) / 1e5, float64(lng) / 1e5})
	}
	return points
}

func encodePolyline(points []coord) string {
	var out []byte
	var prevLat, prevLng int
	put := func(v int) {
		v <<= 1
		if v < 0 {
			v = ^v
		}
		for v >= 0x20 {
			out = append(out, byte((0x20|(v&0x1f))+63))
			v >>= 5
		}
		out = append(out, byte(v+63))
	}
	for _, p := range points {
		lat, lng := int(math.Round(p.lat*1e5)), int(math.Round(p.lng*1e5))
		put(lat - prevLat)
		put(lng - prevLng)
		prevLat, prevLng = lat, lng
	}
	return string(out)
}

// simplify drops points that lie within `tolerance` degrees of the line between their neighbours
// (Douglas-Peucker), so a long route fits in a URL while keeping its shape.
func simplify(points []coord, tolerance float64) []coord {
	if len(points) < 3 || tolerance <= 0 {
		return points
	}
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true
	var walk func(lo, hi int)
	walk = func(lo, hi int) {
		farthest, index := 0.0, -1
		for i := lo + 1; i < hi; i++ {
			if d := distanceToSegment(points[i], points[lo], points[hi]); d > farthest {
				farthest, index = d, i
			}
		}
		if index != -1 && farthest > tolerance {
			keep[index] = true
			walk(lo, index)
			walk(index, hi)
		}
	}
	walk(0, len(points)-1)

	out := make([]coord, 0, len(points))
	for i, p := range points {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

func distanceToSegment(p, a, b coord) float64 {
	dx, dy := b.lng-a.lng, b.lat-a.lat
	if dx == 0 && dy == 0 {
		return math.Hypot(p.lng-a.lng, p.lat-a.lat)
	}
	t := ((p.lng-a.lng)*dx + (p.lat-a.lat)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p.lng-(a.lng+t*dx), p.lat-(a.lat+t*dy))
}

// simplifyEncoded re-encodes a polyline with fewer points.
func simplifyEncoded(encoded string, tolerance float64) string {
	return encodePolyline(simplify(decodePolyline(encoded), tolerance))
}

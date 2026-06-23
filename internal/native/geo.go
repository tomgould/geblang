package native

import (
	"fmt"
	"math"

	"geblang/internal/runtime"
)

const geoEarthRadiusKm = 6371.0

func geoDistanceFromKm(km float64, unit string) (float64, error) {
	switch unit {
	case "km", "":
		return km, nil
	case "m":
		return km * 1000, nil
	case "mi":
		return km / 1.609344, nil
	case "nmi":
		return km / 1.852, nil
	}
	return 0, fmt.Errorf("geo: unknown distance unit %q", unit)
}

func geoKmFromDistance(d float64, unit string) (float64, error) {
	switch unit {
	case "km", "":
		return d, nil
	case "m":
		return d / 1000, nil
	case "mi":
		return d * 1.609344, nil
	case "nmi":
		return d * 1.852, nil
	}
	return 0, fmt.Errorf("geo: unknown distance unit %q", unit)
}

func geoLatArg(args []runtime.Value, i int, label string) (float64, error) {
	v, err := FloatLike(args[i])
	if err != nil {
		return 0, err
	}
	if v < -90 || v > 90 {
		return 0, fmt.Errorf("%s: latitude must be in [-90, 90]", label)
	}
	return v, nil
}

func geoUnitArg(args []runtime.Value, i int) (string, error) {
	if len(args) <= i {
		return "km", nil
	}
	s, ok := args[i].(runtime.String)
	if !ok {
		return "", fmt.Errorf("geo: unit must be a string")
	}
	return s.Value, nil
}

func geoLatLonDict(lat, lon float64) runtime.Value {
	d := runtime.NewDictHint(2)
	statsPutFloat(&d, "lat", lat)
	statsPutFloat(&d, "lon", lon)
	return d
}

func registerGeo(r *Registry) {
	r.Register("geo", "haversineDistance", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 4 || len(args) > 5 {
			return nil, fmt.Errorf("geo.haversineDistance expects (lat1, lon1, lat2, lon2, unit?)")
		}
		lat1, err := geoLatArg(args, 0, "geo.haversineDistance")
		if err != nil {
			return nil, err
		}
		lon1, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		lat2, err := geoLatArg(args, 2, "geo.haversineDistance")
		if err != nil {
			return nil, err
		}
		lon2, err := FloatLike(args[3])
		if err != nil {
			return nil, err
		}
		unit, err := geoUnitArg(args, 4)
		if err != nil {
			return nil, err
		}
		p1 := lat1 * math.Pi / 180
		p2 := lat2 * math.Pi / 180
		dp := (lat2 - lat1) * math.Pi / 180
		dl := (lon2 - lon1) * math.Pi / 180
		a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
		c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
		out, err := geoDistanceFromKm(geoEarthRadiusKm*c, unit)
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: out}, nil
	})
	r.Register("geo", "bearing", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 4 {
			return nil, fmt.Errorf("geo.bearing expects (lat1, lon1, lat2, lon2)")
		}
		lat1, err := geoLatArg(args, 0, "geo.bearing")
		if err != nil {
			return nil, err
		}
		lon1, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		lat2, err := geoLatArg(args, 2, "geo.bearing")
		if err != nil {
			return nil, err
		}
		lon2, err := FloatLike(args[3])
		if err != nil {
			return nil, err
		}
		p1 := lat1 * math.Pi / 180
		p2 := lat2 * math.Pi / 180
		dl := (lon2 - lon1) * math.Pi / 180
		y := math.Sin(dl) * math.Cos(p2)
		x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
		deg := math.Atan2(y, x) * 180 / math.Pi
		return runtime.Float{Value: math.Mod(deg+360, 360)}, nil
	})
	r.Register("geo", "midpoint", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 4 {
			return nil, fmt.Errorf("geo.midpoint expects (lat1, lon1, lat2, lon2)")
		}
		lat1, err := geoLatArg(args, 0, "geo.midpoint")
		if err != nil {
			return nil, err
		}
		lon1, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		lat2, err := geoLatArg(args, 2, "geo.midpoint")
		if err != nil {
			return nil, err
		}
		lon2, err := FloatLike(args[3])
		if err != nil {
			return nil, err
		}
		p1 := lat1 * math.Pi / 180
		p2 := lat2 * math.Pi / 180
		l1 := lon1 * math.Pi / 180
		dl := (lon2 - lon1) * math.Pi / 180
		bx := math.Cos(p2) * math.Cos(dl)
		by := math.Cos(p2) * math.Sin(dl)
		p3 := math.Atan2(math.Sin(p1)+math.Sin(p2), math.Sqrt((math.Cos(p1)+bx)*(math.Cos(p1)+bx)+by*by))
		l3 := l1 + math.Atan2(by, math.Cos(p1)+bx)
		return geoLatLonDict(p3*180/math.Pi, math.Mod(l3*180/math.Pi+540, 360)-180), nil
	})
	r.Register("geo", "destination", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 4 || len(args) > 5 {
			return nil, fmt.Errorf("geo.destination expects (lat, lon, bearing, distance, unit?)")
		}
		lat, err := geoLatArg(args, 0, "geo.destination")
		if err != nil {
			return nil, err
		}
		lon, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		brng, err := FloatLike(args[2])
		if err != nil {
			return nil, err
		}
		dist, err := FloatLike(args[3])
		if err != nil {
			return nil, err
		}
		unit, err := geoUnitArg(args, 4)
		if err != nil {
			return nil, err
		}
		km, err := geoKmFromDistance(dist, unit)
		if err != nil {
			return nil, err
		}
		ad := km / geoEarthRadiusKm
		p1 := lat * math.Pi / 180
		l1 := lon * math.Pi / 180
		th := brng * math.Pi / 180
		p2 := math.Asin(math.Sin(p1)*math.Cos(ad) + math.Cos(p1)*math.Sin(ad)*math.Cos(th))
		l2 := l1 + math.Atan2(math.Sin(th)*math.Sin(ad)*math.Cos(p1), math.Cos(ad)-math.Sin(p1)*math.Sin(p2))
		return geoLatLonDict(p2*180/math.Pi, math.Mod(l2*180/math.Pi+540, 360)-180), nil
	})
}

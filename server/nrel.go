package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NREL Alternative Fuel Stations API, nearby-route endpoint. NREL moved from developer.nrel.gov
// (no longer resolves, 2026-09) to developer.nlr.gov; same api.data.gov gateway and keys. The key
// goes in the X-Api-Key header (a form-body api_key is ignored) and is never logged.

const nrelDefaultBase = "https://developer.nlr.gov/api/alt-fuel-stations/v1"

type nrelClient struct {
	base string
	key  string
	http *http.Client
}

func newNRELClient(base, key string) *nrelClient {
	return &nrelClient{base: strings.TrimRight(base, "/"), key: key, http: &http.Client{Timeout: 30 * time.Second}}
}

type nrelStation struct {
	ID              int     `json:"id"`
	Name            string  `json:"station_name"`
	Lat             float64 `json:"latitude"`
	Lon             float64 `json:"longitude"`
	Street          string  `json:"street_address"`
	City            string  `json:"city"`
	Network         string  `json:"ev_network"`
	Hours           string  `json:"access_days_time"`
	Restricted      bool    `json:"restricted_access"`
	Pricing         string  `json:"ev_pricing"`
	LastConfirmed   string  `json:"date_last_confirmed"`
	OffRouteKM      float64 `json:"distance_km"`
	EVChargingUnits []struct {
		ChargingLevel string `json:"charging_level"`
		Connectors    map[string]struct {
			PowerKW   *float64 `json:"power_kw"`
			PortCount int      `json:"port_count"`
		} `json:"connectors"`
	} `json:"ev_charging_units"`
}

// usablePorts counts Level 2 J1772 and Tesla destination ports (the SR/S takes J1772; Tesla via a
// Tap Mini adapter) and returns the best advertised power, 0 if none is published.
func (s *nrelStation) usablePorts(want []string) (ports int, powerKW float64, connectors []string) {
	if len(want) == 0 {
		want = []string{"J1772", "TESLA"}
	}
	seen := map[string]bool{}
	for _, u := range s.EVChargingUnits {
		if u.ChargingLevel != "" && u.ChargingLevel != "2" {
			continue
		}
		for _, name := range want {
			c, ok := u.Connectors[name]
			if !ok || c.PortCount == 0 {
				continue
			}
			ports += c.PortCount
			if c.PowerKW != nil && *c.PowerKW > powerKW {
				powerKW = *c.PowerKW
			}
			if !seen[name] {
				seen[name] = true
				connectors = append(connectors, name)
			}
		}
	}
	return ports, powerKW, connectors
}

// routeWKT thins the polyline to vertices >= stepKM apart (NREL only needs the corridor).
func routeWKT(coords [][]float64, stepKM float64) string {
	var b strings.Builder
	b.WriteString("LINESTRING(")
	last := -1
	for i, c := range coords {
		if last >= 0 && i != len(coords)-1 && haversineKM(coords[last], c) < stepKM {
			continue
		}
		if last >= 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%.5f %.5f", c[0], c[1])
		last = i
	}
	b.WriteString(")")
	return b.String()
}

// allStations fetches every public electric station in the US in one request — the whole point of
// ADR-028, since the per-plan call is capped at 1,000 an hour. Connector filtering happens locally,
// so this asks for everything a bike in the catalogue might use.
func (c *nrelClient) allStations(ctx context.Context) ([]nrelStation, error) {
	q := url.Values{}
	q.Set("fuel_type", "ELEC")
	q.Set("country", "US")
	q.Set("status", "E")
	q.Set("access", "public")
	q.Set("ev_connector_type", "J1772,TESLA,J1772COMBO,CHADEMO")
	q.Set("limit", "all")
	// ".json" hangs off the version, not off a path segment: .../v1.json, never .../v1/.json.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+".json?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.key)
	client := &http.Client{Timeout: 10 * time.Minute} // 260 MB of JSON
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nrel: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, fmt.Errorf("nrel: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nrel %d: %.200s", resp.StatusCode, body)
	}
	return parseNREL(body)
}

// nearbyRoute returns public, open, Level 2 J1772/Tesla stations within corridorMiles of the route.
func (c *nrelClient) nearbyRoute(ctx context.Context, coords [][]float64, corridorMiles float64, connectors string) ([]nrelStation, error) {
	form := url.Values{}
	form.Set("route", routeWKT(coords, 1.0))
	form.Set("distance", strconv.FormatFloat(corridorMiles, 'f', 1, 64))
	form.Set("fuel_type", "ELEC")
	form.Set("ev_charging_level", "2")
	form.Set("ev_connector_type", connectors)
	form.Set("status", "E")
	form.Set("access", "public")
	form.Set("limit", "all")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/nearby-route.json", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Api-Key", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nrel: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("nrel: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nrel %d: %.200s", resp.StatusCode, body)
	}
	return parseNREL(body)
}

func parseNREL(body []byte) ([]nrelStation, error) {
	var r struct {
		Stations []nrelStation `json:"fuel_stations"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("nrel: undecodable response: %w", err)
	}
	return r.Stations, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GraphHopper client. Only the parts of the /route API serpentine uses.

var ghDetails = []string{"road_class", "curvature", "urban_density", "max_speed", "surface"}

type ghClient struct {
	base string
	http *http.Client
}

func newGHClient(base string) *ghClient {
	return &ghClient{base: base, http: &http.Client{Timeout: 30 * time.Second}}
}

type ghRequest struct {
	Profile       string       `json:"profile"`
	Points        [][2]float64 `json:"points"`
	PointsEncoded bool         `json:"points_encoded"`
	Elevation     bool         `json:"elevation"`
	Instructions  bool         `json:"instructions"`
	Details       []string     `json:"details,omitempty"`
	Algorithm     string       `json:"algorithm,omitempty"`
	RTDistance    float64      `json:"round_trip.distance,omitempty"`
	RTSeed        int64        `json:"round_trip.seed,omitempty"`
	// POST bodies take "headings"; "heading" is silently ignored (infra/README.md gotchas).
	Headings    []float64    `json:"headings,omitempty"`
	CustomModel *customModel `json:"custom_model,omitempty"`
}

type customModel struct {
	Priority []cmRule `json:"priority,omitempty"`
}

type cmRule struct {
	If         string `json:"if,omitempty"`
	ElseIf     string `json:"else_if,omitempty"`
	MultiplyBy string `json:"multiply_by"`
}

type ghResponse struct {
	Paths   []ghPath `json:"paths"`
	Message string   `json:"message"`
	Info    struct {
		Took int `json:"took"`
	} `json:"info"`
}

type ghPath struct {
	Distance float64 `json:"distance"` // m
	Time     int64   `json:"time"`     // ms
	Ascend   float64 `json:"ascend"`
	Descend  float64 `json:"descend"`
	Points   struct {
		Coordinates [][]float64 `json:"coordinates"` // [lon, lat, ele]
	} `json:"points"`
	Instructions []ghInstruction       `json:"instructions"`
	Details      map[string][]ghDetail `json:"details"`
}

type ghInstruction struct {
	Distance   float64 `json:"distance"`
	Time       int64   `json:"time"`
	Text       string  `json:"text"`
	Sign       int     `json:"sign"`
	Interval   [2]int  `json:"interval"`
	StreetName string  `json:"street_name"`
	StreetRef  string  `json:"street_ref"`
}

// ghDetail is one [from, to, value] path detail; value is a string, number or null.
type ghDetail struct {
	From, To int
	Str      string
	Num      float64
	IsNum    bool
}

func (d *ghDetail) UnmarshalJSON(b []byte) error {
	var raw [3]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[0], &d.From); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &d.To); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[2], &d.Num); err == nil {
		d.IsNum = true
		return nil
	}
	var s *string
	if err := json.Unmarshal(raw[2], &s); err != nil {
		return err
	}
	if s != nil {
		d.Str = *s
	}
	return nil
}

// ghError is a GraphHopper 4xx: a request it understood and couldn't route (bad point, no loop).
type ghError struct {
	Status  int
	Message string
}

func (e *ghError) Error() string { return fmt.Sprintf("graphhopper %d: %s", e.Status, e.Message) }

func (c *ghClient) route(ctx context.Context, req ghRequest) (*ghPath, error) {
	req.Profile = "motorcycle"
	req.PointsEncoded = false
	req.Elevation = true
	req.Instructions = true
	req.Details = ghDetails
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/route", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	return parseGHResponse(resp.StatusCode, data)
}

func parseGHResponse(status int, data []byte) (*ghPath, error) {
	var r ghResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("graphhopper %d: undecodable response: %w", status, err)
	}
	if status >= 400 && status < 500 {
		return nil, &ghError{Status: status, Message: r.Message}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("graphhopper %d: %s", status, r.Message)
	}
	if len(r.Paths) == 0 {
		return nil, &ghError{Status: 400, Message: "no path"}
	}
	return &r.Paths[0], nil
}

type ghInfo struct {
	Version    string `json:"version"`
	DataDate   string `json:"data_date"`
	ImportDate string `json:"import_date"`
}

func (c *ghClient) info(ctx context.Context) (*ghInfo, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graphhopper /info %d", resp.StatusCode)
	}
	var info ghInfo
	return &info, json.NewDecoder(resp.Body).Decode(&info)
}

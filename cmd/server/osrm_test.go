package main

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSolveOpenPathVisitsNearestFirstAndImprovesCrossings(t *testing.T) {
	// Depósito no 0 e paradas numa reta: 1 fica em 30, 2 em 10, 3 em 20.
	pos := []float64{0, 30, 10, 20}
	m := make([][]float64, len(pos))
	for i := range pos {
		m[i] = make([]float64, len(pos))
		for j := range pos {
			m[i][j] = math.Abs(pos[i] - pos[j])
		}
	}
	got := solveOpenPath(m)
	if len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 1 {
		t.Fatalf("path = %v, want [2 3 1]", got)
	}
	if solveOpenPath([][]float64{{0}}) != nil {
		t.Fatal("route with only the depot has no stops")
	}

	// Numa reta com o depósito em 0 e paradas em +1, -2 e +3, o vizinho mais próximo
	// vai a +1, volta a -2 e cruza até +3 (1+3+5=9). O 2-opt acha -2, +1, +3 (2+3+2=7).
	line := []float64{0, 1, -2, 3}
	trap := make([][]float64, len(line))
	for i := range line {
		trap[i] = make([]float64, len(line))
		for j := range line {
			trap[i][j] = math.Abs(line[i] - line[j])
		}
	}
	p := solveOpenPath(trap)
	if len(p) != 3 || p[0] != 2 || p[1] != 1 || p[2] != 3 {
		t.Fatalf("path = %v, want [2 1 3] (cost 7)", p)
	}
}

func TestHaversineAndEstimatedMatrix(t *testing.T) {
	d := haversineMeters(latLng{0, 0}, latLng{0, 1})
	if math.Abs(d-111195) > 50 {
		t.Fatalf("one degree on the equator = %.0f m, want ~111195", d)
	}
	m := estimatedMatrix([]latLng{{-3.7319, -38.5267}, {-3.5432, -38.8123}})
	if m.Distances[0][0] != 0 || m.Distances[0][1] < 40000 || m.Durations[0][1] <= 0 {
		t.Fatalf("estimate Fortaleza→Pecém = %v m, %v s", m.Distances[0][1], m.Durations[0][1])
	}
}

// fakeOSRM responde ao table/v1 com a distância em linha reta a 10 m/s, e guarda a
// última lista de coordenadas pedida.
func fakeOSRM(t *testing.T, lastCoords *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.URL.Path, "/table/v1/driving/")
		*lastCoords = raw
		var pts []latLng
		for _, c := range strings.Split(raw, ";") {
			parts := strings.Split(c, ",")
			lng, _ := strconv.ParseFloat(parts[0], 64)
			lat, _ := strconv.ParseFloat(parts[1], 64)
			pts = append(pts, latLng{lat, lng})
		}
		dur := make([][]float64, len(pts))
		dist := make([][]float64, len(pts))
		for i := range pts {
			dur[i] = make([]float64, len(pts))
			dist[i] = make([]float64, len(pts))
			for j := range pts {
				dist[i][j] = haversineMeters(pts[i], pts[j])
				dur[i][j] = dist[i][j] / 10
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "Ok", "durations": dur, "distances": dist})
	}))
}

func TestOSRMTableSendsLongitudeFirstAndRejectsUnroutablePairs(t *testing.T) {
	var coords string
	srv := fakeOSRM(t, &coords)
	defer srv.Close()
	m, err := osrmTable(context.Background(), srv.URL, []latLng{{-3.5, -38.8}, {-3.6, -38.7}})
	if err != nil || len(m.Durations) != 2 || m.Durations[0][1] <= 0 {
		t.Fatalf("matrix=%v err=%v", m, err)
	}
	if coords != "-38.800000,-3.500000;-38.700000,-3.600000" {
		t.Fatalf("coordinates sent = %q, OSRM expects lng,lat", coords)
	}

	noRoad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"Ok","durations":[[0,null],[null,0]],"distances":[[0,null],[null,0]]}`))
	}))
	defer noRoad.Close()
	if _, err := osrmTable(context.Background(), noRoad.URL, []latLng{{0, 0}, {1, 1}}); err == nil || !strings.Contains(err.Error(), "no road") {
		t.Fatalf("null cell must fail, err=%v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"InvalidQuery","message":"bad"}`))
	}))
	defer bad.Close()
	if _, err := osrmTable(context.Background(), bad.URL, []latLng{{0, 0}, {1, 1}}); err == nil {
		t.Fatal("error code from OSRM must fail")
	}
}

func optimizeTestPlace(t *testing.T, a *app, director loginData, body map[string]any) int64 {
	t.Helper()
	return routeTestCreate(t, a, director, "/api/places", body)
}

func TestRouteOptimizeOrdersStopsWritesETAAndWarnsLateWindows(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "opt-director", "secret", "Diretor", true)
	director := doLogin(t, a, "opt-director", "secret")
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	customer := recipeTestCustomer(t, a, director, "Cliente Roteiro")

	// Paradas a leste do depósito, ao longo do equador para facilitar a conta.
	place := func(name, lng, windowEnd string) int64 {
		body := map[string]any{"name": name, "kind": "cliente", "customer_id": customer, "address": "Rua " + name, "latitude": "0", "longitude": lng}
		if windowEnd != "" {
			body["window_start"], body["window_end"] = "06:00", windowEnd
		}
		return optimizeTestPlace(t, a, director, body)
	}
	far := place("Longe", "0.3", "")
	near := place("Perto", "0.1", "")
	middle := place("Meio", "0.2", "07:30")
	noCoord := routeTestCreate(t, a, director, "/api/places", map[string]any{"name": "Sem mapa", "kind": "cliente", "customer_id": customer, "address": "Rua sem número"})

	order := func(op string, p int64) int64 {
		return routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody(op, p, wh, []map[string]any{{"item_id": arroz, "quantity": "1"}}))
	}
	oFar, oNear, oMiddle, oNoCoord := order("opt-far", far), order("opt-near", near), order("opt-middle", middle), order("opt-nocoord", noCoord)
	van := routeTestCreate(t, a, director, "/api/vehicles", map[string]any{"plate": "OPT1A11", "description": "Van", "capacity_kg": "900"})
	driver := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "Rita"})
	route := routeTestCreate(t, a, director, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": van, "driver_id": driver})
	base := "/api/routes/" + itoa(route)
	put := func(ids []int64, version int64) int64 {
		rr := authReq(a, http.MethodPut, base+"/stops", map[string]any{"order_ids": ids, "version": version}, director, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("stops status=%d body=%s", rr.Code, rr.Body.String())
		}
		return routeVersion(t, rr)
	}
	version := put([]int64{oFar, oNoCoord}, 1)
	logisticsErrorIs(t, authReq(a, http.MethodPost, base+"/optimize", map[string]any{"version": version}, director, ""), http.StatusBadRequest, "Locais sem coordenada: Sem mapa")
	version = put([]int64{oFar, oNear, oMiddle}, version)
	logisticsErrorIs(t, authReq(a, http.MethodPost, base+"/optimize", map[string]any{"version": version}, director, ""), http.StatusBadRequest, "Cadastre o local do depósito")
	optimizeTestPlace(t, a, director, map[string]any{"name": "Cozinha central", "kind": "deposito", "warehouse_id": wh, "address": "Rua da cozinha", "latitude": "0", "longitude": "0"})

	var coords string
	srv := fakeOSRM(t, &coords)
	defer srv.Close()
	t.Setenv("OSRM_URL", srv.URL+"/")
	logisticsErrorIs(t, authReq(a, http.MethodPost, base+"/optimize", map[string]any{"version": version + 5}, director, ""), http.StatusConflict, "alterado por outra pessoa")
	rr := authReq(a, http.MethodPost, base+"/optimize", map[string]any{"version": version, "departure": "07:00"}, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("optimize status=%d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Version  int64    `json:"version"`
		Source   string   `json:"source"`
		Distance int      `json:"total_distance_m"`
		Warnings []string `json:"warnings"`
		Stops    []struct {
			OrderID int64  `json:"order_id"`
			ETA     string `json:"eta"`
		} `json:"stops"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Source != "osrm" || len(res.Stops) != 3 || res.Stops[0].OrderID != oNear || res.Stops[1].OrderID != oMiddle || res.Stops[2].OrderID != oFar {
		t.Fatalf("optimize result = %+v", res)
	}
	// 0,1° no equador ≈ 11,1 km a 10 m/s = 18min32s por trecho, mais 10 min parado em cada entrega.
	if res.Stops[0].ETA != "07:18" || res.Stops[1].ETA != "07:47" || math.Abs(float64(res.Distance)-33359) > 30 {
		t.Fatalf("ETAs %+v distance %d", res.Stops, res.Distance)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Parada 2 (Meio) chega 07:47") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	var stored int
	if err := a.db.QueryRow(`SELECT count(*) FROM route_stops WHERE route_id=$1 AND eta IS NOT NULL AND duration_from_prev_s>0`, route).Scan(&stored); err != nil || stored != 3 {
		t.Fatalf("stored legs=%d err=%v", stored, err)
	}

	// Manter a ordem: só calcula os trechos. Com o OSRM fora do ar, usa a estimativa.
	version = put([]int64{oFar, oNear, oMiddle}, res.Version)
	t.Setenv("OSRM_URL", "http://127.0.0.1:1")
	rr = authReq(a, http.MethodPost, base+"/optimize", map[string]any{"version": version, "keep_order": true}, director, "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"source":"estimativa"`) {
		t.Fatalf("fallback status=%d body=%s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	if res.Stops[0].OrderID != oFar || res.Stops[2].OrderID != oMiddle {
		t.Fatalf("keep_order must keep the manual order, got %+v", res.Stops)
	}
}

func TestSolveOpenPathAlwaysReturnsAPermutationNoWorseThanNearestNeighbor(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 300; round++ {
		n := 2 + rng.Intn(9)
		m := make([][]float64, n)
		for i := range m {
			m[i] = make([]float64, n)
			for j := range m[i] {
				if i != j {
					m[i][j] = float64(1 + rng.Intn(100)) // assimétrica de propósito
				}
			}
		}
		cost := func(p []int) float64 {
			c, prev := 0.0, 0
			for _, k := range p {
				c += m[prev][k]
				prev = k
			}
			return c
		}
		nn, cur, seen := []int{}, 0, map[int]bool{0: true}
		for len(nn) < n-1 {
			best := -1
			for j := 1; j < n; j++ {
				if !seen[j] && (best < 0 || m[cur][j] < m[cur][best]) {
					best = j
				}
			}
			seen[best] = true
			nn = append(nn, best)
			cur = best
		}
		got := solveOpenPath(m)
		check := map[int]bool{}
		for _, k := range got {
			if k < 1 || k >= n || check[k] {
				t.Fatalf("round %d: %v is not a permutation of 1..%d", round, got, n-1)
			}
			check[k] = true
		}
		if len(got) != n-1 || cost(got) > cost(nn)+1e-9 {
			t.Fatalf("round %d: path %v cost %v worse than nearest neighbor %v", round, got, cost(got), cost(nn))
		}
	}
}

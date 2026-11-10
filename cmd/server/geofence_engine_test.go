package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestGeofenceGeometryAndConfirmations(t *testing.T) {
	if d := geofenceDistanceM(-3.7, -38.5, -3.7, -38.5); d > 0.01 {
		t.Fatalf("same point distance: %.4f", d)
	}
	if d := geofenceDistanceM(-3.7, -38.5, -3.701, -38.5); math.Abs(d-111) > 2 {
		t.Fatalf("unexpected distance %.4f", d)
	}
	now := time.Now().UTC()
	st := geofenceState{}
	st, ok, _ := st.advance(false, true, now)
	if ok || st.InsideSeen {
		t.Fatal("cannot infer departure without observed depot")
	}
	st, ok, _ = st.advance(true, true, now.Add(time.Second))
	if ok || !st.InsideSeen {
		t.Fatal("origin not recorded")
	}
	st, ok, _ = st.advance(false, true, now.Add(2*time.Second))
	if ok {
		t.Fatal("one fix is insufficient")
	}
	var at time.Time
	st, ok, at = st.advance(false, true, now.Add(3*time.Second))
	if !ok || !at.Equal(now.Add(2*time.Second)) {
		t.Fatalf("departure confirmation %v at %v", ok, at)
	}
	st = geofenceState{}
	st, ok, _ = st.advance(true, false, now)
	if ok {
		t.Fatal("one client fix must not confirm arrival")
	}
	st, ok, _ = st.advance(false, false, now.Add(time.Second))
	if ok || st.Count != 0 {
		t.Fatal("boundary bounce must reset arrival")
	}
	st, ok, _ = st.advance(true, false, now.Add(2*time.Second))
	if ok {
		t.Fatal("one new fix must not confirm arrival")
	}
	_, ok, at = st.advance(true, false, now.Add(3*time.Second))
	if !ok || !at.Equal(now.Add(2*time.Second)) {
		t.Fatal("two inside fixes should confirm first entry")
	}
	st = geofenceState{}
	st, ok, _ = st.advance(true, false, now)
	if ok {
		t.Fatal("first arrival fix")
	}
	st, ok, _ = st.advance(true, false, now.Add(6*time.Minute))
	if ok || st.Count != 1 {
		t.Fatal("widely spaced GPS fixes must not confirm arrival")
	}

}

func TestGeofenceDeviceTimeRequiresTimezone(t *testing.T) {
	raw := "2026-10-16 08:21:00"
	if _, ok := parseRastroGPSTime(raw, ""); ok {
		t.Fatal("naive provider time must not be presumed as UTC")
	}
	tm, ok := parseRastroGPSTime(raw, "America/Fortaleza")
	if !ok || tm.Format(time.RFC3339) != "2026-10-16T11:21:00Z" {
		t.Fatalf("wrong timezone %v %v", tm, ok)
	}
	if _, ok := parseRastroGPSTime("2026-10-16T11:21:00Z", ""); !ok {
		t.Fatal("RFC3339 should work without configuration")
	}
	if _, ok := parseRastroGPSTime("garbage", "America/Fortaleza"); ok {
		t.Fatal("invalid device time accepted")
	}
	if _, ok := parseRastroGPSTime(raw, "Unknown/Provider"); ok {
		t.Fatal("invalid timezone accepted")
	}
}

func TestGeofencePostgresDepartureArrivalAndIdempotency(t *testing.T) {
	a, _, warehouse, _ := newStockTestApp(t)
	db := a.db
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var depot, client, vehicle, driver, order, route int64
	must(db.QueryRow(`INSERT INTO places(name,kind,warehouse_id,address,latitude,longitude,geofence_radius_m)
 VALUES('Geo depósito teste','deposito',$1,'Rua A',-3.700,-38.500,150) RETURNING id`, warehouse).Scan(&depot))
	must(db.QueryRow(`INSERT INTO places(name,kind,address,latitude,longitude,geofence_radius_m)
 VALUES('Geo cliente teste','outro','Rua B',-3.710,-38.510,100) RETURNING id`).Scan(&client))
	must(db.QueryRow(`INSERT INTO vehicles(plate,description,capacity_kg) VALUES('GEO1A23','Van geofence',200) RETURNING id`).Scan(&vehicle))
	must(db.QueryRow(`INSERT INTO drivers(name) VALUES('Condutor geofence') RETURNING id`).Scan(&driver))
	now := time.Now().UTC()
	day := now.In(fortalezaLocation()).Format("2006-01-02")
	must(db.QueryRow(`INSERT INTO delivery_orders(operation_id,place_id,origin_warehouse_id,day,due_at,status,tracking_token)
 VALUES('geo-order',$1,$2,$3,$4,'despachado','012345678901234567890123456789012345')
 RETURNING id`, client, warehouse, day, now.Add(2*time.Hour)).Scan(&order))
	released := now.Add(-4 * time.Minute)
	deadline := now.Add(-10 * time.Minute)
	must(db.QueryRow(`INSERT INTO routes(day,vehicle_id,driver_id,status,released_at,departure_deadline)
 VALUES($1,$2,$3,'liberado',$4,$5) RETURNING id`, day, vehicle, driver, released, deadline).Scan(&route))
	_, err := db.Exec(`INSERT INTO route_stops(route_id,sequence,order_id) VALUES($1,1,$2)`, route, order)
	must(err)
	fixtures := []struct {
		lat, lng float64
		ago      time.Duration
	}{
		{-3.700, -38.500, 210 * time.Second},
		{-3.703, -38.503, 180 * time.Second},
		{-3.704, -38.504, 150 * time.Second},
		{-3.71005, -38.51005, 120 * time.Second},
		{-3.71002, -38.51002, 90 * time.Second},
	}
	for _, f := range fixtures {
		gpsAt := now.Add(-f.ago)
		_, err = db.Exec(`INSERT INTO geofence_positions(vehicle_id,remote_id,latitude,longitude,gps_at,raw_device_time)
 VALUES($1,'id-from-provider',$2,$3,$4,$5)`, vehicle, f.lat, f.lng, gpsAt, gpsAt.Format(time.RFC3339Nano))
		must(err)
	}
	must(a.detectGeofence(ctx))
	must(a.detectGeofence(ctx))
	var gotDeparture, gotArrival time.Time
	must(db.QueryRow(`SELECT actual_departure_at FROM routes WHERE id=$1`, route).Scan(&gotDeparture))
	must(db.QueryRow(`SELECT arrived_at FROM route_stops WHERE route_id=$1 AND order_id=$2`, route, order).Scan(&gotArrival))
	if math.Abs(float64(gotDeparture.Sub(now.Add(-180*time.Second)))) > float64(time.Millisecond) || math.Abs(float64(gotArrival.Sub(now.Add(-120*time.Second)))) > float64(time.Millisecond) {
		t.Fatalf("unexpected GPS events departure=%s arrival=%s", gotDeparture, gotArrival)
	}
	assertCount(t, db, `SELECT count(*) FROM geofence_events WHERE route_id=$1`, 2, route)
	var orderStatus string
	must(db.QueryRow(`SELECT status FROM delivery_orders WHERE id=$1`, order).Scan(&orderStatus))
	if orderStatus != "despachado" {
		t.Fatal("geofence must not silently change driver's operational state")
	}
	loginUser := createTestUser(t, a, "geo-director", "secret", "Diretor", true)
	if loginUser <= 0 {
		t.Fatal("user not created")
	}
	session := doLogin(t, a, "geo-director", "secret")
	rr := authReq(a, http.MethodGet, "/api/geofencing/overview?day="+day, nil, session, "")
	if rr.Code != 200 {
		t.Fatalf("overview http=%d body=%s", rr.Code, rr.Body.String())
	}
	var report []struct {
		RouteID int64            `json:"route_id"`
		Status  string           `json:"status"`
		Stops   []map[string]any `json:"stops"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 || report[0].RouteID != route || report[0].Status != "saida_atrasada" || len(report[0].Stops) != 1 {
		t.Fatalf("unexpected overview: %+v", report)
	}
}

func TestGeofenceMissingOriginAndOldGPSCannotInventDeparture(t *testing.T) {
	a, _, warehouse, _ := newStockTestApp(t)
	db := a.db
	var depot, client, vehicle, driver, order, route int64
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(db.QueryRow(`INSERT INTO places(name,kind,warehouse_id,address,latitude,longitude)
 VALUES('Geo dep old','deposito',$1,'A',-3.70,-38.50) RETURNING id`, warehouse).Scan(&depot))
	must(db.QueryRow(`INSERT INTO places(name,kind,address,latitude,longitude)
 VALUES('Geo stop old','outro','B',-3.71,-38.51) RETURNING id`).Scan(&client))
	must(db.QueryRow(`INSERT INTO vehicles(plate,description,capacity_kg) VALUES('GEO2A34','Van',200) RETURNING id`).Scan(&vehicle))
	must(db.QueryRow(`INSERT INTO drivers(name) VALUES('Geo driver old') RETURNING id`).Scan(&driver))
	now := time.Now().UTC()
	day := now.In(fortalezaLocation()).Format("2006-01-02")
	must(db.QueryRow(`INSERT INTO delivery_orders(operation_id,place_id,origin_warehouse_id,day,due_at,tracking_token) VALUES('geo-old',$1,$2,$3,$4,'012345678901234567890123456789012346') RETURNING id`, client, warehouse, day, now).Scan(&order))
	must(db.QueryRow(`INSERT INTO routes(day,vehicle_id,driver_id,status,released_at) VALUES($1,$2,$3,'liberado',$4) RETURNING id`, day, vehicle, driver, now.Add(-time.Hour)).Scan(&route))
	_, err := db.Exec(`INSERT INTO route_stops(route_id,sequence,order_id) VALUES($1,1,$2)`, route, order)
	must(err)
	for i := 0; i < 2; i++ {
		timestamp := now.Add(time.Duration(-10+i) * time.Minute)
		_, err = db.Exec(`INSERT INTO geofence_positions(vehicle_id,remote_id,latitude,longitude,gps_at,raw_device_time) VALUES($1,'old',$2,$3,$4,$5)`, vehicle, -3.703, -38.503, timestamp, timestamp.Format(time.RFC3339Nano))
		must(err)
	}
	must(a.detectGeofence(context.Background()))
	assertCount(t, db, `SELECT count(*) FROM geofence_events WHERE route_id=$1`, 0, route)
}

func TestGeofenceRouteDeadlineAPI(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	db := a.db
	loginID := createTestUser(t, a, "geofence-deadline-director", "secret", "Diretor", true)
	if loginID < 1 {
		t.Fatal("invalid user")
	}
	session := doLogin(t, a, "geofence-deadline-director", "secret")
	var vehicle, driver, route int64
	if err := db.QueryRow(`INSERT INTO vehicles(plate,description,capacity_kg) VALUES('GEO3A45','Rota prazo',120) RETURNING id`).Scan(&vehicle); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO drivers(name) VALUES('Geo driver deadline') RETURNING id`).Scan(&driver); err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(fortalezaLocation()).Format("2006-01-02")
	if err := db.QueryRow(`INSERT INTO routes(day,vehicle_id,driver_id) VALUES($1,$2,$3) RETURNING id`, day, vehicle, driver).Scan(&route); err != nil {
		t.Fatal(err)
	}
	url := "/api/routes/" + itoa(route) + "/departure-deadline"
	rr := authReq(a, http.MethodPut, url, map[string]any{"deadline": "07:30", "version": 1}, session, "")
	if rr.Code != 200 {
		t.Fatalf("deadline save HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var version int64
	var got time.Time
	if err := db.QueryRow(`SELECT version,departure_deadline FROM routes WHERE id=$1`, route).Scan(&version, &got); err != nil {
		t.Fatal(err)
	}
	expected, err := time.ParseInLocation("2006-01-02 15:04", day+" 07:30", fortalezaLocation())
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 || !got.Equal(expected) {
		t.Fatalf("unexpected due %+v version %d", got, version)
	}
	rr = authReq(a, http.MethodPut, url, map[string]any{"deadline": "09:30", "version": 1}, session, "")
	if rr.Code != 409 {
		t.Fatalf("stale edit accepted: %d", rr.Code)
	}
	rr = authReq(a, http.MethodPut, url, map[string]any{"deadline": "99:77", "version": 2}, session, "")
	if rr.Code != 400 {
		t.Fatalf("invalid due accepted: %d", rr.Code)
	}
	if _, err := db.Exec(`UPDATE routes SET status='liberado',released_at=now() WHERE id=$1`, route); err != nil {
		t.Fatal(err)
	}
	rr = authReq(a, http.MethodPut, url, map[string]any{"deadline": "09:30", "version": 2}, session, "")
	if rr.Code != 409 {
		t.Fatalf("deadline changed after release: %d", rr.Code)
	}
}

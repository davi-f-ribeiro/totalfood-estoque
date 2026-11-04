package main

import (
	"math"
	"net/http"
	"testing"
	"time"
)

func testGPS() assetGPSPoint {
	return assetGPSPoint{Latitude: -3.7319, Longitude: -38.5267,
		AccuracyM: 12.5, CapturedAt: time.Now().UTC().Truncate(time.Millisecond)}
}

func TestValidAssetGPS(t *testing.T) {
	valid := testGPS()
	if !validGPSPoint(valid) {
		t.Fatal("valid capture rejected")
	}
	bad := []assetGPSPoint{}
	p := valid
	p.Latitude = 91
	bad = append(bad, p)
	p = valid
	p.Longitude = -181
	bad = append(bad, p)
	p = valid
	p.AccuracyM = -1
	bad = append(bad, p)
	p = valid
	p.AccuracyM = math.Inf(1)
	bad = append(bad, p)
	p = valid
	p.Latitude = math.NaN()
	bad = append(bad, p)
	p = valid
	p.CapturedAt = time.Now().Add(-31 * 24 * time.Hour)
	bad = append(bad, p)
	p = valid
	p.CapturedAt = time.Now().Add(time.Hour)
	bad = append(bad, p)
	for i, sample := range bad {
		if validGPSPoint(sample) {
			t.Fatalf("invalid GPS accepted: %d", i)
		}
	}
}

func TestAssetGPSAtomicOperationsAndAudit(t *testing.T) {
	a, _, warehouseA, warehouseB := newStockTestApp(t)
	createTestUser(t, a, "director-gps", "secret", "Diretor", true)
	createTestUser(t, a, "sales-gps", "secret", "Vendas", true)
	director := doLogin(t, a, "director-gps", "secret")
	sales := doLogin(t, a, "sales-gps", "secret")
	var typeID int64
	if err := a.db.QueryRow("INSERT INTO asset_types(name,active) VALUES('Patrimonio GPS',true) RETURNING id").Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	tag := "PAT-GPS-01"
	gps := testGPS()
	create := map[string]any{"operation_id": "create-gps", "asset_type_id": typeID,
		"tag": tag, "status": "ATIVO", "warehouse_id": warehouseA, "gps": gps}
	for retry := 0; retry < 2; retry++ {
		rr := authReq(a, http.MethodPost, "/api/assets", create, director, "")
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d: %d %s", retry, rr.Code, rr.Body.String())
		}
	}
	var assetID int64
	if err := a.db.QueryRow("SELECT id FROM assets WHERE tag=$1", tag).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	assertCount(t, a.db, "SELECT count(*) FROM assets WHERE tag=$1", 1, tag)
	assertCount(t, a.db, "SELECT count(*) FROM asset_events WHERE operation_id='create-gps' AND event_type='CREATE'", 1)
	assertCount(t, a.db, "SELECT count(*) FROM asset_gps_history WHERE asset_id=$1 AND source='CREATE'", 1, assetID)
	conflict := map[string]any{"operation_id": "create-gps", "asset_type_id": typeID,
		"tag": "PAT-GPS-OTHER", "status": "ATIVO", "warehouse_id": warehouseA, "gps": gps}
	if rr := authReq(a, http.MethodPost, "/api/assets", conflict, director, ""); rr.Code != 409 {
		t.Fatalf("changed create replay: %d", rr.Code)
	}
	moveGPS := testGPS()
	moveGPS.Longitude += 0.002
	move := map[string]any{"operation_id": "move-gps", "warehouse_id": warehouseB, "gps": moveGPS}
	for retry := 0; retry < 2; retry++ {
		rr := authReq(a, http.MethodPost, "/api/assets/"+tag+"/move", move, director, "")
		if rr.Code != 200 {
			t.Fatalf("move %d: %d %s", retry, rr.Code, rr.Body.String())
		}
	}
	assertCount(t, a.db, "SELECT count(*) FROM asset_events WHERE operation_id='move-gps'", 1)
	assertCount(t, a.db, "SELECT count(*) FROM asset_gps_history WHERE asset_id=$1 AND source='MOVE'", 1, assetID)

	// Missing GPS must not invent coordinates or prevent a legitimate operation.
	status := map[string]any{"operation_id": "status-no-gps", "status": "EM_MANUTENCAO"}
	if rr := authReq(a, http.MethodPost, "/api/assets/"+tag+"/status", status, director, ""); rr.Code != 200 {
		t.Fatalf("status without GPS: %d %s", rr.Code, rr.Body.String())
	}
	assertCount(t, a.db, "SELECT count(*) FROM asset_events WHERE operation_id='status-no-gps'", 1)
	assertCount(t, a.db, "SELECT count(*) FROM asset_gps_history WHERE asset_id=$1", 2, assetID)

	manualGPS := testGPS()
	manualGPS.Longitude += 0.003
	manual := map[string]any{"operation_id": "manual-gps", "gps": manualGPS}
	for retry := 0; retry < 2; retry++ {
		rr := authReq(a, http.MethodPost, "/api/assets/"+tag+"/gps", manual, director, "")
		want := 201
		if retry == 1 {
			want = 200
		}
		if rr.Code != want {
			t.Fatalf("manual %d: %d %s", retry, rr.Code, rr.Body.String())
		}
	}
	manual["gps"] = gps
	if rr := authReq(a, http.MethodPost, "/api/assets/"+tag+"/gps", manual, director, ""); rr.Code != 409 {
		t.Fatalf("manual payload tamper: %d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/assets/"+tag+"/gps", manual, sales, ""); rr.Code != 403 {
		t.Fatalf("unauthorized GPS write: %d", rr.Code)
	}
	rr := authReq(a, http.MethodGet, "/api/assets/"+tag+"/gps", nil, director, "")
	if rr.Code != 200 {
		t.Fatalf("history: %d %s", rr.Code, rr.Body.String())
	}
	assertCount(t, a.db, "SELECT count(*) FROM asset_gps_history WHERE asset_id=$1", 3, assetID)
	if _, err := a.db.Exec("UPDATE asset_gps_history SET accuracy_m=999 WHERE operation_id='manual-gps'"); err == nil {
		t.Fatal("audit history allowed UPDATE")
	}
	if _, err := a.db.Exec("DELETE FROM asset_gps_history WHERE operation_id='manual-gps'"); err == nil {
		t.Fatal("audit history allowed DELETE")
	}
}

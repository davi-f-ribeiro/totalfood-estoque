package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestAssetsPostgreSQLFlowAuthorizationAndIdempotency(t *testing.T) {
	db := openTestDatabase(t)
	a := &app{db: db}
	ctx := context.Background()
	if err := runMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	directorID := createTestUser(t, a, "director-assets", "secret", "Diretor", true)
	createTestUser(t, a, "sales-assets", "secret", "Vendas", true)
	director := doLogin(t, a, "director-assets", "secret")
	sales := doLogin(t, a, "sales-assets", "secret")

	if rr := authReq(a, http.MethodPost, "/api/assets/types", map[string]any{"name": "Cadeira de plástico preta"}, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("assets type without permission status=%d", rr.Code)
	}

	rr := authReq(a, http.MethodPost, "/api/assets/types", map[string]any{"name": "Cadeira de plástico preta"}, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create type status=%d body=%s", rr.Code, rr.Body.String())
	}
	var typeID int64
	if err := db.QueryRow(`SELECT id FROM asset_types WHERE name='Cadeira de plástico preta'`).Scan(&typeID); err != nil {
		t.Fatalf("type not created: %v", err)
	}

	var vrID, dryID int64
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE name='VR'`).Scan(&vrID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE name='Totalfood Estoque Seco'`).Scan(&dryID); err != nil {
		t.Fatal(err)
	}

	rr = authReq(a, http.MethodPost, "/api/assets", map[string]any{"asset_type_id": typeID, "tag": "PAT-001", "status": "ATIVO", "warehouse_id": vrID}, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create asset status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/assets", map[string]any{"asset_type_id": typeID, "tag": "PAT-001", "status": "ATIVO", "warehouse_id": vrID}, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate tag status=%d", rr.Code)
	}
	var assetID, current int64
	var status string
	if err := db.QueryRow(`SELECT id,status,current_warehouse_id FROM assets WHERE tag='PAT-001'`).Scan(&assetID, &status, &current); err != nil {
		t.Fatal(err)
	}
	if status != "ATIVO" || current != vrID {
		t.Fatalf("initial status/current = %s/%d", status, current)
	}

	move := map[string]any{"operation_id": "asset-move-1", "warehouse_id": dryID}
	rr = authReq(a, http.MethodPost, "/api/assets/PAT-001/move", move, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("move status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = authReq(a, http.MethodPost, "/api/assets/PAT-001/move", move, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("move retry status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := db.QueryRow(`SELECT current_warehouse_id FROM assets WHERE id=$1`, assetID).Scan(&current); err != nil || current != dryID {
		t.Fatalf("current after move=%d err=%v", current, err)
	}
	assertCount(t, db, `SELECT count(*) FROM asset_events WHERE asset_id=$1 AND event_type='MOVE' AND origin_warehouse_id=$2 AND destination_warehouse_id=$3 AND user_id=$4`, 1, assetID, vrID, dryID, directorID)

	for _, tc := range []struct{ op, want string }{{"maint", "EM_MANUTENCAO"}, {"active", "ATIVO"}, {"retire", "BAIXADO"}} {
		rr = authReq(a, http.MethodPost, "/api/assets/PAT-001/status", map[string]any{"operation_id": "asset-status-" + tc.op, "status": tc.want}, director, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status %s http=%d body=%s", tc.want, rr.Code, rr.Body.String())
		}
		if err := db.QueryRow(`SELECT status FROM assets WHERE id=$1`, assetID).Scan(&status); err != nil || status != tc.want {
			t.Fatalf("status got=%s err=%v", status, err)
		}
	}
	var whNull bool
	if err := db.QueryRow(`SELECT current_warehouse_id IS NULL FROM assets WHERE id=$1`, assetID).Scan(&whNull); err != nil || !whNull {
		t.Fatalf("baixado must clear current warehouse null=%v err=%v", whNull, err)
	}
	assertCount(t, db, `SELECT count(*) FROM asset_events WHERE asset_id=$1`, 4, assetID)
}


func TestAssetPatchCannotBypassOperationalEventHistory(t *testing.T) {
	a, _, wh1, wh2 := newStockTestApp(t)
	director := createTestUser(t, a, "asset-patch-director", "secret", "Diretor", true)
	login := doLogin(t, a, "asset-patch-director", "secret")

	var typeA, typeB int64
	if err := a.db.QueryRow("INSERT INTO asset_types(name,active) VALUES('Patch Type A',true) RETURNING id").Scan(&typeA); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("INSERT INTO asset_types(name,active) VALUES('Patch Type B',true) RETURNING id").Scan(&typeB); err != nil {
		t.Fatal(err)
	}

	rr := authReq(a, http.MethodPost, "/api/assets", map[string]any{
		"asset_type_id": typeA,
		"tag": "PAT-PATCH-1",
		"status": "ATIVO",
		"warehouse_id": wh1,
	}, login, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}

	var assetID int64
	if err := a.db.QueryRow("SELECT id FROM assets WHERE tag='PAT-PATCH-1'").Scan(&assetID); err != nil {
		t.Fatal(err)
	}

	rr = authReq(a, http.MethodPatch, "/api/assets/"+itoa(assetID), map[string]any{
		"asset_type_id": typeB,
		"tag": "PAT-PATCH-2",
		"status": "EM_MANUTENCAO",
		"warehouse_id": wh2,
		"version": crudVersion(t, a, "assets", assetID),
	}, login, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("operational fields accepted by generic patch: status=%d body=%s", rr.Code, rr.Body.String())
	}

	var tag, status string
	var warehouseID, typeID int64
	if err := a.db.QueryRow("SELECT tag,status,current_warehouse_id,asset_type_id FROM assets WHERE id=$1", assetID).Scan(&tag, &status, &warehouseID, &typeID); err != nil {
		t.Fatal(err)
	}
	if tag != "PAT-PATCH-1" || status != "ATIVO" || warehouseID != wh1 || typeID != typeA {
		t.Fatalf("rejected patch changed asset: tag=%s status=%s warehouse=%d type=%d", tag, status, warehouseID, typeID)
	}
	assertCount(t, a.db, "SELECT count(*) FROM asset_events WHERE asset_id=$1", 0, assetID)

	rr = authReq(a, http.MethodPatch, "/api/assets/"+itoa(assetID), map[string]any{
		"asset_type_id": typeB,
		"tag": "PAT-PATCH-2",
		"version": crudVersion(t, a, "assets", assetID),
	}, login, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("cadastral patch status=%d body=%s", rr.Code, rr.Body.String())
	}

	if err := a.db.QueryRow("SELECT tag,status,current_warehouse_id,asset_type_id FROM assets WHERE id=$1", assetID).Scan(&tag, &status, &warehouseID, &typeID); err != nil {
		t.Fatal(err)
	}
	if tag != "PAT-PATCH-2" || status != "ATIVO" || warehouseID != wh1 || typeID != typeB {
		t.Fatalf("cadastral patch changed operational state: tag=%s status=%s warehouse=%d type=%d", tag, status, warehouseID, typeID)
	}
	assertCount(t, a.db, "SELECT count(*) FROM asset_events WHERE asset_id=$1", 0, assetID)

	if director <= 0 {
		t.Fatal("director not created")
	}
}

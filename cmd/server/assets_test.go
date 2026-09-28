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

package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestCRUDOptimisticConcurrencyRejectsStaleEdits(t *testing.T) {
	a, _, wh1, wh2 := newStockTestApp(t)
	createTestUser(t, a, "crud-version-director", "secret", "Diretor", true)
	director := doLogin(t, a, "crud-version-director", "secret")

	t.Run("catalog item", func(t *testing.T) {
		catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)
		create := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{
			"name": "CRUD Version Item", "category_id": catID, "unit_code": "KG", "minimum_stock": "0",
		}, director, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create item status=%d body=%s", create.Code, create.Body.String())
		}
		id := jsonID(t, create.Body.Bytes())
		version := crudVersion(t, a, "inventory_items", id)

		first := authReq(a, http.MethodPatch, "/api/catalog/items/"+itoa(id), map[string]any{
			"name": "CRUD Version Item First", "category_id": catID, "unit_code": "KG",
			"minimum_stock": "1", "notes": "primeira edição", "active": true, "version": version,
		}, director, "")
		if first.Code != http.StatusOK {
			t.Fatalf("first item edit status=%d body=%s", first.Code, first.Body.String())
		}
		stale := authReq(a, http.MethodPatch, "/api/catalog/items/"+itoa(id), map[string]any{
			"name": "CRUD Version Item Stale", "category_id": catID, "unit_code": "KG",
			"minimum_stock": "9", "notes": "edição stale", "active": true, "version": version,
		}, director, "")
		if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale item edit status=%d body=%s", stale.Code, stale.Body.String())
		}
		var name, minimum string
		if err := a.db.QueryRow(`SELECT name,minimum_stock::text FROM inventory_items WHERE id=$1`, id).Scan(&name, &minimum); err != nil {
			t.Fatal(err)
		}
		if name != "CRUD Version Item First" || minimum != "1.000" {
			t.Fatalf("stale item overwrote first edit name=%q minimum=%q", name, minimum)
		}
	})

	t.Run("sku", func(t *testing.T) {
		catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)
		parent := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{
			"name": "CRUD Version SKU Parent", "category_id": catID, "unit_code": "UN", "minimum_stock": "0",
		}, director, "")
		parentID := jsonID(t, parent.Body.Bytes())
		brandID := createTestBrand(t,a,"CRUD Version Brand")
		create := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{
			"item_id": parentID, "brand_id":brandID, "content_quantity":"1", "barcode": "7899999000001",
		}, director, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create sku status=%d body=%s", create.Code, create.Body.String())
		}
		id := jsonID(t, create.Body.Bytes())
		version := crudVersion(t, a, "product_skus", id)

		first := authReq(a, http.MethodPatch, "/api/catalog/skus/"+itoa(id), map[string]any{
			"item_id": parentID, "brand_id":brandID, "content_quantity":"2", "barcode": "7899999000001",
			"active": true, "version": version,
		}, director, "")
		if first.Code != http.StatusOK {
			t.Fatalf("first sku edit status=%d body=%s", first.Code, first.Body.String())
		}
		stale := authReq(a, http.MethodPatch, "/api/catalog/skus/"+itoa(id), map[string]any{
			"item_id": parentID, "brand_id":brandID, "content_quantity":"3", "barcode": "7899999000001",
			"active": true, "version": version,
		}, director, "")
		if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale sku edit status=%d body=%s", stale.Code, stale.Body.String())
		}
		var name string
		if err := a.db.QueryRow(`SELECT name FROM product_skus WHERE id=$1`, id).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(name,"CRUD Version Brand") || !strings.Contains(name,"2 UN") {
			t.Fatalf("stale sku overwrote first edit name=%q", name)
		}
	})

	t.Run("user and password endpoint share version", func(t *testing.T) {
		create := authReq(a, http.MethodPost, "/api/admin/users", map[string]any{
			"login": "crud-managed", "password": "oldpass",
			"profile_id": profileID(t, a, "Vendas"), "active": true,
		}, director, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create user status=%d body=%s", create.Code, create.Body.String())
		}
		id := jsonID(t, create.Body.Bytes())
		version := crudVersion(t, a, "users", id)

		first := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(id), map[string]any{
			"login": "crud-managed-first", "profile_id": profileID(t, a, "Vendas"),
			"active": true, "version": version,
		}, director, "")
		if first.Code != http.StatusOK {
			t.Fatalf("first user edit status=%d body=%s", first.Code, first.Body.String())
		}
		stale := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(id), map[string]any{
			"login": "crud-managed-stale", "profile_id": profileID(t, a, "Vendas"),
			"active": true, "version": version,
		}, director, "")
		if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale user edit status=%d body=%s", stale.Code, stale.Body.String())
		}
		stalePassword := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(id)+"/password", map[string]any{
			"password": "must-not-apply", "version": version,
		}, director, "")
		if stalePassword.Code != http.StatusConflict || !strings.Contains(stalePassword.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale password reset status=%d body=%s", stalePassword.Code, stalePassword.Body.String())
		}
		var login string
		if err := a.db.QueryRow(`SELECT login FROM users WHERE id=$1`, id).Scan(&login); err != nil {
			t.Fatal(err)
		}
		if login != "crud-managed-first" {
			t.Fatalf("stale user edit overwrote login=%q", login)
		}
		if rr := loginAttempt(a, "crud-managed-first", "oldpass"); rr.Code != http.StatusOK {
			t.Fatalf("stale password reset changed password status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("profile name and permissions share version", func(t *testing.T) {
		create := authReq(a, http.MethodPost, "/api/admin/profiles", map[string]any{
			"name": "CRUD Version Profile",
		}, director, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create profile status=%d body=%s", create.Code, create.Body.String())
		}
		id := jsonID(t, create.Body.Bytes())
		version := crudVersion(t, a, "profiles", id)

		first := authReq(a, http.MethodPatch, "/api/admin/profiles/"+itoa(id), map[string]any{
			"name": "CRUD Version Profile First", "version": version,
		}, director, "")
		if first.Code != http.StatusOK {
			t.Fatalf("first profile edit status=%d body=%s", first.Code, first.Body.String())
		}
		stalePermissions := authReq(a, http.MethodPut, "/api/admin/profiles/"+itoa(id)+"/permissions", map[string]any{
			"permission_ids": []int64{permissionID(t, a, "stock", "read")}, "version": version,
		}, director, "")
		if stalePermissions.Code != http.StatusConflict || !strings.Contains(stalePermissions.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale profile permissions status=%d body=%s", stalePermissions.Code, stalePermissions.Body.String())
		}
		var name string
		var permissionCount int
		if err := a.db.QueryRow(`SELECT name FROM profiles WHERE id=$1`, id).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if err := a.db.QueryRow(`SELECT count(*) FROM profile_permissions WHERE profile_id=$1`, id).Scan(&permissionCount); err != nil {
			t.Fatal(err)
		}
		if name != "CRUD Version Profile First" || permissionCount != 0 {
			t.Fatalf("stale profile mutation persisted name=%q permissions=%d", name, permissionCount)
		}
	})

	t.Run("asset operational change invalidates stale cadastral edit", func(t *testing.T) {
		typeResp := authReq(a, http.MethodPost, "/api/assets/types", map[string]any{
			"name": "CRUD Version Asset Type",
		}, director, "")
		if typeResp.Code != http.StatusCreated {
			t.Fatalf("create asset type status=%d body=%s", typeResp.Code, typeResp.Body.String())
		}
		typeID := jsonID(t, typeResp.Body.Bytes())
		create := authReq(a, http.MethodPost, "/api/assets", map[string]any{
			"asset_type_id": typeID, "tag": "PAT-CRUD-VERSION", "status": "ATIVO", "warehouse_id": wh1,
		}, director, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create asset status=%d body=%s", create.Code, create.Body.String())
		}
		id := jsonID(t, create.Body.Bytes())
		version := crudVersion(t, a, "assets", id)

		move := authReq(a, http.MethodPost, "/api/assets/PAT-CRUD-VERSION/move", map[string]any{
			"operation_id": "crud-version-asset-move", "warehouse_id": wh2,
		}, director, "")
		if move.Code != http.StatusOK {
			t.Fatalf("asset move status=%d body=%s", move.Code, move.Body.String())
		}
		stale := authReq(a, http.MethodPatch, "/api/assets/"+itoa(id), map[string]any{
			"asset_type_id": typeID, "tag": "PAT-CRUD-STALE", "version": version,
		}, director, "")
		if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale asset edit status=%d body=%s", stale.Code, stale.Body.String())
		}
		var tag string
		var warehouseID int64
		if err := a.db.QueryRow(`SELECT tag,current_warehouse_id FROM assets WHERE id=$1`, id).Scan(&tag, &warehouseID); err != nil {
			t.Fatal(err)
		}
		if tag != "PAT-CRUD-VERSION" || warehouseID != wh2 {
			t.Fatalf("stale asset edit changed operational result tag=%q warehouse=%d", tag, warehouseID)
		}
	})
}

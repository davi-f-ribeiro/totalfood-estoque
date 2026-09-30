package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type inventoryTestEnv struct {
	a      *app
	db     *sql.DB
	userID int64
}

func newInventoryTestEnv(t *testing.T) inventoryTestEnv {
	t.Helper()
	db := openTestDatabase(t)
	ctx := context.Background()
	if err := runMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE warehouses SET active=false`); err != nil {
		t.Fatalf("deactivate seeded warehouses: %v", err)
	}
	var profileID, userID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM profiles WHERE name='Diretor'`).Scan(&profileID); err != nil {
		t.Fatalf("director profile: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (login,password_hash,profile_id,active,must_change_password)
		 VALUES ($1,'test', $2,true,false) RETURNING id`,
		"inventory-"+t.Name(), profileID).Scan(&userID); err != nil {
		t.Fatalf("test user: %v", err)
	}
	return inventoryTestEnv{a: &app{db: db}, db: db, userID: userID}
}

func (e inventoryTestEnv) warehouse(t *testing.T, name string, active bool) int64 {
	t.Helper()
	var id int64
	if err := e.db.QueryRow(`INSERT INTO warehouses (name,active) VALUES ($1,$2) RETURNING id`, t.Name()+"-"+name, active).Scan(&id); err != nil {
		t.Fatalf("warehouse: %v", err)
	}
	return id
}

func (e inventoryTestEnv) sku(t *testing.T, label string, itemActive, skuActive bool) int64 {
	t.Helper()
	var catID, itemID, skuID int64
	if err := e.db.QueryRow(`SELECT id FROM categories ORDER BY id LIMIT 1`).Scan(&catID); err != nil {
		t.Fatalf("category: %v", err)
	}
	if err := e.db.QueryRow(
		`INSERT INTO inventory_items (name,category_id,unit_code,active) VALUES ($1,$2,'KG',$3) RETURNING id`,
		t.Name()+"-"+label, catID, itemActive).Scan(&itemID); err != nil {
		t.Fatalf("item: %v", err)
	}
	if err := e.db.QueryRow(
		`INSERT INTO product_skus (item_id,name,barcode,active) VALUES ($1,$2,$3,$4) RETURNING id`,
		itemID, label, fmt.Sprintf("%s-%s", t.Name(), label), skuActive).Scan(&skuID); err != nil {
		t.Fatalf("sku: %v", err)
	}
	return skuID
}

func (e inventoryTestEnv) movement(t *testing.T, skuID, warehouseID int64, direction string, qty int) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	typ := "ENTRADA"
	if direction == "OUT" {
		typ = "SAIDA"
	}
	var movementID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO movements (movement_type,user_id) VALUES ($1,$2) RETURNING id`, typ, e.userID).Scan(&movementID); err != nil {
		t.Fatalf("movement header: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO movement_lines (movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
		 VALUES ($1,$2,$3,$4,$5,1)`,
		movementID, skuID, warehouseID, direction, qty); err != nil {
		t.Fatalf("movement line: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("movement commit: %v", err)
	}
}

func TestBlindSampleSizePolicy(t *testing.T) {
	cases := map[int]int{
		2: 1, 3: 1, 4: 2, 10: 5, 11: 5, 20: 10, 21: 10, 30: 10,
		31: 15, 60: 15, 61: 20, 100: 20, 101: 30, 200: 30, 201: 40, 500: 40,
	}
	for eligible, want := range cases {
		if got := blindSampleSize(eligible); got != want {
			t.Fatalf("eligible=%d got=%d want=%d", eligible, got, want)
		}
	}
	for eligible := 2; eligible <= 1000; eligible++ {
		got := blindSampleSize(eligible)
		if got > eligible/2 {
			t.Fatalf("eligible=%d sample=%d exceeds floor(n/2)=%d", eligible, got, eligible/2)
		}
	}
	if got := blindSampleSize(1); got != 0 {
		t.Fatalf("single SKU warehouse must be ineligible, got sample=%d", got)
	}
}

func TestInventoryEligibilityAndTotal(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "main", true)
	other := e.warehouse(t, "other", true)
	inactiveWH := e.warehouse(t, "inactive", false)

	positive := e.sku(t, "positive", true, true)
	zero := e.sku(t, "zero", true, true)
	negative := e.sku(t, "negative", true, true)
	inactiveSKU := e.sku(t, "inactive-sku", true, false)
	inactiveItem := e.sku(t, "inactive-item", false, true)
	otherOnly := e.sku(t, "other-only", true, true)

	e.movement(t, positive, wh, "IN", 5)
	e.movement(t, zero, wh, "IN", 5)
	e.movement(t, zero, wh, "OUT", 5)
	e.movement(t, negative, wh, "OUT", 2)
	e.movement(t, inactiveSKU, wh, "IN", 3)
	e.movement(t, inactiveItem, wh, "IN", 3)
	e.movement(t, otherOnly, other, "IN", 4)

	rows, err := inventoryEligibility(ctx, e.db, wh)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[int64]string{}
	for _, row := range rows {
		reasons[row.SKUID] = row.Reason
	}
	if reasons[positive] != "eligible" || reasons[zero] != "zero_balance" || reasons[negative] != "negative_balance" {
		t.Fatalf("balance eligibility reasons=%v", reasons)
	}
	if reasons[inactiveSKU] != "sku_inactive" || reasons[inactiveItem] != "item_inactive" {
		t.Fatalf("inactive eligibility reasons=%v", reasons)
	}
	if reasons[otherOnly] != "zero_balance" {
		t.Fatalf("SKU with stock only in another warehouse must be zero here: %v", reasons[otherOnly])
	}

	res, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatalf("create total: %v", err)
	}
	if res.Type != "TOTAL" || res.WarehouseID != wh || len(res.SKUIDs) != 1 || res.SKUIDs[0] != positive {
		t.Fatalf("TOTAL selected wrong SKUs: %+v", res)
	}

	if _, err := e.a.createTotalInventory(ctx, e.userID, 999999); err != errInventoryWarehouseNotFound {
		t.Fatalf("missing warehouse error=%v", err)
	}
	if _, err := e.a.createTotalInventory(ctx, e.userID, inactiveWH); err != errInventoryWarehouseInactive {
		t.Fatalf("inactive warehouse error=%v", err)
	}
	empty := e.warehouse(t, "empty", true)
	if _, err := e.a.createTotalInventory(ctx, e.userID, empty); err != errInventoryNoEligibleSKUs {
		t.Fatalf("empty warehouse error=%v", err)
	}
}

func TestBlindPayloadRejectsClientSelectionControls(t *testing.T) {
	for _, body := range []string{
		`{"warehouse_id":1}`,
		`{"quantity":2}`,
		`{"sku_id":3}`,
		`{"seed":"x"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/inventory-validations/blind", strings.NewReader(body))
		rec := httptest.NewRecorder()
		var dst struct{}
		if decodeInventoryJSON(rec, req, &dst, true) {
			t.Fatalf("client selection control accepted: %s", body)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, rec.Code)
		}
	}
}

func TestInventoryCreationRequiresWritePermission(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	var profileID, userID int64
	if err := e.db.QueryRowContext(ctx, `SELECT id FROM profiles WHERE name='Estoquista'`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx,
		`INSERT INTO users (login,password_hash,profile_id,active,must_change_password)
		 VALUES ($1,'test',$2,true,false) RETURNING id`,
		"no-inventory-"+t.Name(), profileID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	token, csrf := "session-token", "csrf-token"
	if _, err := e.db.ExecContext(ctx,
		`INSERT INTO sessions (user_id,token_hash,csrf_token_hash,expires_at)
		 VALUES ($1,$2,$3,now()+interval '1 hour')`,
		userID, hashToken(token), hashToken(csrf)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/inventory-validations/blind", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	req.Header.Set(csrfHeaderName, csrf)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBlindSKUHistoryAndRerollCancellation(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "blind", true)
	skus := []int64{
		e.sku(t, "A", true, true),
		e.sku(t, "B", true, true),
		e.sku(t, "C", true, true),
		e.sku(t, "D", true, true),
	}
	for _, sku := range skus {
		e.movement(t, sku, wh, "IN", 10)
	}

	first, err := e.a.createBlindInventory(ctx, e.userID)
	if err != nil {
		t.Fatalf("first blind: %v", err)
	}
	if first.WarehouseID != wh || len(first.SKUIDs) != 2 {
		t.Fatalf("first blind=%+v", first)
	}
	if _, err := e.db.ExecContext(ctx,
		`UPDATE inventory_validations SET status='CANCELLED',cancelled_at=now() WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := e.a.createBlindInventory(ctx, e.userID)
	if err != nil {
		t.Fatalf("second blind: %v", err)
	}
	if len(second.SKUIDs) != 2 {
		t.Fatalf("second sample=%v", second.SKUIDs)
	}
	firstSet := map[int64]bool{}
	for _, id := range first.SKUIDs {
		firstSet[id] = true
	}
	for _, id := range second.SKUIDs {
		if firstSet[id] {
			t.Fatalf("cancelled selection did not influence reroll; repeated sku=%d first=%v second=%v", id, first.SKUIDs, second.SKUIDs)
		}
	}
	var persisted int
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_validation_lines WHERE validation_id=$1`, first.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 2 {
		t.Fatalf("cancelled inventory selection was not preserved: %d", persisted)
	}

	loaded, err := e.a.getInventoryValidation(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(loaded.SKUIDs) != fmt.Sprint(second.SKUIDs) {
		t.Fatalf("read rerolled selection: created=%v loaded=%v", second.SKUIDs, loaded.SKUIDs)
	}
}

func TestBlindSKUHistoricalPriority(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "history", true)
	aSKU := e.sku(t, "A", true, true)
	bSKU := e.sku(t, "B", true, true)
	cSKU := e.sku(t, "C", true, true)
	for _, sku := range []int64{aSKU, bSKU, cSKU} {
		e.movement(t, sku, wh, "IN", 5)
	}
	oldID := insertInventoryHistory(t, e, wh, "TOTAL", time.Now().Add(-48*time.Hour), []int64{bSKU})
	_ = oldID
	insertInventoryHistory(t, e, wh, "TOTAL", time.Now().Add(-2*time.Hour), []int64{cSKU})

	tx, _ := e.db.BeginTx(ctx, nil)
	selected, err := selectBlindSKUs(ctx, tx, wh, 1)
	tx.Rollback()
	if err != nil || len(selected) != 1 || selected[0] != aSKU {
		t.Fatalf("never-selected SKU must win: selected=%v err=%v", selected, err)
	}

	insertInventoryHistory(t, e, wh, "TOTAL", time.Now().Add(-1*time.Hour), []int64{aSKU})
	tx, _ = e.db.BeginTx(ctx, nil)
	selected, err = selectBlindSKUs(ctx, tx, wh, 1)
	tx.Rollback()
	if err != nil || len(selected) != 1 || selected[0] != bSKU {
		t.Fatalf("oldest SKU must win: selected=%v err=%v", selected, err)
	}
}

func TestBlindWarehouseHistoricalPriority(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wa := e.warehouse(t, "A", true)
	wb := e.warehouse(t, "B", true)
	wc := e.warehouse(t, "C", true)
	for wi, wh := range []int64{wa, wb, wc} {
		for j := 0; j < 2; j++ {
			sku := e.sku(t, fmt.Sprintf("w%d-s%d", wi, j), true, true)
			e.movement(t, sku, wh, "IN", 3)
		}
	}
	insertInventoryHistory(t, e, wb, "BLIND", time.Now().Add(-48*time.Hour), nil)
	insertInventoryHistory(t, e, wc, "BLIND", time.Now().Add(-2*time.Hour), nil)

	tx, _ := e.db.BeginTx(ctx, nil)
	got, count, err := selectBlindWarehouse(ctx, tx)
	tx.Rollback()
	if err != nil || got != wa || count != 2 {
		t.Fatalf("never-used warehouse must win got=%d count=%d err=%v", got, count, err)
	}

	insertInventoryHistory(t, e, wa, "BLIND", time.Now().Add(-1*time.Hour), nil)
	tx, _ = e.db.BeginTx(ctx, nil)
	got, _, err = selectBlindWarehouse(ctx, tx)
	tx.Rollback()
	if err != nil || got != wb {
		t.Fatalf("oldest warehouse must win got=%d err=%v", got, err)
	}
}

func TestInventorySelectionAtomicity(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "atomic", true)
	sku := e.sku(t, "atomic-sku", true, true)
	e.movement(t, sku, wh, "IN", 1)

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = persistInventorySelection(ctx, tx, "TOTAL", e.userID, wh, []int64{sku, sku})
	if err == nil {
		t.Fatal("expected duplicate SKU insertion to fail")
	}
	_ = tx.Rollback()

	var headers, lines int
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_validations WHERE warehouse_id=$1`, wh).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_validation_lines l JOIN inventory_validations v ON v.id=l.validation_id WHERE v.warehouse_id=$1`, wh).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if headers != 0 || lines != 0 {
		t.Fatalf("partial inventory persisted headers=%d lines=%d", headers, lines)
	}
}

func TestTotalAndBlindDoNotMoveStock(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "anti-move", true)
	for i := 0; i < 4; i++ {
		sku := e.sku(t, fmt.Sprintf("anti-%d", i), true, true)
		e.movement(t, sku, wh, "IN", i+1)
	}
	var movementsBefore, linesBefore int64
	var balanceBefore string
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM movements`).Scan(&movementsBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM movement_lines`).Scan(&linesBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text FROM movement_lines`).Scan(&balanceBefore); err != nil {
		t.Fatal(err)
	}

	if _, err := e.a.createTotalInventory(ctx, e.userID, wh); err != nil {
		t.Fatalf("total: %v", err)
	}
	if _, err := e.a.createBlindInventory(ctx, e.userID); err != nil {
		t.Fatalf("blind: %v", err)
	}

	var movementsAfter, linesAfter int64
	var balanceAfter string
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM movements`).Scan(&movementsAfter)
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM movement_lines`).Scan(&linesAfter)
	_ = e.db.QueryRowContext(ctx, `SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text FROM movement_lines`).Scan(&balanceAfter)
	if movementsBefore != movementsAfter || linesBefore != linesAfter || balanceBefore != balanceAfter {
		t.Fatalf("inventory changed stock movements %d->%d lines %d->%d balance %s->%s",
			movementsBefore, movementsAfter, linesBefore, linesAfter, balanceBefore, balanceAfter)
	}
}

func insertInventoryHistory(t *testing.T, e inventoryTestEnv, warehouseID int64, typ string, createdAt time.Time, skuIDs []int64) int64 {
	t.Helper()
	var id int64
	if err := e.db.QueryRow(
		`INSERT INTO inventory_validations (type,warehouse_id,status,responsible_user_id,created_at)
		 VALUES ($1,$2,'OPEN',$3,$4) RETURNING id`,
		typ, warehouseID, e.userID, createdAt).Scan(&id); err != nil {
		t.Fatalf("history header: %v", err)
	}
	for _, sku := range skuIDs {
		if _, err := e.db.Exec(`INSERT INTO inventory_validation_lines (validation_id,sku_id,status,created_at) VALUES ($1,$2,'OPEN',$3)`, id, sku, createdAt); err != nil {
			t.Fatalf("history line: %v", err)
		}
	}
	return id
}

func TestBlindSelectionHasNoDuplicates(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "duplicates", true)
	for i := 0; i < 10; i++ {
		sku := e.sku(t, fmt.Sprintf("d-%d", i), true, true)
		e.movement(t, sku, wh, "IN", 1)
	}
	res, err := e.a.createBlindInventory(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SKUIDs) != 5 {
		t.Fatalf("sample size=%d want=5", len(res.SKUIDs))
	}
	ids := append([]int64(nil), res.SKUIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("duplicate SKU in BLIND: %v", ids)
		}
	}
}

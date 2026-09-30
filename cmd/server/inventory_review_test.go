package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func countInventoryToReview(t *testing.T, e inventoryTestEnv, inv inventorySelection, quantities map[int64]string) {
	t.Helper()
	ctx := context.Background()
	view, err := e.a.getInventoryCountingView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range view.Lines {
		q, ok := quantities[line.SKUID]
		if !ok {
			t.Fatalf("missing count for sku=%d", line.SKUID)
		}
		if _, _, err := e.a.saveInventoryCount(ctx, inv.ID, line.SKUID, q, line.Version, e.userID); err != nil {
			t.Fatalf("count sku=%d: %v", line.SKUID, err)
		}
	}
	var status string
	if err := e.db.QueryRowContext(ctx, `SELECT status FROM inventory_validations WHERE id=$1`, inv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "REVIEW" {
		t.Fatalf("status=%s want REVIEW", status)
	}
}

func reviewBySKU(t *testing.T, view inventoryReviewView) map[int64]inventoryReviewLine {
	t.Helper()
	out := map[int64]inventoryReviewLine{}
	for _, line := range view.Lines {
		out[line.SKUID] = line
	}
	return out
}

func TestInventoryReviewMathExactNoTolerance(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	_, inv, skus := setupTotalCounting(t, e, 3, "10.000")
	countInventoryToReview(t, e, inv, map[int64]string{
		skus[0]: "10.000",
		skus[1]: "8.000",
		skus[2]: "12.000",
	})

	view, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	lines := reviewBySKU(t, view)
	cases := map[int64]struct {
		state string
		diff  string
	}{
		skus[0]: {"MATCH", "0.000"},
		skus[1]: {"DIVERGENT", "-2.000"},
		skus[2]: {"DIVERGENT", "2.000"},
	}
	for sku, want := range cases {
		got := lines[sku]
		if got.State != want.state || got.Difference != want.diff {
			t.Fatalf("sku=%d state=%s diff=%s want state=%s diff=%s", sku, got.State, got.Difference, want.state, want.diff)
		}
		if got.SnapshotQuantity != "10.000" {
			t.Fatalf("sku=%d snapshot=%s", sku, got.SnapshotQuantity)
		}
	}
	if view.Lines[0].State != "DIVERGENT" || view.Lines[1].State != "DIVERGENT" {
		t.Fatalf("review must prioritize divergences: %+v", view.Lines)
	}

	decimalWH := e.warehouse(t, "review-decimal", true)
	decimalSKU := e.sku(t, "review-decimal-sku", true, true)
	e.movementDecimal(t, decimalSKU, decimalWH, "IN", "10.125")
	decimalInv, err := e.a.createTotalInventory(ctx, e.userID, decimalWH)
	if err != nil {
		t.Fatal(err)
	}
	countInventoryToReview(t, e, decimalInv, map[int64]string{decimalSKU: "10.124"})
	decimalView, err := e.a.getInventoryReviewView(ctx, decimalInv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := decimalView.Lines[0]; got.State != "DIVERGENT" || got.Difference != "-0.001" {
		t.Fatalf("exact decimal comparison failed: %+v", got)
	}
}

func TestInventoryRecountAuditLimitAndResult(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	_, inv, skus := setupTotalCounting(t, e, 3, "10.000")
	countInventoryToReview(t, e, inv, map[int64]string{
		skus[0]: "8.000",
		skus[1]: "8.000",
		skus[2]: "10.000",
	})
	view, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	lines := reviewBySKU(t, view)

	beforeFirst := lines[skus[0]]
	recountMatch, err := e.a.saveInventoryRecount(ctx, inv.ID, skus[0], "10.000", beforeFirst.Version, e.userID)
	if err != nil {
		t.Fatalf("recount to match: %v", err)
	}
	if recountMatch.State != "MATCH" || recountMatch.Difference != "0.000" || recountMatch.RecountQuantity == nil || *recountMatch.RecountQuantity != "10.000" {
		t.Fatalf("match recount=%+v", recountMatch)
	}
	if recountMatch.FirstQuantity != "8.000" || recountMatch.FirstCountedBy == "" || recountMatch.FirstCountedAt == nil {
		t.Fatalf("first observation lost after recount: %+v", recountMatch)
	}
	if recountMatch.RecountedBy == "" || recountMatch.RecountedAt == nil {
		t.Fatalf("recount audit missing: %+v", recountMatch)
	}
	if recountMatch.RecountAvailable {
		t.Fatal("second formal recount must not be available")
	}

	beforeSecond := lines[skus[1]]
	recountStillDivergent, err := e.a.saveInventoryRecount(ctx, inv.ID, skus[1], "9.000", beforeSecond.Version, e.userID)
	if err != nil {
		t.Fatalf("recount still divergent: %v", err)
	}
	if recountStillDivergent.State != "DIVERGENT" || recountStillDivergent.Difference != "-1.000" {
		t.Fatalf("divergent recount=%+v", recountStillDivergent)
	}
	if _, err := e.a.saveInventoryRecount(ctx, inv.ID, skus[1], "10.000", recountStillDivergent.Version, e.userID); !errors.Is(err, errInventoryRecountExists) {
		t.Fatalf("second formal recount error=%v", err)
	}

	matchLine := lines[skus[2]]
	if _, err := e.a.saveInventoryRecount(ctx, inv.ID, skus[2], "10.000", matchLine.Version, e.userID); !errors.Is(err, errInventoryNotDivergent) {
		t.Fatalf("MATCH recount error=%v", err)
	}

	var firstQty string
	if err := e.db.QueryRowContext(ctx, `
SELECT physical_quantity::text
FROM inventory_validation_lines
WHERE validation_id=$1 AND sku_id=$2`, inv.ID, skus[0]).Scan(&firstQty); err != nil {
		t.Fatal(err)
	}
	if firstQty != "8.000" {
		t.Fatalf("recount overwrote first observation: %s", firstQty)
	}
}

func TestInventoryRecountOptimisticConcurrencyHTTP(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, skus := setupTotalCounting(t, e, 2, "10.000")
	countInventoryToReview(t, e, inv, map[int64]string{
		skus[0]: "8.000",
		skus[1]: "10.000",
	})
	viewA, err := e.a.getInventoryReviewView(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	viewB, err := e.a.getInventoryReviewView(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := reviewBySKU(t, viewA)[skus[0]]
	b := reviewBySKU(t, viewB)[skus[0]]
	if a.Version != b.Version {
		t.Fatal("operators did not read same recount version")
	}

	token, csrf := inventorySession(t, e.db, e.userID)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)
	url := fmt.Sprintf("/api/inventory-validations/%d/lines/%d/recount", inv.ID, skus[0])

	recA := httptest.NewRecorder()
	mux.ServeHTTP(recA, inventoryRequest(http.MethodPut, url,
		fmt.Sprintf(`{"physical_quantity":"9.000","expected_version":%d}`, a.Version), token, csrf))
	if recA.Code != http.StatusOK {
		t.Fatalf("A status=%d body=%s", recA.Code, recA.Body.String())
	}

	recB := httptest.NewRecorder()
	mux.ServeHTTP(recB, inventoryRequest(http.MethodPut, url,
		fmt.Sprintf(`{"physical_quantity":"10.000","expected_version":%d}`, b.Version), token, csrf))
	if recB.Code != http.StatusConflict {
		t.Fatalf("B status=%d body=%s want 409", recB.Code, recB.Body.String())
	}

	var qty string
	if err := e.db.QueryRow(`
SELECT quantity::text FROM inventory_validation_recounts
WHERE validation_id=$1 AND sku_id=$2`, inv.ID, skus[0]).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != "9.000" {
		t.Fatalf("stale recount overwrote A: %s", qty)
	}
}

func TestInventoryRecountPayloadRejectsClientAuditFields(t *testing.T) {
	for _, body := range []string{
		`{"physical_quantity":"1","expected_version":1,"recounted_by_user_id":99}`,
		`{"physical_quantity":"1","expected_version":1,"recounted_at":"2026-09-30T10:00:00Z"}`,
		`{"physical_quantity":"1","expected_version":1,"system_quantity_snapshot":"1"}`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
		rec := httptest.NewRecorder()
		var dst inventoryRecountRequest
		if decodeInventoryJSON(rec, req, &dst, false) {
			t.Fatalf("unexpected recount field accepted: %s", body)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, rec.Code)
		}
	}
}

func TestBlindReviewLeakBoundaryHTTP(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	_, inv, _ := setupBlindCounting(t, e, 2, "987.321")
	token, csrf := inventorySession(t, e.db, e.userID)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)

	countingRec := httptest.NewRecorder()
	mux.ServeHTTP(countingRec, inventoryRequest(http.MethodGet,
		fmt.Sprintf("/api/inventory-validations/%d/counting", inv.ID), "", token, ""))
	if countingRec.Code != http.StatusOK {
		t.Fatalf("counting status=%d body=%s", countingRec.Code, countingRec.Body.String())
	}
	before := countingRec.Body.String()
	for _, forbidden := range []string{"987.321", "system_quantity_snapshot", "difference", "MATCH", "DIVERGENT"} {
		if strings.Contains(before, forbidden) {
			t.Fatalf("BLIND pre-review leaked %q: %s", forbidden, before)
		}
	}

	reviewEarly := httptest.NewRecorder()
	mux.ServeHTTP(reviewEarly, inventoryRequest(http.MethodGet,
		fmt.Sprintf("/api/inventory-validations/%d/review", inv.ID), "", token, ""))
	if reviewEarly.Code != http.StatusConflict {
		t.Fatalf("early review status=%d body=%s", reviewEarly.Code, reviewEarly.Body.String())
	}
	for _, forbidden := range []string{"987.321", "system_quantity_snapshot", "difference", "MATCH", "DIVERGENT"} {
		if strings.Contains(reviewEarly.Body.String(), forbidden) {
			t.Fatalf("early review error leaked %q", forbidden)
		}
	}

	view, err := e.a.getInventoryCountingView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Lines) != 1 {
		t.Fatalf("blind lines=%d", len(view.Lines))
	}
	line := view.Lines[0]
	saveRec := httptest.NewRecorder()
	mux.ServeHTTP(saveRec, inventoryRequest(http.MethodPut,
		fmt.Sprintf("/api/inventory-validations/%d/lines/%d/count", inv.ID, line.SKUID),
		fmt.Sprintf(`{"physical_quantity":"987.321","expected_version":%d}`, line.Version), token, csrf))
	if saveRec.Code != http.StatusOK {
		t.Fatalf("blind count status=%d body=%s", saveRec.Code, saveRec.Body.String())
	}

	reviewRec := httptest.NewRecorder()
	mux.ServeHTTP(reviewRec, inventoryRequest(http.MethodGet,
		fmt.Sprintf("/api/inventory-validations/%d/review", inv.ID), "", token, ""))
	if reviewRec.Code != http.StatusOK {
		t.Fatalf("review status=%d body=%s", reviewRec.Code, reviewRec.Body.String())
	}
	after := reviewRec.Body.String()
	for _, required := range []string{"987.321", "reference_quantity", "difference", "MATCH"} {
		if !strings.Contains(after, required) {
			t.Fatalf("authorized review missing %q: %s", required, after)
		}
	}
}

func TestInventoryReviewUsesOriginalSnapshotAfterLegitimateMovement(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "review-movement", true)
	skuA := e.sku(t, "review-movement-a", true, true)
	skuB := e.sku(t, "review-movement-b", true, true)
	e.movementDecimal(t, skuA, wh, "IN", "10.000")
	e.movementDecimal(t, skuB, wh, "IN", "10.000")
	inv, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}

	e.movementDecimal(t, skuA, wh, "IN", "5.000")
	current, err := getStockSnapshot(ctx, e.db, skuA, wh)
	if err != nil {
		t.Fatal(err)
	}
	if current.Quantity != "15.000" {
		t.Fatalf("current balance=%s want 15.000", current.Quantity)
	}

	countInventoryToReview(t, e, inv, map[int64]string{
		skuA: "10.000",
		skuB: "10.000",
	})
	review, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, review)[skuA]
	if line.SnapshotQuantity != "10.000" || line.State != "MATCH" || line.Difference != "0.000" {
		t.Fatalf("review used current balance instead of original snapshot: %+v", line)
	}
}

func TestInventoryReviewAntiMovement(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	_, inv, skus := setupTotalCounting(t, e, 2, "10.000")

	var movementsBefore, linesBefore int64
	var stockBefore string
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM movements`).Scan(&movementsBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM movement_lines`).Scan(&linesBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `
SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text
FROM movement_lines`).Scan(&stockBefore); err != nil {
		t.Fatal(err)
	}

	countInventoryToReview(t, e, inv, map[int64]string{
		skus[0]: "8.000",
		skus[1]: "10.000",
	})
	review, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, review)[skus[0]]
	if _, err := e.a.saveInventoryRecount(ctx, inv.ID, skus[0], "9.000", line.Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.getInventoryReviewView(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}

	var movementsAfter, linesAfter int64
	var stockAfter string
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM movements`).Scan(&movementsAfter)
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM movement_lines`).Scan(&linesAfter)
	_ = e.db.QueryRowContext(ctx, `
SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text
FROM movement_lines`).Scan(&stockAfter)
	if movementsBefore != movementsAfter || linesBefore != linesAfter || stockBefore != stockAfter {
		t.Fatalf("review/recount changed stock: movements %d->%d lines %d->%d stock %s->%s",
			movementsBefore, movementsAfter, linesBefore, linesAfter, stockBefore, stockAfter)
	}
}

func TestInventoryReviewACLReadAndWrite(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	_, inv, skus := setupTotalCounting(t, e, 2, "10.000")
	countInventoryToReview(t, e, inv, map[int64]string{
		skus[0]: "8.000",
		skus[1]: "10.000",
	})
	var profileID, userID int64
	if err := e.db.QueryRowContext(ctx, `SELECT id FROM profiles WHERE name='Estoquista'`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(ctx, `
INSERT INTO profile_permissions (profile_id,permission_id)
SELECT $1,id FROM permissions
WHERE resource='inventory_validation' AND action='read'
ON CONFLICT DO NOTHING`, profileID); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `
INSERT INTO users (login,password_hash,profile_id,active,must_change_password)
VALUES ($1,'test',$2,true,false) RETURNING id`,
		"inventory-review-readonly-"+t.Name(), profileID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	token, csrf := inventorySession(t, e.db, userID)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)

	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, inventoryRequest(http.MethodGet,
		fmt.Sprintf("/api/inventory-validations/%d/review", inv.ID), "", token, ""))
	if getRec.Code != http.StatusOK {
		t.Fatalf("review GET status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var review inventoryReviewView
	if err := json.Unmarshal(getRec.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, review)[skus[0]]
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, inventoryRequest(http.MethodPut,
		fmt.Sprintf("/api/inventory-validations/%d/lines/%d/recount", inv.ID, skus[0]),
		fmt.Sprintf(`{"physical_quantity":"9.000","expected_version":%d}`, line.Version), token, csrf))
	if putRec.Code != http.StatusForbidden {
		t.Fatalf("read-only recount status=%d body=%s", putRec.Code, putRec.Body.String())
	}
}

func TestInventoryReviewMigration017CleanAndIncremental(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	migrationsDir := filepath.Join("..", "..", "migrations")
	db := openTestDatabase(t)

	baseDir := t.TempDir()
	for i := 1; i <= 16; i++ {
		matches, err := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_*.sql", i)))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %03d matches=%v err=%v", i, matches, err)
		}
		body, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(baseDir, filepath.Base(matches[0])), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrations(ctx, db, baseDir); err != nil {
		t.Fatalf("001-016: %v", err)
	}

	var movementSchemaBefore string
	if err := db.QueryRowContext(ctx, `
SELECT string_agg(column_name||':'||data_type||':'||is_nullable,',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name='movement_lines'`).Scan(&movementSchemaBefore); err != nil {
		t.Fatal(err)
	}

	mig017 := filepath.Join(migrationsDir, "017_inventory_recounts.sql")
	body, err := os.ReadFile(mig017)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(body)), "movement_lines") {
		t.Fatal("migration 017 must not alter movement_lines")
	}
	incDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(incDir, filepath.Base(mig017)), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, incDir); err != nil {
		t.Fatalf("incremental 017: %v", err)
	}

	var movementSchemaAfter string
	if err := db.QueryRowContext(ctx, `
SELECT string_agg(column_name||':'||data_type||':'||is_nullable,',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name='movement_lines'`).Scan(&movementSchemaAfter); err != nil {
		t.Fatal(err)
	}
	if movementSchemaBefore != movementSchemaAfter {
		t.Fatalf("movement_lines changed by 017")
	}

	var qtyType, nullable string
	if err := db.QueryRowContext(ctx, `
SELECT data_type,is_nullable FROM information_schema.columns
WHERE table_schema=current_schema()
  AND table_name='inventory_validation_recounts'
  AND column_name='quantity'`).Scan(&qtyType, &nullable); err != nil {
		t.Fatal(err)
	}
	if qtyType != "numeric" || nullable != "NO" {
		t.Fatalf("recount quantity schema type=%s nullable=%s", qtyType, nullable)
	}

	cleanDB := openTestDatabase(t)
	if err := runMigrations(ctx, cleanDB, migrationsDir); err != nil {
		t.Fatalf("clean 001-017: %v", err)
	}
	var tableExists bool
	if err := cleanDB.QueryRowContext(ctx, `
SELECT to_regclass(current_schema()||'.inventory_validation_recounts') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if !tableExists {
		t.Fatal("clean migration missing recount table")
	}
}

func TestInventoryScannerStillOnlyLocatesDuringReview(t *testing.T) {
	js, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	start := strings.Index(src, "initScanner('scanInventory'")
	if start < 0 {
		t.Fatal("inventory scanner does not reuse initScanner")
	}
	end := strings.Index(src[start:], "let initial=")
	if end < 0 {
		t.Fatal("scanner callback boundary not found")
	}
	callback := src[start : start+end]
	for _, forbidden := range []string{"/recount", "/count", "physical_quantity", "system_quantity_snapshot", "difference"} {
		if strings.Contains(callback, forbidden) {
			t.Fatalf("scanner callback contains forbidden mutation/reference %q: %s", forbidden, callback)
		}
	}
	if !strings.Contains(callback, "focusSKU") {
		t.Fatal("scanner callback no longer only locates/focuses SKU")
	}
}

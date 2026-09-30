package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTotalCounting(t *testing.T, e inventoryTestEnv, n int, qty string) (int64, inventorySelection, []int64) {
	t.Helper()
	wh := e.warehouse(t, "counting-total", true)
	skus := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		sku := e.sku(t, fmt.Sprintf("count-total-%d", i), true, true)
		e.movementDecimal(t, sku, wh, "IN", qty)
		skus = append(skus, sku)
	}
	res, err := e.a.createTotalInventory(context.Background(), e.userID, wh)
	if err != nil {
		t.Fatalf("create total inventory: %v", err)
	}
	return wh, res, skus
}

func setupBlindCounting(t *testing.T, e inventoryTestEnv, n int, qty string) (int64, inventorySelection, []int64) {
	t.Helper()
	wh := e.warehouse(t, "counting-blind", true)
	skus := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		sku := e.sku(t, fmt.Sprintf("count-blind-%d", i), true, true)
		e.movementDecimal(t, sku, wh, "IN", qty)
		skus = append(skus, sku)
	}
	res, err := e.a.createBlindInventory(context.Background(), e.userID)
	if err != nil {
		t.Fatalf("create blind inventory: %v", err)
	}
	if res.WarehouseID != wh {
		t.Fatalf("blind warehouse=%d want=%d", res.WarehouseID, wh)
	}
	return wh, res, skus
}

func inventorySession(t *testing.T, db *sql.DB, userID int64) (string, string) {
	t.Helper()
	token := "inventory-session-" + strings.ReplaceAll(t.Name(), "/", "-") + fmt.Sprint(userID)
	csrf := "inventory-csrf-" + strings.ReplaceAll(t.Name(), "/", "-") + fmt.Sprint(userID)
	if _, err := db.Exec(
		`INSERT INTO sessions (user_id,token_hash,csrf_token_hash,expires_at)
		 VALUES ($1,$2,$3,now()+interval '1 hour')`,
		userID, hashToken(token), hashToken(csrf)); err != nil {
		t.Fatalf("create inventory session: %v", err)
	}
	return token, csrf
}

func inventoryRequest(method, target, body, token, csrf string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return req
}

func TestPhysicalQuantityValidation(t *testing.T) {
	valid := []string{"0", "0.000", "1", "1.2", "1.234", "99999999999.999"}
	for _, v := range valid {
		if !validPhysicalQuantity(v) {
			t.Fatalf("expected valid quantity %q", v)
		}
	}
	invalid := []string{"", "-1", "-0.001", "abc", "1.2345", "100000000000.000", ".5", "01.000", "1e3"}
	for _, v := range invalid {
		if validPhysicalQuantity(v) {
			t.Fatalf("expected invalid quantity %q", v)
		}
	}
}

func TestCountRequestRejectsUnknownAndTrailingInput(t *testing.T) {
	for _, body := range []string{
		`{"physical_quantity":"1","expected_version":0,"counted_by_user_id":999}`,
		`{"physical_quantity":"1","expected_version":0,"counted_at":"2026-09-30T10:00:00Z"}`,
		`{"physical_quantity":"1","expected_version":0} trailing`,
		`{"physical_quantity":"1","expected_version":0} {}`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
		rec := httptest.NewRecorder()
		var dst inventoryCountRequest
		if decodeCountJSON(rec, req, &dst) {
			t.Fatalf("invalid request accepted: %s", body)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, rec.Code)
		}
	}
}

func TestInventoryCountStatesZeroAuditAndResume(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	_, inv, _ := setupTotalCounting(t, e, 2, "5.000")

	viewA, err := e.a.getInventoryCountingView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if viewA.Status != "OPEN" || viewA.Counted != 0 || viewA.Total != 2 || viewA.Progress != 0 {
		t.Fatalf("initial view=%+v", viewA)
	}
	first := viewA.Lines[0]
	second := viewA.Lines[1]
	if first.PhysicalQuantity != nil || second.PhysicalQuantity != nil {
		t.Fatalf("new lines must be pending: %+v", viewA.Lines)
	}

	before := time.Now().Add(-time.Second)
	line, status, err := e.a.saveInventoryCount(ctx, inv.ID, first.SKUID, "0.000", first.Version, e.userID)
	if err != nil {
		t.Fatalf("save zero count: %v", err)
	}
	if status != "COUNTING" {
		t.Fatalf("first count status=%s want COUNTING", status)
	}
	if line.PhysicalQuantity == nil || *line.PhysicalQuantity != "0.000" {
		t.Fatalf("zero was not persisted distinctly: %+v", line)
	}
	if line.Version != first.Version+1 {
		t.Fatalf("version=%d want=%d", line.Version, first.Version+1)
	}

	var qty sql.NullString
	var countedBy sql.NullInt64
	var countedAt sql.NullTime
	if err := e.db.QueryRowContext(ctx, `
SELECT physical_quantity::text,counted_by_user_id,counted_at
FROM inventory_validation_lines
WHERE validation_id=$1 AND sku_id=$2`, inv.ID, first.SKUID).Scan(&qty, &countedBy, &countedAt); err != nil {
		t.Fatal(err)
	}
	if !qty.Valid || qty.String != "0.000" {
		t.Fatalf("stored zero=%+v", qty)
	}
	if !countedBy.Valid || countedBy.Int64 != e.userID {
		t.Fatalf("counted_by=%+v want user=%d", countedBy, e.userID)
	}
	if !countedAt.Valid || countedAt.Time.Before(before) {
		t.Fatalf("counted_at not generated by backend: %+v", countedAt)
	}

	resumed, err := e.a.getInventoryCountingView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != "COUNTING" || resumed.Counted != 1 || resumed.Total != 2 || resumed.Progress != 50 {
		t.Fatalf("resume view=%+v", resumed)
	}
	var zeroFound, pendingFound bool
	for _, l := range resumed.Lines {
		if l.SKUID == first.SKUID {
			zeroFound = l.PhysicalQuantity != nil && *l.PhysicalQuantity == "0.000"
		}
		if l.SKUID == second.SKUID {
			pendingFound = l.PhysicalQuantity == nil
		}
	}
	if !zeroFound || !pendingFound {
		t.Fatalf("resume did not preserve zero vs NULL: %+v", resumed.Lines)
	}

	var secondVersion int64
	for _, l := range resumed.Lines {
		if l.SKUID == second.SKUID {
			secondVersion = l.Version
		}
	}
	_, status, err = e.a.saveInventoryCount(ctx, inv.ID, second.SKUID, "2.125", secondVersion, e.userID)
	if err != nil {
		t.Fatalf("last count: %v", err)
	}
	if status != "REVIEW" {
		t.Fatalf("last count status=%s want REVIEW", status)
	}

	if _, _, err := e.a.saveInventoryCount(ctx, inv.ID, first.SKUID, "1.000", line.Version, e.userID); !errors.Is(err, errInventoryReadOnly) {
		t.Fatalf("REVIEW edit error=%v want read-only", err)
	}
	var finalStatus string
	if err := e.db.QueryRowContext(ctx, `SELECT status FROM inventory_validations WHERE id=$1`, inv.ID).Scan(&finalStatus); err != nil {
		t.Fatal(err)
	}
	if finalStatus != "REVIEW" {
		t.Fatalf("final status=%s", finalStatus)
	}
}

func TestInventoryReadOnlyStatesRejectCounts(t *testing.T) {
	for _, state := range []string{"CANCELLED", "COMPLETED", "REVIEW"} {
		t.Run(state, func(t *testing.T) {
			e := newInventoryTestEnv(t)
			ctx := context.Background()
			_, inv, _ := setupTotalCounting(t, e, 2, "3.000")
			if _, err := e.db.ExecContext(ctx, `UPDATE inventory_validations SET status=$1 WHERE id=$2`, state, inv.ID); err != nil {
				t.Fatal(err)
			}
			view, err := e.a.getInventoryCountingView(ctx, inv.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = e.a.saveInventoryCount(ctx, inv.ID, view.Lines[0].SKUID, "1.000", view.Lines[0].Version, e.userID)
			if !errors.Is(err, errInventoryReadOnly) {
				t.Fatalf("state=%s error=%v want read-only", state, err)
			}
		})
	}
}

func TestInventoryOptimisticConcurrencyReturns409(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, _ := setupTotalCounting(t, e, 2, "9.000")
	viewA, err := e.a.getInventoryCountingView(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	viewB, err := e.a.getInventoryCountingView(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if viewA.Lines[0].Version != viewB.Lines[0].Version {
		t.Fatalf("operators did not read same version")
	}
	skuID := viewA.Lines[0].SKUID
	version := viewA.Lines[0].Version

	token, csrf := inventorySession(t, e.db, e.userID)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)

	recA := httptest.NewRecorder()
	mux.ServeHTTP(recA, inventoryRequest(
		http.MethodPut,
		fmt.Sprintf("/api/inventory-validations/%d/lines/%d/count", inv.ID, skuID),
		fmt.Sprintf(`{"physical_quantity":"10.000","expected_version":%d}`, version),
		token, csrf))
	if recA.Code != http.StatusOK {
		t.Fatalf("operator A status=%d body=%s", recA.Code, recA.Body.String())
	}

	recB := httptest.NewRecorder()
	mux.ServeHTTP(recB, inventoryRequest(
		http.MethodPut,
		fmt.Sprintf("/api/inventory-validations/%d/lines/%d/count", inv.ID, skuID),
		fmt.Sprintf(`{"physical_quantity":"12.000","expected_version":%d}`, version),
		token, csrf))
	if recB.Code != http.StatusConflict {
		t.Fatalf("operator B status=%d body=%s want 409", recB.Code, recB.Body.String())
	}

	var stored string
	if err := e.db.QueryRow(
		`SELECT physical_quantity::text FROM inventory_validation_lines WHERE validation_id=$1 AND sku_id=$2`,
		inv.ID, skuID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "10.000" {
		t.Fatalf("stale operator overwrote count: %s", stored)
	}

	recRetry := httptest.NewRecorder()
	mux.ServeHTTP(recRetry, inventoryRequest(
		http.MethodPut,
		fmt.Sprintf("/api/inventory-validations/%d/lines/%d/count", inv.ID, skuID),
		fmt.Sprintf(`{"physical_quantity":"10.000","expected_version":%d}`, version),
		token, csrf))
	if recRetry.Code != http.StatusOK {
		t.Fatalf("idempotent replay status=%d body=%s", recRetry.Code, recRetry.Body.String())
	}
}

func TestInventoryACLReadWithoutWrite(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, _ := setupTotalCounting(t, e, 2, "4.000")
	ctx := context.Background()

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
		"inventory-readonly-"+t.Name(), profileID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	token, csrf := inventorySession(t, e.db, userID)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)

	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, inventoryRequest(
		http.MethodGet,
		fmt.Sprintf("/api/inventory-validations/%d/counting", inv.ID),
		"", token, ""))
	if getRec.Code != http.StatusOK {
		t.Fatalf("read-only GET status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	var view inventoryCountingView
	if err := json.Unmarshal(getRec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, inventoryRequest(
		http.MethodPut,
		fmt.Sprintf("/api/inventory-validations/%d/lines/%d/count", inv.ID, view.Lines[0].SKUID),
		`{"physical_quantity":"1.000","expected_version":0}`, token, csrf))
	if putRec.Code != http.StatusForbidden {
		t.Fatalf("read-only PUT status=%d body=%s want 403", putRec.Code, putRec.Body.String())
	}
}

func TestBlindCountingHTTPDoesNotLeakSystemQuantity(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, _ := setupBlindCounting(t, e, 2, "987.321")
	token, _ := inventorySession(t, e.db, e.userID)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, inventoryRequest(
		http.MethodGet,
		fmt.Sprintf("/api/inventory-validations/%d/counting", inv.ID),
		"", token, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("counting GET status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"system_quantity_snapshot", "difference", "987.321", "MATCH", "DIVERGENT"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("BLIND HTTP payload leaked %q: %s", forbidden, body)
		}
	}

	htmlRec := httptest.NewRecorder()
	mux.ServeHTTP(htmlRec, inventoryRequest(http.MethodGet, "/inventory", "", token, ""))
	if htmlRec.Code != http.StatusOK {
		t.Fatalf("inventory HTML status=%d body=%s", htmlRec.Code, htmlRec.Body.String())
	}
	html := htmlRec.Body.String()
	for _, forbidden := range []string{"system_quantity_snapshot", "987.321", "MATCH", "DIVERGENT"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("BLIND HTML leaked %q", forbidden)
		}
	}

	for _, path := range []string{"static/app.js", "templates/inventory.html"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		s := string(b)
		if strings.Contains(s, "system_quantity_snapshot") || strings.Contains(s, "987.321") {
			t.Fatalf("%s contains forbidden snapshot data", path)
		}
	}
}

func TestInventoryScannerLookupOnlyLocatesSelectedSKU(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	_, inv, _ := setupBlindCounting(t, e, 2, "6.000")
	if len(inv.SKUIDs) != 1 {
		t.Fatalf("blind sample=%v want 1", inv.SKUIDs)
	}
	selected := inv.SKUIDs[0]

	var selectedBarcode string
	if err := e.db.QueryRowContext(ctx, `SELECT barcode FROM product_skus WHERE id=$1`, selected).Scan(&selectedBarcode); err != nil {
		t.Fatal(err)
	}
	outside := e.sku(t, "scanner-outside", true, true)
	var outsideBarcode string
	if err := e.db.QueryRowContext(ctx, `SELECT barcode FROM product_skus WHERE id=$1`, outside).Scan(&outsideBarcode); err != nil {
		t.Fatal(err)
	}

	beforeView, err := e.a.getInventoryCountingView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	selectedResult, err := e.a.lookupInventoryBarcode(ctx, inv.ID, selectedBarcode)
	if err != nil {
		t.Fatal(err)
	}
	if !selectedResult.Known || !selectedResult.InInventory || selectedResult.SKUID != selected {
		t.Fatalf("selected lookup=%+v", selectedResult)
	}
	outsideResult, err := e.a.lookupInventoryBarcode(ctx, inv.ID, outsideBarcode)
	if err != nil {
		t.Fatal(err)
	}
	if !outsideResult.Known || outsideResult.InInventory {
		t.Fatalf("outside lookup=%+v", outsideResult)
	}
	unknownResult, err := e.a.lookupInventoryBarcode(ctx, inv.ID, "UNKNOWN-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if unknownResult.Known || unknownResult.InInventory || unknownResult.SKUID != 0 {
		t.Fatalf("unknown lookup=%+v", unknownResult)
	}
	afterView, err := e.a.getInventoryCountingView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeView.Counted != afterView.Counted {
		t.Fatalf("scanner changed count progress before=%d after=%d", beforeView.Counted, afterView.Counted)
	}
	for _, line := range afterView.Lines {
		if line.PhysicalQuantity != nil {
			t.Fatalf("scanner filled quantity for sku=%d", line.SKUID)
		}
	}

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
		t.Fatal("inventory scanner callback boundary not found")
	}
	callback := src[start : start+end]
	if strings.Contains(callback, "/count") || strings.Contains(callback, "physical_quantity") {
		t.Fatalf("scanner callback performs count mutation: %s", callback)
	}
	if !strings.Contains(callback, "focusSKU") {
		t.Fatalf("scanner callback does not locate/focus selected line")
	}
}

func TestInventoryCountingAntiMovement(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "anti-count", true)
	for i := 0; i < 4; i++ {
		sku := e.sku(t, fmt.Sprintf("anti-count-%d", i), true, true)
		e.movementDecimal(t, sku, wh, "IN", fmt.Sprintf("%d.000", i+5))
	}
	total, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}
	blind, err := e.a.createBlindInventory(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}

	var movementsBefore, linesBefore int64
	var balanceBefore string
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM movements`).Scan(&movementsBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM movement_lines`).Scan(&linesBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(ctx, `
SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text
FROM movement_lines`).Scan(&balanceBefore); err != nil {
		t.Fatal(err)
	}

	totalView, err := e.a.getInventoryCountingView(ctx, total.ID)
	if err != nil {
		t.Fatal(err)
	}
	blindView, err := e.a.getInventoryCountingView(ctx, blind.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(totalView.Lines) < 2 || len(blindView.Lines) < 1 {
		t.Fatalf("unexpected lines total=%d blind=%d", len(totalView.Lines), len(blindView.Lines))
	}
	line, _, err := e.a.saveInventoryCount(ctx, total.ID, totalView.Lines[0].SKUID, "1.000", totalView.Lines[0].Version, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.a.saveInventoryCount(ctx, total.ID, line.SKUID, "2.000", line.Version, e.userID); err != nil {
		t.Fatalf("edit allowed count: %v", err)
	}
	if _, _, err := e.a.saveInventoryCount(ctx, blind.ID, blindView.Lines[0].SKUID, "3.000", blindView.Lines[0].Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if blindView.Lines[0].Barcode != "" {
		if _, err := e.a.lookupInventoryBarcode(ctx, blind.ID, blindView.Lines[0].Barcode); err != nil {
			t.Fatal(err)
		}
	}

	var movementsAfter, linesAfter int64
	var balanceAfter string
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM movements`).Scan(&movementsAfter)
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM movement_lines`).Scan(&linesAfter)
	_ = e.db.QueryRowContext(ctx, `
SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text
FROM movement_lines`).Scan(&balanceAfter)
	if movementsBefore != movementsAfter || linesBefore != linesAfter || balanceBefore != balanceAfter {
		t.Fatalf("counting changed stock: movements %d->%d lines %d->%d balance %s->%s",
			movementsBefore, movementsAfter, linesBefore, linesAfter, balanceBefore, balanceAfter)
	}
}

func TestInventoryCountMigration016CleanAndIncremental(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	migrationsDir := filepath.Join("..", "..", "migrations")

	db := openTestDatabase(t)
	baseDir := t.TempDir()
	for i := 1; i <= 15; i++ {
		pattern := filepath.Join(migrationsDir, fmt.Sprintf("%03d_*.sql", i))
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 1 {
			t.Fatalf("resolve migration %03d: matches=%v err=%v", i, matches, err)
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
		t.Fatalf("apply 001-015: %v", err)
	}

	var movementSchemaBefore string
	if err := db.QueryRowContext(ctx, `
SELECT string_agg(column_name||':'||data_type||':'||is_nullable,',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name='movement_lines'`).Scan(&movementSchemaBefore); err != nil {
		t.Fatal(err)
	}

	incrementalDir := t.TempDir()
	mig016 := filepath.Join(migrationsDir, "016_inventory_count_version.sql")
	body, err := os.ReadFile(mig016)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(body)), "movement_lines") {
		t.Fatal("migration 016 must not alter movement_lines")
	}
	if err := os.WriteFile(filepath.Join(incrementalDir, filepath.Base(mig016)), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, incrementalDir); err != nil {
		t.Fatalf("apply 016 incrementally: %v", err)
	}

	var movementSchemaAfter string
	if err := db.QueryRowContext(ctx, `
SELECT string_agg(column_name||':'||data_type||':'||is_nullable,',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name='movement_lines'`).Scan(&movementSchemaAfter); err != nil {
		t.Fatal(err)
	}
	if movementSchemaBefore != movementSchemaAfter {
		t.Fatalf("movement_lines schema changed by 016: before=%s after=%s", movementSchemaBefore, movementSchemaAfter)
	}

	var dataType, nullable, defaultValue string
	if err := db.QueryRowContext(ctx, `
SELECT data_type,is_nullable,column_default
FROM information_schema.columns
WHERE table_schema=current_schema()
  AND table_name='inventory_validation_lines'
  AND column_name='count_version'`).Scan(&dataType, &nullable, &defaultValue); err != nil {
		t.Fatal(err)
	}
	if dataType != "bigint" || nullable != "NO" || !strings.Contains(defaultValue, "0") {
		t.Fatalf("count_version schema type=%s nullable=%s default=%s", dataType, nullable, defaultValue)
	}

	cleanDB := openTestDatabase(t)
	if err := runMigrations(ctx, cleanDB, migrationsDir); err != nil {
		t.Fatalf("clean migrations 001-016: %v", err)
	}
	var cleanVersion int64
	if err := cleanDB.QueryRowContext(ctx, `
SELECT count_version
FROM inventory_validation_lines
LIMIT 1`).Scan(&cleanVersion); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("clean schema count_version query: %v", err)
	}
}

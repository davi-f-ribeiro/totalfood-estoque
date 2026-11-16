package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func crateTestRequest(t *testing.T, fn http.HandlerFunc, method, path string, body any, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload)).WithContext(ctx)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	fn(rr, req)
	return rr
}
func crateTestUser(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`INSERT INTO users(login,password_hash,profile_id,active,must_change_password)
 VALUES($1,'test-hash',(SELECT id FROM profiles WHERE name='Diretor'),true,false) RETURNING id`,
		fmt.Sprintf("test-crate-%d", time.Now().UnixNano())).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func crateTestSKU(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var item, sku int64
	err := db.QueryRow(`INSERT INTO inventory_items(name,category_id,unit_code)
 VALUES($1,(SELECT id FROM categories WHERE name='Alimentos'),'KG') RETURNING id`,
		fmt.Sprintf("Tomate lote test %d", time.Now().UnixNano())).Scan(&item)
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow(`INSERT INTO product_skus(item_id,brand_id,content_quantity,active)
 VALUES($1,0,1,true) RETURNING id`, item).Scan(&sku)
	if err != nil {
		t.Fatal(err)
	}
	return sku
}
func TestCratesReceivingWeighingAndPrintQueue(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db}
	user := crateTestUser(t, db)
	sku := crateTestSKU(t, db)
	var wh int64
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE name='VR'`).Scan(&wh); err != nil {
		t.Fatal(err)
	}
	userCtx := context.WithValue(context.Background(), authCtxKey, authUser{ID: user})
	receive := crateReceiveRequest{OperationID: "crate-receipt-test-1", SKUID: sku, WarehouseID: wh, UnitCost: "3.3",
		DocumentNumber: "123456", SupplierName: "CEASA permissionario", Weights: []string{"20.000", "18.500"}}
	callReceive := func() map[string]any {
		t.Helper()
		rr := crateTestRequest(t, a.handleCrateReceive, "POST", "/api/crates/receive", receive, userCtx)
		if rr.Code != 200 {
			t.Fatalf("receive status=%d body=%s", rr.Code, rr.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := callReceive()
	second := callReceive()
	if first["movement_id"] != second["movement_id"] {
		t.Fatal("idempotency returned different movements")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM stock_crates`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("crates=%d error=%v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM print_jobs`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("jobs=%d error=%v", count, err)
	}
	entries := first["crates"].([]any)
	crateID := int64(entries[0].(map[string]any)["id"].(float64))
	// Generic exit must not silently allocate a tracked lot.
	_, err := a.createExit(context.Background(), movementRequest{OperationID: "generic-crate-exit", SKUID: sku, WarehouseID: wh,
		Quantity: "1.000", UserID: user}, "generic-crate-exit-hash")
	if !errors.Is(err, errCrateTrackedStock) {
		t.Fatalf("generic exit should be rejected: %v", err)
	}
	weighing := crateWeighRequest{OperationID: "crate-weigh-test-1", Weight: "12.000", Notes: "retorno da cozinha"}
	url := fmt.Sprintf("/api/crates/%d/weigh", crateID)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", url, bytes.NewReader(mustJSON(t, weighing))).WithContext(userCtx)
		req.SetPathValue("id", fmt.Sprint(crateID))
		rr := httptest.NewRecorder()
		a.handleCrateWeigh(rr, req)
		if rr.Code != 200 {
			t.Fatalf("weigh %d: %d %s", i, rr.Code, rr.Body.String())
		}
	}
	var rem string
	err = db.QueryRow(`SELECT current_weight::text FROM stock_crates WHERE id=$1`, crateID).Scan(&rem)
	if err != nil || !equalCrateWeights(rem, "12") {
		t.Fatalf("crate remaining=%s error=%v", rem, err)
	}
	err = db.QueryRow(`SELECT count(*) FROM stock_crate_events WHERE crate_id=$1`, crateID).Scan(&count)
	if err != nil || count != 2 {
		t.Fatalf("events=%d error=%v", count, err)
	}
	err = db.QueryRow(`SELECT count(*) FROM print_jobs WHERE crate_id=$1 AND status='PENDING'`, crateID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("pending jobs=%d error=%v", count, err)
	}
	err = db.QueryRow(`SELECT count(*) FROM print_jobs WHERE crate_id=$1 AND status='CANCELLED'`, crateID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("cancelled jobs=%d error=%v", count, err)
	}
	err = db.QueryRow(`SELECT COALESCE(sum(a.quantity),0)::text FROM stock_lot_allocations a
 JOIN stock_lots l ON l.id=a.stock_lot_id JOIN stock_crates c ON c.stock_lot_id=l.id WHERE c.id=$1`, crateID).Scan(&rem)
	if err != nil || !equalCrateWeights(rem, "8") {
		t.Fatalf("allocations=%s error=%v", rem, err)
	}
	// A box may move between warehouses and keep its receipt lot and ID.
	var destWh int64
	if err := db.QueryRow("SELECT id FROM warehouses WHERE name='Totalfood Câmara Fria'").Scan(&destWh); err != nil {
		t.Fatal(err)
	}
	transfer := crateTransferRequest{OperationID: "crate-transfer-test-1", ToWarehouseID: destWh}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/crates/%d/transfer", crateID), bytes.NewReader(mustJSON(t, transfer))).WithContext(userCtx)
		req.SetPathValue("id", fmt.Sprint(crateID))
		rr := httptest.NewRecorder()
		a.handleCrateTransfer(rr, req)
		if rr.Code != 200 {
			t.Fatalf("transfer %d: %d %s", i, rr.Code, rr.Body.String())
		}
	}
	var originID, currentLot int64
	if err := db.QueryRow("SELECT origin_lot_id,stock_lot_id FROM stock_crates WHERE id=$1 AND warehouse_id=$2", crateID, destWh).Scan(&originID, &currentLot); err != nil {
		t.Fatal(err)
	}
	if originID == currentLot {
		t.Fatal("transfer must retain origin and create destination lot")
	}
	// Subsequent consumption is allocated at the destination lot, not the old location.
	secondWeigh := crateWeighRequest{OperationID: "crate-weigh-test-2", Weight: "11.000"}
	wreq := httptest.NewRequest("POST", url, bytes.NewReader(mustJSON(t, secondWeigh))).WithContext(userCtx)
	wreq.SetPathValue("id", fmt.Sprint(crateID))
	wrec := httptest.NewRecorder()
	a.handleCrateWeigh(wrec, wreq)
	if wrec.Code != 200 {
		t.Fatalf("destination weigh: %d %s", wrec.Code, wrec.Body.String())
	}
	var destinationConsumed string
	if err := db.QueryRow("SELECT COALESCE(sum(quantity),0)::text FROM stock_lot_allocations WHERE stock_lot_id=$1", currentLot).Scan(&destinationConsumed); err != nil || !equalCrateWeights(destinationConsumed, "1.000") {
		t.Fatalf("destination consumed %s error %v", destinationConsumed, err)
	}
	// Pair the ESP and confirm a job. Neither the token nor ZPL are put in logs.
	pair := crateTestRequest(t, a.handlePrintPair, "POST", "/api/print/devices", map[string]string{"name": "zebra-test"}, userCtx)
	if pair.Code != 201 {
		t.Fatalf("pair status=%d body=%s", pair.Code, pair.Body.String())
	}
	var pairing struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err = json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	printerCtx := context.WithValue(context.Background(), printerCtxKey{}, pairing.ID)
	claim := crateTestRequest(t, a.handlePrintClaim, "POST", "/api/print/jobs/claim", map[string]any{}, printerCtx)
	if claim.Code != 200 {
		t.Fatalf("claim status=%d body=%s", claim.Code, claim.Body.String())
	}
	var job printJob
	if err = json.Unmarshal(claim.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.ClaimToken == "" || job.ZPL == "" {
		t.Fatal("missing claim token or ZPL")
	}
	ackURL := fmt.Sprintf("/api/print/jobs/%d/ack", job.ID)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", ackURL, bytes.NewReader(mustJSON(t, printAckInput{ClaimToken: job.ClaimToken}))).WithContext(printerCtx)
		req.SetPathValue("id", fmt.Sprint(job.ID))
		rr := httptest.NewRecorder()
		a.handlePrintAck(rr, req)
		if rr.Code != 200 {
			t.Fatalf("ack=%d %s", rr.Code, rr.Body.String())
		}
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM print_jobs WHERE id=$1`, job.ID).Scan(&status); err != nil || status != "SENT" {
		t.Fatalf("job state=%s err=%v", status, err)
	}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Zerar o peso esvazia a caixa: nenhuma etiqueta nova entra na fila e a que ainda
// estava pendente, com o peso antigo, é cancelada.
func TestCrateWeighedToZeroQueuesNoLabel(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db}
	user := crateTestUser(t, db)
	sku := crateTestSKU(t, db)
	var wh int64
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE name='VR'`).Scan(&wh); err != nil {
		t.Fatal(err)
	}
	userCtx := context.WithValue(context.Background(), authCtxKey, authUser{ID: user})
	receive := crateReceiveRequest{OperationID: "crate-zero-receipt", SKUID: sku, WarehouseID: wh, UnitCost: "2",
		DocumentNumber: "777", SupplierName: "CEASA", Weights: []string{"5.000"}}
	rr := crateTestRequest(t, a.handleCrateReceive, "POST", "/api/crates/receive", receive, userCtx)
	if rr.Code != 200 {
		t.Fatalf("receive status=%d body=%s", rr.Code, rr.Body.String())
	}
	var crateID int64
	if err := db.QueryRow(`SELECT id FROM stock_crates WHERE sku_id=$1`, sku).Scan(&crateID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/crates/%d/weigh", crateID),
		bytes.NewReader(mustJSON(t, crateWeighRequest{OperationID: "crate-zero-weigh", Weight: "0"}))).WithContext(userCtx)
	req.SetPathValue("id", fmt.Sprint(crateID))
	wrec := httptest.NewRecorder()
	a.handleCrateWeigh(wrec, req)
	if wrec.Code != 200 {
		t.Fatalf("weigh to zero: %d %s", wrec.Code, wrec.Body.String())
	}
	var out crateRow
	if err := json.Unmarshal(wrec.Body.Bytes(), &out); err != nil || out.Status != "EMPTY" {
		t.Fatalf("crate status=%q error=%v", out.Status, err)
	}
	var total, pending, cancelled int
	var reason sql.NullString
	if err := db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE status='PENDING'), count(*) FILTER (WHERE status='CANCELLED'),
  max(last_error) FILTER (WHERE status='CANCELLED') FROM print_jobs WHERE crate_id=$1`, crateID).Scan(&total, &pending, &cancelled, &reason); err != nil {
		t.Fatal(err)
	}
	if total != 1 || pending != 0 || cancelled != 1 || reason.String != "Caixa vazia" {
		t.Fatalf("print jobs after emptying: total=%d pending=%d cancelled=%d reason=%q, want 1/0/1/Caixa vazia", total, pending, cancelled, reason.String)
	}
}

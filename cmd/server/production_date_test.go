package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProductionDateValidation(t *testing.T) {
	for _, tc := range []struct {
		day   string
		valid bool
	}{
		{"", true}, {"2026-11-14", true}, {"2026-11-15", true},
		{"2026-02-29", false}, {"2024-02-29", true}, {"2026-11-31", false},
		{"14/11/2026", false}, {"2026-1-4", false}, {"0000-01-01", false}, {"2026-11-14T10:00:00Z", false},
	} {
		if got := validProductionDate(tc.day); got != tc.valid {
			t.Errorf("validProductionDate(%q)=%v, want %v", tc.day, got, tc.valid)
		}
	}
}

func TestExitProductionDateSeparateFromActualMovementAndIdempotent(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	_, err := a.createEntry(ctx, movementRequest{OperationID: "prod-entry", SKUID: sku, WarehouseID: wh, Quantity: "8", UnitCost: "10"}, "prod-entry-hash")
	if err != nil {
		t.Fatal(err)
	}

	productionDay := time.Now().In(time.FixedZone("fortaleza", -3*3600)).AddDate(0, 0, 2).Format("2006-01-02")
	req := movementRequest{OperationID: "prod-exit", SKUID: sku, WarehouseID: wh, Quantity: "3", ProductionDate: productionDay}
	first, err := a.createExit(ctx, req, "prod-exit-hash")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := a.createExit(ctx, req, "prod-exit-hash")
	if err != nil || first.MovementID != replay.MovementID {
		t.Fatalf("replay = %+v %v", replay, err)
	}
	assertStock(t, a, sku, wh, "5.000", "10.0000")

	var occurred time.Time
	var production string
	if err := a.db.QueryRow("SELECT occurred_at,production_date::text FROM movements WHERE id=$1", first.MovementID).Scan(&occurred, &production); err != nil {
		t.Fatal(err)
	}
	if production != productionDay {
		t.Fatalf("production=%s want %s", production, productionDay)
	}
	if delta := time.Since(occurred); delta < 0 || delta > time.Minute {
		t.Fatalf("actual occurred_at drift: %v", delta)
	}

	report, err := a.listMovementReport(ctx, "SAIDA", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), wh)
	if err != nil || len(report) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if report[0].ProductionDate != productionDay || report[0].MovementID != first.MovementID {
		t.Fatalf("report wrong dates: %+v", report[0])
	}

	// O período da produção não modifica o momento do abatimento do estoque.
	var plannedDayQty string
	err = a.db.QueryRow(`SELECT COALESCE(sum(ml.quantity),0)::text FROM movement_lines ml
    JOIN movements m ON m.id=ml.movement_id
    WHERE m.movement_type='SAIDA' AND ml.direction='OUT'
    AND COALESCE(m.production_date,(m.occurred_at AT TIME ZONE 'America/Fortaleza')::date)=$1::date`, productionDay).Scan(&plannedDayQty)
	if err != nil || plannedDayQty != "3.000" {
		t.Fatalf("production quantity=%s %v", plannedDayQty, err)
	}

	other := req
	other.ProductionDate = "2026-01-01"
	if _, err := a.createExit(ctx, other, "new-hash"); !errors.Is(err, errOperationConflict) {
		t.Fatalf("different production date with same operation id must conflict: %v", err)
	}
	assertStock(t, a, sku, wh, "5.000", "10.0000")
}

func TestBatchExitKeepsSharedProductionDateAndRejectsOtherMovements(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "prod-batch-entry", SKUID: sku, WarehouseID: wh, Quantity: "10", UnitCost: "2"}, "hash"); err != nil {
		t.Fatal(err)
	}
	second := newSecondSKU(t, a, sku)
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "prod-batch-entry-2", SKUID: second, WarehouseID: wh, Quantity: "10", UnitCost: "3"}, "hash"); err != nil {
		t.Fatal(err)
	}

	req := batchRequest{OperationID: "prod-batch-exit", WarehouseID: wh, ProductionDate: "2027-01-12", Lines: []batchLineRequest{{SKUID: sku, Quantity: "2"}, {SKUID: second, Quantity: "3"}}}
	result, err := a.createBatch(ctx, "exit", req, 0)
	if err != nil || result.Count != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if again, err := a.createBatch(ctx, "exit", req, 0); err != nil || again.MovementIDs[0] != result.MovementIDs[0] {
		t.Fatalf("idempotency=%+v %v", again, err)
	}
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM movements WHERE production_date='2027-01-12'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	assertStock(t, a, sku, wh, "8.000", "2.0000")

	req.OperationID = "prod-batch-invalid"
	req.ProductionDate = "2026-02-30"
	if _, err := a.createBatch(ctx, "exit", req, 0); !errors.Is(err, errMovementProductionDateInvalid) {
		t.Fatalf("invalid date: %v", err)
	}
	req.ProductionDate = "2027-01-12"
	if _, err := a.createBatch(ctx, "entry", req, 0); !errors.Is(err, errMovementProductionDateInvalid) {
		t.Fatalf("entry must reject production date: %v", err)
	}
}

func TestProductionDatePayloadContract(t *testing.T) {
	body := `{"operation_id":"prod-invalid","sku_id":1,"warehouse_id":1,"quantity":"1","production_date":"2026-02-30"}`
	// Teste HTTP de validação sem precisar executar movimentação.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/movements/exit", strings.NewReader(body))
	_, _, ok := decodeMovementRequest(rr, req, false)
	if ok || rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "produção") {
		t.Fatalf("invalid date response status=%d body=%s", rr.Code, rr.Body.String())
	}
}

package main

import (
	"context"
	"errors"
	"testing"
)

func newSecondSKU(t *testing.T, a *app, first int64) int64 {
	t.Helper()
	var cat, item, sku int64
	if err := a.db.QueryRow(`SELECT category_id FROM inventory_items i JOIN product_skus s ON s.item_id=i.id WHERE s.id=$1`, first).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`INSERT INTO inventory_items (name, category_id, unit_code) VALUES ('Feijão', $1, 'KG') RETURNING id`, cat).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`INSERT INTO product_skus (item_id, brand, barcode) VALUES ($1, 'Outra', $2) RETURNING id`, item, t.Name()+"-2").Scan(&sku); err != nil {
		t.Fatal(err)
	}
	return sku
}

func TestBatchEntryExitTransferAreAtomicAndRepeatable(t *testing.T) {
	a, sku1, wh1, wh2 := newStockTestApp(t)
	sku2 := newSecondSKU(t, a, sku1)
	ctx := context.Background()

	in := batchRequest{OperationID: "lote-1", WarehouseID: wh1, Lines: []batchLineRequest{
		{SKUID: sku2, Quantity: "4", UnitCost: "3"},
		{SKUID: sku1, Quantity: "10", UnitCost: "2"},
	}}
	r1, err := a.createBatch(ctx, "entry", in, 0)
	if err != nil || r1.Count != 2 || r1.Status != "confirmed" {
		t.Fatalf("entry batch: %+v %v", r1, err)
	}
	assertStock(t, a, sku1, wh1, "10.000", "2.0000")
	assertStock(t, a, sku2, wh1, "4.000", "3.0000")

	// Mesmo envio de novo: nada é gravado duas vezes.
	r2, err := a.createBatch(ctx, "entry", in, 0)
	if err != nil || r2.MovementIDs[0] != r1.MovementIDs[0] || r2.MovementIDs[1] != r1.MovementIDs[1] {
		t.Fatalf("replay: %+v %v", r2, err)
	}
	assertStock(t, a, sku1, wh1, "10.000", "2.0000")

	// Mesmo identificador com dados diferentes é recusado.
	other := in
	other.Lines = []batchLineRequest{{SKUID: sku2, Quantity: "5", UnitCost: "3"}, {SKUID: sku1, Quantity: "10", UnitCost: "2"}}
	if _, err := a.createBatch(ctx, "entry", other, 0); !errors.Is(err, errOperationConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	// Reenvio com menos itens também.
	fewer := in
	fewer.Lines = in.Lines[:1]
	if _, err := a.createBatch(ctx, "entry", fewer, 0); !errors.Is(err, errOperationConflict) {
		t.Fatalf("want conflict for shorter replay, got %v", err)
	}
	if got, err := a.getOperation(ctx, "lote-1"); err != nil || got.Type != "ENTRADA" {
		t.Fatalf("lookup batch: %+v %v", got, err)
	}

	// Saída: o segundo item falha e o primeiro não pode ficar gravado.
	out := batchRequest{OperationID: "lote-2", WarehouseID: wh1, Lines: []batchLineRequest{
		{SKUID: sku1, Quantity: "3"},
		{SKUID: sku2, Quantity: "99"},
	}}
	_, err = a.createBatch(ctx, "exit", out, 0)
	var le batchLineError
	if !errors.As(err, &le) || le.Index != 1 || !errors.Is(err, errInsufficientStock) {
		t.Fatalf("want insufficient on line 2, got %v", err)
	}
	assertStock(t, a, sku1, wh1, "10.000", "2.0000")

	out.Lines[1].Quantity = "1"
	if _, err := a.createBatch(ctx, "exit", out, 0); err != nil {
		t.Fatal(err)
	}
	assertStock(t, a, sku1, wh1, "7.000", "2.0000")
	assertStock(t, a, sku2, wh1, "3.000", "3.0000")

	tr := batchRequest{OperationID: "lote-3", FromWarehouseID: wh1, ToWarehouseID: wh2, Lines: []batchLineRequest{
		{SKUID: sku1, Quantity: "2"}, {SKUID: sku2, Quantity: "3"},
	}}
	if _, err := a.createBatch(ctx, "transfer", tr, 0); err != nil {
		t.Fatal(err)
	}
	assertStock(t, a, sku1, wh2, "2.000", "2.0000")
	assertStock(t, a, sku2, wh1, "0.000", "0")
}

func TestBatchRejectsDuplicateAndInvalidLines(t *testing.T) {
	a, sku1, wh1, _ := newStockTestApp(t)
	ctx := context.Background()
	dup := batchRequest{OperationID: "dup", WarehouseID: wh1, Lines: []batchLineRequest{{SKUID: sku1, Quantity: "1", UnitCost: "1"}, {SKUID: sku1, Quantity: "1", UnitCost: "1"}}}
	if _, err := a.createBatch(ctx, "entry", dup, 0); !errors.Is(err, errDuplicateBatchSKU) {
		t.Fatalf("want duplicate, got %v", err)
	}
	bad := batchRequest{OperationID: "bad", WarehouseID: wh1, Lines: []batchLineRequest{{SKUID: sku1, Quantity: "0", UnitCost: "1"}}}
	if _, err := a.createBatch(ctx, "entry", bad, 0); !errors.Is(err, errMovementQuantityInvalid) {
		t.Fatalf("want quantity error, got %v", err)
	}
	if _, err := a.createBatch(ctx, "entry", batchRequest{OperationID: "nowh", Lines: dup.Lines[:1]}, 0); !errors.Is(err, errMovementWarehouseRequired) {
		t.Fatalf("want warehouse error, got %v", err)
	}
}

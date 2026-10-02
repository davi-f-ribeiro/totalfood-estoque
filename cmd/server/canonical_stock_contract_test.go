package main

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// Gate 01 regression contracts for canonical stock identity.
// These tests intentionally describe the corrected domain: sibling SKUs are
// presentations of the same inventory item and must consume one shared balance.
func TestCanonicalStockSiblingSKUCanConsumeParentBalance(t *testing.T) {
	a, skuIn, wh, _ := newStockTestApp(t)
	ctx := context.Background()

	var itemID int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, skuIn).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	var skuOut int64
	if err := a.db.QueryRow(`
INSERT INTO product_skus (item_id,brand,name,barcode)
VALUES ($1,'Marca','Pacote 1kg',$2)
RETURNING id`, itemID, t.Name()+"-out").Scan(&skuOut); err != nil {
		t.Fatal(err)
	}

	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "canonical-sibling-entry",
		SKUID: skuIn,
		WarehouseID: wh,
		Quantity: "10",
		UnitCost: "5",
	}, "canonical-sibling-entry-hash"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.createExit(ctx, movementRequest{
		OperationID: "canonical-sibling-exit",
		SKUID: skuOut,
		WarehouseID: wh,
		Quantity: "1",
	}, "canonical-sibling-exit-hash"); err != nil {
		if errors.Is(err, errInsufficientStock) {
			t.Fatalf("sibling SKU must consume the shared item balance; got insufficient stock: %v", err)
		}
		t.Fatal(err)
	}

	s, err := getStockSnapshot(ctx, a.db, skuOut, wh)
	if err != nil {
		t.Fatal(err)
	}
	if s.Quantity != "9.000" || s.AvgCost != "5.0000" {
		t.Fatalf("shared parent stock got qty=%s avg=%s want qty=9.000 avg=5.0000", s.Quantity, s.AvgCost)
	}
}

func TestCanonicalStockSiblingSKUsShareWeightedAverageCost(t *testing.T) {
	a, skuA, wh, _ := newStockTestApp(t)
	ctx := context.Background()

	var itemID int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, skuA).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	var skuB int64
	if err := a.db.QueryRow(`
INSERT INTO product_skus (item_id,brand,name,barcode)
VALUES ($1,'Outra Marca','Outra apresentação',$2)
RETURNING id`, itemID, t.Name()+"-b").Scan(&skuB); err != nil {
		t.Fatal(err)
	}

	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "canonical-cost-a",
		SKUID: skuA,
		WarehouseID: wh,
		Quantity: "10",
		UnitCost: "10",
	}, "canonical-cost-a-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "canonical-cost-b",
		SKUID: skuB,
		WarehouseID: wh,
		Quantity: "10",
		UnitCost: "14",
	}, "canonical-cost-b-hash"); err != nil {
		t.Fatal(err)
	}

	sA, err := getStockSnapshot(ctx, a.db, skuA, wh)
	if err != nil {
		t.Fatal(err)
	}
	sB, err := getStockSnapshot(ctx, a.db, skuB, wh)
	if err != nil {
		t.Fatal(err)
	}
	for label, s := range map[string]stockSnapshot{"skuA": sA, "skuB": sB} {
		if s.Quantity != "20.000" || s.AvgCost != "12.0000" {
			t.Fatalf("%s canonical stock got qty=%s avg=%s want qty=20.000 avg=12.0000", label, s.Quantity, s.AvgCost)
		}
	}
}


func TestCanonicalStockSiblingSKUsCannotDoubleSpendSharedBalance(t *testing.T) {
	a, skuA, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	var itemID int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, skuA).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	var skuB int64
	if err := a.db.QueryRow(`
INSERT INTO product_skus (item_id,brand,name,barcode)
VALUES ($1,'Outra Marca','SKU irmão',$2)
RETURNING id`, itemID, t.Name()+"-b").Scan(&skuB); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "canonical-concurrency-seed",
		SKUID: skuA, WarehouseID: wh, Quantity: "10", UnitCost: "10",
	}, "canonical-concurrency-seed-hash"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, skuID := range []int64{skuA, skuB} {
		wg.Add(1)
		go func(i int, skuID int64) {
			defer wg.Done()
			_, errs[i] = a.createExit(ctx, movementRequest{
				OperationID: []string{"canonical-concurrency-a", "canonical-concurrency-b"}[i],
				SKUID: skuID, WarehouseID: wh, Quantity: "7",
			}, []string{"canonical-concurrency-a-hash", "canonical-concurrency-b-hash"}[i])
		}(i, skuID)
	}
	wg.Wait()

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
			continue
		}
		if !errors.Is(err, errInsufficientStock) {
			t.Fatalf("unexpected sibling concurrent exit error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one sibling exit to succeed, got %d (%v)", success, errs)
	}
	s, err := getStockSnapshot(ctx, a.db, skuA, wh)
	if err != nil {
		t.Fatal(err)
	}
	if s.Quantity != "3.000" {
		t.Fatalf("shared stock after concurrent exits=%s want 3.000", s.Quantity)
	}
}


func TestInventorySiblingBarcodeResolvesSingleCanonicalLine(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "canonical-inventory", true)

	var catID, itemID, skuA, skuB int64
	if err := e.db.QueryRow(`SELECT id FROM categories ORDER BY id LIMIT 1`).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(`
INSERT INTO inventory_items (name,category_id,unit_code,active)
VALUES ($1,$2,'KG',true) RETURNING id`, t.Name()+"-item", catID).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(`
INSERT INTO product_skus (item_id,name,barcode,active)
VALUES ($1,'Big Bag 10kg',$2,true) RETURNING id`, itemID, t.Name()+"-a").Scan(&skuA); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(`
INSERT INTO product_skus (item_id,name,barcode,active)
VALUES ($1,'Pacote 1kg',$2,true) RETURNING id`, itemID, t.Name()+"-b").Scan(&skuB); err != nil {
		t.Fatal(err)
	}
	e.movement(t, skuA, wh, "IN", 10)

	res, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SKUIDs) != 1 {
		t.Fatalf("canonical inventory created %d lines for one item: %v", len(res.SKUIDs), res.SKUIDs)
	}

	lookup, err := e.a.lookupInventoryBarcode(ctx, res.ID, t.Name()+"-b")
	if err != nil {
		t.Fatal(err)
	}
	if !lookup.Known || !lookup.InInventory {
		t.Fatalf("sibling barcode not resolved into canonical inventory line: %+v", lookup)
	}
	if lookup.SKUID != res.SKUIDs[0] {
		t.Fatalf("lookup line sku=%d want representative=%d", lookup.SKUID, res.SKUIDs[0])
	}

	var snapshot string
	if err := e.db.QueryRow(`
SELECT system_quantity_snapshot::text
FROM inventory_validation_lines
WHERE validation_id=$1`, res.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot != "10.000" {
		t.Fatalf("canonical inventory snapshot=%s want 10.000", snapshot)
	}
	_ = skuB
}

package main

import (
	"context"
	"errors"
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

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


func TestPresentationNormalizationAlhoThreeKg(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	if _, err := a.db.Exec(`UPDATE product_skus SET content_quantity=3 WHERE id=$1`, sku); err != nil {
		t.Fatal(err)
	}
	res, err := a.createEntry(ctx, movementRequest{
		OperationID: "alho-3kg-entry",
		SKUID: sku, WarehouseID: wh, Quantity: "1", UnitCost: "39.90",
	}, "alho-3kg-entry-hash")
	if err != nil {
		t.Fatal(err)
	}
	var qty, unitCost string
	if err := a.db.QueryRow(`
SELECT quantity::text,unit_cost::text FROM movement_lines
WHERE movement_id=$1`, res.MovementID).Scan(&qty, &unitCost); err != nil {
		t.Fatal(err)
	}
	if qty != "3.000" || unitCost != "13.3000" {
		t.Fatalf("alho ledger qty=%s cost=%s want 3.000 and 13.3000", qty, unitCost)
	}
	var total string
	if err := a.db.QueryRow(`SELECT (quantity*unit_cost)::numeric(14,2)::text FROM movement_lines WHERE movement_id=$1`, res.MovementID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != "39.90" {
		t.Fatalf("alho ledger total=%s want 39.90", total)
	}
}

func TestPresentationNormalizationBigBagEntryPackExit(t *testing.T) {
	a, bigBag, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	var itemID int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, bigBag).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE product_skus SET content_quantity=10 WHERE id=$1`, bigBag); err != nil {
		t.Fatal(err)
	}
	var pack int64
	if err := a.db.QueryRow(`
INSERT INTO product_skus (item_id,brand,name,barcode,content_quantity)
VALUES ($1,'Marca','Pacote 1kg',$2,1)
RETURNING id`, itemID, t.Name()+"-pack").Scan(&pack); err != nil {
		t.Fatal(err)
	}

	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "bigbag-entry",
		SKUID: bigBag, WarehouseID: wh, Quantity: "1", UnitCost: "50",
	}, "bigbag-entry-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createExit(ctx, movementRequest{
		OperationID: "pack-exit",
		SKUID: pack, WarehouseID: wh, Quantity: "1",
	}, "pack-exit-hash"); err != nil {
		t.Fatal(err)
	}
	s, err := getStockSnapshot(ctx, a.db, pack, wh)
	if err != nil {
		t.Fatal(err)
	}
	if s.Quantity != "9.000" || s.AvgCost != "5.0000" {
		t.Fatalf("rice stock qty=%s avg=%s want 9.000 and 5.0000", s.Quantity, s.AvgCost)
	}
	var inQty, inCost, outQty, outCost string
	if err := a.db.QueryRow(`
SELECT
  max(quantity::text) FILTER (WHERE direction='IN'),
  max(unit_cost::text) FILTER (WHERE direction='IN'),
  max(quantity::text) FILTER (WHERE direction='OUT'),
  max(unit_cost::text) FILTER (WHERE direction='OUT')
FROM movement_lines
WHERE sku_id IN ($1,$2)`, bigBag, pack).Scan(&inQty, &inCost, &outQty, &outCost); err != nil {
		t.Fatal(err)
	}
	if inQty != "10.000" || inCost != "5.0000" || outQty != "1.000" || outCost != "5.0000" {
		t.Fatalf("rice ledger in=%s@%s out=%s@%s", inQty, inCost, outQty, outCost)
	}
}

func TestPresentationNormalizationTransferUsesCanonicalQuantity(t *testing.T) {
	a, sku, fromWH, _ := newStockTestApp(t)
	ctx := context.Background()
	if _, err := a.db.Exec(`UPDATE product_skus SET content_quantity=10 WHERE id=$1`, sku); err != nil {
		t.Fatal(err)
	}
	var toWH int64
	if err := a.db.QueryRow(`INSERT INTO warehouses (name,active) VALUES ($1,true) RETURNING id`, t.Name()+"-dest").Scan(&toWH); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "transfer-seed", SKUID: sku, WarehouseID: fromWH, Quantity: "2", UnitCost: "50",
	}, "transfer-seed-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createTransfer(ctx, movementRequest{
		OperationID: "transfer-one-bag", SKUID: sku, FromWarehouseID: fromWH, ToWarehouseID: toWH, Quantity: "1",
	}, "transfer-one-bag-hash"); err != nil {
		t.Fatal(err)
	}
	from, err := getStockSnapshot(ctx, a.db, sku, fromWH)
	if err != nil { t.Fatal(err) }
	to, err := getStockSnapshot(ctx, a.db, sku, toWH)
	if err != nil { t.Fatal(err) }
	if from.Quantity != "10.000" || to.Quantity != "10.000" {
		t.Fatalf("transfer canonical balances from=%s to=%s want 10.000/10.000", from.Quantity, to.Quantity)
	}
}


func TestCanonicalStockPreservesHistoricalParentAfterSKUReassignment(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()

	var originalItem, catID int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, sku).Scan(&originalItem); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT category_id FROM inventory_items WHERE id=$1`, originalItem).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "historical-parent-entry", SKUID: sku, WarehouseID: wh, Quantity: "5", UnitCost: "10",
	}, "historical-parent-entry-hash"); err != nil {
		t.Fatal(err)
	}

	var newItem int64
	if err := a.db.QueryRow(`
INSERT INTO inventory_items (name,category_id,unit_code,minimum_stock,active)
VALUES ($1,$2,'KG',0,true) RETURNING id`, t.Name()+"-new-parent", catID).Scan(&newItem); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE product_skus SET item_id=$1 WHERE id=$2`, newItem, sku); err != nil {
		t.Fatal(err)
	}

	var historicalItem int64
	if err := a.db.QueryRow(`SELECT item_id FROM movement_lines WHERE sku_id=$1 LIMIT 1`, sku).Scan(&historicalItem); err != nil {
		t.Fatal(err)
	}
	if historicalItem != originalItem {
		t.Fatalf("movement history item=%d want original=%d", historicalItem, originalItem)
	}

	var qtyOriginal, qtyNew string
	if err := a.db.QueryRow(`
SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text
FROM movement_lines WHERE item_id=$1 AND warehouse_id=$2`, originalItem, wh).Scan(&qtyOriginal); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`
SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text
FROM movement_lines WHERE item_id=$1 AND warehouse_id=$2`, newItem, wh).Scan(&qtyNew); err != nil {
		t.Fatal(err)
	}
	if qtyOriginal != "5.000" || qtyNew != "0" {
		t.Fatalf("historical balances original=%s new=%s", qtyOriginal, qtyNew)
	}
}

func TestCatalogContentQuantityDefaultsAndUpdates(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-content-quantity", "secret", "Diretor", true)
	director := doLogin(t, a, "director-content-quantity", "secret")
	catID := lookupID(t, a, `SELECT id FROM categories ORDER BY id LIMIT 1`)

	itemResp := authReq(a, "POST", "/api/catalog/items", map[string]any{
		"name": "Apresentacao Teste", "category_id": catID, "unit_code": "KG", "minimum_stock": "0",
	}, director, "")
	if itemResp.Code != 201 {
		t.Fatalf("create item status=%d body=%s", itemResp.Code, itemResp.Body.String())
	}
	itemID := jsonID(t, itemResp.Body.Bytes())

	skuResp := authReq(a, "POST", "/api/catalog/skus", map[string]any{
		"item_id": itemID, "name": "Pacote", "barcode": t.Name()+"-ean",
	}, director, "")
	if skuResp.Code != 201 {
		t.Fatalf("create sku status=%d body=%s", skuResp.Code, skuResp.Body.String())
	}
	skuID := jsonID(t, skuResp.Body.Bytes())
	var content string
	if err := a.db.QueryRow(`SELECT content_quantity::text FROM product_skus WHERE id=$1`, skuID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "1.0000" {
		t.Fatalf("default content=%s want 1.0000", content)
	}

	edit := authReq(a, "PATCH", "/api/catalog/skus/"+itoa(skuID), map[string]any{
		"item_id": itemID, "name": "Pacote", "barcode": t.Name()+"-ean",
		"content_quantity": "3", "active": true, "version": crudVersion(t, a, "product_skus", skuID),
	}, director, "")
	if edit.Code != 200 {
		t.Fatalf("update sku status=%d body=%s", edit.Code, edit.Body.String())
	}
	if err := a.db.QueryRow(`SELECT content_quantity::text FROM product_skus WHERE id=$1`, skuID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "3.0000" {
		t.Fatalf("updated content=%s want 3.0000", content)
	}
}


func TestCanonicalStockSharesBalanceAcrossBrandsOfSameParent(t *testing.T) {
	a, skuBrandB, wh, _ := newStockTestApp(t)
	ctx := context.Background()

	var itemID int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, skuBrandB).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`
UPDATE product_skus
SET brand='Marca B',name='Marca B 10kg',content_quantity=10
WHERE id=$1`, skuBrandB); err != nil {
		t.Fatal(err)
	}

	var skuBrandA int64
	if err := a.db.QueryRow(`
INSERT INTO product_skus (item_id,brand,name,barcode,content_quantity,active)
VALUES ($1,'Marca A','Marca A 1kg',$2,1,true)
RETURNING id`, itemID, t.Name()+"-marca-a").Scan(&skuBrandA); err != nil {
		t.Fatal(err)
	}

	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "cross-brand-entry",
		SKUID: skuBrandB,
		WarehouseID: wh,
		Quantity: "1",
		UnitCost: "50",
	}, "cross-brand-entry-hash"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.createExit(ctx, movementRequest{
		OperationID: "cross-brand-exit",
		SKUID: skuBrandA,
		WarehouseID: wh,
		Quantity: "1",
	}, "cross-brand-exit-hash"); err != nil {
		t.Fatalf("brands under the same parent must share stock: %v", err)
	}

	s, err := getStockSnapshot(ctx, a.db, skuBrandA, wh)
	if err != nil {
		t.Fatal(err)
	}
	if s.Quantity != "9.000" || s.AvgCost != "5.0000" {
		t.Fatalf("shared cross-brand balance qty=%s avg=%s want 9.000 and 5.0000", s.Quantity, s.AvgCost)
	}

	var inSKU, outSKU int64
	if err := a.db.QueryRow(`
SELECT
  max(sku_id) FILTER (WHERE direction='IN'),
  max(sku_id) FILTER (WHERE direction='OUT')
FROM movement_lines
WHERE movement_id IN (
  SELECT id FROM movements WHERE operation_id IN ('cross-brand-entry','cross-brand-exit')
)`).Scan(&inSKU, &outSKU); err != nil {
		t.Fatal(err)
	}
	if inSKU != skuBrandB || outSKU != skuBrandA {
		t.Fatalf("presentation trace lost: entry sku=%d want %d, exit sku=%d want %d", inSKU, skuBrandB, outSKU, skuBrandA)
	}
}


func TestLotsFIFOWithoutExpiry(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()

	first, err := a.createEntry(ctx, movementRequest{
		OperationID: "lot-fifo-entry-1", SKUID: sku, WarehouseID: wh, Quantity: "4", UnitCost: "10",
	}, "lot-fifo-entry-1-hash")
	if err != nil { t.Fatal(err) }
	second, err := a.createEntry(ctx, movementRequest{
		OperationID: "lot-fifo-entry-2", SKUID: sku, WarehouseID: wh, Quantity: "5", UnitCost: "12",
	}, "lot-fifo-entry-2-hash")
	if err != nil { t.Fatal(err) }

	exit, err := a.createExit(ctx, movementRequest{
		OperationID: "lot-fifo-exit", SKUID: sku, WarehouseID: wh, Quantity: "6",
	}, "lot-fifo-exit-hash")
	if err != nil { t.Fatal(err) }

	var firstLot, secondLot int64
	if err := a.db.QueryRow(`
SELECT l.id FROM stock_lots l
JOIN movement_lines ml ON ml.id=l.source_movement_line_id
WHERE ml.movement_id=$1`, first.MovementID).Scan(&firstLot); err != nil { t.Fatal(err) }
	if err := a.db.QueryRow(`
SELECT l.id FROM stock_lots l
JOIN movement_lines ml ON ml.id=l.source_movement_line_id
WHERE ml.movement_id=$1`, second.MovementID).Scan(&secondLot); err != nil { t.Fatal(err) }

	rows, err := a.db.Query(`
SELECT a.stock_lot_id,a.quantity::text
FROM stock_lot_allocations a
JOIN movement_lines ml ON ml.id=a.movement_line_id
WHERE ml.movement_id=$1
ORDER BY a.stock_lot_id`, exit.MovementID)
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	got := map[int64]string{}
	for rows.Next() {
		var id int64
		var q string
		if err := rows.Scan(&id,&q); err != nil { t.Fatal(err) }
		got[id]=q
	}
	if got[firstLot]!="4.000" || got[secondLot]!="2.000" {
		t.Fatalf("FIFO allocations=%v want first 4.000 second 2.000", got)
	}
}

func TestLotsFEFOWhenExpiryExists(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()

	oldEntry, err := a.createEntry(ctx, movementRequest{
		OperationID: "lot-fefo-old-entry", SKUID: sku, WarehouseID: wh, Quantity: "5", UnitCost: "10",
	}, "lot-fefo-old-entry-hash")
	if err != nil { t.Fatal(err) }
	newEntry, err := a.createEntry(ctx, movementRequest{
		OperationID: "lot-fefo-new-entry", SKUID: sku, WarehouseID: wh, Quantity: "5", UnitCost: "10",
	}, "lot-fefo-new-entry-hash")
	if err != nil { t.Fatal(err) }

	var oldLot, newLot int64
	if err := a.db.QueryRow(`SELECT l.id FROM stock_lots l JOIN movement_lines ml ON ml.id=l.source_movement_line_id WHERE ml.movement_id=$1`, oldEntry.MovementID).Scan(&oldLot); err != nil { t.Fatal(err) }
	if err := a.db.QueryRow(`SELECT l.id FROM stock_lots l JOIN movement_lines ml ON ml.id=l.source_movement_line_id WHERE ml.movement_id=$1`, newEntry.MovementID).Scan(&newLot); err != nil { t.Fatal(err) }
	if _, err := a.db.Exec(`UPDATE stock_lots SET expires_at='2026-12-31' WHERE id=$1`, oldLot); err != nil { t.Fatal(err) }
	if _, err := a.db.Exec(`UPDATE stock_lots SET expires_at='2026-11-30' WHERE id=$1`, newLot); err != nil { t.Fatal(err) }

	exit, err := a.createExit(ctx, movementRequest{
		OperationID: "lot-fefo-exit", SKUID: sku, WarehouseID: wh, Quantity: "3",
	}, "lot-fefo-exit-hash")
	if err != nil { t.Fatal(err) }

	var chosen int64
	var qty string
	if err := a.db.QueryRow(`
SELECT a.stock_lot_id,a.quantity::text
FROM stock_lot_allocations a
JOIN movement_lines ml ON ml.id=a.movement_line_id
WHERE ml.movement_id=$1`, exit.MovementID).Scan(&chosen,&qty); err != nil { t.Fatal(err) }
	if chosen != newLot || qty != "3.000" {
		t.Fatalf("FEFO chose lot=%d qty=%s want lot=%d qty=3.000", chosen, qty, newLot)
	}
}

func TestLotsCrossBrandExitTracksEntryOrigin(t *testing.T) {
	a, skuBrandB, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	var itemID int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, skuBrandB).Scan(&itemID); err != nil { t.Fatal(err) }
	if _, err := a.db.Exec(`UPDATE product_skus SET brand='Marca B',name='Marca B 10kg',content_quantity=10 WHERE id=$1`, skuBrandB); err != nil { t.Fatal(err) }
	var skuBrandA int64
	if err := a.db.QueryRow(`
INSERT INTO product_skus (item_id,brand,name,barcode,content_quantity,active)
VALUES ($1,'Marca A','Marca A 1kg',$2,1,true) RETURNING id`, itemID, t.Name()+"-a").Scan(&skuBrandA); err != nil { t.Fatal(err) }

	entry, err := a.createEntry(ctx, movementRequest{
		OperationID: "lot-cross-brand-entry", SKUID: skuBrandB, WarehouseID: wh, Quantity: "1", UnitCost: "50",
	}, "lot-cross-brand-entry-hash")
	if err != nil { t.Fatal(err) }
	exit, err := a.createExit(ctx, movementRequest{
		OperationID: "lot-cross-brand-exit", SKUID: skuBrandA, WarehouseID: wh, Quantity: "2",
	}, "lot-cross-brand-exit-hash")
	if err != nil { t.Fatal(err) }

	var sourceSKU, exitSKU int64
	var allocated string
	if err := a.db.QueryRow(`
SELECT source_ml.sku_id,exit_ml.sku_id,a.quantity::text
FROM stock_lot_allocations a
JOIN movement_lines exit_ml ON exit_ml.id=a.movement_line_id
JOIN stock_lots l ON l.id=a.stock_lot_id
JOIN movement_lines source_ml ON source_ml.id=l.source_movement_line_id
WHERE exit_ml.movement_id=$1`, exit.MovementID).Scan(&sourceSKU,&exitSKU,&allocated); err != nil { t.Fatal(err) }
	if sourceSKU != skuBrandB || exitSKU != skuBrandA || allocated != "2.000" {
		t.Fatalf("cross-brand trace source=%d exit=%d qty=%s", sourceSKU, exitSKU, allocated)
	}
	_ = entry
}

func TestTransferLotsPreserveLineageAndExpiry(t *testing.T) {
	a, sku, fromWH, toWH := newStockTestApp(t)
	ctx := context.Background()

	entry, err := a.createEntry(ctx, movementRequest{
		OperationID: "lot-transfer-entry", SKUID: sku, WarehouseID: fromWH, Quantity: "8", UnitCost: "10",
	}, "lot-transfer-entry-hash")
	if err != nil { t.Fatal(err) }
	var sourceLot int64
	if err := a.db.QueryRow(`SELECT l.id FROM stock_lots l JOIN movement_lines ml ON ml.id=l.source_movement_line_id WHERE ml.movement_id=$1`, entry.MovementID).Scan(&sourceLot); err != nil { t.Fatal(err) }
	if _, err := a.db.Exec(`UPDATE stock_lots SET expires_at='2026-12-20',manufacturer_lot_code='FAB-123' WHERE id=$1`, sourceLot); err != nil { t.Fatal(err) }

	transfer, err := a.createTransfer(ctx, movementRequest{
		OperationID: "lot-transfer", SKUID: sku, FromWarehouseID: fromWH, ToWarehouseID: toWH, Quantity: "3",
	}, "lot-transfer-hash")
	if err != nil { t.Fatal(err) }

	var derivedSource int64
	var qty, lotCode string
	var expiry string
	if err := a.db.QueryRow(`
SELECT l.source_lot_id,l.initial_quantity::text,COALESCE(l.manufacturer_lot_code,''),l.expires_at::text
FROM stock_lots l
JOIN movement_lines ml ON ml.id=l.source_movement_line_id
WHERE ml.movement_id=$1 AND ml.direction='IN'`, transfer.MovementID).Scan(&derivedSource,&qty,&lotCode,&expiry); err != nil { t.Fatal(err) }
	if derivedSource != sourceLot || qty != "3.000" || lotCode != "FAB-123" || expiry != "2026-12-20" {
		t.Fatalf("transfer lineage source=%d qty=%s code=%s expiry=%s", derivedSource, qty, lotCode, expiry)
	}
}

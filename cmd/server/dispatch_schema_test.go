package main

import (
	"strings"
	"testing"
)

// Regras de expedição que dependem do banco: cada uma precisa recusar o dado inválido
// mesmo quando a API for chamada direto, sem passar pelo Go.
func TestDispatchSchemaRejectsInvalidRows(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	db := a.db
	var placeID, vehicleID, driverID, recipeID, itemID int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.QueryRow(`INSERT INTO places (name,kind,address) VALUES ('Refeitório teste','outro','Rua T, 1') RETURNING id`).Scan(&placeID))
	must(db.QueryRow(`INSERT INTO vehicles (plate,description,capacity_kg) VALUES ('ABC1D23','Van','800') RETURNING id`).Scan(&vehicleID))
	must(db.QueryRow(`INSERT INTO drivers (name) VALUES ('Motorista teste') RETURNING id`).Scan(&driverID))
	must(db.QueryRow(`INSERT INTO recipes (name,portions) VALUES ('Frango assado',10) RETURNING id`).Scan(&recipeID))
	must(db.QueryRow(`SELECT id FROM inventory_items WHERE name='Arroz'`).Scan(&itemID))

	order := func(op, token string) int64 {
		t.Helper()
		var id int64
		must(db.QueryRow(`INSERT INTO delivery_orders (operation_id,place_id,origin_warehouse_id,day,due_at,tracking_token)
VALUES ($1,$2,$3,'2026-10-16','2026-10-16 11:00+00',$4) RETURNING id`, op, placeID, wh, token).Scan(&id))
		return id
	}
	const token = "0123456789abcdef0123456789abcdef"
	orderID := order("op-expedicao-1", token)

	expectErr := func(name, fragment, query string, args ...any) {
		t.Helper()
		_, err := db.Exec(query, args...)
		if err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("%s: want error containing %q, got %v", name, fragment, err)
		}
	}

	// Linha mista: refeição e insumo na mesma linha.
	expectErr("mixed line", "delivery_line_kind",
		`INSERT INTO delivery_order_lines (order_id,recipe_id,portions,item_id,quantity) VALUES ($1,$2,5,$3,1)`, orderID, recipeID, itemID)
	// Linha de refeição sem porções.
	expectErr("meal without portions", "delivery_line_kind",
		`INSERT INTO delivery_order_lines (order_id,recipe_id) VALUES ($1,$2)`, orderID, recipeID)
	// Token curto demais para ser link público.
	expectErr("short token", "delivery_orders_tracking_token_check",
		`INSERT INTO delivery_orders (operation_id,place_id,origin_warehouse_id,day,due_at,tracking_token) VALUES ('op-x',$1,$2,'2026-10-16','2026-10-16 11:00+00','curto')`, placeID, wh)
	// Evento sem par de coordenadas.
	expectErr("half coordinate", "delivery_events_coordinates_pair",
		`INSERT INTO delivery_events (order_id,status,latitude,occurred_at) VALUES ($1,'despachado',-3.7,now())`, orderID)
	// Foto sem conteúdo quando o tipo é foto.
	var eventID int64
	must(db.QueryRow(`INSERT INTO delivery_events (order_id,status,occurred_at) VALUES ($1,'entregue',now()) RETURNING id`, orderID).Scan(&eventID))
	expectErr("photo without content", "delivery_proofs_image_needs_content",
		`INSERT INTO delivery_proofs (order_id,event_id,kind) VALUES ($1,$2,'foto')`, orderID, eventID)

	// Duas rotas abertas no mesmo dia para o mesmo veículo.
	if _, err := db.Exec(`INSERT INTO routes (day,vehicle_id,driver_id) VALUES ('2026-10-16',$1,$2)`, vehicleID, driverID); err != nil {
		t.Fatal(err)
	}
	var otherDriverID int64
	must(db.QueryRow(`INSERT INTO drivers (name) VALUES ('Outro motorista') RETURNING id`).Scan(&otherDriverID))
	expectErr("second open route same vehicle", "idx_routes_open_vehicle_day",
		`INSERT INTO routes (day,vehicle_id,driver_id) VALUES ('2026-10-16',$1,$2)`, vehicleID, otherDriverID)
}

func TestVehicleOwnershipDefaultsToOwnedAndRejectsUnknownValues(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	var ownership string
	if err := a.db.QueryRow(`INSERT INTO vehicles (plate,description,capacity_kg) VALUES ('DEF2G34','Fiorino','650') RETURNING ownership`).Scan(&ownership); err != nil {
		t.Fatal(err)
	}
	if ownership != "propria" {
		t.Fatalf("default ownership = %q", ownership)
	}
	_, err := a.db.Exec(`INSERT INTO vehicles (plate,description,capacity_kg,ownership) VALUES ('GHI3J45','Van','800','emprestada')`)
	if err == nil || !strings.Contains(err.Error(), "vehicles_ownership_check") {
		t.Fatalf("want ownership check error, got %v", err)
	}
}

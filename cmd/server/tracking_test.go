package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicTrackingUsesIndependentSecretAndExposesOnlyDeliveryStatus(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "tracking-director", "secret", "Diretor", true)
	director := doLogin(t, a, "tracking-director", "secret")
	customer := recipeTestCustomer(t, a, director, "Cliente Sigiloso")
	place := dispatchTestPlace(t, a, director, customer)
	item := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	created := authReq(a, http.MethodPost, "/api/delivery-orders",
		dispatchOrderBody("tracking-test-order", place, wh,
			[]map[string]any{{"item_id": item, "quantity": "3"}}), director, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	orderID := jsonID(t, created.Body.Bytes())
	detail := authReq(a, http.MethodGet, "/api/delivery-orders/"+itoa(orderID), nil, director, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var tokens struct {
		TrackingToken       string `json:"tracking_token"`
		PublicTrackingToken string `json:"public_tracking_token"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens.PublicTrackingToken) != 64 || tokens.PublicTrackingToken == tokens.TrackingToken {
		t.Fatalf("public tracking and confirmation code must be different credentials")
	}
	publicGet := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	got := publicGet("/tracking/" + tokens.PublicTrackingToken)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Pedido recebido") {
		t.Fatalf("public status=%d body=%s", got.Code, got.Body.String())
	}
	for _, sensitive := range []string{tokens.TrackingToken, "Cliente Sigiloso", "Refeitório Pecém", "Arroz", "operation_id", "tracking_token", "photo", "latitude"} {
		if strings.Contains(got.Body.String(), sensitive) {
			t.Fatalf("public page leaked sensitive detail %q", sensitive)
		}
	}
	if got.Header().Get("Cache-Control") != "no-store" || got.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(got.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("tracking response must prevent caching, referrer leakage and framing: %v", got.Header())
	}
	if rr := publicGet("/tracking/" + tokens.TrackingToken); rr.Code != http.StatusNotFound {
		t.Fatalf("private confirmation code must not open tracking page: %d", rr.Code)
	}
	if rr := publicGet("/tracking/invalid-token"); rr.Code != http.StatusNotFound {
		t.Fatalf("invalid public token must not open tracking page: %d", rr.Code)
	}
	if _, err := a.db.Exec(`UPDATE delivery_orders SET status='entregue' WHERE id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	updated := publicGet("/tracking/" + tokens.PublicTrackingToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "Entregue") || strings.Contains(updated.Body.String(), "Pedido recebido") {
		t.Fatalf("public tracking must reflect current database status: %s", updated.Body.String())
	}
}

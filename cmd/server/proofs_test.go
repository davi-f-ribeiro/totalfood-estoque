package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deliverReq(a *app, order int64, fields map[string]string, file []byte, ld loginData) *httptest.ResponseRecorder {
	return multipartReq(a, "/api/delivery-orders/"+itoa(order)+"/deliver", fields, file, ld)
}

func driverDeliverReq(a *app, order int64, fields map[string]string, file []byte, ld loginData) *httptest.ResponseRecorder {
	return multipartReq(a, "/api/driver/orders/"+itoa(order)+"/deliver", fields, file, ld)
}

func multipartReq(a *app, path string, fields map[string]string, file []byte, ld loginData) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "comprovante")
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(ld.Cookie)
	req.Header.Set(csrfHeaderName, ld.CSRF)
	rr := httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
	return rr
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// proofTestOrderAtStop cria um pedido e o leva até "chegou" pelos eventos.
func proofTestOrderAtStop(t *testing.T, a *app, director loginData, op string, place, wh, item int64, podRequired bool) int64 {
	t.Helper()
	body := dispatchOrderBody(op, place, wh, []map[string]any{{"item_id": item, "quantity": "1"}})
	body["pod_required"] = podRequired
	id := routeTestCreate(t, a, director, "/api/delivery-orders", body)
	for _, st := range []string{"programado", "despachado", "em_rota", "chegou"} {
		rr := authReq(a, http.MethodPost, "/api/delivery-orders/"+itoa(id)+"/events", map[string]any{"operation_id": op + "-" + st, "status": st}, director, "")
		if rr.Code != http.StatusCreated {
			t.Fatalf("%s status=%d body=%s", st, rr.Code, rr.Body.String())
		}
	}
	return id
}

func TestDeliveryWithProofIsAtomicValidatedAndReplayable(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "proof-director", "secret", "Diretor", true)
	director := doLogin(t, a, "proof-director", "secret")
	customer := recipeTestCustomer(t, a, director, "Cliente Comprovante")
	place := dispatchTestPlace(t, a, director, customer)
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)

	early := routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody("proof-early", place, wh, []map[string]any{{"item_id": arroz, "quantity": "1"}}))
	logisticsErrorIs(t, deliverReq(a, early, map[string]string{"operation_id": "early-1", "kind": "codigo", "code": "x"}, nil, director), http.StatusConflict, "Registre a chegada antes")

	order := proofTestOrderAtStop(t, a, director, "proof-code", place, wh, arroz, true)
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/delivery-orders/"+itoa(order)+"/events", map[string]any{"operation_id": "plain-done", "status": "entregue"}, director, ""), http.StatusConflict, "exige comprovante")
	logisticsErrorIs(t, deliverReq(a, order, map[string]string{"operation_id": "d-1", "kind": "foto"}, nil, director), http.StatusBadRequest, "Anexe a foto")
	logisticsErrorIs(t, deliverReq(a, order, map[string]string{"operation_id": "d-2", "kind": "foto"}, []byte("isto não é uma imagem"), director), http.StatusBadRequest, "PNG ou JPEG")
	logisticsErrorIs(t, deliverReq(a, order, map[string]string{"operation_id": "d-3", "kind": "foto"}, make([]byte, proofMaxBytes+1), director), http.StatusRequestEntityTooLarge, "passa de 5 MB")
	logisticsErrorIs(t, deliverReq(a, order, map[string]string{"operation_id": "d-4", "kind": "codigo", "code": "errado"}, nil, director), http.StatusBadRequest, "não confere")
	logisticsErrorIs(t, deliverReq(a, order, map[string]string{"operation_id": "d-5", "kind": "selfie"}, nil, director), http.StatusBadRequest, "foto, assinatura ou código")

	var token string
	if err := a.db.QueryRow(`SELECT tracking_token FROM delivery_orders WHERE id=$1`, order).Scan(&token); err != nil {
		t.Fatal(err)
	}
	ok := deliverReq(a, order, map[string]string{"operation_id": "d-ok", "kind": "codigo", "code": token, "receiver_name": "Ana"}, nil, director)
	if ok.Code != http.StatusCreated || orderStatus(t, a, order) != "entregue" {
		t.Fatalf("deliver with code status=%d body=%s", ok.Code, ok.Body.String())
	}
	again := deliverReq(a, order, map[string]string{"operation_id": "d-ok", "kind": "codigo", "code": token}, nil, director)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"replayed":true`) {
		t.Fatalf("replayed delivery status=%d body=%s", again.Code, again.Body.String())
	}

	// Refeição pronta: foto do recebimento com a temperatura medida na entrega.
	meal := proofTestOrderAtStop(t, a, director, "proof-photo", place, wh, arroz, true)
	logisticsErrorIs(t, deliverReq(a, meal, map[string]string{"operation_id": "p-hot", "kind": "foto", "temperature_c": "150"}, tinyPNG(t), director), http.StatusBadRequest, "fora da faixa")
	if orderStatus(t, a, meal) != "chegou" {
		t.Fatalf("a rejected proof must not deliver the order, status=%s", orderStatus(t, a, meal))
	}
	photo := tinyPNG(t)
	rr := deliverReq(a, meal, map[string]string{"operation_id": "p-ok", "kind": "foto", "temperature_c": "65,5", "receiver_name": "Copa", "latitude": "-3,5432", "longitude": "-38.8123"}, photo, director)
	if rr.Code != http.StatusCreated {
		t.Fatalf("deliver with photo status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		ProofID int64 `json:"proof_id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	content := authReq(a, http.MethodGet, "/api/delivery-proofs/"+itoa(created.ProofID)+"/content", nil, director, "")
	if content.Code != http.StatusOK || content.Header().Get("Content-Type") != "image/png" || !bytes.Equal(content.Body.Bytes(), photo) {
		t.Fatalf("proof content status=%d type=%s len=%d", content.Code, content.Header().Get("Content-Type"), content.Body.Len())
	}
	detail := authReq(a, http.MethodGet, "/api/delivery-orders/"+itoa(meal), nil, director, "")
	if !strings.Contains(detail.Body.String(), `"temperature_c":"65.50"`) || !strings.Contains(detail.Body.String(), `"has_image":true`) {
		t.Fatalf("detail must list the proof without the image: %s", detail.Body.String())
	}

	// Sem exigência de comprovante, o evento simples basta.
	loose := proofTestOrderAtStop(t, a, director, "proof-loose", place, wh, arroz, false)
	if rr := authReq(a, http.MethodPost, "/api/delivery-orders/"+itoa(loose)+"/events", map[string]any{"operation_id": "loose-done", "status": "entregue"}, director, ""); rr.Code != http.StatusCreated {
		t.Fatalf("plain delivery without POD status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestProofEndpointsRequireDispatchPermission(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "proof-manager", "secret", "Gerente", true)
	createTestUser(t, a, "proof-stock", "secret", "Estoquista", true)
	manager := doLogin(t, a, "proof-manager", "secret")
	stock := doLogin(t, a, "proof-stock", "secret")
	if rr := deliverReq(a, 1, map[string]string{"operation_id": "x", "kind": "codigo"}, nil, manager); rr.Code != http.StatusForbidden {
		t.Fatalf("manager deliver status=%d, want 403", rr.Code)
	}
	if rr := authReq(a, http.MethodGet, "/api/delivery-proofs/1/content", nil, stock, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("stock clerk proof status=%d, want 403", rr.Code)
	}
}

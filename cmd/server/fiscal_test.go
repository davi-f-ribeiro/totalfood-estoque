package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFiscalQRInfrastructureSecurity(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-fiscal", "secret", "Diretor", true)
	createTestUser(t, a, "sales-fiscal", "secret", "Vendas", true)
	director := doLogin(t, a, "director-fiscal", "secret")
	sales := doLogin(t, a, "sales-fiscal", "secret")

	if rr := fiscalReq(a, "not a url", director, ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid qr status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := fiscalReq(a, "https://nao-permitido.example/nf", director, ""); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "domínio fiscal não permitido") {
		t.Fatalf("domain status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := fiscalReq(a, "https://localhost/nf", director, ""); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "destino fiscal não permitido") {
		t.Fatalf("localhost status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := fiscalReq(a, "https://127.0.0.1/nf", director, ""); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "destino fiscal não permitido") {
		t.Fatalf("loopback status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := fiscalReq(a, "https://10.0.0.1/nf", director, ""); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "destino fiscal não permitido") {
		t.Fatalf("private status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := fiscalReq(a, "https://fiscal.test/nf", sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("permission status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := fiscalReq(a, "https://fiscal.test/nf", director, "bad-csrf"); rr.Code != http.StatusForbidden {
		t.Fatalf("csrf status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestFiscalQRFetchLimitsAndParserBlocker(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-fiscal-ok", "secret", "Diretor", true)
	director := doLogin(t, a, "director-fiscal-ok", "secret")
	oldHosts, oldLookup, oldClient := fiscalAllowedHosts, fiscalLookupIP, fiscalHTTPClient
	t.Cleanup(func() { fiscalAllowedHosts, fiscalLookupIP, fiscalHTTPClient = oldHosts, oldLookup, oldClient })
	fiscalAllowedHosts = func() map[string]bool { return map[string]bool{"fiscal.test": true, "nfce.sefaz.ce.gov.br": true} }
	fiscalLookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
	}

	ok := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("NF TESTE MINIMA")) }))
	defer ok.Close()
	fiscalHTTPClient = fiscalTestClient(t, ok.URL, fiscalTimeout)
	rr := fiscalReq(a, strings.Replace(ok.URL, "127.0.0.1", "fiscal.test", 1), director, "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "parser fiscal depende") {
		t.Fatalf("valid fetch status=%d body=%s", rr.Code, rr.Body.String())
	}
	realQR := "http://nfce.sefaz.ce.gov.br/pages/ShowNFCe.html?p=23260903720882003416650070000391961071250200%7C3%7C1"
	u, meta, err := validateFiscalURL(context.Background(), realQR)
	if err != nil || u.Scheme != "http" || meta.AccessKey != "23260903720882003416650070000391961071250200" || meta.Version != "3" || meta.Environment != "1" {
		t.Fatalf("real qr parse url=%v meta=%+v err=%v", u, meta, err)
	}

	big := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(make([]byte, fiscalMaxBody+1)) }))
	defer big.Close()
	fiscalHTTPClient = fiscalTestClient(t, big.URL, fiscalTimeout)
	if rr := fiscalReq(a, strings.Replace(big.URL, "127.0.0.1", "fiscal.test", 1), director, ""); rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "resposta fiscal excedeu o limite") {
		t.Fatalf("body limit status=%d body=%s", rr.Code, rr.Body.String())
	}

	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://127.0.0.1/privado", http.StatusFound)
	}))
	defer redir.Close()
	fiscalHTTPClient = fiscalTestClient(t, redir.URL, fiscalTimeout)
	if rr := fiscalReq(a, strings.Replace(redir.URL, "127.0.0.1", "fiscal.test", 1), director, ""); rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "redirect fiscal rejeitado") {
		t.Fatalf("redirect status=%d body=%s", rr.Code, rr.Body.String())
	}

	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("slow"))
	}))
	defer slow.Close()
	fiscalHTTPClient = fiscalTestClient(t, slow.URL, 20*time.Millisecond)
	if rr := fiscalReq(a, strings.Replace(slow.URL, "127.0.0.1", "fiscal.test", 1), director, ""); rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "consulta fiscal expirou") {
		t.Fatalf("timeout status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestFiscalAccessKeyParsingAndRouting(t *testing.T) {
	key, err := parseFiscalAccessKey("23260903720882003416650070000391961071250200")
	if err != nil {
		t.Fatalf("real key rejected: %v", err)
	}
	if key.CUF != "23" || key.UF != "CE" || key.AAMM != "2609" || key.CNPJ != "03720882003416" || key.Model != "65" || key.Series != "007" || key.Number != "000039196" || key.EmissionType != "1" || key.NumericCode != "07125020" || key.CheckDigit != "0" {
		t.Fatalf("bad decomposition: %+v", key)
	}
	if route, ok := fiscalRouteForKey(key); !ok || route != "ce" {
		t.Fatalf("bad route route=%q ok=%v", route, ok)
	}
	if _, err := parseFiscalAccessKey("2326090372088200341665007000039196107125020"); !errors.Is(err, errFiscalKeyInvalid) {
		t.Fatalf("short key err=%v", err)
	}
	if _, err := parseFiscalAccessKey("2326090372088200341665007000039196107125020A"); !errors.Is(err, errFiscalKeyInvalid) {
		t.Fatalf("non numeric err=%v", err)
	}
	if _, err := parseFiscalAccessKey("23260903720882003416650070000391961071250201"); !errors.Is(err, errFiscalKeyInvalid) {
		t.Fatalf("bad dv err=%v", err)
	}
	unsupportedModel := validAccessKeyForTest("2326090372088200341655007000039196107125020")
	if _, err := parseFiscalAccessKey(unsupportedModel); !errors.Is(err, errFiscalModelUnsupported) {
		t.Fatalf("unsupported model err=%v", err)
	}
	unknownUF := validAccessKeyForTest("9926090372088200341665007000039196107125020")
	if _, err := parseFiscalAccessKey(unknownUF); !errors.Is(err, errFiscalUFUnsupported) {
		t.Fatalf("unknown uf err=%v", err)
	}
	scKey := validAccessKeyForTest("4226090372088200341665007000039196107125020")
	u, _ := url.Parse("http://nfce.sefaz.ce.gov.br/pages/ShowNFCe.html?p=" + scKey + "%7C3%7C1")
	meta := parseFiscalQRMeta(u)
	if meta.Key.UF != "SC" || !meta.Divergence || meta.Route != "" {
		t.Fatalf("divergence/route meta=%+v", meta)
	}
}

func validAccessKeyForTest(prefix43 string) string {
	return prefix43 + string(rune('0'+calculateAccessKeyDV(prefix43)))
}

func fiscalReq(a *app, qr string, ld loginData, csrf string) *httptest.ResponseRecorder {
	return authReq(a, http.MethodPost, "/api/fiscal/qr/inspect", map[string]any{"qr": qr}, ld, csrf)
}

func fiscalTestClient(t *testing.T, target string, timeout time.Duration) *http.Client {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{}
			conn, err := d.DialContext(ctx, network, u.Host)
			if err != nil {
				return nil, err
			}
			return tls.Client(conn, &tls.Config{InsecureSkipVerify: true}), nil
		},
	}}
}

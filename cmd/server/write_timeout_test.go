package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// O prazo geral de escrita conta desde o início da requisição. Uma rota lenta
// com withWriteTimeout precisa conseguir responder depois dele; sem o wrapper,
// a resposta se perde (o cliente vê a conexão cair).
func TestSlowRouteWriteDeadline(t *testing.T) {
	slow := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		io.WriteString(w, "ok")
	}
	get := func(h http.HandlerFunc) (string, error) {
		srv := httptest.NewUnstartedServer(h)
		srv.Config.WriteTimeout = 150 * time.Millisecond
		srv.Start()
		defer srv.Close()
		res, err := http.Get(srv.URL)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		return string(b), err
	}
	if body, err := get(slow); err == nil && body == "ok" {
		t.Fatal("control: the default write timeout should have dropped the slow response")
	}
	body, err := get(withWriteTimeout(2*time.Second, slow))
	if err != nil || body != "ok" {
		t.Fatalf("slow route with its own deadline: body=%q err=%v", body, err)
	}
}

// As rotas lentas conhecidas usam prazo próprio, e a foto de entrega amplia
// leitura e escrita.
func TestSlowRoutesHaveTheirOwnWriteDeadline(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for file, routes := range map[string][]string{
		"movements.go": {`"GET /api/reports/movements", withWriteTimeout(`, `"GET /api/reports/balance", withWriteTimeout(`},
		"planner.go":   {`"POST /api/plans/{id}/solve", withWriteTimeout(`},
		"proofs.go":    {`"GET /api/delivery-proofs/{id}/content", withWriteTimeout(`, `extendDeadlines(w, proofTransferTime, `},
	} {
		src := read(file)
		for _, want := range routes {
			if !strings.Contains(src, want) {
				t.Errorf("%s: missing %s", file, want)
			}
		}
	}
}

// O healthcheck do Compose roda "/app/estoque healthcheck <url>": a imagem
// final não tem wget nem curl.
func TestHealthcheckCommand(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "ok\n")
	}))
	defer ok.Close()
	if err := healthcheck(ok.URL + "/health"); err != nil {
		t.Fatalf("healthy server: %v", err)
	}
	if err := healthcheck(ok.URL + "/missing"); err == nil {
		t.Fatal("non-200 must fail")
	}
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(compose), `"wget"`) || strings.Contains(string(compose), `"curl"`) {
		t.Fatal("compose.yaml healthcheck uses a tool the runtime image does not have")
	}
	if !strings.Contains(string(compose), `"/app/estoque", "healthcheck"`) {
		t.Fatal("compose.yaml app healthcheck must call /app/estoque healthcheck")
	}
	docker, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(docker), "COPY --from=build /out/estoque /app/estoque") {
		t.Fatal("Dockerfile no longer installs the binary at /app/estoque")
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const rastroBaseURL = "https://rastroconnect.rastrosystem.com.br"

var errRastroFleetSchema = errors.New("O rastreador enviou veículos sem placa ou identificador válido; nenhuma importação foi realizada.")

var (
	rastroInputRx = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	rastroAttrRx  = regexp.MustCompile(`(?i)([\w-]+)\s*=\s*["']([^"']*)["']`)
	rastroTokenRx = regexp.MustCompile(`(?i)(?:var|let|const)?\s*token_ajax\s*=\s*["']([^"']+)["']`)
)

// rastroFleet queries the read-only monitoring endpoint used by the web panel.
// A caller-supplied origin exists for local integration tests; production fixes it
// to the provider host, never to arbitrary user input.
func rastroFleet(ctx context.Context, origin, company, username, password string) ([]rastroVehicle, []string, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 18 * time.Second, Jar: jar}
	loginURL := origin + "/acl/login/?next=/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return nil, nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("abrir página de login: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK {
		return nil, nil, errors.New("não foi possível abrir o login do rastreador")
	}

	form := url.Values{"empresa": {company}, "login": {username}, "senha": {password}}
	for _, tag := range rastroInputRx.FindAllString(string(body), -1) {
		attrs := make(map[string]string)
		for _, kv := range rastroAttrRx.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(kv[1])] = html.UnescapeString(kv[2])
		}
		if attrs["type"] == "hidden" && attrs["name"] != "" && attrs["name"] != "empresa" {
			form.Set(attrs["name"], attrs["value"])
		}
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", loginURL)
	res, err = client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("autenticar no rastreador: %w", err)
	}
	body, err = io.ReadAll(io.LimitReader(res.Body, 4<<20))
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || strings.Contains(res.Request.URL.Path, "/acl/login") {
		return nil, nil, errors.New("Rastro Connect rejeitou o acesso; confira as credenciais")
	}
	token := rastroTokenRx.FindSubmatch(body)
	if len(token) != 2 {
		return nil, nil, errors.New("Rastro Connect não disponibilizou o token de monitoramento")
	}
	payload, _ := json.Marshal(map[string]any{
		"op": "update", "kargs": map[string]any{"ids": []int{}, "rastro": false},
		"limitPlot": nil, "busca": "", "eqpToShow": []int{},
	})
	reqBody := url.Values{"json": {string(payload)}}.Encode()
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, origin+"/home/api/monitoramento/", strings.NewReader(reqBody))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "token "+string(token[1]))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", res.Request.URL.String())
	res, err = client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("consultar monitoramento: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("monitoramento indisponível (HTTP %d)", res.StatusCode)
	}
	var items []map[string]any
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&items); err != nil {
		return nil, nil, errors.New("formato de resposta do monitoramento incompatível")
	}
	if items == nil {
		return nil, nil, errors.New("monitoramento devolveu resposta vazia")
	}
	out := make([]rastroVehicle, 0, len(items))
	skipped := []string{}
	for index, item := range items {
		if v, ok := parseRastroVehicle(item); ok {
			out = append(out, v)
			continue
		}
		plate := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(rastroString(item["placa"])))
		if platePattern.MatchString(plate) {
			skipped = append(skipped, plate+": identificador remoto inválido")
		} else {
			skipped = append(skipped, fmt.Sprintf("Registro %d: placa inválida", index+1))
		}
	}
	if len(items) > 0 && len(out) == 0 {
		return nil, skipped, errRastroFleetSchema
	}
	return out, skipped, nil
}

type rastroVehicle struct {
	Plate, Description, RemoteID, EquipmentID, SourceTime string
	Latitude, Longitude, Speed                            *float64
	Ignition                                              *bool
}

func rastroString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}
func rastroFloat(v any) *float64 {
	s := rastroString(v)
	if s == "" {
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}
func rastroBool(v any) *bool {
	if b, ok := v.(bool); ok {
		return &b
	}
	switch rastroString(v) {
	case "1", "true", "True":
		b := true
		return &b
	case "0", "false", "False":
		b := false
		return &b
	}
	return nil
}
func parseRastroVehicle(m map[string]any) (rastroVehicle, bool) {
	plate := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(rastroString(m["placa"])))
	id := rastroString(m["veiculo_id"])
	if !platePattern.MatchString(plate) || id == "" || id == "0" {
		return rastroVehicle{}, false
	}
	name := rastroString(m["veiculo"])
	if name == "" {
		name = "Veículo " + plate
	}
	lat, lng := rastroFloat(m["latitude"]), rastroFloat(m["longitude"])
	if lat == nil || lng == nil || *lat < -90 || *lat > 90 || *lng < -180 || *lng > 180 {
		lat, lng = nil, nil
	}
	speed := rastroFloat(m["velocidade"])
	if speed != nil && (*speed < 0 || *speed > 1000) {
		speed = nil
	}
	return rastroVehicle{
		Plate: plate, Description: name, RemoteID: id,
		EquipmentID: rastroString(m["eqp_id"]), Latitude: lat, Longitude: lng,
		Speed: speed, Ignition: rastroBool(m["ignition"]),
		SourceTime: rastroString(m["position_devicetime"]),
	}, true
}

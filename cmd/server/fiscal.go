package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const fiscalMaxBody int64 = 512 * 1024
const fiscalTimeout = 5 * time.Second

var officialFiscalHTTPHosts = map[string]bool{
	"nfce.sefaz.ce.gov.br":  true,
	"nfceh.sefaz.ce.gov.br": true,
}

type fiscalQRRequest struct {
	QR string `json:"qr"`
}

type fiscalConfirmRequest struct {
	OperationID string             `json:"operation_id"`
	AccessKey   string             `json:"access_key"`
	WarehouseID int64              `json:"warehouse_id"`
	QuantitySemantics string       `json:"quantity_semantics,omitempty"`
	Lines       []entryLineRequest `json:"lines"`
}

type fiscalFetchResult struct {
	URL         string
	Host        string
	ContentType string
	Bytes       int
	Meta        fiscalQRMeta
}

type fiscalQRMeta struct {
	AccessKey   string          `json:"access_key,omitempty"`
	Version     string          `json:"version,omitempty"`
	Environment string          `json:"environment,omitempty"`
	Format      string          `json:"format,omitempty"`
	Key         fiscalAccessKey `json:"key,omitempty"`
	Route       string          `json:"route,omitempty"`
	HostUF      string          `json:"host_uf,omitempty"`
	Divergence  bool            `json:"divergence"`
}

var fiscalAllowedHosts = loadFiscalAllowedHosts
var fiscalLookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}
var fiscalHTTPClient = &http.Client{Timeout: fiscalTimeout}

func (a *app) registerFiscalHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /fiscal", a.uiPage("fiscal", "Importar NF", "fiscal_import", "write"))
	mux.HandleFunc("POST /api/fiscal/qr/inspect", a.requireAuth("fiscal_import", "write", a.handleFiscalQRInspect))
	mux.HandleFunc("POST /api/fiscal/confirm", a.requireAuth("fiscal_import", "write", a.requireAuth("movement_entry", "write", a.handleFiscalConfirm)))
}

func (a *app) handleFiscalQRInspect(w http.ResponseWriter, r *http.Request) {
	var req fiscalQRRequest
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.QR) == "" {
		writeJSONError(w, http.StatusBadRequest, "QR code inválido.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fiscalTimeout)
	defer cancel()
	res, err := fetchFiscalQR(ctx, strings.TrimSpace(req.QR))
	if err != nil {
		status, msg := fiscalHTTPError(err)
		log.Printf("fiscal qr inspect failed reason=%s", msg)
		writeJSONError(w, status, msg)
		return
	}
	if res.Meta.Divergence {
		log.Printf("fiscal qr host diverges from key host_uf=%s key_uf=%s", res.Meta.HostUF, res.Meta.Key.UF)
	}
	log.Printf("fiscal parser blocked host=%s content_type=%s bytes=%d", res.Host, res.ContentType, res.Bytes)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "parser_blocked",
		"message":      "BLOQUEIO: parser fiscal depende dos dados estruturados da fonte real; a amostra NFC-e CE retornou shell HTML e exige consulta fiscal complementar sem itens expostos nesta resposta.",
		"url":          res.URL,
		"host":         res.Host,
		"content_type": res.ContentType,
		"bytes":        res.Bytes,
		"fiscal":       res.Meta,
	})
}

func (a *app) handleFiscalConfirm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req fiscalConfirmRequest
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo.")
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.QuantitySemantics = strings.TrimSpace(req.QuantitySemantics)
	req.AccessKey = strings.TrimSpace(req.AccessKey)
	key, err := parseFiscalAccessKey(req.AccessKey)
	if err != nil || key.Model != "65" {
		writeJSONError(w, http.StatusBadRequest, "Chave da nota inválida.")
		return
	}
	if req.QuantitySemantics != "" && req.QuantitySemantics != presentationQuantitySemantics {
		writeJSONError(w, http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo.")
		return
	}
	if req.OperationID == "" {
		writeJSONError(w, http.StatusBadRequest, "Não foi possível identificar este envio. Atualize a página e tente de novo.")
		return
	}
	if req.WarehouseID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Escolha o depósito de entrada da nota.")
		return
	}
	if len(req.Lines) == 0 {
		writeJSONError(w, http.StatusBadRequest, "Adicione ao menos um item à nota.")
		return
	}
	body, _ := json.Marshal(req)
	h := sha256.Sum256(body)
	mreq := movementRequest{OperationID: req.OperationID, WarehouseID: req.WarehouseID, QuantitySemantics: req.QuantitySemantics, DocumentNumber: req.AccessKey, Notes: "Entrada por fallback manual de NFC-e", UserID: userFromContext(r.Context()).ID}
	res, err := a.createEntryLines(r.Context(), mreq, req.Lines, hex.EncodeToString(h[:]), req.AccessKey)
	a.writeOperationResponse(w, "ENTRADA", req.OperationID, res, err)
}

var (
	errFiscalInvalidURL       = errors.New("QR code inválido.")
	errFiscalDomainDenied     = errors.New("Este endereço de nota fiscal não é aceito.")
	errFiscalTargetDenied     = errors.New("Este endereço de nota fiscal não é aceito.")
	errFiscalRedirectRejected = errors.New("Não foi possível consultar esta nota. Confira o endereço.")
	errFiscalBodyTooLarge     = errors.New("A consulta da nota não respondeu como esperado. Tente de novo.")
	errFiscalTimeout          = errors.New("A consulta da nota demorou demais. Tente de novo.")
)

func fetchFiscalQR(ctx context.Context, raw string) (fiscalFetchResult, error) {
	u, meta, err := validateFiscalURL(ctx, raw)
	if err != nil {
		return fiscalFetchResult{}, err
	}
	client := *fiscalHTTPClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errFiscalRedirectRejected
		}
		if _, _, err := validateFiscalURL(req.Context(), req.URL.String()); err != nil {
			return errFiscalRedirectRejected
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fiscalFetchResult{}, errFiscalInvalidURL
	}
	req.Header.Set("User-Agent", "estoque-nf-import/1.0")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			return fiscalFetchResult{}, errFiscalTimeout
		}
		if errors.Is(err, errFiscalRedirectRejected) {
			return fiscalFetchResult{}, errFiscalRedirectRejected
		}
		return fiscalFetchResult{}, fmt.Errorf("Não foi possível consultar a nota agora. Tente de novo.")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, fiscalMaxBody+1))
	if err != nil {
		return fiscalFetchResult{}, fmt.Errorf("Não foi possível consultar a nota agora. Tente de novo.")
	}
	if int64(len(body)) > fiscalMaxBody {
		return fiscalFetchResult{}, errFiscalBodyTooLarge
	}
	return fiscalFetchResult{URL: u.String(), Host: u.Hostname(), ContentType: resp.Header.Get("Content-Type"), Bytes: len(body), Meta: meta}, nil
}

func validateFiscalURL(ctx context.Context, raw string) (*url.URL, fiscalQRMeta, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, fiscalQRMeta{}, errFiscalInvalidURL
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && officialFiscalHTTPHosts[host]) {
		return nil, fiscalQRMeta{}, errFiscalInvalidURL
	}
	if host == "localhost" {
		return nil, fiscalQRMeta{}, errFiscalTargetDenied
	}
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return nil, fiscalQRMeta{}, errFiscalTargetDenied
		}
	} else {
		if !fiscalAllowedHosts()[host] {
			return nil, fiscalQRMeta{}, errFiscalDomainDenied
		}
		ips, err := fiscalLookupIP(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fiscalQRMeta{}, errFiscalTargetDenied
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, fiscalQRMeta{}, errFiscalTargetDenied
			}
		}
	}
	if net.ParseIP(host) != nil && !fiscalAllowedHosts()[host] {
		return nil, fiscalQRMeta{}, errFiscalDomainDenied
	}
	return u, parseFiscalQRMeta(u), nil
}

func parseFiscalQRMeta(u *url.URL) fiscalQRMeta {
	hostUF := hostUFHint(strings.ToLower(u.Hostname()))
	if strings.Contains(u.Path, "ShowNFCe.html") {
		p := u.Query().Get("p")
		parts := strings.Split(p, "|")
		if len(parts) >= 3 && len(parts[0]) == 44 {
			return buildFiscalQRMeta(parts[0], parts[1], parts[2], "NFC-e qrcode v"+parts[1], hostUF)
		}
	}
	if ch := u.Query().Get("chNFe"); len(ch) == 44 {
		return buildFiscalQRMeta(ch, u.Query().Get("nVersao"), u.Query().Get("tpAmb"), "NFC-e qrcode v100", hostUF)
	}
	return fiscalQRMeta{}
}

func buildFiscalQRMeta(accessKey, version, environment, format, hostUF string) fiscalQRMeta {
	meta := fiscalQRMeta{AccessKey: accessKey, Version: version, Environment: environment, Format: format, HostUF: hostUF}
	key, err := parseFiscalAccessKey(accessKey)
	if err == nil || errors.Is(err, errFiscalModelUnsupported) {
		meta.Key = key
		if route, ok := fiscalRouteForKey(key); ok {
			meta.Route = route
		}
		meta.Divergence = hostUF != "" && key.UF != "" && hostUF != key.UF
	}
	return meta
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}

func loadFiscalAllowedHosts() map[string]bool {
	out := map[string]bool{}
	for h := range officialFiscalHTTPHosts {
		out[h] = true
	}
	for _, h := range strings.Split(os.Getenv("FISCAL_ALLOWED_HOSTS"), ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			out[h] = true
		}
	}
	return out
}

func fiscalHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, errFiscalInvalidURL):
		return http.StatusBadRequest, "QR code inválido."
	case errors.Is(err, errFiscalDomainDenied):
		return http.StatusBadRequest, "Este endereço de nota fiscal não é aceito."
	case errors.Is(err, errFiscalTargetDenied):
		return http.StatusBadRequest, "Este endereço de nota fiscal não é aceito."
	case errors.Is(err, errFiscalRedirectRejected):
		return http.StatusBadGateway, "Não foi possível consultar esta nota. Confira o endereço."
	case errors.Is(err, errFiscalBodyTooLarge):
		return http.StatusBadGateway, "A consulta da nota não respondeu como esperado. Tente de novo."
	case errors.Is(err, errFiscalTimeout):
		return http.StatusBadGateway, "A consulta da nota demorou demais. Tente de novo."
	default:
		return http.StatusBadGateway, "Não foi possível consultar a nota agora. Tente de novo."
	}
}

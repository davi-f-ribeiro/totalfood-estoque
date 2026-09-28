package main

import (
	"errors"
	"strings"
)

type fiscalAccessKey struct {
	Raw             string `json:"raw,omitempty"`
	CUF             string `json:"cuf,omitempty"`
	UF              string `json:"uf,omitempty"`
	AAMM            string `json:"aamm,omitempty"`
	CNPJ            string `json:"cnpj,omitempty"`
	Model           string `json:"model,omitempty"`
	Series          string `json:"series,omitempty"`
	Number          string `json:"number,omitempty"`
	EmissionType    string `json:"emission_type,omitempty"`
	NumericCode     string `json:"numeric_code,omitempty"`
	CheckDigit      string `json:"check_digit,omitempty"`
	Supported       bool   `json:"supported"`
	UnsupportedNote string `json:"unsupported_note,omitempty"`
}

var (
	errFiscalKeyInvalid       = errors.New("chave fiscal inválida")
	errFiscalUFUnsupported    = errors.New("UF fiscal ainda não suportada")
	errFiscalModelUnsupported = errors.New("modelo fiscal não suportado")
)

func parseFiscalAccessKey(raw string) (fiscalAccessKey, error) {
	if len(raw) != 44 {
		return fiscalAccessKey{}, errFiscalKeyInvalid
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return fiscalAccessKey{}, errFiscalKeyInvalid
		}
	}
	if calculateAccessKeyDV(raw[:43]) != int(raw[43]-'0') {
		return fiscalAccessKey{}, errFiscalKeyInvalid
	}
	uf, ok := fiscalUFByCUF(raw[:2])
	if !ok {
		return fiscalAccessKey{}, errFiscalUFUnsupported
	}
	key := fiscalAccessKey{
		Raw: raw, CUF: raw[:2], UF: uf, AAMM: raw[2:6], CNPJ: raw[6:20], Model: raw[20:22],
		Series: raw[22:25], Number: raw[25:34], EmissionType: raw[34:35], NumericCode: raw[35:43], CheckDigit: raw[43:44],
	}
	if key.Model != "65" {
		key.UnsupportedNote = "Somente modelo 65 (NFC-e) é suportado nesta fase."
		return key, errFiscalModelUnsupported
	}
	key.Supported = true
	return key, nil
}

func calculateAccessKeyDV(prefix43 string) int {
	weight, sum := 2, 0
	for i := len(prefix43) - 1; i >= 0; i-- {
		sum += int(prefix43[i]-'0') * weight
		weight++
		if weight > 9 {
			weight = 2
		}
	}
	dv := 11 - (sum % 11)
	if dv >= 10 {
		return 0
	}
	return dv
}

func fiscalUFByCUF(cuf string) (string, bool) {
	m := map[string]string{"11": "RO", "12": "AC", "13": "AM", "14": "RR", "15": "PA", "16": "AP", "17": "TO", "21": "MA", "22": "PI", "23": "CE", "24": "RN", "25": "PB", "26": "PE", "27": "AL", "28": "SE", "29": "BA", "31": "MG", "32": "ES", "33": "RJ", "35": "SP", "41": "PR", "42": "SC", "43": "RS", "50": "MS", "51": "MT", "52": "GO", "53": "DF"}
	v, ok := m[cuf]
	return v, ok
}

func fiscalRouteForKey(key fiscalAccessKey) (string, bool) {
	switch key.CUF {
	case "23":
		return "ce", true
	default:
		return "", false
	}
}

func hostUFHint(host string) string {
	if strings.Contains(host, ".ce.gov.br") {
		return "CE"
	}
	if strings.Contains(host, ".sc.gov.br") {
		return "SC"
	}
	return ""
}

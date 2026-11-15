package main

import (
	"strings"
	"testing"
)

func TestCrateLabelZPLFits60mmAndEscapesInput(t *testing.T) {
	crate := crateRow{
		ID: 7, Code: "CX-000007", Product: "Tomate Italiano Extra Selecionado Grande",
		Weight: "19.500", Position: 1, Total: 3, Document: "NF-1234567890 ^FS^XZ",
		Received: "15/11/2026", OriginLotID: 23, Revision: 2,
	}
	zpl := crateZPL(crate)
	if !strings.HasPrefix(zpl, "^XA") || !strings.HasSuffix(zpl, "^XZ\n") {
		t.Fatal("invalid Zebra job delimiters")
	}
	if strings.Count(zpl, "^XA") != 1 || strings.Count(zpl, "^XZ") != 1 {
		t.Fatal("untrusted product text must not inject commands")
	}
	if !strings.Contains(zpl, "^PW480") || !strings.Contains(zpl, "^LL480") || !strings.Contains(zpl, "^FB448,2,3,L,0") {
		t.Fatal("60mm canvas requires a two-line product field that cannot run beyond the label")
	}
	if !strings.Contains(zpl, "19,500 KG") || !strings.Contains(zpl, "CAIXA 1/3") ||
		!strings.Contains(zpl, "CX-000007") || !strings.Contains(zpl, "LOTE: TF-23") ||
		!strings.Contains(zpl, "REV 2") {
		t.Fatalf("required traceability attributes missing from label: %s", zpl)
	}
	if len(zpl) > 65536 {
		t.Fatal("ZPL exceeds the maximum accepted queue payload")
	}
}

func TestCrateLabelUsesExistingTotalFoodIdentity(t *testing.T) {
	zpl := crateZPL(crateRow{Product: "Tomate Italiano", Code: "CX-000007", Weight: "19.500", Position: 1, Total: 3, Received: "15/11/2026", OriginLotID: 23, Revision: 2})
	for _, part := range []string{"^PW480", "^LL480", "^FO160,10^XGR:LOGODP.GRF,1,1^FS", "^FO16,84^GB448,3,3^FS", "^FO16,346^GB448,3,3^FS", "TOTAL FOOD ALIMENTOS", "CNPJ: 17.053.741/0001-33", "RUA MARIO GUEDES, 50", "CEP 60811-012 - FORTALEZA, CE", "^BQN,2,4", "^FO16,242^A0N,28,24^FD19,500 KG^FS", "^PQ1"} {
		if !strings.Contains(zpl, part) {
			t.Errorf("missing TotalFood identity %q", part)
		}
	}
}

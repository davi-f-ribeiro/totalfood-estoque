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
	if !strings.Contains(zpl, "^PW480") || !strings.Contains(zpl, "^LL480") || !strings.Contains(zpl, "^FB440,2,4,L,0") {
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

"""Source-level guards for the Zebra status shown to the operator.

Caso real (25/11/2026): o ribbon acabou, a ZD220 se pausou e a tela só dizia
"Impressora pausada". Estes testes garantem que a causa e o que fazer aparecem.
Não substituem compilar no core 3.0.5 nem testar na impressora.
"""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
INO = (ROOT / "etiqueta_zd220" / "etiqueta_zd220.ino").read_text(encoding="utf-8")
PAGES = (ROOT / "etiqueta_zd220" / "web_pages.h").read_text(encoding="utf-8")

# Resposta ~HS da ZD220 sem ribbon, capturada no PC em 25/11/2026
HS_SEM_RIBBON = ["030,0,1,0478,000,0,0,0,000,0,0,0", "001,0,0,1,1,2,6,0,00000000,1,000"]
STATES = ["cabeca_aberta", "sem_ribbon", "sem_papel", "temperatura", "memoria", "buffer_cheio", "pausada"]


def section(marker, nxt):
    start = INO.index(marker)
    return INO[start:INO.index(nxt, start + len(marker))]


def parsed_fields():
    """Índices que parseHS() lê de cada linha: {nome: [(linha, índice), ...]}."""
    body = section("static void parseHS(", "\n}\n")
    out = {}
    for name, expr in re.findall(r"hs\.(\w+)\s*=\s*(.+?);", body):
        out[name] = [(int(l), int(i)) for l, i in re.findall(r"field\(seg\[(\d)\],\s*(\d+)\)", expr)]
    return out


class PrinterStatusContract(unittest.TestCase):
    def test_hs_field_map(self):
        f = parsed_fields()
        self.assertEqual(f["paperOut"], [(0, 1)])
        self.assertEqual(f["paused"], [(0, 2)])
        self.assertEqual(f["headUp"], [(1, 2)])
        # ribbon só conta no modo transferência térmica (q = 1)
        self.assertEqual(f["ribbonOut"], [(1, 3), (1, 4)])
        self.assertEqual(f["tempOut"], [(0, 10), (0, 11)])

    def test_real_capture_reads_as_ribbon_out(self):
        a, b = (s.split(",") for s in HS_SEM_RIBBON)
        self.assertEqual((b[3], b[4]), ("1", "1"))      # ribbon acabou, modo com ribbon
        self.assertEqual(a[2], "1")                     # e por isso pausada

    def test_cause_comes_before_paused(self):
        body = section("static const char* printerState() {", "\n}\n")
        order = [body.index('"%s"' % s) for s in STATES]
        self.assertEqual(order, sorted(order), "a causa física deve vir antes de 'pausada'")

    def test_every_state_has_problem_help_and_badge(self):
        for st in STATES + ["desconectada"]:
            self.assertRegex(INO, r'\{ "%s", "[^"]+",\s*\n?\s*"' % st, st)
            self.assertIn("%s:[" % st, PAGES, "selo do cabeçalho para " + st)

    def test_status_api_and_mqtt_carry_help(self):
        for marker, nxt in [("static void handleStatus() {", "\n}\n"), ("static void publishStatus() {", "\n}\n")]:
            body = section(marker, nxt)
            self.assertIn('\\"ajuda\\"', body, marker)
            self.assertIn("printerStateHelp(st)", body, marker)

    def test_resume_is_open_but_guarded(self):
        body = section("static void handleResume() {", "\n}\n")
        self.assertNotIn("requireAuth()", body)
        self.assertLess(body.index("printerNeedsHands(st)"), body.index("usbPrinterSend(cmd"))
        self.assertIn('server.on("/api/resume",    HTTP_POST, handleResume)', INO)

    def test_queue_pull_refuses_printer_needing_hands(self):
        body = section("static void handleQueuePull() {", "\n}\n")
        self.assertLess(body.index("printerNeedsHands(st)"), body.index("wmsQueueStart"))

    def test_page_shows_help_and_resume_only_when_paused(self):
        self.assertIn('id="sthelp"', PAGES)
        self.assertIn("$('resume').hidden=j.printer!=='pausada'", PAGES)


if __name__ == "__main__":
    unittest.main()

"""Source-level safety guards for the manual queue protocol.

These are not a substitute for compiling against ESP32 core 3.0.5 or for
printing a real label on the Zebra ZD220.
"""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
INO = (ROOT / "etiqueta_zd220" / "etiqueta_zd220.ino").read_text(encoding="utf-8")
WORKER = (ROOT / "etiqueta_zd220" / "wms_queue.cpp").read_text(encoding="utf-8")
SERVER = (ROOT.parents[0] / "cmd" / "server" / "print_queue.go").read_text(encoding="utf-8")


def function_section(source, marker, next_marker):
    first = source.index(marker)
    last = source.index(next_marker, first + len(marker))
    return source[first:last]


class ManualQueueSafetyContract(unittest.TestCase):
    def test_local_trigger_requires_basic_auth_before_print(self):
        handler = function_section(INO, "static void handleQueuePull() {", "static void setupWeb()")
        self.assertIn("if (!requireAuth()) return;", handler)
        self.assertLess(handler.index("if (!requireAuth()) return;"), handler.index("wmsQueueStart"))
        self.assertIn('if (cfg.adminPass == "etiqueta")', handler)
        self.assertIn('server.on("/api/queue/pull", HTTP_POST, handleQueuePull)', INO)

    def test_local_queue_status_is_authenticated(self):
        handler = function_section(INO, "static void handleQueueStatus() {", "static void handleQueuePull()")
        self.assertIn("if (!requireAuth()) return;", handler)
        self.assertLess(handler.index("if (!requireAuth()) return;"), handler.index("wmsQueueStatus"))

    def test_no_automatic_render_polling(self):
        loop = INO[INO.index("void loop() {"):]
        self.assertNotIn("wmsQueueStart(", loop)
        self.assertNotIn("http.POST(", loop)
        self.assertIn("xTaskCreatePinnedToCore(queueWorker", WORKER)

    def test_https_certificate_verification(self):
        self.assertIn("client.setCACert(WMS_ROOT_CA)", WORKER)
        self.assertNotIn("setInsecure(", WORKER)
        self.assertIn('baseURL.startsWith("https://")', WORKER)

    def test_read_only_warmup_precedes_claim_and_fits_core_timeout(self):
        config = function_section(WORKER, "static void configureHTTP", "static bool warmUpWMS")
        timeout = config.split("http.setTimeout(", 1)[1].split(")", 1)[0]
        self.assertLessEqual(int(timeout), 65535)
        warmup = function_section(WORKER, "static bool warmUpWMS", "static int sendResult")
        self.assertIn('url+"/health"', warmup)
        self.assertIn("http.GET()", warmup)
        self.assertNotIn("http.POST(", warmup)
        worker = function_section(WORKER, "static void queueWorker", "bool wmsQueueStart")
        self.assertLess(worker.index("warmUpWMS(cloudURL)"), worker.index("/api/print/jobs/claim"))

    def test_persist_before_usb_and_reconcile_after_reboot(self):
        self.assertIn('prefs.putString("state","CLAIMED")', WORKER)
        self.assertLess(WORKER.index('prefs.putString("state","CLAIMED")'), WORKER.index("usbPrinterSend((const uint8_t*)zpl"))
        self.assertIn('state=="CLAIMED"?"UNCERTAIN":state', WORKER)
        self.assertIn('String outcome=sent?"SENT":(attempted?"UNCERTAIN":"FAILED")', WORKER)
        self.assertIn("if(!finishPending(prefs))", WORKER)
        self.assertIn("if(previous==\"CLAIMED\"||previous==\"UNCERTAIN\")", WORKER)

    def test_server_requires_explicit_retry_and_accepts_late_ack(self):
        self.assertIn("FOR UPDATE SKIP LOCKED", SERVER)
        self.assertIn("interval '5 minutes'", SERVER)
        self.assertIn('status IN (\'CLAIMED\',\'UNCERTAIN\') OR status=$1', SERVER)
        self.assertIn("claim_token_hash=NULL", SERVER)
        self.assertIn('status IN (\'UNCERTAIN\',\'FAILED\')', SERVER)
        self.assertIn('/api/print/jobs/{id}/uncertain', SERVER)

    def test_pending_confirmation_cleared_only_on_success_or_final_answer(self):
        final = function_section(WORKER, "static bool finalAnswer", "static void clearPending")
        self.assertIn("return code==400||code==401||code==409;", final)
        finish = function_section(WORKER, "static bool finishPending", "static void queueWorker")
        after_send = finish[finish.index("int code=sendResult("):]
        self.assertIn("if(code==200){clearPending(prefs);return true;}", after_send)
        final_branch_end = after_send.index("if(finalAnswer(code)){")
        retry_tail = after_send[after_send.index("}", after_send.index("return false;", final_branch_end)) + 1:]
        # Network errors, 5xx, 403 and 429 must keep the record for another try.
        self.assertNotIn("clearPending", retry_tail)
        self.assertIn("return false;", retry_tail)

    def test_unconfirmable_local_record_does_not_block_forever(self):
        finish = function_section(WORKER, "static bool finishPending", "static void queueWorker")
        invalid = finish[:finish.index("int code=sendResult(")]
        self.assertIn("claim.length()<32", invalid)
        self.assertIn("clearPending(prefs);", invalid)

    def test_discard_is_authenticated_and_never_runs_with_the_worker(self):
        handler = function_section(INO, "static void handleQueueDiscard() {", "static void setupWeb()")
        self.assertLess(handler.index("if (!requireAuth()) return;"), handler.index("wmsQueueDiscardPending"))
        self.assertLess(handler.index("wmsQueueBusy()"), handler.index("wmsQueueDiscardPending"))
        self.assertIn('server.on("/api/queue/discard", HTTP_POST, handleQueueDiscard)', INO)
        discard = function_section(WORKER, "int wmsQueueDiscardPending()", "bool wmsQueueStart")
        self.assertIn("if(queueActive)return -1;", discard)
        self.assertIn('prefs.begin("wms-ack",false)', discard)

    def test_server_answers_401_only_for_unknown_or_revoked_device(self):
        auth = function_section(SERVER, "func (a *app) requirePrinter", "func printerID")
        self.assertLess(auth.index("errors.Is(err, sql.ErrNoRows)"), auth.index("writeJSONError(w, 401, \"Impressora não autorizada.\")\n\t\t\treturn\n\t\t}\n\t\tif err != nil"))
        self.assertIn("writeJSONError(w, 503,", auth)


if __name__ == "__main__":
    unittest.main()

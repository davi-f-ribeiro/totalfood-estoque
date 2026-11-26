"""Source-level guards for keeping the saved Wi-Fi across reboots.

Com wm.setHostname(), o autoConnect() do WiFiManager 2.0.17 desliga o Wi-Fi e o
religa com persistent(false); no core esp32 3.x o driver passa a gravar só na RAM,
e a rede escolhida no portal se perdia no reinício seguinte.
"""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
INO = (ROOT / "etiqueta_zd220" / "etiqueta_zd220.ino").read_text(encoding="utf-8")


def section(marker, nxt="\n}\n"):
    start = INO.index(marker)
    return INO[start:INO.index(nxt, start + len(marker))]


class WifiPersistenceContract(unittest.TestCase):
    def test_wifimanager_hostname_is_not_used(self):
        self.assertNotIn("wm.setHostname(", INO.replace("// Sem wm.setHostname()", ""))
        body = section("static void setupWifi() {")
        self.assertLess(body.index("WiFi.setHostname("), body.index("WiFi.mode(WIFI_STA)"))

    def test_saved_network_is_tried_before_portal(self):
        body = section("static void setupWifi() {")
        self.assertLess(body.index("connectSavedWifi("), body.index("wm.autoConnect("))

    def test_network_is_remembered_before_any_restart_after_portal(self):
        body = section("static void setupWifi() {")
        after = body[body.index("wm.autoConnect("):]
        self.assertLess(after.index("rememberWifi();"), after.index("if (portalSaved)"))
        self.assertIn("rememberWifi();", section("static void watchWifi() {"))

    def test_wifi_reset_also_clears_own_copy(self):
        body = section("static void handleWifiReset() {")
        self.assertIn('prefs.remove("wifi_ssid")', body)
        self.assertIn('prefs.remove("wifi_pass")', body)
        self.assertIn("requireAuth()", body)


if __name__ == "__main__":
    unittest.main()

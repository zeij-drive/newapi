import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parents[1]))

from core import (  # noqa: E402
    gptstatus_cooldown_remaining,
    parse_gptstatus_command,
    parse_sapi_command,
    parse_status_html,
    sign_request,
    valid_email,
    verify_signed_request,
)


class CoreTests(unittest.TestCase):
    def test_commands_and_email_validation(self):
        self.assertEqual(parse_sapi_command("/sapi"), ("help", []))
        self.assertEqual(parse_sapi_command("sapi"), ("help", []))
        self.assertEqual(parse_sapi_command("／sapi"), ("help", []))
        self.assertEqual(parse_sapi_command("<@bot> /sapi 邀请码 user@example.com"), ("invite", ["user@example.com"]))
        self.assertEqual(parse_sapi_command("/sapi 倍率通知 开启"), ("invalid", ["倍率通知", "开启"]))
        self.assertEqual(parse_sapi_command("/sapi unknown"), ("invalid", ["unknown"]))
        self.assertEqual(parse_gptstatus_command("/gptstatus"), "query")
        self.assertEqual(parse_gptstatus_command("gptstatus"), "query")
        self.assertEqual(parse_gptstatus_command("／gptstatus"), "query")
        self.assertEqual(parse_gptstatus_command("/gptstatus auto on"), "invalid")
        self.assertTrue(valid_email("person@example.com"))
        self.assertFalse(valid_email("person@example.com extra"))
        self.assertFalse(valid_email("person@example"))

    def test_gptstatus_cooldown_remaining(self):
        self.assertEqual(gptstatus_cooldown_remaining(None, 100), 0)
        self.assertEqual(gptstatus_cooldown_remaining(100, 100), 180)
        self.assertEqual(gptstatus_cooldown_remaining(100, 101.2), 179)
        self.assertEqual(gptstatus_cooldown_remaining(100, 280), 0)

    def test_signed_request_uses_exact_canonical_form(self):
        secret = "s" * 32
        body = b'{"groups":[]}'
        signature = sign_request(secret, "POST", "/callback", "1700000000", "a" * 32, body)
        self.assertTrue(
            verify_signed_request(
                secret,
                "POST",
                "/callback",
                "1700000000",
                "a" * 32,
                signature,
                body,
                now=1700000000,
            )
        )
        self.assertFalse(
            verify_signed_request(
                secret,
                "POST",
                "/callback",
                "1700000000",
                "a" * 32,
                signature[:-1] + "0",
                body,
                now=1700000000,
            )
        )

    def test_status_html_parser(self):
        source = """
        <section data-testid="heads-up">
          <svg class="text-icon-operational"></svg> All Systems Operational
        </section>
        <section data-testid="system-status">
          <div><span class="text-icon-degraded-performance"></span><h3>GPT-5</h3></div>
          <div><span class="text-icon-operational"></span><h3>API</h3></div>
        </section>
        <footer data-testid="status-page-footer"></footer>
        """
        snapshot = parse_status_html(source)
        self.assertEqual(snapshot.indicator, "operational")
        self.assertEqual(snapshot.description, "All Systems Operational")
        self.assertEqual(len(snapshot.components), 2)
        self.assertIn("status.openai.com", snapshot.text())

    def test_status_html_parser_ignores_css_and_reads_current_status_markup(self):
        source = """
        <html><head><style>
          :root { --sp-maintenance-surface-primary: 219 233 254; }
          .text-icon-under-maintenance { color: blue; }
        </style></head><body>
          <div data-testid="heads-up" class="Pj3ila_default">
            <div><svg class="text-icon-full-outage"></svg>We're currently experiencing issues</div>
            <div>Elevated error rates with some U.S. business Mastercards</div>
          </div>
          <div data-testid="system-status">
            <div><svg class="text-icon-operational"></svg><h3>APIs</h3></div>
          </div>
        </body></html>
        """
        snapshot = parse_status_html(source)
        self.assertEqual(snapshot.indicator, "critical")
        self.assertEqual(
            snapshot.description,
            "We're currently experiencing issues: Elevated error rates with some U.S. business Mastercards",
        )
        self.assertNotIn(":root", snapshot.description)
        self.assertEqual(snapshot.components[0].name, "APIs")

    def test_status_html_parser_without_banner_does_not_infer_from_css(self):
        source = """
        <style>:root{--sp-maintenance-surface-primary:1}.text-icon-under-maintenance{}</style>
        <div data-testid="system-status">
          <h2>System status</h2>
          <div><svg class="text-icon-operational"></svg><h3>API</h3></div>
        </div>
        """
        snapshot = parse_status_html(source)
        self.assertEqual(snapshot.indicator, "operational")
        self.assertNotIn(":root", snapshot.description)

    def test_status_html_parser_handles_updated_page_markup_without_test_ids(self):
        incident = """
        <html><head><style>.text-icon-under-maintenance{}</style></head><body>
          <h1>OpenAI Status</h1>
          <div><svg class="text-icon-full-outage"></svg>
            <h2>We are experiencing a service issue</h2>
            <p>Elevated error rates for some users</p>
          </div>
        </body></html>
        """
        snapshot = parse_status_html(incident)
        self.assertEqual(snapshot.indicator, "critical")

        operational = parse_status_html(
            "<html><body><h1>OpenAI Status</h1><div>All systems operational</div></body></html>"
        )
        self.assertEqual(operational.indicator, "operational")
        self.assertEqual(operational.description, "All systems operational")

        with self.assertRaisesRegex(ValueError, "no visible status indicator"):
            parse_status_html(
                "<html><head><style>.text-icon-full-outage{}</style></head><body><h1>OpenAI Status</h1></body></html>"
            )


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Static safety checks for the production canonical-host routing workflow."""

from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = (ROOT / ".github" / "workflows" / "restore-canonical-host-routing.yml").read_text(
    encoding="utf-8"
)


class CanonicalHostRoutingGuardsTest(unittest.TestCase):
    def test_each_destructive_handoff_has_separate_default_off_approval(self):
        for name in (
            "confirm_canonical_host_routing:",
            "confirm_legacy_api_domain_transfer:",
            "confirm_legacy_webfinger_route_transfer:",
        ):
            self.assertIn(name, WORKFLOW)
        self.assertIn("CONFIRM_LEGACY_WEBFINGER_ROUTE_TRANSFER: ${{ inputs.confirm_legacy_webfinger_route_transfer }}", WORKFLOW)
        self.assertIn("default: false", WORKFLOW)

    def test_webfinger_preflight_requires_exact_legacy_owner_and_approval(self):
        self.assertIn('select(.pattern=="testagram.site/.well-known/webfinger*")', WORKFLOW)
        self.assertIn('[[ "$legacy_script" == "testagram-web" && -n "$legacy_route_id" ]]', WORKFLOW)
        self.assertIn('[[ "$CONFIRM_LEGACY_WEBFINGER_ROUTE_TRANSFER" == "true" ]]', WORKFLOW)
        self.assertIn("after staging acceptance", WORKFLOW)
        self.assertIn("refusing stale approval", WORKFLOW)

    def test_webfinger_delete_rechecks_exact_route_identity(self):
        self.assertIn('route_id="${{ steps.preflight.outputs.legacy_webfinger_route_id }}"', WORKFLOW)
        self.assertIn('.id == $id and .pattern == "testagram.site/.well-known/webfinger*" and .script == "testagram-web"', WORKFLOW)
        self.assertIn('DELETE "$API/zones/$zone_id/workers/routes/$route_id"', WORKFLOW)
        self.assertIn("Verified the stale WebFinger-to-Flutter route is removed", WORKFLOW)

    def test_failed_apex_verification_restores_only_the_exact_legacy_route(self):
        self.assertIn("Restore the exact legacy WebFinger route if apex verification fails", WORKFLOW)
        self.assertIn("steps.verify_gateway.outcome != 'success'", WORKFLOW)
        self.assertIn('.[0].script == "testagram-web"', WORKFLOW)
        self.assertIn('POST "$API/zones/$zone_id/workers/routes"', WORKFLOW)
        self.assertIn('{"pattern":"testagram.site/.well-known/webfinger*","script":"testagram-web"}', WORKFLOW)
        self.assertIn("Restored and verified the exact legacy WebFinger-to-testagram-web route", WORKFLOW)

    def test_workflow_does_not_delete_other_routes_or_dns(self):
        self.assertNotIn('DELETE "$API/zones/$zone_id/workers/routes/"', WORKFLOW)
        self.assertNotIn('DELETE "$API/zones/$zone_id/dns_records/"', WORKFLOW)


if __name__ == "__main__":
    unittest.main()

import unittest

from public_hosting_redirect import safe_redirect


class PublicHostingRedirectTests(unittest.TestCase):
    current = "https://cloud.example.com/"
    host = "cloud.example.com"

    def test_relative_redirect_is_normalized(self):
        self.assertEqual(
            safe_redirect(self.current, "/organizations", self.host),
            "https://cloud.example.com/organizations",
        )

    def test_same_host_https_and_idna_equivalent_redirects_are_allowed(self):
        self.assertEqual(
            safe_redirect(self.current, "https://CLOUD.example.com:443/organizations", self.host),
            "https://cloud.example.com/organizations",
        )

    def test_rejects_downgrade_cross_host_ip_userinfo_and_bad_forms(self):
        for location in (
            "http://cloud.example.com/organizations",
            "https://evil.example.com/",
            "https://8.8.8.8/",
            "https://user@cloud.example.com/",
            "//cloud.example.com/organizations",
            "https://cloud.example.com:444/organizations",
            "https://cloud.example.com:0443/organizations",
            "https:////evil.example/organizations",
            "https://cloud.example.com/%zz",
            "https://cloud.example.com/%5c@evil.example/",
        ):
            with self.subTest(location=location), self.assertRaises(ValueError):
                safe_redirect(self.current, location, self.host)

    def test_relative_redirect_uses_current_origin_path(self):
        self.assertEqual(
            safe_redirect("https://cloud.example.com/account/", "organizations", self.host),
            "https://cloud.example.com/account/organizations",
        )


if __name__ == "__main__":
    unittest.main()

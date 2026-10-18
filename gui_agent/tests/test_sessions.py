"""Session startup policy tests that do not launch a browser."""
import unittest

from operators.sessions import browser_proxy_from_env


class BrowserProxyTests(unittest.TestCase):
    def test_prefers_explicit_browser_proxy_and_preserves_auth_privately(self):
        proxy = browser_proxy_from_env({
            "BROWSER_PROXY_SERVER": "http://user:p%40ss@proxy.example:8080",
            "HTTPS_PROXY": "http://ignored.example:9000",
            "BROWSER_PROXY_BYPASS": "localhost,127.0.0.1",
        })
        self.assertEqual(proxy, {
            "server": "http://proxy.example:8080",
            "username": "user",
            "password": "p@ss",
            "bypass": "localhost,127.0.0.1",
        })

    def test_uses_standard_proxy_and_no_proxy(self):
        self.assertEqual(browser_proxy_from_env({
            "HTTPS_PROXY": "http://proxy.example:3128",
            "NO_PROXY": "localhost,.internal",
        }), {
            "server": "http://proxy.example:3128",
            "bypass": "localhost,.internal",
        })

    def test_empty_environment_disables_proxy(self):
        self.assertIsNone(browser_proxy_from_env({}))

    def test_maps_host_loopback_proxy_to_docker_gateway(self):
        self.assertEqual(browser_proxy_from_env({
            "HTTPS_PROXY": "http://127.0.0.1:7877",
        }), {"server": "http://host.docker.internal:7877"})

    def test_invalid_explicit_proxy_fails_startup(self):
        with self.assertRaises(ValueError):
            browser_proxy_from_env({"BROWSER_PROXY_SERVER": "ftp://proxy.example"})

"""Tests for relay config loading."""
import os
import tempfile
import pytest
from relay.config import load_destinations


def _write_config(content):
    f = tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False)
    f.write(content)
    f.close()
    return f.name


class TestLoadDestinations:

    def test_loads_destinations(self):
        path = _write_config("""
destinations:
  - name: test-hook
    url: https://example.com/hook
    headers:
      X-Custom: value
""")
        try:
            dests = load_destinations(path)
            assert len(dests) == 1
            assert dests[0]["name"] == "test-hook"
            assert dests[0]["url"] == "https://example.com/hook"
            assert dests[0]["headers"]["X-Custom"] == "value"
        finally:
            os.unlink(path)

    def test_empty_destinations(self):
        path = _write_config("destinations: []")
        try:
            dests = load_destinations(path)
            assert dests == []
        finally:
            os.unlink(path)

    def test_missing_destinations_key(self):
        path = _write_config("other_key: true")
        try:
            dests = load_destinations(path)
            assert dests == []
        finally:
            os.unlink(path)

    def test_auth_env_overrides_url(self, monkeypatch):
        monkeypatch.setenv("TEST_SECRET_URL", "https://secret.example.com/hook")
        path = _write_config("""
destinations:
  - name: secret-hook
    url: https://placeholder.example.com
    auth_env: TEST_SECRET_URL
""")
        try:
            dests = load_destinations(path)
            assert dests[0]["url"] == "https://secret.example.com/hook"
        finally:
            os.unlink(path)

    def test_auth_env_missing_keeps_original_url(self):
        path = _write_config("""
destinations:
  - name: fallback
    url: https://fallback.example.com
    auth_env: NONEXISTENT_VAR_12345
""")
        try:
            dests = load_destinations(path)
            assert dests[0]["url"] == "https://fallback.example.com"
        finally:
            os.unlink(path)

    def test_multiple_destinations(self):
        path = _write_config("""
destinations:
  - name: first
    url: https://first.example.com
  - name: second
    url: https://second.example.com
""")
        try:
            dests = load_destinations(path)
            assert len(dests) == 2
            assert dests[0]["name"] == "first"
            assert dests[1]["name"] == "second"
        finally:
            os.unlink(path)

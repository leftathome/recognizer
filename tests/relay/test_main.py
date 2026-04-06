"""Integration tests for the notification relay HTTP server."""
import json
import threading
from http.server import HTTPServer
import urllib.request
import urllib.error
import pytest
from relay.main import RelayHandler


@pytest.fixture()
def relay_server(tmp_path):
    RelayHandler.destinations = []
    server = HTTPServer(("127.0.0.1", 0), RelayHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever)
    thread.daemon = True
    thread.start()
    yield f"http://127.0.0.1:{port}"
    server.shutdown()


VALID_EVENT = json.dumps({
    "schema_version": "1.0",
    "source": "optical-ripper",
    "event_type": "disc-extraction-complete",
    "timestamp": "2026-04-04T18:30:00Z",
    "output_path": "/volume1/incoming/video/Test (2024)/",
    "media_type": "video/bluray",
    "metadata": {
        "title": "Test",
        "year": 2024,
        "source_device": "pioneer-bdr-xs07uhd",
        "disc_label": "TEST",
        "page_count": None,
        "session_id": None,
    },
    "node_name": "k8s-node-02",
}).encode()


class TestRelayEndpoints:

    def test_post_valid_event_returns_202(self, relay_server):
        req = urllib.request.Request(
            f"{relay_server}/event",
            data=VALID_EVENT,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req) as resp:
            assert resp.status == 202
            body = json.loads(resp.read())
            assert body["accepted"] is True

    def test_post_invalid_json_returns_400(self, relay_server):
        req = urllib.request.Request(
            f"{relay_server}/event",
            data=b"not json",
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == 400

    def test_post_invalid_event_returns_400(self, relay_server):
        bad_event = json.dumps({"bad": "event"}).encode()
        req = urllib.request.Request(
            f"{relay_server}/event",
            data=bad_event,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == 400

    def test_healthz_returns_200(self, relay_server):
        req = urllib.request.Request(f"{relay_server}/healthz", method="GET")
        with urllib.request.urlopen(req) as resp:
            assert resp.status == 200
            body = json.loads(resp.read())
            assert body["status"] == "ok"

    def test_unknown_path_returns_404(self, relay_server):
        req = urllib.request.Request(
            f"{relay_server}/unknown",
            data=VALID_EVENT,
            method="POST",
        )
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == 404

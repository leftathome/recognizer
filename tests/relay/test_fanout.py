"""Tests for relay fan-out logic."""
import json
from http.server import HTTPServer, BaseHTTPRequestHandler
import threading
import pytest
from relay.fanout import fan_out, DeliveryResult


class _RecordingHandler(BaseHTTPRequestHandler):
    """HTTP handler that records requests and returns a configurable status."""
    received = []
    response_code = 200

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        _RecordingHandler.received.append({
            "path": self.path,
            "body": json.loads(body),
            "headers": dict(self.headers),
        })
        self.send_response(_RecordingHandler.response_code)
        self.end_headers()

    def log_message(self, format, *args):
        pass  # suppress stderr output


@pytest.fixture()
def mock_server():
    _RecordingHandler.received = []
    _RecordingHandler.response_code = 200
    server = HTTPServer(("127.0.0.1", 0), _RecordingHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever)
    thread.daemon = True
    thread.start()
    yield f"http://127.0.0.1:{port}"
    server.shutdown()


SAMPLE_EVENT = {
    "schema_version": "1.0",
    "source": "optical-ripper",
    "event_type": "disc-extraction-complete",
    "timestamp": "2026-04-04T18:30:00Z",
    "output_path": "/volume1/incoming/video/Test (2024)/",
    "media_type": "video/bluray",
    "metadata": {"source_device": "pioneer-bdr-xs07uhd"},
    "node_name": "k8s-node-02",
}


class TestFanOut:

    def test_single_destination_success(self, mock_server):
        dests = [{"name": "test", "url": f"{mock_server}/event", "headers": {}}]
        results = fan_out(SAMPLE_EVENT, dests)
        assert len(results) == 1
        assert results[0].success is True
        assert results[0].status_code == 200
        assert len(_RecordingHandler.received) == 1
        assert _RecordingHandler.received[0]["body"]["source"] == "optical-ripper"

    def test_multiple_destinations(self, mock_server):
        dests = [
            {"name": "first", "url": f"{mock_server}/a", "headers": {}},
            {"name": "second", "url": f"{mock_server}/b", "headers": {}},
        ]
        results = fan_out(SAMPLE_EVENT, dests)
        assert len(results) == 2
        assert all(r.success for r in results)
        assert len(_RecordingHandler.received) == 2

    def test_empty_destinations(self):
        results = fan_out(SAMPLE_EVENT, [])
        assert results == []

    def test_empty_url_fails(self):
        dests = [{"name": "bad", "url": "", "headers": {}}]
        results = fan_out(SAMPLE_EVENT, dests)
        assert len(results) == 1
        assert results[0].success is False
        assert "empty URL" in results[0].error

    def test_unreachable_destination(self):
        dests = [{"name": "dead", "url": "http://127.0.0.1:1/nope", "headers": {}}]
        results = fan_out(SAMPLE_EVENT, dests, timeout=1)
        assert len(results) == 1
        assert results[0].success is False

    def test_custom_headers_sent(self, mock_server):
        dests = [{"name": "custom", "url": f"{mock_server}/event", "headers": {"X-Test": "hello"}}]
        fan_out(SAMPLE_EVENT, dests)
        assert _RecordingHandler.received[0]["headers"]["X-Test"] == "hello"

    def test_content_type_always_json(self, mock_server):
        dests = [{"name": "json", "url": f"{mock_server}/event", "headers": {}}]
        fan_out(SAMPLE_EVENT, dests)
        assert _RecordingHandler.received[0]["headers"]["Content-Type"] == "application/json"

    def test_partial_failure(self, mock_server):
        dests = [
            {"name": "ok", "url": f"{mock_server}/event", "headers": {}},
            {"name": "dead", "url": "http://127.0.0.1:1/nope", "headers": {}},
        ]
        results = fan_out(SAMPLE_EVENT, dests, timeout=1)
        assert results[0].success is True
        assert results[1].success is False

"""Integration tests for the notification relay HTTP server."""
import json
import os
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

from relay.destination import Destination
from relay.main import RelayHandler, RelayServer


def _wait_until(predicate, timeout=2.0, interval=0.02):
    """Poll predicate() until it's truthy or timeout elapses.

    Delivery now happens on background workers, so tests that used to
    assert immediate synchronous delivery instead poll for it here, with
    generous margins.
    """
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(interval)
    return predicate()


@pytest.fixture()
def make_relay_server(tmp_path):
    """Factory fixture: build+start a RelayServer with per-test params.

    Each call gets its own RelayServer instance (own queue, own worker
    pool), so tests never share state through module globals.
    """
    servers = []

    def _make(
        destinations=None,
        dead_letter_dir=None,
        queue_max=None,
        worker_count=None,
        max_attempts=None,
        retry_delays=None,
        deadletter_max=None,
    ):
        server = RelayServer(
            ("127.0.0.1", 0),
            RelayHandler,
            destinations=destinations if destinations is not None else [],
            dead_letter_dir=dead_letter_dir or str(tmp_path / "dead-letter"),
            queue_max=queue_max,
            worker_count=worker_count,
            max_attempts=3 if max_attempts is None else max_attempts,
            retry_delays=retry_delays,
            deadletter_max=deadletter_max,
        )
        server.start_workers()
        thread = threading.Thread(target=server.serve_forever)
        thread.daemon = True
        thread.start()
        servers.append(server)
        port = server.server_address[1]
        return server, f"http://127.0.0.1:{port}"

    yield _make

    for server in servers:
        server.shutdown()
        server.server_close()


@pytest.fixture()
def relay_server(make_relay_server):
    """Plain relay server with no fan-out destinations configured."""
    _, base_url = make_relay_server()
    return base_url


class _RecordingDestination(BaseHTTPRequestHandler):
    """Mock fan-out destination that records received bodies and can
    stall before responding, to simulate a slow/hanging destination."""

    received = []
    delay_seconds = 0
    response_code = 200

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        if self.delay_seconds:
            time.sleep(self.delay_seconds)
        _RecordingDestination.received.append(json.loads(body))
        self.send_response(_RecordingDestination.response_code)
        self.end_headers()

    def log_message(self, format, *args):
        pass


@pytest.fixture()
def mock_destination():
    _RecordingDestination.received = []
    _RecordingDestination.delay_seconds = 0
    _RecordingDestination.response_code = 200
    server = HTTPServer(("127.0.0.1", 0), _RecordingDestination)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever)
    thread.daemon = True
    thread.start()
    yield f"http://127.0.0.1:{port}", _RecordingDestination
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


def _post(base_url, data=VALID_EVENT, headers=None, path="/event"):
    req = urllib.request.Request(
        f"{base_url}{path}",
        data=data,
        headers=headers or {"Content-Type": "application/json"},
        method="POST",
    )
    return urllib.request.urlopen(req)


class TestRelayEndpoints:

    def test_post_valid_event_returns_202(self, relay_server):
        with _post(relay_server) as resp:
            assert resp.status == 202
            body = json.loads(resp.read())
            assert body["accepted"] is True

    def test_post_invalid_json_returns_400(self, relay_server):
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            _post(relay_server, data=b"not json")
        assert exc_info.value.code == 400

    def test_post_invalid_event_returns_400(self, relay_server):
        bad_event = json.dumps({"bad": "event"}).encode()
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            _post(relay_server, data=bad_event)
        assert exc_info.value.code == 400

    def test_healthz_returns_200(self, relay_server):
        req = urllib.request.Request(f"{relay_server}/healthz", method="GET")
        with urllib.request.urlopen(req) as resp:
            assert resp.status == 200
            body = json.loads(resp.read())
            assert body["status"] == "ok"

    def test_unknown_path_returns_404(self, relay_server):
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            _post(relay_server, path="/unknown")
        assert exc_info.value.code == 404


class TestAsyncDelivery:
    """The request handler no longer delivers synchronously -- it enqueues
    and returns 202 immediately, and a worker pool drains the queue."""

    def test_event_is_delivered_to_destination_in_background(
        self, make_relay_server, mock_destination
    ):
        dest_url, recorder = mock_destination
        _, base_url = make_relay_server(
            destinations=[Destination(name="test", url=f"{dest_url}/event")],
        )

        with _post(base_url) as resp:
            assert resp.status == 202
            body = json.loads(resp.read())
            assert body == {"accepted": True, "queued": True}

        assert _wait_until(lambda: len(recorder.received) == 1)
        assert recorder.received[0]["source"] == "optical-ripper"

    def test_slow_destination_does_not_block_next_request(
        self, make_relay_server, mock_destination
    ):
        """The whole point of B3: a slow/hanging destination must not
        wedge the relay for a second, unrelated request."""
        dest_url, recorder = mock_destination
        _RecordingDestination.delay_seconds = 2.0  # generous stall

        _, base_url = make_relay_server(
            destinations=[Destination(name="slow", url=f"{dest_url}/event")],
            worker_count=1,
        )

        start = time.monotonic()
        with _post(base_url) as resp1:
            assert resp1.status == 202
        first_elapsed = time.monotonic() - start

        start2 = time.monotonic()
        with _post(base_url) as resp2:
            assert resp2.status == 202
        second_elapsed = time.monotonic() - start2

        # Neither accept should wait anywhere near the destination's 2s
        # stall -- generous margin, but well under it.
        assert first_elapsed < 1.0
        assert second_elapsed < 1.0

        # Both events still get delivered once the (single) worker gets
        # to them -- nothing was lost, delivery was just serialized.
        assert _wait_until(lambda: len(recorder.received) == 2, timeout=6.0)

    def test_queue_full_dead_letters_immediately_and_still_202s(
        self, make_relay_server, tmp_path
    ):
        dl_dir = str(tmp_path / "dead-letter")
        _, base_url = make_relay_server(
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            queue_max=1,
            worker_count=0,  # nothing drains -- overflow is deterministic
            dead_letter_dir=dl_dir,
        )

        with _post(base_url) as resp1:
            assert resp1.status == 202
            body1 = json.loads(resp1.read())
        assert body1 == {"accepted": True, "queued": True}

        with _post(base_url) as resp2:
            assert resp2.status == 202
            body2 = json.loads(resp2.read())
        assert body2["accepted"] is True
        assert body2["queued"] is False
        assert body2["dead_lettered"] is True

        files = os.listdir(dl_dir)
        assert len(files) == 1
        with open(os.path.join(dl_dir, files[0])) as f:
            envelope = json.load(f)
        assert "queue full" in envelope["error"]

    def test_concurrent_failed_deliveries_produce_distinct_dead_letters(
        self, make_relay_server, tmp_path
    ):
        """Reproduces the burst scenario directly: several requests that
        all fail delivery around the same instant must not overwrite each
        other's dead-letter file."""
        dl_dir = str(tmp_path / "dead-letter")
        _, base_url = make_relay_server(
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            worker_count=4,
            max_attempts=1,  # fail fast, no backoff sleeps
            dead_letter_dir=dl_dir,
        )

        results = []

        def post():
            with _post(base_url) as resp:
                results.append(resp.status)

        threads = [threading.Thread(target=post) for _ in range(5)]
        for t in threads:
            t.start()
        for t in threads:
            t.join(timeout=5)

        assert results == [202] * 5
        assert _wait_until(lambda: len(os.listdir(dl_dir)) == 5, timeout=5.0)
        assert len(set(os.listdir(dl_dir))) == 5  # no collisions/overwrites

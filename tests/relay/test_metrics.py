"""Tests for relay.metrics and the /metrics HTTP endpoint.

Metric names here are load-bearing: NotificationDeadLetterBacklog
(charts/recognizer/templates/monitoring/prometheusrule.yaml) alerts on
`capture_notification_dead_letter_total`. This file greps the rendered
output for the exact names so a rename anywhere would fail loudly here
before it silently breaks the alert.
"""
import json
import threading
import time
import urllib.error
import urllib.request

import pytest

from relay.destination import Destination
from relay.main import RelayHandler, RelayServer
from relay.metrics import Counter, Gauge, Metrics


EXPECTED_METRIC_NAMES = [
    "capture_notification_events_total",
    "capture_notification_delivery_total",
    "capture_notification_dead_letter_total",
    "capture_notification_queue_depth",
]


def _wait_until(predicate, timeout=2.0, interval=0.02):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(interval)
    return predicate()


class TestCounter:

    def test_seeded_labels_render_at_zero(self):
        c = Counter("test_total", "help", label_values=["a", "b"])
        lines = c.render(label_name="status")
        assert 'test_total{status="a"} 0' in lines
        assert 'test_total{status="b"} 0' in lines

    def test_inc_increments_the_right_label(self):
        c = Counter("test_total", "help", label_values=["a", "b"])
        c.inc("a")
        c.inc("a")
        c.inc("b")
        lines = c.render(label_name="status")
        assert 'test_total{status="a"} 2' in lines
        assert 'test_total{status="b"} 1' in lines

    def test_unlabeled_counter_renders_bare_name(self):
        c = Counter("test_total", "help")
        c.inc()
        c.inc()
        lines = c.render()
        assert "test_total 2" in lines


class TestGauge:

    def test_gauge_reads_value_fn_live(self):
        state = {"n": 0}
        g = Gauge("test_gauge", "help", lambda: state["n"])
        assert "test_gauge 0" in g.render()
        state["n"] = 5
        assert "test_gauge 5" in g.render()


class TestMetricsRender:

    def test_render_contains_all_expected_metric_names(self):
        m = Metrics()
        output = m.render()
        for name in EXPECTED_METRIC_NAMES:
            assert name in output

    def test_render_has_help_and_type_lines(self):
        m = Metrics()
        output = m.render()
        assert "# HELP capture_notification_dead_letter_total" in output
        assert "# TYPE capture_notification_dead_letter_total counter" in output
        assert "# TYPE capture_notification_queue_depth gauge" in output

    def test_event_accepted_and_rejected_move_independently(self):
        m = Metrics()
        m.event_accepted()
        m.event_accepted()
        m.event_rejected()
        output = m.render()
        assert 'capture_notification_events_total{status="accepted"} 2' in output
        assert 'capture_notification_events_total{status="rejected"} 1' in output

    def test_dead_lettered_increments(self):
        m = Metrics()
        assert "capture_notification_dead_letter_total 0" in m.render()
        m.dead_lettered()
        assert "capture_notification_dead_letter_total 1" in m.render()

    def test_queue_depth_reflects_live_callable(self):
        m = Metrics(queue_depth_fn=lambda: 7)
        assert "capture_notification_queue_depth 7" in m.render()


@pytest.fixture()
def make_relay_server(tmp_path):
    servers = []

    def _make(destinations=None, worker_count=None, queue_max=None):
        server = RelayServer(
            ("127.0.0.1", 0),
            RelayHandler,
            destinations=destinations if destinations is not None else [],
            dead_letter_dir=str(tmp_path / "dead-letter"),
            worker_count=worker_count,
            queue_max=queue_max,
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


def _get_metrics(base_url):
    req = urllib.request.Request(f"{base_url}/metrics", method="GET")
    with urllib.request.urlopen(req) as resp:
        return resp.status, resp.headers.get("Content-Type"), resp.read().decode()


def _post_event(base_url, data=VALID_EVENT):
    req = urllib.request.Request(
        f"{base_url}/event",
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    return urllib.request.urlopen(req)


class TestMetricsEndpoint:

    def test_metrics_endpoint_returns_text_plain(self, make_relay_server):
        _, base_url = make_relay_server()
        status, content_type, body = _get_metrics(base_url)
        assert status == 200
        assert content_type.startswith("text/plain")
        assert "version=0.0.4" in content_type

    def test_metrics_contains_all_metric_names_at_startup(self, make_relay_server):
        _, base_url = make_relay_server()
        _, _, body = _get_metrics(base_url)
        for name in EXPECTED_METRIC_NAMES:
            assert name in body

    def test_accepted_event_moves_the_counter(self, make_relay_server):
        _, base_url = make_relay_server()

        _, _, before = _get_metrics(base_url)
        assert 'capture_notification_events_total{status="accepted"} 0' in before

        with _post_event(base_url) as resp:
            assert resp.status == 202

        _, _, after = _get_metrics(base_url)
        assert 'capture_notification_events_total{status="accepted"} 1' in after

    def test_rejected_event_moves_the_counter(self, make_relay_server):
        _, base_url = make_relay_server()

        req = urllib.request.Request(
            f"{base_url}/event", data=b"not json", method="POST"
        )
        try:
            urllib.request.urlopen(req)
        except urllib.error.HTTPError:
            pass

        _, _, after = _get_metrics(base_url)
        assert 'capture_notification_events_total{status="rejected"} 1' in after

    def test_dead_letter_from_queue_full_moves_the_counter(self, make_relay_server):
        _, base_url = make_relay_server(
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            queue_max=1,
            worker_count=0,
        )

        with _post_event(base_url) as resp1:
            assert resp1.status == 202
        with _post_event(base_url) as resp2:
            body2 = json.loads(resp2.read())
        assert body2["dead_lettered"] is True

        _, _, after = _get_metrics(base_url)
        assert "capture_notification_dead_letter_total 1" in after

    def test_delivery_success_and_dead_letter_from_worker_failure(
        self, make_relay_server
    ):
        server, base_url = make_relay_server(
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            worker_count=1,
        )
        server.max_attempts = 1  # fail fast, no backoff sleeps

        with _post_event(base_url) as resp:
            assert resp.status == 202

        def _dead_lettered_once():
            _, _, body = _get_metrics(base_url)
            return "capture_notification_dead_letter_total 1" in body

        assert _wait_until(_dead_lettered_once)

        _, _, after = _get_metrics(base_url)
        assert 'capture_notification_delivery_total{status="failed"} 1' in after

    def test_queue_depth_reflects_pending_events(self, make_relay_server):
        _, base_url = make_relay_server(
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            worker_count=0,  # nothing drains -- depth stays put
            queue_max=10,
        )

        with _post_event(base_url) as resp:
            assert resp.status == 202

        _, _, after = _get_metrics(base_url)
        assert "capture_notification_queue_depth 1" in after

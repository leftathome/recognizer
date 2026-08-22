"""Tests for the dead-letter drain (relay.drain / `python -m relay.drain`)."""
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

from relay.deadletter import write_dead_letter
from relay.destination import Destination
from relay.drain import drain, strip_envelope


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


class _RecordingHandler(BaseHTTPRequestHandler):
    """Mock delivery destination: records bodies, returns a configurable
    status code (200 = success, 503 = always fail)."""
    received = []
    response_code = 200

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        _RecordingHandler.received.append(json.loads(body))
        self.send_response(_RecordingHandler.response_code)
        self.end_headers()

    def log_message(self, format, *args):
        pass


@pytest.fixture()
def mock_destination():
    _RecordingHandler.received = []
    _RecordingHandler.response_code = 200
    server = HTTPServer(("127.0.0.1", 0), _RecordingHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever)
    thread.daemon = True
    thread.start()
    yield f"http://127.0.0.1:{port}", _RecordingHandler
    server.shutdown()


def _dead_letter(tmp_path, event=None, error="connection refused"):
    dl_dir = str(tmp_path / "dead-letter")
    path = write_dead_letter(event or SAMPLE_EVENT, error, dl_dir)
    return dl_dir, path


class TestDrainSuccess:

    def test_success_deletes_file(self, tmp_path, mock_destination):
        dest_url, recorder = mock_destination
        dl_dir, path = _dead_letter(tmp_path)

        summary = drain(
            dead_letter_dir=dl_dir,
            destinations=[Destination(name="test", url=f"{dest_url}/event")],
            max_attempts=1,
        )

        assert not os.path.exists(path)
        assert summary["delivered"] == 1
        assert summary["failed"] == 0
        assert len(recorder.received) == 1


class TestDrainFailure:

    def test_destination_failure_retains_file(self, tmp_path):
        dl_dir, path = _dead_letter(tmp_path)

        summary = drain(
            dead_letter_dir=dl_dir,
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            max_attempts=1,
        )

        assert os.path.exists(path)
        assert summary["delivered"] == 0
        assert summary["failed"] == 1

    def test_partial_failure_across_destinations_retains_file(
        self, tmp_path, mock_destination
    ):
        """Full success requires *every* destination to accept the event --
        one failing destination must not let the file be deleted, or that
        destination silently loses the event forever."""
        dest_url, recorder = mock_destination
        dl_dir, path = _dead_letter(tmp_path)

        summary = drain(
            dead_letter_dir=dl_dir,
            destinations=[
                Destination(name="ok", url=f"{dest_url}/event"),
                Destination(name="dead", url="http://127.0.0.1:1/nope"),
            ],
            max_attempts=1,
        )

        assert os.path.exists(path)
        assert summary["failed"] == 1
        assert summary["delivered"] == 0


class TestDrainAgeOut:

    def test_age_out_deletes_old_file(self, tmp_path):
        dl_dir, path = _dead_letter(tmp_path)
        old_time = time.time() - (20 * 86400)  # 20 days old
        os.utime(path, (old_time, old_time))

        summary = drain(dead_letter_dir=dl_dir, destinations=[], max_age_days=14)

        assert not os.path.exists(path)
        assert summary["aged_out"] == 1
        assert summary["delivered"] == 0
        assert summary["failed"] == 0

    def test_recent_file_is_not_aged_out(self, tmp_path):
        dl_dir, path = _dead_letter(tmp_path)

        summary = drain(
            dead_letter_dir=dl_dir,
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            max_age_days=14,
            max_attempts=1,
        )

        assert os.path.exists(path)  # still present -- delivery failed, not aged out
        assert summary["aged_out"] == 0
        assert summary["failed"] == 1

    def test_negative_max_age_disables_age_out(self, tmp_path):
        dl_dir, path = _dead_letter(tmp_path)
        old_time = time.time() - (365 * 86400)
        os.utime(path, (old_time, old_time))

        summary = drain(
            dead_letter_dir=dl_dir,
            destinations=[Destination(name="nowhere", url="http://127.0.0.1:1/nope")],
            max_age_days=-1,
            max_attempts=1,
        )

        assert summary["aged_out"] == 0
        assert summary["failed"] == 1


class TestDrainBatchCap:

    def test_batch_cap_respected(self, tmp_path, mock_destination):
        dest_url, recorder = mock_destination
        dl_dir = str(tmp_path / "dead-letter")
        for i in range(7):
            write_dead_letter(SAMPLE_EVENT, f"err-{i}", dl_dir)
            time.sleep(0.01)  # keep mtimes distinct/ordered

        summary = drain(
            dead_letter_dir=dl_dir,
            destinations=[Destination(name="test", url=f"{dest_url}/event")],
            batch=3,
            max_attempts=1,
        )

        assert summary["scanned"] == 3
        assert summary["delivered"] == 3
        remaining = os.listdir(dl_dir)
        assert len(remaining) == 4  # 7 - 3 processed

    def test_oldest_files_processed_first(self, tmp_path, mock_destination):
        dest_url, recorder = mock_destination
        dl_dir = str(tmp_path / "dead-letter")
        paths = []
        for i in range(3):
            _, path = _dead_letter(tmp_path, error=f"err-{i}")
            paths.append(path)
            time.sleep(0.01)

        summary = drain(
            dead_letter_dir=dl_dir,
            destinations=[Destination(name="test", url=f"{dest_url}/event")],
            batch=1,
            max_attempts=1,
        )

        assert summary["scanned"] == 1
        # The oldest (first-written) file should be the one gone.
        assert not os.path.exists(paths[0])
        assert os.path.exists(paths[1])
        assert os.path.exists(paths[2])


class TestEnvelopeStripping:

    def test_strip_envelope_returns_original_event(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "boom", dl_dir)
        with open(path) as f:
            envelope = json.load(f)

        event = strip_envelope(envelope)

        assert event == SAMPLE_EVENT
        assert "error" not in event
        assert "dead_lettered_at" not in event
        assert "original_event" not in event

    def test_drain_delivers_stripped_event_not_envelope(self, tmp_path, mock_destination):
        dest_url, recorder = mock_destination
        dl_dir, _ = _dead_letter(tmp_path)

        drain(
            dead_letter_dir=dl_dir,
            destinations=[Destination(name="test", url=f"{dest_url}/event")],
            max_attempts=1,
        )

        assert len(recorder.received) == 1
        delivered = recorder.received[0]
        assert delivered == SAMPLE_EVENT
        assert "original_event" not in delivered
        assert "dead_lettered_at" not in delivered


class TestDrainMisc:

    def test_missing_directory_is_a_noop(self, tmp_path):
        dl_dir = str(tmp_path / "does-not-exist")
        summary = drain(dead_letter_dir=dl_dir, destinations=[])
        assert summary == {"scanned": 0, "delivered": 0, "failed": 0, "aged_out": 0}

    def test_corrupt_file_is_left_in_place(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        os.makedirs(dl_dir)
        bad_path = os.path.join(dl_dir, "20260101T000000Z-deadbeef.json")
        with open(bad_path, "w") as f:
            f.write("not valid json{{{")

        summary = drain(dead_letter_dir=dl_dir, destinations=[])

        assert os.path.exists(bad_path)
        assert summary["failed"] == 1

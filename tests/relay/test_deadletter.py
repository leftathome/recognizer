"""Tests for dead-letter file writing."""
import importlib
import json
import os
import pytest
import relay.deadletter as dl
from relay.deadletter import write_dead_letter


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


class TestDeadLetter:

    def test_writes_file(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "connection refused", dl_dir)
        assert os.path.exists(path)
        assert path.endswith(".json")

    def test_file_is_valid_json(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "timeout", dl_dir)
        with open(path) as f:
            envelope = json.load(f)
        assert "original_event" in envelope
        assert "error" in envelope
        assert "dead_lettered_at" in envelope

    def test_contains_original_event(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "error", dl_dir)
        with open(path) as f:
            envelope = json.load(f)
        assert envelope["original_event"]["source"] == "optical-ripper"
        assert envelope["original_event"]["media_type"] == "video/bluray"

    def test_contains_error_info(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "HTTP 503 Service Unavailable", dl_dir)
        with open(path) as f:
            envelope = json.load(f)
        assert envelope["error"] == "HTTP 503 Service Unavailable"

    def test_creates_directory(self, tmp_path):
        dl_dir = str(tmp_path / "nested" / "dead-letter")
        assert not os.path.exists(dl_dir)
        write_dead_letter(SAMPLE_EVENT, "error", dl_dir)
        assert os.path.isdir(dl_dir)

    def test_atomic_write(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "error", dl_dir)
        # No .tmp file should remain
        tmp_file = path + ".tmp"
        assert not os.path.exists(tmp_file)

    def test_attempts_in_envelope(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "error", dl_dir, attempts=5)
        with open(path) as f:
            envelope = json.load(f)
        assert envelope["attempts"] == 5

    def test_attempts_default(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "error", dl_dir)
        with open(path) as f:
            envelope = json.load(f)
        assert envelope["attempts"] == 3


class TestUniqueFilenames:

    def test_same_timestamp_produces_distinct_files(self, tmp_path, monkeypatch):
        """Two dead-letters written within the same wall-clock second (the
        common case for a burst of concurrent failures) must not collide:
        the uuid4 suffix, not just the second-granularity timestamp, is
        what makes the filename unique."""

        class _FixedDatetime(dl.datetime):
            @classmethod
            def now(cls, tz=None):
                return dl.datetime(2026, 4, 4, 18, 30, 0, tzinfo=tz)

        monkeypatch.setattr(dl, "datetime", _FixedDatetime)

        dl_dir = str(tmp_path / "dead-letter")
        path1 = write_dead_letter(SAMPLE_EVENT, "err1", dl_dir)
        path2 = write_dead_letter(SAMPLE_EVENT, "err2", dl_dir)

        assert path1 != path2
        assert os.path.exists(path1)
        assert os.path.exists(path2)
        assert len(os.listdir(dl_dir)) == 2

    def test_filename_still_starts_with_timestamp(self, tmp_path, monkeypatch):
        class _FixedDatetime(dl.datetime):
            @classmethod
            def now(cls, tz=None):
                return dl.datetime(2026, 4, 4, 18, 30, 0, tzinfo=tz)

        monkeypatch.setattr(dl, "datetime", _FixedDatetime)

        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "err", dl_dir)
        basename = os.path.basename(path)
        assert basename.startswith("20260404T183000Z-")
        # timestamp, dash, 32 hex chars, ".json"
        assert basename == f"20260404T183000Z-{basename.split('-', 1)[1]}"
        suffix = basename[len("20260404T183000Z-"):-len(".json")]
        assert len(suffix) == 32
        int(suffix, 16)  # raises ValueError if not hex


class TestDeadLetterCap:

    def test_allows_writes_up_to_the_cap(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        paths = [
            write_dead_letter(SAMPLE_EVENT, "err", dl_dir, max_files=3)
            for _ in range(3)
        ]
        assert all(p is not None for p in paths)
        assert len(os.listdir(dl_dir)) == 3

    def test_drops_once_at_capacity(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        for _ in range(3):
            write_dead_letter(SAMPLE_EVENT, "err", dl_dir, max_files=3)
        before = set(os.listdir(dl_dir))

        result = write_dead_letter(SAMPLE_EVENT, "one-too-many", dl_dir, max_files=3)

        assert result is None
        assert set(os.listdir(dl_dir)) == before  # nothing new written

    def test_zero_cap_drops_immediately(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        result = write_dead_letter(SAMPLE_EVENT, "err", dl_dir, max_files=0)
        assert result is None

    def test_negative_cap_means_unlimited(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        for _ in range(5):
            result = write_dead_letter(SAMPLE_EVENT, "err", dl_dir, max_files=-1)
            assert result is not None
        assert len(os.listdir(dl_dir)) == 5

    def test_default_cap_reads_from_env(self, monkeypatch):
        monkeypatch.setenv("RELAY_DEADLETTER_MAX", "5")
        importlib.reload(dl)
        try:
            assert dl.DEFAULT_DEADLETTER_MAX == 5
        finally:
            monkeypatch.delenv("RELAY_DEADLETTER_MAX", raising=False)
            importlib.reload(dl)
            assert dl.DEFAULT_DEADLETTER_MAX == 10000

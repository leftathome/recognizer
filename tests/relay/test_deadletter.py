"""Tests for dead-letter file writing."""
import json
import os
import pytest
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

    def test_retry_count_in_envelope(self, tmp_path):
        dl_dir = str(tmp_path / "dead-letter")
        path = write_dead_letter(SAMPLE_EVENT, "error", dl_dir)
        with open(path) as f:
            envelope = json.load(f)
        assert envelope["retry_count"] == 3

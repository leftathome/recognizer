"""Tests for the ARM post-rip notification hook."""
import json
import os
import sys

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "images", "optical-ripper", "hooks"))

from post_rip import build_event, post_event, DISC_TYPE_MAP


# -- Schema validation --

SCHEMAS_DIR = os.path.join(os.path.dirname(__file__), "..", "..", "schemas")


@pytest.fixture(scope="module")
def notification_schema():
    from jsonschema import Draft202012Validator
    with open(os.path.join(SCHEMAS_DIR, "notification-event.v1.schema.json")) as f:
        schema = json.load(f)
    return Draft202012Validator(schema)


def _base_env(**overrides):
    env = {
        "ARM_DISCTYPE": "bluray",
        "ARM_TITLE": "Test Movie",
        "ARM_YEAR": "2024",
        "ARM_MEDIA_DIR": "/out/video/Test Movie (2024)",
        "ARM_LABEL": "TEST_MOVIE",
        "ARM_DEVICE_NAME": "pioneer-bdr-xs07uhd",
        "HOSTNAME": "k8s-node-02",
    }
    env.update(overrides)
    return env


class TestBuildEvent:

    def test_bluray_event_validates(self, notification_schema):
        event = build_event(_base_env())
        notification_schema.validate(event)

    def test_uhd_bluray_media_type(self):
        event = build_event(_base_env(ARM_HAS_UHD="true"))
        assert event["media_type"] == "video/uhd-bluray"

    def test_bluray_media_type(self):
        event = build_event(_base_env())
        assert event["media_type"] == "video/bluray"

    def test_dvd_media_type(self):
        event = build_event(_base_env(ARM_DISCTYPE="dvd"))
        assert event["media_type"] == "video/dvd"

    def test_audio_cd_media_type(self):
        event = build_event(_base_env(ARM_DISCTYPE="music"))
        assert event["media_type"] == "audio/cd"

    def test_data_disc_media_type(self):
        event = build_event(_base_env(ARM_DISCTYPE="data"))
        assert event["media_type"] == "data/iso"

    def test_unknown_disc_defaults_to_data(self):
        event = build_event(_base_env(ARM_DISCTYPE="weird"))
        assert event["media_type"] == "data/iso"

    def test_source_is_optical_ripper(self):
        event = build_event(_base_env())
        assert event["source"] == "optical-ripper"
        assert event["event_type"] == "disc-extraction-complete"

    def test_metadata_title_and_year(self):
        event = build_event(_base_env())
        assert event["metadata"]["title"] == "Test Movie"
        assert event["metadata"]["year"] == 2024

    def test_missing_title_is_null(self):
        event = build_event(_base_env(ARM_TITLE=""))
        assert event["metadata"]["title"] is None

    def test_missing_year_is_null(self):
        event = build_event(_base_env(ARM_YEAR=""))
        assert event["metadata"]["year"] is None

    def test_non_numeric_year_is_null(self):
        event = build_event(_base_env(ARM_YEAR="unknown"))
        assert event["metadata"]["year"] is None

    def test_disc_label(self):
        event = build_event(_base_env())
        assert event["metadata"]["disc_label"] == "TEST_MOVIE"

    def test_scan_fields_are_null(self):
        event = build_event(_base_env())
        assert event["metadata"]["page_count"] is None
        assert event["metadata"]["session_id"] is None

    def test_node_name_from_hostname(self):
        event = build_event(_base_env())
        assert event["node_name"] == "k8s-node-02"

    def test_output_path(self):
        event = build_event(_base_env())
        assert event["output_path"] == "/out/video/Test Movie (2024)"

    def test_data_disc_validates(self, notification_schema):
        event = build_event(_base_env(
            ARM_DISCTYPE="data",
            ARM_TITLE="",
            ARM_YEAR="",
            ARM_LABEL="BACKUP_2024",
            ARM_MEDIA_DIR="/out/data/BACKUP_2024_20260404",
        ))
        notification_schema.validate(event)
        assert event["media_type"] == "data/iso"
        assert event["metadata"]["title"] is None
        assert event["metadata"]["year"] is None


class TestPostEvent:

    def test_missing_notify_webhook_raises_error(self):
        """post_event() should raise ValueError when NOTIFY_WEBHOOK is not set."""
        event = build_event(_base_env())
        with pytest.raises(ValueError, match="NOTIFY_WEBHOOK environment variable must be set"):
            post_event(event)

    def test_empty_notify_webhook_raises_error(self):
        """post_event() should raise ValueError when NOTIFY_WEBHOOK is empty."""
        event = build_event(_base_env())
        with pytest.raises(ValueError, match="NOTIFY_WEBHOOK environment variable must be set"):
            post_event(event, relay_url="")

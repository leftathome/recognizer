"""Tests for the ARM post-rip notification hook."""
import json
import os
import sqlite3
import sys

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "images", "optical-ripper", "hooks"))

from post_rip import (
    build_event,
    build_event_from_arm_notify,
    is_completion_notification,
    post_event,
    DISC_TYPE_MAP,
)


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

    def test_node_name_prefers_node_name_over_hostname(self):
        # In-pod HOSTNAME is auto-set to the pod name by the container
        # runtime, not the k8s node -- NODE_NAME (downward-API fieldRef)
        # must win when both are present.
        event = build_event(_base_env(NODE_NAME="worker-3", HOSTNAME="optical-ripper-abcde-xyz"))
        assert event["node_name"] == "worker-3"

    def test_node_name_falls_back_to_unknown(self):
        env = _base_env()
        env.pop("HOSTNAME")
        event = build_event(env)
        assert event["node_name"] == "unknown"

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


# -- ARM's real invocation shape: BASH_SCRIPT called as (title, body) --
#
# Verified against automatic-ripping-machine/automatic-ripping-machine at
# tag 2.23.2 (commit 7c034584466cd3dc167d65ba3f3cde5d555691a9):
#   arm/ripper/utils.py:88-91           bash_notify(): the BASH_SCRIPT exec
#   arm/ripper/arm_ripper.py:170-185    notify_exit(): final video notify
#   arm/ripper/main.py:132              final music notify
#   arm/ripper/main.py:146              final data-disc notify
# ARM sets no ARM_* environment variables for this call -- title/body are
# the only data it hands the script.

class TestIsCompletionNotification:

    def test_video_completion(self):
        # arm_ripper.py:185 -- f"{job.title} {constants.PROCESS_COMPLETE}"
        assert is_completion_notification("Test Movie processing complete.")

    def test_music_completion(self):
        # main.py:132 -- f"Music CD: {job.title} {constants.PROCESS_COMPLETE}"
        assert is_completion_notification("Music CD: Test Album processing complete.")

    def test_data_completion(self):
        # main.py:146 -- f"Data disc: {job.label} copying complete. "
        assert is_completion_notification("Data disc: BACKUP_2024 copying complete. ")

    def test_video_error_is_not_completion(self):
        # arm_ripper.py:180-182 -- the notify_exit() error branch
        assert not is_completion_notification(
            " Test Movie processing completed with errors. Title(s) 1 failed to complete. "
        )

    def test_job_start_is_not_completion(self):
        # utils.py:111-113 -- notify_entry()
        assert not is_completion_notification("Found disc: Test Movie. Disc type is bluray.")

    def test_mid_rip_progress_is_not_completion(self):
        # arm_ripper.py:66 -- rip complete, transcode about to start
        assert not is_completion_notification("Test Movie rip complete. Starting transcode. ")

    def test_duplicate_disc_abort_is_not_completion(self):
        # utils.py:829 -- check_for_dupe_folder()
        assert not is_completion_notification(
            "ARM Detected a duplicate disc. For Test Movie. Duplicate rips are disabled. "
        )

    def test_fatal_error_is_not_completion(self):
        # main.py's run() exception handler
        assert not is_completion_notification(
            "ARM encountered a fatal error processing Test Movie. Check the logs for more details. boom"
        )

    def test_empty_body_is_not_completion(self):
        assert not is_completion_notification("")


@pytest.fixture
def arm_job_db(tmp_path):
    """A minimal stand-in for ARM's sqlite `job` table (see arm/models/job.py;
    table name confirmed from arm/migrations/versions/
    c3a3fa694636_job_config_track_create.py)."""
    db_path = tmp_path / "arm.db"
    conn = sqlite3.connect(db_path)
    conn.execute(
        "CREATE TABLE job (job_id INTEGER PRIMARY KEY, title TEXT, year TEXT, "
        "disctype TEXT, label TEXT, path TEXT)"
    )
    conn.commit()
    conn.close()
    return str(db_path)


def _insert_job(db_path, **row):
    conn = sqlite3.connect(db_path)
    cols = ", ".join(row.keys())
    placeholders = ", ".join("?" for _ in row)
    conn.execute(f"INSERT INTO job ({cols}) VALUES ({placeholders})", list(row.values()))
    conn.commit()
    conn.close()


class TestBuildEventFromArmNotify:

    def _argv(self, title="ARM notification", body="Test Movie processing complete."):
        return ["post_rip.py", title, body]

    def test_non_completion_call_returns_none(self, arm_job_db):
        env = {"ARM_DB_FILE": arm_job_db}
        argv = self._argv(body="Found disc: Test Movie. Disc type is bluray.")
        assert build_event_from_arm_notify(argv, env) is None

    def test_reads_job_metadata_from_db(self, arm_job_db, notification_schema):
        _insert_job(
            arm_job_db,
            title="Test Movie", year="2024", disctype="bluray",
            label="TEST_MOVIE", path="/out/video/Test Movie (2024)",
        )
        env = {"ARM_DB_FILE": arm_job_db, "NODE_NAME": "worker-3"}
        event = build_event_from_arm_notify(self._argv(), env)
        notification_schema.validate(event)
        assert event["metadata"]["title"] == "Test Movie"
        assert event["metadata"]["year"] == 2024
        assert event["media_type"] == "video/bluray"
        assert event["metadata"]["disc_label"] == "TEST_MOVIE"
        assert event["output_path"] == "/out/video/Test Movie (2024)"
        assert event["node_name"] == "worker-3"

    def test_uses_most_recent_job_row(self, arm_job_db):
        _insert_job(arm_job_db, title="Old Movie", year="2020", disctype="dvd",
                    label="OLD", path="/out/video/Old Movie (2020)")
        _insert_job(arm_job_db, title="New Movie", year="2024", disctype="bluray",
                    label="NEW", path="/out/video/New Movie (2024)")
        event = build_event_from_arm_notify(self._argv(), {"ARM_DB_FILE": arm_job_db})
        assert event["metadata"]["title"] == "New Movie"

    def test_data_disc_from_db(self, notification_schema, arm_job_db):
        _insert_job(arm_job_db, title=None, year=None, disctype="data",
                    label="BACKUP_2024", path="/out/data/BACKUP_2024_20260404")
        event = build_event_from_arm_notify(
            self._argv(body="Data disc: BACKUP_2024 copying complete. "),
            {"ARM_DB_FILE": arm_job_db},
        )
        notification_schema.validate(event)
        assert event["media_type"] == "data/iso"
        assert event["metadata"]["title"] is None

    def test_missing_db_falls_back_gracefully(self, tmp_path):
        # Best-effort: a missing/unreadable DB must not raise.
        missing = str(tmp_path / "does-not-exist.db")
        event = build_event_from_arm_notify(self._argv(), {"ARM_DB_FILE": missing})
        assert event is not None
        assert event["schema_version"] == "1.0"
        assert event["event_type"] == "disc-extraction-complete"

    def test_default_db_path_used_when_env_unset(self):
        # No ARM_DB_FILE override -> falls back to ARM's real default path
        # (/home/arm/db/arm.db), which won't exist in the test sandbox --
        # must degrade gracefully rather than raise.
        event = build_event_from_arm_notify(self._argv(), {})
        assert event is not None
        assert event["event_type"] == "disc-extraction-complete"

#!/usr/bin/env python3
"""ARM post-rip hook: construct and POST a notification event to the relay.

ARM 2.23 has no dedicated "post-rip" hook key. The real mechanism is the
generic notification hook: `BASH_SCRIPT` in arm.yaml, invoked by ARM as
`bash "$BASH_SCRIPT" "$title" "$body"` (positional args only -- ARM sets no
ARM_* environment variables for it) every time ARM calls its own
notify()/notify_exit(), i.e. on job start, mid-rip progress, a
duplicate-disc abort, a fatal error, AND on true completion (verified
against the automatic-ripping-machine/automatic-ripping-machine tree at tag
2.23.2, commit 7c034584466cd3dc167d65ba3f3cde5d555691a9):
  - arm/ripper/utils.py:88-91      bash_notify(): the BASH_SCRIPT exec
  - arm/ripper/arm_ripper.py:170-185  notify_exit(): final video notify
  - arm/ripper/main.py:132,146     final music/data notify (no notify_exit)
See build_event_from_arm_notify() below for how this hook adapts that
(title, body) argv into the structured event build_event() expects.
"""
import json
import os
import sqlite3
import sys
import urllib.request
import urllib.error
from datetime import datetime, timezone

# ARM disc type -> our media_type enum
DISC_TYPE_MAP = {
    "bluray": "video/bluray",
    "dvd": "video/dvd",
    "music": "audio/cd",
    "data": "data/iso",
}


def build_event(env=None):
    """Build a notification event dict from ARM environment variables.

    Args:
        env: dict of environment variables (defaults to os.environ).

    Returns:
        A dict matching the notification-event.v1 schema.
    """
    if env is None:
        env = os.environ

    disc_type = env.get("ARM_DISCTYPE", "").lower()

    # UHD Blu-ray is detected by ARM as bluray with uhd flag
    if disc_type == "bluray" and env.get("ARM_HAS_UHD", "").lower() == "true":
        media_type = "video/uhd-bluray"
    else:
        media_type = DISC_TYPE_MAP.get(disc_type, "data/iso")

    title = env.get("ARM_TITLE") or None
    year_str = env.get("ARM_YEAR", "")
    year = int(year_str) if year_str.isdigit() else None

    return {
        "schema_version": "1.0",
        "source": "optical-ripper",
        "event_type": "disc-extraction-complete",
        "timestamp": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "output_path": env.get("ARM_MEDIA_DIR", ""),
        "media_type": media_type,
        "metadata": {
            "title": title,
            "year": year,
            "source_device": env.get("ARM_DEVICE_NAME", "pioneer-bdr-xs07uhd"),
            "disc_label": env.get("ARM_LABEL") or None,
            "page_count": None,
            "session_id": None,
        },
        # NODE_NAME (a Kubernetes downward-API fieldRef to spec.nodeName) is
        # preferred over HOSTNAME: in a pod, HOSTNAME is auto-set by the
        # container runtime to the pod's hostname (== pod name), not the
        # node's -- exactly the wrong value for a field documented as "node
        # where the capture workload ran". NODE_NAME is only ever absent
        # outside the chart (e.g. a bare manual run), where HOSTNAME is the
        # best available fallback.
        "node_name": env.get("NODE_NAME", env.get("HOSTNAME", "unknown")),
    }


def post_event(event, relay_url=None):
    """POST the event to the notification relay.

    Args:
        event: notification event dict.
        relay_url: URL to POST to (defaults to NOTIFY_WEBHOOK env var).

    Returns:
        HTTP status code on success.

    Raises:
        urllib.error.URLError on network failure.
    """
    if relay_url is None:
        relay_url = os.environ.get("NOTIFY_WEBHOOK", "").strip()

    if not relay_url:
        raise ValueError(
            "NOTIFY_WEBHOOK environment variable must be set with a valid relay URL"
        )

    data = json.dumps(event).encode("utf-8")
    req = urllib.request.Request(
        relay_url,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return resp.status


# Body-text markers ARM's own notify() is called with at true job
# completion -- see arm/ripper/arm_ripper.py:185 (video, via notify_exit;
# the sibling error branch at :180 always contains "with errors" so is
# excluded below), arm/ripper/main.py:132 (music) and arm/ripper/main.py:146
# (data disc, whose completion text is "copying complete." rather than
# PROCESS_COMPLETE's "processing complete."). Every OTHER notify()/
# BASH_SCRIPT call in the 2.23.2 tree (job start, mid-rip "rip complete,
# starting transcode", duplicate-disc abort, fatal error) uses different
# wording, so this is sufficient to pick out only the completion call
# without ARM giving us an explicit "stage" argument.
_COMPLETION_MARKERS = ("processing complete.", "copying complete.")


def is_completion_notification(body):
    """True if a BASH_SCRIPT (title, body) call is ARM's final success notify."""
    lowered = (body or "").lower()
    if "error" in lowered:
        return False
    return any(marker in lowered for marker in _COMPLETION_MARKERS)


def _read_last_job(db_path):
    """Read the most recently created row of ARM's own `job` table.

    ARM's BASH_SCRIPT gives us no structured data about the disc that just
    finished (see module docstring) -- but the Job row already has
    title/year/disctype/label/path populated well before notify_exit() ever
    fires (set at identify time; see arm/models/job.py identify() and the
    path set at arm/ripper/arm_ripper.py:36-45), so read it back from
    ARM's sqlite DB (DBFILE, default /home/arm/db/arm.db) instead of
    trying to reconstruct it from the notification text. Table name "job"
    is Flask-SQLAlchemy's default for `class Job(db.Model)` (no
    __tablename__ override; confirmed against arm/migrations/versions/
    c3a3fa694636_job_config_track_create.py which creates it as 'job').

    Best-effort: returns None on any failure (missing/locked/corrupt DB)
    rather than raising -- this hook must never be the reason a disc rip
    fails or ARM's notification pipeline blows up.
    """
    try:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=5)
    except sqlite3.Error:
        return None
    try:
        conn.row_factory = sqlite3.Row
        row = conn.execute(
            "SELECT title, year, disctype, label, path FROM job "
            "ORDER BY job_id DESC LIMIT 1"
        ).fetchone()
        return dict(row) if row else None
    except sqlite3.Error:
        return None
    finally:
        conn.close()


def build_event_from_arm_notify(argv, env=None):
    """Adapt an ARM BASH_SCRIPT invocation (argv = [prog, title, body]) into
    a notification event.

    Returns None when this call is not ARM's final success notification
    (see is_completion_notification) -- the caller should then do nothing.
    """
    if env is None:
        env = os.environ

    body = argv[2] if len(argv) > 2 else ""
    if not is_completion_notification(body):
        return None

    db_path = env.get("ARM_DB_FILE", "/home/arm/db/arm.db")
    job_row = _read_last_job(db_path) or {}

    synthetic_env = dict(env)
    if job_row.get("disctype"):
        synthetic_env["ARM_DISCTYPE"] = str(job_row["disctype"]).lower()
    if job_row.get("title"):
        synthetic_env["ARM_TITLE"] = job_row["title"]
    if job_row.get("year"):
        synthetic_env["ARM_YEAR"] = str(job_row["year"])
    if job_row.get("path"):
        synthetic_env["ARM_MEDIA_DIR"] = job_row["path"]
    if job_row.get("label"):
        synthetic_env["ARM_LABEL"] = job_row["label"]
    return build_event(synthetic_env)


if __name__ == "__main__":
    if len(sys.argv) > 1:
        # Invoked by ARM's BASH_SCRIPT hook shim: argv[1]=title, argv[2]=body.
        event = build_event_from_arm_notify(sys.argv)
        if event is None:
            # Not the completion call (job start / mid-rip progress /
            # duplicate-disc abort / fatal error) -- nothing to notify.
            sys.exit(0)
    else:
        # Direct/manual invocation: build straight from ARM_* env vars.
        event = build_event()
    try:
        status = post_event(event)
        print(f"Notification sent: HTTP {status}")
    except ValueError as e:
        print(f"Configuration error: {e}", file=sys.stderr)
        sys.exit(1)
    except (urllib.error.URLError, OSError) as e:
        print(f"Notification failed: {e}", file=sys.stderr)
        sys.exit(1)

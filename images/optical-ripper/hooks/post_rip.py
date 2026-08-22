#!/usr/bin/env python3
"""ARM post-rip hook: construct and POST a notification event to the relay."""
import json
import os
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
        "node_name": env.get("HOSTNAME", env.get("NODE_NAME", "unknown")),
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


if __name__ == "__main__":
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

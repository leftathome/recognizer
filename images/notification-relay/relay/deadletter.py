"""Dead-letter handling for failed notification events."""
import json
import os
from datetime import datetime, timezone


def write_dead_letter(event, error_info, dead_letter_dir, attempts=3):
    """Write a failed event to the dead-letter directory.

    Args:
        event: the original notification event dict.
        error_info: string describing the failure.
        dead_letter_dir: path to the dead-letter directory on the NAS.
        attempts: number of delivery attempts that were made.

    Returns:
        Path to the written dead-letter file.
    """
    os.makedirs(dead_letter_dir, exist_ok=True)

    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    filename = f"{timestamp}.json"
    filepath = os.path.join(dead_letter_dir, filename)

    envelope = {
        "original_event": event,
        "error": error_info,
        "dead_lettered_at": datetime.now(timezone.utc).isoformat(),
        "attempts": attempts,
    }

    tmp_path = filepath + ".tmp"
    with open(tmp_path, "w") as f:
        json.dump(envelope, f, indent=2)
    os.rename(tmp_path, filepath)

    return filepath

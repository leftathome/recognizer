"""Dead-letter handling for failed notification events."""
import json
import logging
import os
import uuid
from datetime import datetime, timezone

logger = logging.getLogger("relay")

# Refuse new dead-letter writes once the directory holds this many files,
# so a misbehaving client or a genuinely down destination can't force
# unbounded growth on the backing PVC. Overridable per call via
# write_dead_letter's max_files; this is only the env-derived default.
DEFAULT_DEADLETTER_MAX = int(os.environ.get("RELAY_DEADLETTER_MAX", "10000"))


def write_dead_letter(event, error_info, dead_letter_dir, attempts=3, max_files=None):
    """Write a failed event to the dead-letter directory.

    Args:
        event: the original notification event dict.
        error_info: string describing the failure.
        dead_letter_dir: path to the dead-letter directory on the NAS.
        attempts: number of delivery attempts that were made.
        max_files: cap on files in dead_letter_dir. At/over this count the
            event is logged and dropped instead of written (simplest
            correct behavior for a bounded PVC -- overwriting the oldest
            file was considered but adds complexity, i.e. tracking
            mtimes/ordering, for no real benefit: once the drain job
            (which re-POSTs and deletes on success) has fallen this far
            behind, the operator needs a loud signal, not silent
            reordering). None uses RELAY_DEADLETTER_MAX (default 10000).

    Returns:
        Path to the written dead-letter file, or None if the directory was
        at/over its cap and the event was dropped instead.
    """
    os.makedirs(dead_letter_dir, exist_ok=True)

    cap = DEFAULT_DEADLETTER_MAX if max_files is None else max_files
    if cap >= 0:
        # A single os.scandir directory read (no per-entry stat() beyond
        # what is_file() needs from the dirent) is cheap at the
        # thousands-of-files scale this cap targets -- a few ms even at
        # the 10000 default. If the cap is ever raised by orders of
        # magnitude, replace this with a maintained counter.
        with os.scandir(dead_letter_dir) as entries:
            existing = sum(1 for e in entries if e.is_file())
        if existing >= cap:
            logger.error(
                "dead-letter directory %s at capacity (%d files, cap %d); "
                "dropping event instead of writing another file",
                dead_letter_dir, existing, cap,
            )
            return None

    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    # A uuid4 suffix keeps concurrent/bursty failures -- which routinely
    # land on the same second-granularity timestamp -- from overwriting
    # each other.
    filename = f"{timestamp}-{uuid.uuid4().hex}.json"
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

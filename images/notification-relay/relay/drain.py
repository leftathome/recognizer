"""Dead-letter drain: re-deliver dead-lettered events on a schedule.

Run via `python -m relay.drain` (see the deadletter-drain CronJob in the
chart: charts/recognizer/templates/notification-relay/deadletter-drain-cronjob.yaml).
Reuses relay.config for destination loading and relay.fanout + relay.retry
for delivery -- the exact same fan-out logic the relay's own worker threads
use, just triggered by a re-scan of DEAD_LETTER_DIR instead of an incoming
POST.

Files are processed oldest-first (by mtime), one file at a time:
  - Full delivery success (every configured destination accepted it) ->
    the file is deleted.
  - Any destination failure -> the file is left in place for the next run.
  - Past RELAY_DEADLETTER_MAX_AGE_DAYS, a file is aged out (deleted, with a
    loud log line) instead of retried forever.
A single run touches at most RELAY_DRAIN_BATCH files so a large backlog
can't make one invocation run unboundedly long; the remainder waits for the
next scheduled fire.

Exit code is 0 even when individual events fail to redeliver -- that is
ordinary steady-state behavior and the next hourly run retries them. Only a
hard config error (the destinations file itself missing or invalid) is a
nonzero exit, since that is an operator-actionable problem this run cannot
route around.
"""
import glob
import logging
import json
import os
import sys
import time

from relay.config import load_destinations
from relay.fanout import fan_out
from relay.retry import with_retry

logger = logging.getLogger("relay.drain")

DEAD_LETTER_DIR = os.environ.get(
    "DEAD_LETTER_DIR", "/out/notifications/dead-letter"
)

# Age out (delete) dead-letter files older than this many days instead of
# retrying them forever -- past this point the operator needs a loud signal
# that the event is gone, not silent indefinite retrying.
RELAY_DEADLETTER_MAX_AGE_DAYS = float(
    os.environ.get("RELAY_DEADLETTER_MAX_AGE_DAYS", "14")
)

# Cap on files touched in a single run.
RELAY_DRAIN_BATCH = int(os.environ.get("RELAY_DRAIN_BATCH", "500"))


def strip_envelope(envelope):
    """Return the original notification event a dead-letter envelope
    wrapped, stripping the fields relay.deadletter.write_dead_letter added
    (original_event/error/dead_lettered_at/attempts).

    Falls back to returning the envelope unchanged if it doesn't look like
    one of ours (no 'original_event' key) -- defensive only; in practice
    this directory holds nothing but our own writes.
    """
    if isinstance(envelope, dict) and "original_event" in envelope:
        return envelope["original_event"]
    return envelope


def _redeliver(event, destinations, max_attempts=3, retry_delays=None):
    """Attempt delivery of `event` to every destination. Returns True only
    if *every* destination ultimately succeeds (a partial success still
    leaves the dead-letter file in place -- otherwise the destinations that
    failed would silently lose the event on the next run)."""
    if not destinations:
        # Nothing configured to deliver to: there is nothing this run can
        # do for the event, so treat it the same as any other delivery
        # failure and leave the file for a future run (once destinations
        # are configured, or for an operator to inspect).
        return False

    all_ok = True
    for dest in destinations:
        def _do(d=dest):
            return fan_out(event, [d], timeout=10)[0]

        result = with_retry(_do, max_attempts=max_attempts, delays=retry_delays)
        if not result.success:
            all_ok = False
    return all_ok


def drain(
    dead_letter_dir=None,
    destinations=None,
    max_age_days=None,
    batch=None,
    max_attempts=3,
    retry_delays=None,
):
    """Run one drain pass over dead_letter_dir. Returns a summary dict."""
    dead_letter_dir = dead_letter_dir or DEAD_LETTER_DIR
    max_age_days = RELAY_DEADLETTER_MAX_AGE_DAYS if max_age_days is None else max_age_days
    batch = RELAY_DRAIN_BATCH if batch is None else batch
    destinations = destinations if destinations is not None else []

    summary = {"scanned": 0, "delivered": 0, "failed": 0, "aged_out": 0}

    if not os.path.isdir(dead_letter_dir):
        logger.info(
            "dead-letter directory %s does not exist; nothing to drain",
            dead_letter_dir,
        )
        return summary

    files = glob.glob(os.path.join(dead_letter_dir, "*.json"))
    # Oldest-first by mtime -- more robust than trusting the filename's
    # embedded timestamp (which reflects write time, not necessarily
    # arrival order under clock skew or a manual copy-in).
    files.sort(key=os.path.getmtime)

    now = time.time()
    max_age_seconds = max_age_days * 86400 if max_age_days >= 0 else None

    for path in files[:batch]:
        summary["scanned"] += 1

        if max_age_seconds is not None:
            age_seconds = now - os.path.getmtime(path)
            if age_seconds > max_age_seconds:
                logger.warning(
                    "dead-letter %s is %.1f days old (max %.1f days); "
                    "aging out (deleting) instead of retrying further",
                    path, age_seconds / 86400, max_age_days,
                )
                os.remove(path)
                summary["aged_out"] += 1
                continue

        try:
            with open(path) as f:
                envelope = json.load(f)
        except (OSError, ValueError) as e:
            logger.error(
                "dead-letter %s is unreadable/corrupt (%s); leaving in place",
                path, e,
            )
            summary["failed"] += 1
            continue

        event = strip_envelope(envelope)
        if _redeliver(event, destinations, max_attempts=max_attempts, retry_delays=retry_delays):
            os.remove(path)
            summary["delivered"] += 1
        else:
            summary["failed"] += 1

    logger.info(
        "drain summary: scanned=%d delivered=%d failed=%d aged_out=%d dir=%s",
        summary["scanned"], summary["delivered"], summary["failed"],
        summary["aged_out"], dead_letter_dir,
    )
    return summary


def main():
    logging.basicConfig(level=logging.INFO)

    try:
        destinations = load_destinations()
    except Exception:
        logger.exception("drain: failed to load destination config; aborting")
        return 1

    drain(destinations=destinations)
    return 0


if __name__ == "__main__":
    sys.exit(main())

"""Retry logic with exponential backoff for failed notification deliveries."""
import time


RETRY_DELAYS = [5, 30, 120]  # seconds between attempts


def with_retry(deliver_fn, max_attempts=3, delays=None, sleep_fn=None):
    """Call deliver_fn up to max_attempts times with exponential backoff.

    Args:
        deliver_fn: callable that returns a DeliveryResult.
        max_attempts: total attempts (including first try).
        delays: list of sleep durations between retries (seconds).
        sleep_fn: callable for sleeping (default: time.sleep). Override for testing.

    Returns:
        The last DeliveryResult (success or final failure).
    """
    if delays is None:
        delays = RETRY_DELAYS
    if sleep_fn is None:
        sleep_fn = time.sleep

    result = None
    for attempt in range(max_attempts):
        result = deliver_fn()
        if result.success:
            return result
        if attempt < max_attempts - 1 and attempt < len(delays):
            sleep_fn(delays[attempt])

    return result

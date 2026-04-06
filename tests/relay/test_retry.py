"""Tests for notification relay retry logic."""
from relay.retry import with_retry, RETRY_DELAYS
from relay.fanout import DeliveryResult


def _make_result(success, attempt_num=[0]):
    def deliver():
        n = attempt_num[0]
        attempt_num[0] += 1
        return DeliveryResult(
            destination="test",
            success=success(n) if callable(success) else success,
            status_code=200 if (success(n) if callable(success) else success) else 500,
            error="" if (success(n) if callable(success) else success) else "failed",
        )
    return deliver


class TestRetry:

    def test_success_on_first_try(self):
        calls = []
        def deliver():
            calls.append(1)
            return DeliveryResult("test", True, 200, "")

        result = with_retry(deliver)
        assert result.success
        assert len(calls) == 1

    def test_retries_on_failure(self):
        attempts = []
        sleeps = []

        def deliver():
            attempts.append(1)
            if len(attempts) < 3:
                return DeliveryResult("test", False, 500, "error")
            return DeliveryResult("test", True, 200, "")

        result = with_retry(deliver, max_attempts=3, sleep_fn=sleeps.append)
        assert result.success
        assert len(attempts) == 3
        assert sleeps == [5, 30]

    def test_returns_final_failure(self):
        attempts = []
        sleeps = []

        def deliver():
            attempts.append(1)
            return DeliveryResult("test", False, 500, "always fails")

        result = with_retry(deliver, max_attempts=3, sleep_fn=sleeps.append)
        assert not result.success
        assert len(attempts) == 3
        assert sleeps == [5, 30]

    def test_custom_delays(self):
        sleeps = []

        def deliver():
            return DeliveryResult("test", False, 0, "error")

        with_retry(deliver, max_attempts=4, delays=[1, 2, 4], sleep_fn=sleeps.append)
        assert sleeps == [1, 2, 4]

    def test_success_on_second_try(self):
        attempts = []
        sleeps = []

        def deliver():
            attempts.append(1)
            if len(attempts) == 1:
                return DeliveryResult("test", False, 503, "unavailable")
            return DeliveryResult("test", True, 202, "")

        result = with_retry(deliver, max_attempts=3, sleep_fn=sleeps.append)
        assert result.success
        assert result.status_code == 202
        assert len(attempts) == 2
        assert sleeps == [5]

    def test_default_delays_are_5_30_120(self):
        assert RETRY_DELAYS == [5, 30, 120]

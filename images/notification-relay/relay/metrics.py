"""Stdlib-only Prometheus text-exposition metrics for the relay.

No prometheus_client dependency -- matches the rest of this image, which is
stdlib + jsonschema + PyYAML only. Each RelayServer owns one Metrics
instance (see relay.main), so tests that spin up several servers never
share counters through module globals.

Metric names are load-bearing: `capture_notification_dead_letter_total` is
referenced by the NotificationDeadLetterBacklog alert
(charts/recognizer/templates/monitoring/prometheusrule.yaml). Do not rename
without updating that expr in the same change.
"""
import threading


class Counter:
    """Thread-safe counter, optionally partitioned by one label value.

    `label_values`, if given, seeds every listed label value at 0 so the
    series exists in /metrics output (and therefore in `increase()`/`rate()`
    queries) even before the first increment -- important for the
    dead-letter alert, which would otherwise have no data to evaluate until
    the first failure ever happens.
    """

    def __init__(self, name, help_text, label_values=None):
        self.name = name
        self.help_text = help_text
        self._lock = threading.Lock()
        self._values = {v: 0 for v in (label_values or [None])}

    def inc(self, label=None, amount=1):
        with self._lock:
            self._values[label] = self._values.get(label, 0) + amount

    def render(self, label_name=None):
        lines = [
            f"# HELP {self.name} {self.help_text}",
            f"# TYPE {self.name} counter",
        ]
        with self._lock:
            items = dict(self._values)
        for label, value in sorted(items.items(), key=lambda kv: kv[0] or ""):
            if label_name is None:
                lines.append(f"{self.name} {value}")
            else:
                lines.append(f'{self.name}{{{label_name}="{label}"}} {value}')
        return lines


class Gauge:
    """Gauge backed by a callable, read fresh on every render (e.g. the
    live depth of the delivery queue)."""

    def __init__(self, name, help_text, value_fn):
        self.name = name
        self.help_text = help_text
        self._value_fn = value_fn

    def render(self):
        return [
            f"# HELP {self.name} {self.help_text}",
            f"# TYPE {self.name} gauge",
            f"{self.name} {self._value_fn()}",
        ]


class Metrics:
    """All relay metrics. One instance per RelayServer.

    `queue_depth_fn` should be a zero-arg callable returning the current
    queue size (typically `self.event_queue.qsize`).
    """

    def __init__(self, queue_depth_fn=lambda: 0):
        self.events = Counter(
            "capture_notification_events_total",
            "Total notification events received by the relay, by outcome.",
            label_values=["accepted", "rejected"],
        )
        self.delivery = Counter(
            "capture_notification_delivery_total",
            "Total fan-out delivery attempts, by final outcome.",
            label_values=["delivered", "failed"],
        )
        self.dead_letter = Counter(
            "capture_notification_dead_letter_total",
            "Total events written to the dead-letter directory.",
        )
        self.queue_depth = Gauge(
            "capture_notification_queue_depth",
            "Current number of events queued for background delivery.",
            queue_depth_fn,
        )

    def event_accepted(self):
        self.events.inc("accepted")

    def event_rejected(self):
        self.events.inc("rejected")

    def delivery_succeeded(self):
        self.delivery.inc("delivered")

    def delivery_failed(self):
        self.delivery.inc("failed")

    def dead_lettered(self):
        self.dead_letter.inc()

    def render(self):
        """Render all metrics as Prometheus text exposition format
        (version 0.0.4 -- the format named by the text/plain response
        Content-Type the handler sends alongside this body)."""
        lines = []
        lines += self.events.render(label_name="status")
        lines += self.delivery.render(label_name="status")
        lines += self.dead_letter.render()
        lines += self.queue_depth.render()
        return "\n".join(lines) + "\n"

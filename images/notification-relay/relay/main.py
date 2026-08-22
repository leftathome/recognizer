"""Notification relay HTTP server. Receives events, validates, fans out.

Delivery is decoupled from request handling: `do_POST` only parses,
validates, and hands the event to a bounded in-memory queue, then responds
202 immediately. A small pool of worker threads drains that queue and does
the actual fan-out + retry (which can sleep up to ~155s per destination on
repeated failure -- see relay.retry). This keeps one slow or hanging
destination from wedging the whole relay: the request thread never calls
with_retry itself.
"""
import json
import logging
import os
import queue
import threading
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

from relay.validate import validation_errors
from relay.config import load_destinations
from relay.fanout import fan_out
from relay.retry import with_retry
from relay.deadletter import write_dead_letter
from relay.metrics import Metrics


logger = logging.getLogger("relay")

DEAD_LETTER_DIR = os.environ.get(
    "DEAD_LETTER_DIR", "/out/notifications/dead-letter"
)

# Bound on the number of events waiting for a worker. Past this, do_POST
# stops accepting new work for delivery and dead-letters immediately
# instead (see RelayHandler.do_POST) rather than blocking the request
# thread or growing memory without limit.
RELAY_QUEUE_MAX = int(os.environ.get("RELAY_QUEUE_MAX", "1000"))

# Number of background threads draining the event queue. Kept small by
# default -- this relay fans out to a handful of webhook destinations, not
# thousands of concurrent slow ones.
RELAY_WORKERS = int(os.environ.get("RELAY_WORKERS", "2"))


class RelayServer(ThreadingHTTPServer):
    """ThreadingHTTPServer plus the shared state request handlers and the
    delivery worker pool both need: the destination list, the bounded
    event queue, and the dead-letter settings.

    Threading the server (vs. plain HTTPServer) means concurrent requests
    no longer serialize on the socket accept loop; the event queue on top
    of that means a request never waits on delivery at all.
    """

    daemon_threads = True

    def __init__(
        self,
        server_address,
        handler_cls,
        destinations=None,
        dead_letter_dir=None,
        queue_max=None,
        worker_count=None,
        max_attempts=3,
        retry_delays=None,
        deadletter_max=None,
    ):
        super().__init__(server_address, handler_cls)
        self.destinations = destinations if destinations is not None else []
        self.dead_letter_dir = dead_letter_dir or DEAD_LETTER_DIR
        self.max_attempts = max_attempts
        self.retry_delays = retry_delays
        self.deadletter_max = deadletter_max
        self.event_queue = queue.Queue(
            maxsize=RELAY_QUEUE_MAX if queue_max is None else queue_max
        )
        self.worker_count = RELAY_WORKERS if worker_count is None else worker_count
        self._workers = []
        self.metrics = Metrics(queue_depth_fn=self.event_queue.qsize)

    def start_workers(self):
        """Start the delivery worker pool. Call once, after construction."""
        for i in range(self.worker_count):
            t = threading.Thread(
                target=self._worker_loop, name=f"relay-worker-{i}", daemon=True
            )
            t.start()
            self._workers.append(t)
        return self._workers

    def _worker_loop(self):
        while True:
            event = self.event_queue.get()
            try:
                self._deliver(event)
            except Exception:
                logger.exception("relay worker: unhandled error delivering event")
            finally:
                self.event_queue.task_done()

    def _deliver(self, event):
        for dest in self.destinations:
            def _do(d=dest):
                return fan_out(event, [d], timeout=10)[0]

            result = with_retry(_do, max_attempts=self.max_attempts, delays=self.retry_delays)
            if result.success:
                self.metrics.delivery_succeeded()
            else:
                self.metrics.delivery_failed()
                written = write_dead_letter(
                    event,
                    result.error,
                    self.dead_letter_dir,
                    max_files=self.deadletter_max,
                )
                if written is not None:
                    self.metrics.dead_lettered()


class RelayHandler(BaseHTTPRequestHandler):

    def do_POST(self):
        if self.path != "/event":
            self.send_error(404)
            return

        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)

        server = self.server  # a RelayServer instance

        try:
            event = json.loads(body)
        except (json.JSONDecodeError, ValueError) as e:
            server.metrics.event_rejected()
            self._respond(400, {"error": f"invalid JSON: {e}"})
            return

        errs = validation_errors(event)
        if errs:
            server.metrics.event_rejected()
            self._respond(400, {"error": "validation failed", "details": errs})
            return

        try:
            server.event_queue.put_nowait(event)
        except queue.Full:
            # Worker pool is saturated. Don't block the request thread
            # waiting for room -- that's exactly the head-of-line blocking
            # this queue exists to avoid. Durably record the event via the
            # same dead-letter path a delivery failure would use (the
            # drain CronJob picks it up later) and tell the caller it was
            # accepted: a plain 202 has never meant "delivered
            # synchronously" here, only "durably captured", and that
            # remains true whether capture happened via the queue or via
            # an immediate dead-letter write.
            logger.warning(
                "event queue full (max=%d); dead-lettering immediately",
                server.event_queue.maxsize,
            )
            written = write_dead_letter(
                event,
                "queue full: relay worker backlog exceeded RELAY_QUEUE_MAX",
                server.dead_letter_dir,
                max_files=server.deadletter_max,
            )
            if written is not None:
                server.metrics.dead_lettered()
            server.metrics.event_accepted()
            self._respond(202, {"accepted": True, "queued": False, "dead_lettered": True})
            return

        server.metrics.event_accepted()
        self._respond(202, {"accepted": True, "queued": True})

    def do_GET(self):
        if self.path == "/healthz":
            self._respond(200, {"status": "ok"})
            return
        if self.path == "/metrics":
            self._respond_text(200, self.server.metrics.render())
            return
        self.send_error(404)

    def _respond(self, code, body):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(body).encode("utf-8"))

    def _respond_text(self, code, text):
        body = text.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass


def main():
    logging.basicConfig(level=logging.INFO)

    host = os.environ.get("RELAY_HOST", "0.0.0.0")
    port = int(os.environ.get("RELAY_PORT", "8080"))

    destinations = load_destinations()
    server = RelayServer((host, port), RelayHandler, destinations=destinations)
    server.start_workers()
    print(
        f"Relay listening on {host}:{port} "
        f"({server.worker_count} worker(s), queue max {server.event_queue.maxsize})"
    )
    server.serve_forever()


if __name__ == "__main__":
    main()

"""Notification relay HTTP server. Receives events, validates, fans out."""
import json
import os
from http.server import HTTPServer, BaseHTTPRequestHandler

from relay.validate import validation_errors
from relay.config import load_destinations
from relay.fanout import fan_out, DeliveryResult
from relay.retry import with_retry
from relay.deadletter import write_dead_letter


DEAD_LETTER_DIR = os.environ.get(
    "DEAD_LETTER_DIR", "/out/notifications/dead-letter"
)


class RelayHandler(BaseHTTPRequestHandler):

    destinations = []

    def do_POST(self):
        if self.path != "/event":
            self.send_error(404)
            return

        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)

        try:
            event = json.loads(body)
        except (json.JSONDecodeError, ValueError) as e:
            self._respond(400, {"error": f"invalid JSON: {e}"})
            return

        errs = validation_errors(event)
        if errs:
            self._respond(400, {"error": "validation failed", "details": errs})
            return

        results = []
        for dest in self.destinations:
            def deliver(d=dest):
                return fan_out(event, [d], timeout=10)[0]
            result = with_retry(deliver)
            if not result.success:
                write_dead_letter(event, result.error, DEAD_LETTER_DIR)
            results.append({
                "destination": result.destination,
                "success": result.success,
                "status_code": result.status_code,
            })

        self._respond(202, {"accepted": True, "results": results})

    def do_GET(self):
        if self.path == "/healthz":
            self._respond(200, {"status": "ok"})
            return
        self.send_error(404)

    def _respond(self, code, body):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(body).encode("utf-8"))

    def log_message(self, format, *args):
        pass


def main():
    host = os.environ.get("RELAY_HOST", "0.0.0.0")
    port = int(os.environ.get("RELAY_PORT", "8080"))

    RelayHandler.destinations = load_destinations()
    server = HTTPServer((host, port), RelayHandler)
    print(f"Relay listening on {host}:{port}")
    server.serve_forever()


if __name__ == "__main__":
    main()

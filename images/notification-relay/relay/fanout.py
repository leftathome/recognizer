"""Fan out notification events to configured webhook destinations."""
import json
import urllib.request
import urllib.error
from dataclasses import dataclass
from relay.destination import Destination


@dataclass
class DeliveryResult:
    destination: str
    success: bool
    status_code: int
    error: str


def fan_out(event, destinations, timeout=10):
    """POST the event JSON to each destination.

    Args:
        event: notification event dict.
        destinations: list of Destination objects (or dicts with name/url/headers).
        timeout: HTTP request timeout in seconds.

    Returns:
        List of DeliveryResult, one per destination.
    """
    payload = json.dumps(event).encode("utf-8")
    results = []

    for dest in destinations:
        if isinstance(dest, Destination):
            name, url, extra_headers = dest.name, dest.url, dest.headers
        else:
            name = dest.get("name", "unnamed")
            url = dest.get("url", "")
            extra_headers = dest.get("headers", {})

        if not url:
            results.append(DeliveryResult(
                destination=name,
                success=False,
                status_code=0,
                error="empty URL",
            ))
            continue

        headers = {"Content-Type": "application/json"}
        headers.update(extra_headers)

        req = urllib.request.Request(url, data=payload, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                results.append(DeliveryResult(
                    destination=name,
                    success=True,
                    status_code=resp.status,
                    error="",
                ))
        except urllib.error.HTTPError as e:
            results.append(DeliveryResult(
                destination=name,
                success=False,
                status_code=e.code,
                error=str(e),
            ))
        except (urllib.error.URLError, OSError) as e:
            results.append(DeliveryResult(
                destination=name,
                success=False,
                status_code=0,
                error=str(e),
            ))

    return results

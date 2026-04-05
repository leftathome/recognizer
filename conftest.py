"""Root conftest: configure import paths for service packages."""
import sys
import os

_root = os.path.dirname(__file__)

_service_paths = [
    os.path.join(_root, "images", "notification-relay"),
]

for p in _service_paths:
    if p not in sys.path:
        sys.path.insert(0, p)

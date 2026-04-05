"""Destination dataclass shared between config and fanout."""
from dataclasses import dataclass, field


@dataclass
class Destination:
    name: str = "unnamed"
    url: str = ""
    headers: dict = field(default_factory=dict)

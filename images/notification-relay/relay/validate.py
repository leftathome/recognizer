"""Validate notification events against the v1 schema."""
import json
import os
from jsonschema import Draft202012Validator

_SCHEMA_DIR = os.environ.get(
    "CAPTURE_SCHEMA_DIR",
    os.path.join(os.path.dirname(__file__), "..", "..", "..", "schemas"),
)
# v1.1 is an additive superset of v1.0 (new source/event_type enum
# entries, loosened media_type, optional event_id and v1.1-only metadata
# fields). Validating against v1.1 also accepts every valid v1.0 event
# because the schema_version field's enum is ["1.0", "1.1"] and every
# pre-existing event_type keeps its exact original required-fields list.
# See archiver spec 03 § 6.1. (bead archiver-9xw, no version bump): v1.1
# additionally accepts disc-detected/disc-ejected event_types, which are
# excused from the output_path/media_type requirement via an `allOf`
# conditional since they fire before a rip has produced any output.
_SCHEMA_PATH = os.path.join(_SCHEMA_DIR, "notification-event.v1.1.schema.json")

with open(_SCHEMA_PATH) as _f:
    _SCHEMA = json.load(_f)

_VALIDATOR = Draft202012Validator(_SCHEMA)


def validate_event(event: dict) -> None:
    """Validate an event dict against notification-event.v1 schema.

    Raises jsonschema.ValidationError with a clear message on failure.
    """
    _VALIDATOR.validate(event)


def validation_errors(event: dict) -> list[str]:
    """Return a list of human-readable validation error messages.

    Returns an empty list if the event is valid.
    """
    return [e.message for e in _VALIDATOR.iter_errors(event)]

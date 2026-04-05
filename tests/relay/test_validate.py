"""Tests for notification relay event validation."""
import copy
import json
import os
import pytest
from jsonschema import ValidationError

from relay.validate import validate_event, validation_errors

FIXTURES_DIR = os.path.join(os.path.dirname(__file__), "..", "fixtures")


def _load(name):
    with open(os.path.join(FIXTURES_DIR, name)) as f:
        return json.load(f)


# -- Valid events pass --

def test_valid_optical_event():
    validate_event(_load("valid_notification_optical.json"))


def test_valid_scanner_event():
    validate_event(_load("valid_notification_scanner.json"))


# -- Invalid events raise ValidationError --

def test_missing_source_raises():
    event = _load("valid_notification_optical.json")
    del event["source"]
    with pytest.raises(ValidationError, match="source"):
        validate_event(event)


def test_bad_media_type_raises():
    event = _load("valid_notification_optical.json")
    event["media_type"] = "video/vhs"
    with pytest.raises(ValidationError, match="is not one of"):
        validate_event(event)


def test_extra_field_raises():
    event = _load("valid_notification_optical.json")
    event["surprise"] = "bad"
    with pytest.raises(ValidationError, match="Additional properties"):
        validate_event(event)


def test_wrong_schema_version_raises():
    event = _load("valid_notification_optical.json")
    event["schema_version"] = "2.0"
    with pytest.raises(ValidationError):
        validate_event(event)


# -- validation_errors returns list --

def test_validation_errors_empty_for_valid():
    event = _load("valid_notification_optical.json")
    assert validation_errors(event) == []


def test_validation_errors_returns_messages():
    event = _load("valid_notification_optical.json")
    del event["source"]
    del event["timestamp"]
    errs = validation_errors(event)
    assert len(errs) == 2
    assert any("source" in e for e in errs)
    assert any("timestamp" in e for e in errs)


# -- Schema loaded once at import --

def test_schema_is_cached():
    from relay import validate as mod
    assert mod._SCHEMA is not None
    assert mod._VALIDATOR is not None

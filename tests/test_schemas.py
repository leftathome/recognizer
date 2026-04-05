"""Validate JSON documents against capture framework schemas."""
import copy
import json
import os
import pytest
from jsonschema import validate, ValidationError, Draft202012Validator

SCHEMAS_DIR = os.path.join(os.path.dirname(__file__), "..", "schemas")
FIXTURES_DIR = os.path.join(os.path.dirname(__file__), "fixtures")


def load_json(path):
    with open(path) as f:
        return json.load(f)


@pytest.fixture(scope="module")
def notification_schema():
    return load_json(os.path.join(SCHEMAS_DIR, "notification-event.v1.schema.json"))


@pytest.fixture(scope="module")
def manifest_schema():
    return load_json(os.path.join(SCHEMAS_DIR, "scan-session-manifest.v1.schema.json"))


# -- Valid notification fixtures --

VALID_NOTIFICATIONS = [
    "valid_notification_optical.json",
    "valid_notification_scanner.json",
]


@pytest.mark.parametrize("fixture_file", VALID_NOTIFICATIONS)
def test_valid_notification_accepted(notification_schema, fixture_file):
    doc = load_json(os.path.join(FIXTURES_DIR, fixture_file))
    validate(instance=doc, schema=notification_schema, cls=Draft202012Validator)


# -- Valid manifest fixtures --

VALID_MANIFESTS = [
    "valid_manifest_adf.json",
    "valid_manifest_flatbed.json",
]


@pytest.mark.parametrize("fixture_file", VALID_MANIFESTS)
def test_valid_manifest_accepted(manifest_schema, fixture_file):
    doc = load_json(os.path.join(FIXTURES_DIR, fixture_file))
    validate(instance=doc, schema=manifest_schema, cls=Draft202012Validator)


# -- Invalid notification fixtures --

INVALID_NOTIFICATIONS = [
    ("invalid_notification_missing_source.json", "'source' is a required property"),
    ("invalid_notification_bad_enum.json", "is not one of"),
    ("invalid_notification_extra_field.json", "Additional properties are not allowed"),
]


@pytest.mark.parametrize("fixture_file,expected_msg", INVALID_NOTIFICATIONS)
def test_invalid_notification_rejected(notification_schema, fixture_file, expected_msg):
    doc = load_json(os.path.join(FIXTURES_DIR, fixture_file))
    with pytest.raises(ValidationError) as exc_info:
        validate(instance=doc, schema=notification_schema, cls=Draft202012Validator)
    assert expected_msg in str(exc_info.value), (
        f"Expected error containing '{expected_msg}', got: {exc_info.value.message}"
    )


# -- Invalid manifest fixtures --

INVALID_MANIFESTS = [
    ("invalid_manifest_bad_session_id.json", "does not match"),
    ("invalid_manifest_missing_pages.json", "'pages' is a required property"),
    ("invalid_manifest_bad_page_filename.json", "does not match"),
]


@pytest.mark.parametrize("fixture_file,expected_msg", INVALID_MANIFESTS)
def test_invalid_manifest_rejected(manifest_schema, fixture_file, expected_msg):
    doc = load_json(os.path.join(FIXTURES_DIR, fixture_file))
    with pytest.raises(ValidationError) as exc_info:
        validate(instance=doc, schema=manifest_schema, cls=Draft202012Validator)
    assert expected_msg in str(exc_info.value), (
        f"Expected error containing '{expected_msg}', got: {exc_info.value.message}"
    )


# -- Edge case tests --

def _mutated(fixture_name, **overrides):
    """Load a fixture and return a deep copy with fields overridden."""
    doc = copy.deepcopy(load_json(os.path.join(FIXTURES_DIR, fixture_name)))
    for key, value in overrides.items():
        if "." in key:
            parts = key.split(".", 1)
            doc[parts[0]][parts[1]] = value
        else:
            doc[key] = value
    return doc


def test_notification_schema_rejects_wrong_schema_version(notification_schema):
    doc = _mutated("valid_notification_optical.json", schema_version="2.0")
    with pytest.raises(ValidationError):
        validate(instance=doc, schema=notification_schema, cls=Draft202012Validator)


def test_manifest_empty_pages_rejected(manifest_schema):
    doc = _mutated("valid_manifest_flatbed.json", pages=[], page_count=0)
    with pytest.raises(ValidationError):
        validate(instance=doc, schema=manifest_schema, cls=Draft202012Validator)


def test_manifest_page_extra_field_rejected(manifest_schema):
    doc = copy.deepcopy(load_json(os.path.join(FIXTURES_DIR, "valid_manifest_adf.json")))
    doc["pages"][0]["ocr_text"] = "should not be here"
    with pytest.raises(ValidationError):
        validate(instance=doc, schema=manifest_schema, cls=Draft202012Validator)


def test_notification_metadata_extra_field_rejected(notification_schema):
    doc = _mutated("valid_notification_optical.json", **{"metadata.extra": "nope"})
    with pytest.raises(ValidationError):
        validate(instance=doc, schema=notification_schema, cls=Draft202012Validator)

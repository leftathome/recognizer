"""Tests for document scanner DaemonSet manifest + cross-bead consistency."""
import os
import re
import pytest
import yaml

MANIFESTS_DIR = os.path.join(os.path.dirname(__file__), "..", "manifests")


def _load(relpath):
    with open(os.path.join(MANIFESTS_DIR, relpath)) as f:
        return yaml.safe_load(f)


class TestScannerDaemonSet:

    @pytest.fixture(scope="class")
    def ds(self):
        return _load("document-scanner/daemonset.yaml")

    @pytest.fixture(scope="class")
    def pod_spec(self, ds):
        return ds["spec"]["template"]["spec"]

    def test_node_selector(self, pod_spec):
        assert pod_spec["nodeSelector"]["openclaw.io/device-scanner"] == "epson-ds-1630"

    def test_device_resource_limits(self, pod_spec):
        limits = pod_spec["containers"][0]["resources"]["limits"]
        assert limits["smarter-devices/bus-usb"] == 1

    def test_nfs_volume_mount(self, pod_spec):
        mounts = {m["name"]: m for m in pod_spec["containers"][0]["volumeMounts"]}
        assert "incoming" in mounts
        assert mounts["incoming"]["mountPath"] == "/out"

    def test_pvc_reference(self, pod_spec):
        vols = {v["name"]: v for v in pod_spec["volumes"]}
        assert vols["incoming"]["persistentVolumeClaim"]["claimName"] == "incoming-nfs-pvc"

    def test_capture_framework_label(self, ds):
        labels = ds["spec"]["template"]["metadata"]["labels"]
        assert labels["app.kubernetes.io/part-of"] == "capture-framework"

    def test_web_ui_port(self, pod_spec):
        ports = pod_spec["containers"][0]["ports"]
        assert any(p["containerPort"] == 8080 for p in ports)

    def test_image_uses_leftathome_namespace(self, pod_spec):
        image = pod_spec["containers"][0]["image"]
        assert "ghcr.io/leftathome/archiver/" in image


class TestScannerService:

    @pytest.fixture(scope="class")
    def svc(self):
        return _load("document-scanner/service.yaml")

    def test_selector_matches_daemonset(self, svc):
        ds = _load("document-scanner/daemonset.yaml")
        ds_labels = ds["spec"]["template"]["metadata"]["labels"]
        for k, v in svc["spec"]["selector"].items():
            assert ds_labels.get(k) == v

    def test_port_8080(self, svc):
        ports = svc["spec"]["ports"]
        assert any(p["port"] == 8080 for p in ports)


class TestScannerConfigMap:

    @pytest.fixture(scope="class")
    def config(self):
        cm = _load("document-scanner/configmap.yaml")
        return yaml.safe_load(cm["data"]["config.yaml"])

    def test_idle_timeout(self, config):
        assert config["idle_timeout_seconds"] == 90

    def test_relay_url(self, config):
        assert "notification-relay.capture.svc.cluster.local" in config["relay_url"]

    def test_scan_resolution(self, config):
        assert config["scan_resolution"] == 600

    def test_scan_format(self, config):
        assert config["scan_format"] == "tiff"


class TestScannerCrossBeadConsistency:

    @pytest.fixture(scope="class")
    def nfd_config(self):
        cm = _load("nfd/nfd-worker-config.yaml")
        return yaml.safe_load(cm["data"]["nfd-worker.conf"])

    @pytest.fixture(scope="class")
    def sdm_config(self):
        cm = _load("device-plugin/smarter-device-manager.yaml")
        return yaml.safe_load(cm["data"]["conf.yaml"])

    @pytest.fixture(scope="class")
    def scanner_ds(self):
        return _load("document-scanner/daemonset.yaml")

    def test_nodeselector_matches_nfd_label(self, scanner_ds, nfd_config):
        ds_selector = scanner_ds["spec"]["template"]["spec"]["nodeSelector"]
        epson_rule = next(r for r in nfd_config["rules"] if r["name"] == "epson-ds-1630")
        for label_key, label_val in ds_selector.items():
            assert epson_rule["labels"].get(label_key) == label_val, (
                f"DaemonSet nodeSelector {label_key}={label_val} "
                f"not matched in NFD rule labels: {epson_rule['labels']}"
            )

    def test_device_resources_match_sdm(self, scanner_ds, sdm_config):
        """Verify each smarter-devices resource has a corresponding SDM device pattern.

        SDM resource names are derived from device match patterns by replacing
        path separators with hyphens (e.g. ^bus/usb/ -> bus-usb). We check that
        each requested resource has a plausible SDM pattern by normalizing both.
        """
        limits = scanner_ds["spec"]["template"]["spec"]["containers"][0]["resources"]["limits"]
        # Normalize SDM patterns to resource-name form: strip regex anchors, replace / with -
        sdm_names = []
        for d in sdm_config:
            name = d["devicematch"].strip("^$").rstrip("/").replace("/", "-")
            sdm_names.append(name)
        for resource_name in limits:
            if resource_name.startswith("smarter-devices/"):
                device = resource_name.split("/")[1]
                matched = any(device.startswith(n) for n in sdm_names)
                assert matched, (
                    f"DaemonSet resource {resource_name} has no matching "
                    f"SDM device pattern (normalized: {sdm_names})"
                )

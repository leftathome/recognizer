"""Tests for optical ripper DaemonSet manifest + cross-bead consistency."""
import os
import pytest
import yaml

MANIFESTS_DIR = os.path.join(os.path.dirname(__file__), "..", "manifests")


def _load(relpath):
    with open(os.path.join(MANIFESTS_DIR, relpath)) as f:
        return yaml.safe_load(f)


class TestRipperDaemonSet:

    @pytest.fixture(scope="class")
    def ds(self):
        return _load("optical-ripper/daemonset.yaml")

    @pytest.fixture(scope="class")
    def pod_spec(self, ds):
        return ds["spec"]["template"]["spec"]

    def test_node_selector(self, pod_spec):
        assert pod_spec["nodeSelector"]["openclaw.io/device-optical-drive"] == "pioneer-bdr-xs07uhd"

    def test_device_resource_limits(self, pod_spec):
        limits = pod_spec["containers"][0]["resources"]["limits"]
        assert limits["smarter-devices/sr0"] == 1
        assert limits["smarter-devices/sg0"] == 1

    def test_nfs_volume_mount(self, pod_spec):
        mounts = {m["name"]: m for m in pod_spec["containers"][0]["volumeMounts"]}
        assert "incoming" in mounts
        assert mounts["incoming"]["mountPath"] == "/out"

    def test_pvc_reference(self, pod_spec):
        vols = {v["name"]: v for v in pod_spec["volumes"]}
        assert vols["incoming"]["persistentVolumeClaim"]["claimName"] == "incoming-nfs-pvc"

    def test_secret_env_vars(self, pod_spec):
        env = {e["name"]: e for e in pod_spec["containers"][0]["env"]}
        assert env["KEY"]["valueFrom"]["secretKeyRef"]["name"] == "makemkv-license"
        assert env["KEY"]["valueFrom"]["secretKeyRef"]["key"] == "license-key"
        assert env["OMDB_API_KEY"]["valueFrom"]["secretKeyRef"]["name"] == "omdb-api"
        assert env["OMDB_API_KEY"]["valueFrom"]["secretKeyRef"]["key"] == "api-key"

    def test_image_not_latest(self, pod_spec):
        image = pod_spec["containers"][0]["image"]
        assert ":latest" not in image, "Production image should use a pinned tag"

    def test_capture_framework_label(self, ds):
        labels = ds["spec"]["template"]["metadata"]["labels"]
        assert labels["app.kubernetes.io/part-of"] == "capture-framework"

    def test_web_ui_port(self, pod_spec):
        ports = pod_spec["containers"][0]["ports"]
        assert any(p["containerPort"] == 8080 for p in ports)


class TestRipperService:

    @pytest.fixture(scope="class")
    def svc(self):
        return _load("optical-ripper/service.yaml")

    def test_selector_matches_daemonset(self, svc):
        ds = _load("optical-ripper/daemonset.yaml")
        ds_labels = ds["spec"]["template"]["metadata"]["labels"]
        for k, v in svc["spec"]["selector"].items():
            assert ds_labels.get(k) == v, f"Service selector {k}={v} not in DaemonSet labels"

    def test_port_8080(self, svc):
        ports = svc["spec"]["ports"]
        assert any(p["port"] == 8080 for p in ports)


class TestCrossBeadConsistency:
    """Verify labels and resource names match across NFD, SDM, and DaemonSet."""

    @pytest.fixture(scope="class")
    def nfd_config(self):
        cm = _load("nfd/nfd-worker-config.yaml")
        return yaml.safe_load(cm["data"]["nfd-worker.conf"])

    @pytest.fixture(scope="class")
    def sdm_config(self):
        cm = _load("device-plugin/smarter-device-manager.yaml")
        return yaml.safe_load(cm["data"]["conf.yaml"])

    @pytest.fixture(scope="class")
    def ripper_ds(self):
        return _load("optical-ripper/daemonset.yaml")

    def test_nodeselector_matches_nfd_label(self, ripper_ds, nfd_config):
        ds_selector = ripper_ds["spec"]["template"]["spec"]["nodeSelector"]
        pioneer_rule = next(r for r in nfd_config["rules"] if r["name"] == "pioneer-bdr-xs07uhd")
        for label_key, label_val in ds_selector.items():
            assert pioneer_rule["labels"].get(label_key) == label_val, (
                f"DaemonSet nodeSelector {label_key}={label_val} "
                f"not matched in NFD rule labels: {pioneer_rule['labels']}"
            )

    def test_device_resources_match_sdm(self, ripper_ds, sdm_config):
        limits = ripper_ds["spec"]["template"]["spec"]["containers"][0]["resources"]["limits"]
        sdm_patterns = [d["devicematch"] for d in sdm_config]
        for resource_name in limits:
            if resource_name.startswith("smarter-devices/"):
                device = resource_name.split("/")[1]
                matched = any(
                    __import__("re").search(pat, device)
                    for pat in sdm_patterns
                )
                assert matched, (
                    f"DaemonSet resource {resource_name} has no matching "
                    f"SDM device pattern in {sdm_patterns}"
                )

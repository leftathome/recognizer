"""Validate Kubernetes manifests with kubeconform."""
import ipaddress
import subprocess
import glob
import os
import pytest
import yaml

MANIFESTS_DIR = os.path.join(os.path.dirname(__file__), "..", "manifests")
ALL_MANIFESTS = sorted(glob.glob(os.path.join(MANIFESTS_DIR, "**", "*.yaml"), recursive=True))


def _load_manifest(relpath):
    path = os.path.join(MANIFESTS_DIR, relpath)
    with open(path) as f:
        return yaml.safe_load(f)


class TestKubeconform:

    def test_manifests_exist(self):
        assert len(ALL_MANIFESTS) > 0, "No manifest YAML files found"

    @pytest.mark.parametrize("manifest", ALL_MANIFESTS, ids=lambda p: os.path.relpath(p, MANIFESTS_DIR))
    def test_manifest_valid(self, manifest):
        result = subprocess.run(
            ["kubeconform", "-strict", "-ignore-missing-schemas", "-summary", manifest],
            capture_output=True,
            text=True,
        )
        assert result.returncode == 0, (
            f"kubeconform failed for {manifest}:\n{result.stdout}\n{result.stderr}"
        )


class TestNamespace:

    @pytest.fixture(scope="class")
    def doc(self):
        return _load_manifest("base/namespace.yaml")

    def test_name(self, doc):
        assert doc["metadata"]["name"] == "capture"

    def test_labels(self, doc):
        labels = doc["metadata"]["labels"]
        assert labels["app.kubernetes.io/part-of"] == "capture-framework"


class TestNFS:

    @pytest.fixture(scope="class")
    def pv(self):
        return _load_manifest("base/nfs-pv.yaml")

    @pytest.fixture(scope="class")
    def pvc(self):
        return _load_manifest("base/nfs-pvc.yaml")

    def test_pv_mount_options(self, pv):
        opts = pv["spec"]["mountOptions"]
        assert "nfsvers=4.1" in opts
        assert "hard" in opts
        assert "intr" in opts

    def test_pv_access_mode(self, pv):
        assert "ReadWriteMany" in pv["spec"]["accessModes"]

    def test_pv_nfs_path(self, pv):
        assert pv["spec"]["nfs"]["path"] == "/volume1/incoming"

    def test_pv_no_hardcoded_ip(self, pv):
        server = pv["spec"]["nfs"]["server"]
        try:
            ipaddress.ip_address(server)
            pytest.fail(f"NFS server should be a DNS name, not IP: {server}")
        except ValueError:
            pass  # not an IP -- good

    def test_pv_reclaim_policy(self, pv):
        assert pv["spec"]["persistentVolumeReclaimPolicy"] == "Retain"

    def test_pvc_namespace(self, pvc):
        assert pvc["metadata"]["namespace"] == "capture"

    def test_pvc_volume_binding(self, pvc):
        assert pvc["spec"]["volumeName"] == "incoming-nfs-pv"

    def test_pvc_access_mode(self, pvc):
        assert "ReadWriteMany" in pvc["spec"]["accessModes"]

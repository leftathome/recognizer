"""Validate Layer 1 manifests: NFD rules, SDM config, Cilium policies, ExternalSecrets."""
import os
import re
import pytest
import yaml

MANIFESTS_DIR = os.path.join(os.path.dirname(__file__), "..", "manifests")


def _load(relpath):
    with open(os.path.join(MANIFESTS_DIR, relpath)) as f:
        return yaml.safe_load(f)


def _load_all(relpath):
    with open(os.path.join(MANIFESTS_DIR, relpath)) as f:
        return list(yaml.safe_load_all(f))


# -- NFD custom rules (.4) --

class TestNFDRules:

    @pytest.fixture(scope="class")
    def config(self):
        cm = _load("nfd/nfd-worker-config.yaml")
        return yaml.safe_load(cm["data"]["nfd-worker.conf"])

    def test_usb_class_whitelist(self, config):
        classes = config["sources"]["usb"]["deviceClassWhitelist"]
        assert "08" in classes, "Mass storage class missing"
        assert "ff" in classes, "Vendor-specific class missing"

    def test_pioneer_rule_vendor_id(self, config):
        pioneer = next(r for r in config["rules"] if r["name"] == "pioneer-bdr-xs07uhd")
        usb_match = pioneer["matchFeatures"][0]["matchExpressions"]
        assert "07e8" in usb_match["vendor"]["value"]

    def test_pioneer_rule_label(self, config):
        pioneer = next(r for r in config["rules"] if r["name"] == "pioneer-bdr-xs07uhd")
        assert pioneer["labels"]["openclaw.io/device-optical-drive"] == "pioneer-bdr-xs07uhd"

    def test_epson_rule_vendor_id(self, config):
        epson = next(r for r in config["rules"] if r["name"] == "epson-ds-1630")
        usb_match = epson["matchFeatures"][0]["matchExpressions"]
        assert "04b8" in usb_match["vendor"]["value"]

    def test_epson_rule_label(self, config):
        epson = next(r for r in config["rules"] if r["name"] == "epson-ds-1630")
        assert epson["labels"]["openclaw.io/device-scanner"] == "epson-ds-1630"


# -- Smarter Device Manager config (.5) --

class TestSDMConfig:

    @pytest.fixture(scope="class")
    def devices(self):
        cm = _load("device-plugin/smarter-device-manager.yaml")
        return yaml.safe_load(cm["data"]["conf.yaml"])

    def test_sr_device_match(self, devices):
        sr = next(d for d in devices if "sr" in d["devicematch"])
        assert re.compile(sr["devicematch"]).match("sr0")
        assert sr["nummaxdevices"] == 1

    def test_sg_device_match(self, devices):
        sg = next(d for d in devices if "sg" in d["devicematch"])
        assert re.compile(sg["devicematch"]).match("sg0")
        assert sg["nummaxdevices"] == 1

    def test_usb_bus_device_match(self, devices):
        usb = next(d for d in devices if "bus/usb" in d["devicematch"])
        assert re.search(usb["devicematch"], "bus/usb/001/002")


# -- Cilium network policies (.6) --

class TestCiliumPolicies:

    @pytest.fixture(scope="class")
    def policy(self):
        return _load("base/network-policies.yaml")

    def test_selects_capture_pods(self, policy):
        selector = policy["spec"]["endpointSelector"]["matchLabels"]
        assert selector["app.kubernetes.io/part-of"] == "capture-framework"

    def test_egress_nfs(self, policy):
        nfs_rule = next(
            r for r in policy["spec"]["egress"]
            if "toCIDR" in r
        )
        ports = [p["port"] for ep in nfs_rule["toPorts"] for p in ep["ports"]]
        assert "2049" in ports

    def test_egress_notification_relay(self, policy):
        relay_rule = next(
            r for r in policy["spec"]["egress"]
            if "toEndpoints" in r and any(
                ep.get("matchLabels", {}).get("app") == "notification-relay"
                for ep in r["toEndpoints"]
            )
        )
        ports = [p["port"] for ep in relay_rule["toPorts"] for p in ep["ports"]]
        assert "8080" in ports

    def test_egress_metadata_apis(self, policy):
        fqdn_rule = next(
            r for r in policy["spec"]["egress"]
            if "toFQDNs" in r
        )
        fqdns = [f["matchName"] for f in fqdn_rule["toFQDNs"]]
        assert "www.omdbapi.com" in fqdns
        assert "musicbrainz.org" in fqdns
        ports = [p["port"] for ep in fqdn_rule["toPorts"] for p in ep["ports"]]
        assert "443" in ports

    def test_egress_dns(self, policy):
        dns_rule = next(
            r for r in policy["spec"]["egress"]
            if "toEndpoints" in r and any(
                ep.get("matchLabels", {}).get("k8s-app") == "kube-dns"
                for ep in r["toEndpoints"]
            )
        )
        ports = [p["port"] for ep in dns_rule["toPorts"] for p in ep["ports"]]
        assert "53" in ports

    def test_only_four_egress_rules(self, policy):
        assert len(policy["spec"]["egress"]) == 4, (
            "Expected exactly 4 egress rules (NFS, relay, metadata APIs, DNS)"
        )


# -- ExternalSecrets (.7) --

class TestExternalSecrets:

    @pytest.fixture(scope="class")
    def ripper_secrets(self):
        return _load_all("optical-ripper/external-secret.yaml")

    @pytest.fixture(scope="class")
    def relay_secrets(self):
        return _load_all("notification-relay/external-secret.yaml")

    def test_makemkv_secret_target_name(self, ripper_secrets):
        makemkv = next(s for s in ripper_secrets if s["spec"]["target"]["name"] == "makemkv-license")
        keys = [d["secretKey"] for d in makemkv["spec"]["data"]]
        assert "license-key" in keys

    def test_omdb_secret_target_name(self, ripper_secrets):
        omdb = next(s for s in ripper_secrets if s["spec"]["target"]["name"] == "omdb-api")
        keys = [d["secretKey"] for d in omdb["spec"]["data"]]
        assert "api-key" in keys

    def test_ripper_secrets_namespace(self, ripper_secrets):
        for s in ripper_secrets:
            assert s["metadata"]["namespace"] == "capture"

    def test_ripper_secrets_use_cluster_secret_store(self, ripper_secrets):
        for s in ripper_secrets:
            ref = s["spec"]["secretStoreRef"]
            assert ref["kind"] == "ClusterSecretStore"
            assert ref["name"] == "onepassword-connect"

    def test_relay_secret_target_name(self, relay_secrets):
        es = relay_secrets[0]
        assert es["spec"]["target"]["name"] == "notification-relay-tokens"

    def test_relay_secrets_namespace(self, relay_secrets):
        for s in relay_secrets:
            assert s["metadata"]["namespace"] == "capture"

    def test_no_plaintext_secrets(self, ripper_secrets, relay_secrets):
        """Ensure no secret values are hardcoded in the manifests."""
        for s in ripper_secrets + relay_secrets:
            for d in s["spec"]["data"]:
                assert "remoteRef" in d, f"Secret {d['secretKey']} missing remoteRef (would be plaintext)"

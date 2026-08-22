"""Chart render tests (C6): `helm template` across value permutations.

These are the regression guard for the selector-matches-zero-pods bug (B1),
the `capture`-namespace fossils (B6), the PSS-hardening + automount-token
sweep (B2/B7), the glovebox token-as-file plumbing (B4), and the metrics
wiring (C4). Every assertion here fails loudly against the *rendered*
manifests -- it never inspects or edits chart templates directly, so a
failure here means the chart's actual output regressed, not that this test
guessed wrong about template syntax.

Skips cleanly (whole module) if `helm` isn't on PATH -- these tests need to
actually invoke `helm template`, not just parse YAML fixtures.
"""
import json
import os
import re
import shutil
import subprocess

import pytest
import yaml

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
CHART_DIR = os.path.join(REPO_ROOT, "charts", "recognizer")
KUBE_VERSION = "1.34.0"

HELM = shutil.which("helm")
if HELM is None:
    pytest.skip("helm not found on PATH; skipping chart render tests", allow_module_level=True)

# The five value permutations from docs/analysis/action-plan.md §0 / C6.
PERMUTATIONS = {
    "defaults": [],
    "hardware": [
        "hardware.enabled=true",
        "networkPolicies.enabled=true",
    ],
    "glovebox_ingest": [
        "archiveImporter.enabled=true",
        "archiveImporter.gloveboxIngest.enabled=true",
    ],
    "walhelm_source": [
        "walhelmSource.enabled=true",
        "walhelmSource.subjectPrincipal=walhelm:test",
    ],
    "everything": [
        "hardware.enabled=true",
        "networkPolicies.enabled=true",
        "archiveImporter.enabled=true",
        "archiveImporter.gloveboxIngest.enabled=true",
        "walhelmSource.enabled=true",
        "walhelmSource.subjectPrincipal=walhelm:test",
    ],
}

# The one workload this chart privileges on purpose: ARM needs
# `privileged: true` + hostPath /dev in the hardware namespace to
# self-discover the optical drive regardless of dynamic /dev/sr*, /dev/sg*
# enumeration order (see networkpolicies-hardware.yaml and
# optical-ripper/daemonset.yaml comments). Every other pod template must be
# PSS=restricted-shaped.
PRIVILEGED_EXCEPTION_COMPONENT = "optical-ripper"

DEAD_LETTER_METRIC = "capture_notification_dead_letter_total"
DEAD_LETTER_ALERT = "NotificationDeadLetterBacklog"


def _helm_template(extra_sets):
    cmd = [HELM, "template", CHART_DIR, "--kube-version", KUBE_VERSION]
    for s in extra_sets:
        cmd += ["--set", s]
    result = subprocess.run(cmd, capture_output=True, text=True, cwd=REPO_ROOT)
    assert result.returncode == 0, (
        f"`helm template` failed for --set {extra_sets!r}:\n"
        f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
    )
    return result.stdout


def _pod_templates(docs):
    """Yield (doc, namespace, pod_template_labels, pod_spec) for every
    workload kind that owns a pod template."""
    for d in docs:
        kind = d.get("kind")
        if kind in ("Deployment", "DaemonSet", "StatefulSet", "Job"):
            tmpl = d.get("spec", {}).get("template", {})
        elif kind == "CronJob":
            tmpl = d.get("spec", {}).get("jobTemplate", {}).get("spec", {}).get("template", {})
        else:
            continue
        namespace = d.get("metadata", {}).get("namespace")
        labels = tmpl.get("metadata", {}).get("labels", {}) or {}
        pod_spec = tmpl.get("spec", {}) or {}
        yield d, namespace, labels, pod_spec


def _doc_label(d):
    return f"{d.get('kind')}/{d.get('metadata', {}).get('name')}"


@pytest.fixture(scope="module")
def renders():
    """Render every value permutation once per test module.

    Returns {label: {"text": raw stdout, "docs": [doc, ...] (nulls
    dropped), "sets": the --set list used}}.
    """
    out = {}
    for label, sets in PERMUTATIONS.items():
        text = _helm_template(sets)
        docs = [d for d in yaml.safe_load_all(text) if d]
        out[label] = {"text": text, "docs": docs, "sets": sets}
    return out


# -- B1 regression guard: CNP endpointSelector actually selects real pods --


def test_cnp_selector_is_subset_of_every_pod_in_its_namespace(renders):
    for label, r in renders.items():
        docs = r["docs"]
        cnps = [d for d in docs if d.get("kind") == "CiliumNetworkPolicy"]
        assert cnps, f"[{label}] expected at least one rendered CiliumNetworkPolicy"
        pod_templates = list(_pod_templates(docs))
        for cnp in cnps:
            ns = cnp["metadata"]["namespace"]
            sel = cnp.get("spec", {}).get("endpointSelector", {}).get("matchLabels", {})
            pods_in_ns = [(d, labels) for d, pod_ns, labels, _ in pod_templates if pod_ns == ns]
            assert pods_in_ns, (
                f"[{label}] CNP {cnp['metadata']['name']} lives in namespace {ns!r} "
                f"but no rendered pod template is in that namespace to select"
            )
            for d, labels in pods_in_ns:
                mismatched = {
                    k: (v, labels.get(k))
                    for k, v in sel.items()
                    if labels.get(k) != v
                }
                assert not mismatched, (
                    f"[{label}] CNP {cnp['metadata']['name']}'s endpointSelector "
                    f"{sel} is not a subset of pod {_doc_label(d)}'s labels {labels} "
                    f"(mismatched keys: {mismatched}) -- this selector would match "
                    f"zero pods, reviving the original bug"
                )


# -- B6 regression guard: no `capture`-namespace fossils --


def _find_capture_namespace_refs(obj, path="", in_namespace_ctx=False):
    """Recursively find string values == 'capture' that sit anywhere under a
    key whose name mentions 'namespace' (covers metadata.namespace, Cilium's
    `k8s:io.kubernetes.pod.namespace` matchLabels, and ServiceMonitor's
    namespaceSelector.matchNames list)."""
    found = []
    if isinstance(obj, dict):
        for k, v in obj.items():
            ctx = in_namespace_ctx or "namespace" in str(k).lower()
            key_path = f"{path}.{k}" if path else str(k)
            if isinstance(v, str):
                if ctx and v == "capture":
                    found.append(key_path)
            else:
                found.extend(_find_capture_namespace_refs(v, key_path, ctx))
    elif isinstance(obj, list):
        for i, item in enumerate(obj):
            if isinstance(item, str):
                if in_namespace_ctx and item == "capture":
                    found.append(f"{path}[{i}]")
            else:
                found.extend(_find_capture_namespace_refs(item, f"{path}[{i}]", in_namespace_ctx))
    return found


def test_no_capture_namespace_fossils(renders):
    for label, r in renders.items():
        assert "capture.svc" not in r["text"], (
            f"[{label}] rendered manifests still reference the retired capture.svc host"
        )
        for d in r["docs"]:
            hits = _find_capture_namespace_refs(d)
            assert not hits, (
                f"[{label}] {_doc_label(d)} references namespace 'capture' at {hits}"
            )


# -- B2/B7 regression guard: pod hardening --


def test_pod_security_context_hardening(renders):
    """Every pod template sets securityContext.runAsNonRoot: true, except the
    documented privileged optical-ripper pod. A new pod that lacks
    runAsNonRoot and isn't labeled component=optical-ripper fails here
    immediately -- this is the "new unhardened pod" guard.
    """
    saw_documented_exception = False
    for label, r in renders.items():
        for d, ns, labels, pod_spec in _pod_templates(r["docs"]):
            comp = labels.get("app.kubernetes.io/component")
            sc = pod_spec.get("securityContext") or {}
            if sc.get("runAsNonRoot") is True:
                continue
            assert comp == PRIVILEGED_EXCEPTION_COMPONENT, (
                f"[{label}] {_doc_label(d)} (component={comp!r}) does not set "
                f"securityContext.runAsNonRoot: true and is not the documented "
                f"privileged optical-ripper exception -- looks like a new "
                f"unhardened pod"
            )
            saw_documented_exception = True
    assert saw_documented_exception, (
        "expected the optical-ripper pod's documented runAsNonRoot exception to "
        "actually appear in at least one rendered permutation -- fixture or "
        "chart behavior may have changed underneath this test"
    )


def test_pod_automount_service_account_token_false(renders):
    for label, r in renders.items():
        for d, ns, labels, pod_spec in _pod_templates(r["docs"]):
            assert pod_spec.get("automountServiceAccountToken") is False, (
                f"[{label}] {_doc_label(d)} does not set "
                f"automountServiceAccountToken: false"
            )
        for d in r["docs"]:
            if d.get("kind") == "ServiceAccount":
                assert d.get("automountServiceAccountToken") is False, (
                    f"[{label}] ServiceAccount {d['metadata']['name']} does not set "
                    f"automountServiceAccountToken: false"
                )


# -- C4 regression guard: monitoring wiring only references what exists --


def test_servicemonitor_ports_subset_of_service_ports(renders):
    for label, r in renders.items():
        docs = r["docs"]
        service_ports = set()
        for d in docs:
            if d.get("kind") == "Service":
                for p in d.get("spec", {}).get("ports", []):
                    if p.get("name"):
                        service_ports.add(p["name"])
        service_monitors = [d for d in docs if d.get("kind") == "ServiceMonitor"]
        for sm in service_monitors:
            sm_ports = {
                e.get("port") for e in sm.get("spec", {}).get("endpoints", []) if e.get("port")
            }
            missing = sm_ports - service_ports
            assert not missing, (
                f"[{label}] ServiceMonitor {sm['metadata']['name']} scrapes port(s) "
                f"{missing} that no rendered Service names (available: {service_ports})"
            )


def test_prometheusrule_namespaces_and_deadletter_metric(renders):
    metrics_py_path = os.path.join(
        REPO_ROOT, "images", "notification-relay", "relay", "metrics.py"
    )
    with open(metrics_py_path) as f:
        metrics_src = f.read()
    assert DEAD_LETTER_METRIC in metrics_src, (
        f"test fixture assumption broken: {DEAD_LETTER_METRIC!r} is no longer "
        f"present in {metrics_py_path}"
    )

    namespace_expr_re = re.compile(r'namespace=~?"([^"]+)"')

    for label, r in renders.items():
        docs = r["docs"]
        rendered_namespaces = {
            d["metadata"]["namespace"]
            for d in docs
            if d.get("metadata", {}).get("namespace")
        }
        rules = [d for d in docs if d.get("kind") == "PrometheusRule"]
        assert rules, f"[{label}] expected a rendered PrometheusRule"
        saw_deadletter_alert = False
        for rule_doc in rules:
            for group in rule_doc.get("spec", {}).get("groups", []):
                for rule in group.get("rules", []):
                    if "alert" not in rule:
                        continue
                    expr = rule.get("expr", "")
                    for m in namespace_expr_re.finditer(expr):
                        ns_filter = m.group(1)
                        # Alternation (release|hardware namespace) is expected;
                        # every literal alternative must be a namespace this
                        # chart actually rendered into.
                        for part in ns_filter.split("|"):
                            assert part in rendered_namespaces, (
                                f"[{label}] alert {rule['alert']!r} expr filters on "
                                f"namespace {part!r}, which is neither the release "
                                f"nor the hardware namespace rendered here "
                                f"({rendered_namespaces}): {expr}"
                            )
                    if rule.get("alert") == DEAD_LETTER_ALERT:
                        saw_deadletter_alert = True
                        assert DEAD_LETTER_METRIC in expr, (
                            f"[{label}] alert {DEAD_LETTER_ALERT!r} expr does not "
                            f"reference {DEAD_LETTER_METRIC!r}: {expr}"
                        )
        assert saw_deadletter_alert, (
            f"[{label}] expected to find the {DEAD_LETTER_ALERT!r} alert in the "
            f"rendered PrometheusRule"
        )


# -- B4 regression guard: glovebox token is file-mounted, not env secretKeyRef --


def test_glovebox_token_is_file_mounted_in_both_cronjobs(renders):
    r = renders["everything"]
    cronjobs = {d["metadata"]["name"]: d for d in r["docs"] if d.get("kind") == "CronJob"}
    seen_token_file_in = []
    for name, d in cronjobs.items():
        containers = d["spec"]["jobTemplate"]["spec"]["template"]["spec"]["containers"]
        for c in containers:
            for e in c.get("env", []):
                if e.get("name") == "GLOVEBOX_INGEST_TOKEN":
                    assert "secretKeyRef" not in e, (
                        f"CronJob {name} container {c['name']} still injects "
                        f"GLOVEBOX_INGEST_TOKEN via secretKeyRef env -- the token "
                        f"must be file-mounted (B4), re-read per delivery"
                    )
                if e.get("name") == "GLOVEBOX_INGEST_TOKEN_FILE":
                    seen_token_file_in.append(name)

    for expected_component in ("archive-importer", "walhelm-fetch"):
        assert any(expected_component in n for n in seen_token_file_in), (
            f"expected a CronJob whose name contains {expected_component!r} to set "
            f"GLOVEBOX_INGEST_TOKEN_FILE; CronJobs that did: {seen_token_file_in}"
        )


# -- C1 drift guard: inlined post_rip.py copy stays byte-identical --


def test_post_rip_hook_configmap_matches_source_file(renders):
    r = renders["hardware"]
    hook_cm = None
    for d in r["docs"]:
        if d.get("kind") == "ConfigMap" and d["metadata"]["name"].endswith("-arm-hook"):
            hook_cm = d
            break
    assert hook_cm is not None, (
        "expected an *-arm-hook ConfigMap when hardware.enabled=true "
        "(opticalRipper.enabled defaults to true)"
    )
    rendered_source = hook_cm.get("data", {}).get("post_rip.py")
    assert rendered_source is not None, (
        f"ConfigMap {hook_cm['metadata']['name']} has no 'post_rip.py' data key"
    )

    src_path = os.path.join(REPO_ROOT, "images", "optical-ripper", "hooks", "post_rip.py")
    with open(src_path) as f:
        actual_source = f.read()

    assert rendered_source == actual_source, (
        f"the arm-hook ConfigMap's inlined post_rip.py has drifted from "
        f"{src_path} -- keep them byte-identical by hand (see hook-configmap.yaml "
        f"header comment) until the chart gets real image-packaged hooks"
    )


# -- C5: Grafana dashboard ConfigMap, if rendered, carries valid JSON --


def test_grafana_dashboard_configmap_json_is_valid(renders):
    checked_any = False
    for label, r in renders.items():
        for d in r["docs"]:
            if d.get("kind") != "ConfigMap":
                continue
            if not d.get("metadata", {}).get("labels", {}).get("grafana_dashboard"):
                continue
            data = d.get("data", {})
            assert data, f"[{label}] Grafana dashboard ConfigMap {d['metadata']['name']} has no data"
            for key, value in data.items():
                checked_any = True
                assert isinstance(value, str), (
                    f"[{label}] ConfigMap {d['metadata']['name']} data key {key!r} "
                    f"did not render as a string (got {type(value).__name__}) -- "
                    f"Kubernetes ConfigMap.data values must be strings"
                )
                try:
                    json.loads(value)
                except json.JSONDecodeError as e:
                    pytest.fail(
                        f"[{label}] ConfigMap {d['metadata']['name']} data key "
                        f"{key!r} is not valid JSON: {e}"
                    )
    if not checked_any:
        pytest.skip("no grafana_dashboard-labeled ConfigMap rendered in any permutation")

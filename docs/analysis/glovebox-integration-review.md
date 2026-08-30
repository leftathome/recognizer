# Glovebox integration review — 2026-08-22

**Scope:** what changed on the glovebox side (release notes, specs, security
assessment) and what recognizer must do to keep up its end of the integration.
Reviewed against glovebox `main` @ `b862f28` (PR #56), the GitHub releases
list, and this repo @ `68b6883` (chart 0.6.2).

**Companion documents:**
- [architecture-implementation-review.md](architecture-implementation-review.md) — designed-vs-built audit of recognizer itself
- [action-plan.md](action-plan.md) — work packets derived from both reviews

> **Update 2026-08-29 — glovebox v0.8.0 is released.** The three changes this
> review flagged as UNRELEASED have shipped, one of them BREAKING. See
> [§8 (v0.8.0 addendum)](#8-v080-addendum--2026-08-29) for what changed, the
> corrections v0.8.0's docs make to §4/§5 below, and the migration sequence.
> Sections 1–7 are preserved as the 2026-08-22 record.

---

## 1. Version reality — read this before acting on anything else

The GitHub release **v0.7.0 (2026-08-05) does NOT contain the mTLS work.**
That tag's `CHANGELOG.md` was fetched and diffed: its `[Unreleased]` section
holds only the `/healthz`+`/readyz` probes, operator-supplied registry files,
and the silent-delivery-stall fix. The v0.7.0 tag number tracks the *Helm
chart* version (`charts/glovebox/Chart.yaml: version: 0.7.0`,
`appVersion: "0.6.1"`); the newest app changelog section is **0.6.4
(2026-06-26)**.

**There is no `v0.6.0` on GitHub — neither a release nor a tag.** GitHub's tags
jump `v0.5.0` → `v0.6.1`. Glovebox withdrew it: its published artifacts carried
household `entity_id` defaults and de-pseudonymizing name comments baked into
the public Helm chart, connector/importer configs, tests and docs. v0.6.1 is
the scrubbed supersession, with *no functional or source change* beyond the
scrub (glovebox `CHANGELOG.md` `[0.6.1]` "Note"; the PII scrub is
`glovebox-0nzk`). The clean v0.6.0 remains on their primary GitLab remote.

So where this document cites **"0.6.0"** it means the changelog *section* — that
work is on GitHub from **v0.6.1 onward**. Also absent as GitHub *releases*
(though the tags exist): `v0.4.0`–`v0.4.3` and `v0.5.0`. Verified against the
GitHub release and tag lists on 2026-08-30.

Everything below marked **UNRELEASED** is on glovebox `main` but in no tagged
release as of 2026-08-22:

- Mutual TLS for `/v1/ingest` (spec 08 §3.10) + Helm wiring (`ingest.tls`)
- The fix for `ingest.tls.mode: required` taking `/v1/archives` and
  `/v1/sanitize` offline (this outage hit *our* uploads in their testing)
- The `ingest.bearer_port` split (fix for security finding P0-7)
- Scanning of recognizer-scan `content.extracted.md` + fail-closed
  `ErrExtractUnscanned`
- Vault TLS verification default flip (`tlsSkipVerify: true → false`)
- `archive_id` all-dots rejection (LOW-10)
- Ruleset provenance / digest pinning; adversarial corpus CI gate

**Posture:** recognizer's job right now is to *prepare* for these in a
coordinated window with the glovebox operator — not to scramble because
something already broke. Nothing in the currently-released glovebox breaks us.

## 2. The "advisory" — what actually exists

There is **no advisory GitHub issue and no GHSA**. Checked 2026-08-22: open
and closed issues on `leftathome/glovebox` and `leftathome/recognizer` (all
zero), and glovebox's published security advisories page (empty). What exists
instead, and what this review treats as the advisory content:

1. **`glovebox/docs/assessments/2026-08-20/`** — a point-in-time security
   review (12 findings + action plan). Two findings name recognizer directly:
   - **P0-7 (cross-namespace widening):** glovebox's
     `archive-networkpolicy.yaml` opens ingest port 9091 to the whole
     recognizer namespace for `/v1/archives`; because `/v1/ingest` shares that
     port, **our namespace also reaches unauthenticated connector intake**.
     The `bearer_port` split fixes this, but it defaults to `0` (shared), so
     the exposure is live in every default install today. Any pod in our
     namespace is inside that blast radius — one more reason our own
     NetworkPolicy (currently a no-op, see the architecture review) matters.
   - **MEDIUM-7 (recognizer-scan OCR text unscanned):** `tree/ocr.txt` from a
     recognizer-scan archive was rendered to `content.extracted.md` and
     consumed by the operator agent **without passing glovebox's injection
     engine** — OCR of a hostile physical document (a printed prompt
     injection) went straight to an agent. Fixed on `main`: the text is now
     scanned, a quarantine verdict withholds the body, and finalize **fails
     closed** (`ErrExtractUnscanned`) if no scanner is configured.
2. **`glovebox/docs/handoffs/recognizer-archive-delivery.md`** — the producer
   contract written explicitly for this team ("Audience: humans + LLMs on the
   recognizer team"). It is the document we are expected to track, and it is
   currently **stale** (see §6).

## 3. How recognizer integrates today (verified in this repo)

Two producers, both speaking **tus.io v1.0.0 over plaintext HTTP with a
static bearer token** to `/v1/archives`:

| | archive-importer | walhelm-fetch |
|---|---|---|
| Template | `charts/recognizer/templates/archive-importer/cronjob.yaml` | `charts/recognizer/templates/walhelm-fetch/cronjob.yaml` |
| URL (values.yaml) | `http://glovebox.glovebox.svc.cluster.local:9091` | same |
| source_id | `recognizer-smoke-test` (prod: negotiated) | `recognizer-walhelm` |
| Token | Vault `secret/glovebox/ingest-tokens/<source-id>` via ESO → **env var** `GLOVEBOX_INGEST_TOKEN` | same |
| Payload | `archive/google-takeout-subtree` (tar) | `archive/walhelm-export` (tar, PHI) with spec-15 provenance keys |
| Client | `images/archive-importer/internal/delivery/client.go` — Bearer header on every POST/HEAD/PATCH, 32 MiB chunks, 303-replay handling | same client |

A third lane exists on the glovebox side waiting for us:
`archive/recognizer-scan` (the document scanner's output), gated fail-closed
to source-id **`recognizer-scanner`** in glovebox's `sources.json`, with the
contract in `glovebox/docs/superpowers/plans/2026-06-15-recognizer-scanner-connector.md`
(tar root must contain `manifest.json`, the OCR'd PDF/A, WebP proxies, and
`ocr.txt` as UTF-8 plaintext at tar root; extraction is **recognizer's** job).
Our scanner is nowhere near producing this yet (see the architecture review).

Prerequisite that lives outside both charts: the recognizer namespace must be
labeled `name=openclaw-recognizer` or glovebox's NetworkPolicy drops us at L4
(TCP timeouts, not 401s).

## 4. mTLS: what it does and does not mean for us

**mTLS covers `/v1/ingest` only.** Glovebox's own doc
(`docs/ingest-mtls.md`) is explicit: `/v1/archives*` and `/v1/sanitize` stay
on spec-10 bearer tokens ("Not yet covered — a later spec can retire the
tokens or keep them as a second factor for archive-scale sources"). Since
both our producers use `/v1/archives`, **we do not need client certificates
today**, and the user-visible headline "they added mTLS to ingest" translates
for us into three concrete, indirect obligations:

1. **The `required`-mode outage.** On a glovebox build with the mTLS work but
   *without* the follow-up `planPlaintextListeners` fix, setting
   `ingest.tls.mode: required` closes the plaintext listener that
   `/v1/archives` rides on — our uploads die with `connection refused` and
   nothing in any log. Glovebox's changelog describes exactly this happening
   to "the recognizer's uploads". **Obligation:** before the operator flips
   any `ingest.tls.mode`, verify the deployed glovebox contains the fix
   (bearer surface served in all three modes; under `required`, `/v1/ingest`
   answers 404 on the shared listener).
2. **The `bearer_port` split (coordinated migration).** New
   `config.ingest.bearerPort` moves `/v1/archives*`+`/v1/sanitize` onto their
   own listener. It defaults to `0` (= share 9091) precisely because *we* are
   configured against 9091. When the operator sets it (e.g. 9093), our
   `gloveboxIngest.url` values must change **in the same window**. Our chart
   already takes the full URL from values (good — no code change needed), but
   the migration must be written down and the two chart releases coordinated.
3. **Future producer identity.** `spiffe://glovebox/producer/recognizer` is
   documented in glovebox's mTLS doc and the `producer` kind is accepted by
   their Go SAN parser — but **their chart has no `producer` certificate
   template** (only server, per-connector, Schoology, per-importer). If/when
   archives move to mTLS or we ever use `/v1/ingest`, our cert must be issued
   out-of-band (cert-manager `Certificate` in *our* chart against their
   `glovebox-ingest-ca` ClusterIssuer, SPIFFE URI SAN, 24h duration / 8h
   renewBefore, TLS 1.3, key+cert re-read on rotation). Worth filing upstream
   now so the gap is on their books.

## 5. Released + unreleased changes that DO affect our path

| Change | Released? | Impact on recognizer | Action |
|---|---|---|---|
| 60s `ReadTimeout` killed >60s archive PATCHes (fixed 0.6.4) | **0.6.4** | Any multi-GB upload against ≤0.6.3 breaks with `curl (55)` broken pipe | Pin **minimum glovebox app 0.6.4** in our integration docs |
| `required`-mode outage fix | UNRELEASED | See §4.1 | Gate any TLS-mode change on this fix being deployed |
| `bearer_port` split | UNRELEASED | See §4.2 | Document + coordinate; keep URL fully value-driven |
| Vault `tlsSkipVerify: true → false` | UNRELEASED | If glovebox's Vault uses a self-signed CA and no `caSecret` is set, **token lookup fails and every one of our uploads 401s/503s** after their upgrade | Confirm with the operator that `caSecret` is set before they upgrade |
| recognizer-scan OCR scanning + `ErrExtractUnscanned` fail-closed | UNRELEASED | When we ship `archive/recognizer-scan`: a quarantine verdict withholds the extracted body; a glovebox with no scanner configured **rejects our finalize** instead of accepting silently | Design scanner delivery + retry/alerting around a finalize that can now fail for content reasons |
| `archive_id` all-dots rejected (LOW-10) | UNRELEASED | Our IDs are timestamp/uuid-shaped; no impact, but validation tightened | Keep `archive_id` within `^[a-zA-Z0-9._-]{1,128}$`, never dots-only |
| Media-type allowlist now 6 entries (`mbox`, `google-takeout-subtree`, `generic-tarball`, `imap-export`, `walhelm-export`, `recognizer-scan`) | mixed (0.6.0 + later) | Handoff doc lists only 4 | Track code, not the handoff doc, until upstream refreshes it |
| spec-15 provenance keys required for `archive/walhelm-export`: `acq_provider`, `acq_account_id`, `acq_auth_method` (enum: exactly `browser_session`), `data_subject` (`walhelm:<id>`, never a name), optional `audience` | 0.6.0 | walhelm-fetch already sends these (covered by `run_e2e_test.go`) | No change; keep the e2e test asserting the headers |
| Known-subjects registry, fail-closed `subject_unresolved` quarantine when `enforce: true` | 0.6.0 | Our walhelm `data_subject` principals must be registered glovebox-side before enforcement turns on | Coordinate principal registration (our bead `archiver-vry` is exactly this; still open) |
| Tarballs must be **uncompressed** in v1 | 0.6.0 | `.tar.gz` fails at finalize (`invalid tar header`) | Already compliant; assert in tests |

("0.6.0" rows above: that tag was withdrawn from GitHub — the work is
available there from **v0.6.1** onward. See §1.)
| Token rotation semantics: ESO refresh (1m) + glovebox in-process reload (300s) ≈ up to ~6 min propagation; handoff says "re-read the token on every send" | n/a | We inject the token as an **env var**, cached for the pod's life. Fine for short-lived CronJob pods; wrong if any producer becomes long-running | Move token to a mounted file, re-read per delivery (also fixes the env-var exposure finding) |
| Resource sizing: 12 GiB upload peaked ~3.0 GiB server-side; glovebox values say "raise it for the recognizer's expected concurrent set" | n/a | Our concurrency (per-source cap 4) shapes their memory | Tell the operator our expected concurrent upload profile |

## 6. Upstream issues worth filing (with the operator's blessing)

Glovebox's handoff doc invites this explicitly ("File an issue if you spot a
discrepancy"):

1. `docs/handoffs/recognizer-archive-delivery.md` staleness: lists 4 of 6
   media types, no `bearerPort` section, no `required`-mode caveat, no
   `archive/recognizer-scan` section, references chart 0.4.2.
2. No `producer`-kind certificate template in the glovebox chart despite
   `spiffe://glovebox/producer/recognizer` being documented (§4.3).
3. Doc/chart mount-path discrepancy for mTLS env files
   (`/etc/glovebox/tls/` in docs vs `/etc/ingest-tls/` in the chart) — will
   bite whoever wires our future producer cert.

## 7. Obligations checklist (our end of the integration)

- [ ] Pin **glovebox app ≥ 0.6.4** as the supported floor in docs and values comments (multi-GB uploads).
- [ ] Replace the stale "v0.7.0" framing anywhere it appears: mTLS is unreleased; v0.7.0 is a chart tag.
- [x] Keep `gloveboxIngest.url` fully operator-configurable (already true); add an explicit values comment about the `bearerPort` migration and the `required`-mode precondition. **Port moved to 9093 for v0.8.0 — see §8.1.**
- [ ] Move `GLOVEBOX_INGEST_TOKEN` from env injection to a read-only file mount, re-read per delivery (rotation + `/proc/<pid>/environ` exposure).
- [ ] Add optional TLS support to the delivery client (accept `https://` URLs + optional CA bundle file) so the bearer listener can be encrypted the day glovebox offers it — today the transport is plaintext HTTP carrying PHI and a bearer token.
- [ ] Fix our own CiliumNetworkPolicy so egress to glovebox:9091 is actually permitted-and-constrained (today the policy selects zero pods — see the architecture review).
- [ ] Coordinate with the glovebox operator before their Vault `tlsSkipVerify` flip (confirm `caSecret`).
- [ ] Register walhelm subject principals glovebox-side before `subjects.json` enforcement turns on (`archiver-vry`).
- [ ] Design the `archive/recognizer-scan` producer (source-id `recognizer-scanner`, `ocr.txt` at tar root) with fail-closed finalize in mind — blocked on the scanner actually working (see architecture review §3). Client-side groundwork done: the media type is on our allow-list and finalize content failures are non-retryable (§8.3).
- [x] File the three upstream issues in §6 — filed 2026-08-22 with sign-off: **glovebox#65** (handoff-doc drift), **glovebox#69** (no `producer` cert template), **glovebox#70** (mTLS client mount-path doc/chart mismatch).

---

## 8. v0.8.0 addendum — 2026-08-29

glovebox **v0.8.0** shipped 2026-08-29. Everything §1 listed as UNRELEASED is
now released. Two upstream documents are authoritative and supersede the
auto-generated release notes (which do not call out the breaking change):
[`docs/upgrading.md`](https://github.com/leftathome/glovebox/blob/v0.8.0/docs/upgrading.md)
and `CHANGELOG.md` `[0.8.0]`. Raised for us in recognizer#5.

### 8.1 BREAKING — bearer endpoints moved to port 9093

`config.ingest.bearerPort` now defaults to **9093**: `/v1/archives*` and
`/v1/sanitize` have their own listener, 9091 serves only `/v1/ingest`.

**Done in this repo:** both producers' `gloveboxIngest.url` and
`gloveboxIngest.egress.port` are 9093.

**There is no overlap window.** Glovebox's archive NetworkPolicy grants exactly
one port, aimed at whichever listener the bearer endpoints are on, so 9091 and
9093 are never simultaneously reachable from our namespace; the old port times
out rather than redirecting. Sequence: agree window → operator upgrades
(archives unreachable from here) → we roll out the new URL → verify.

In-flight uploads do not survive the operator's restart; partial upload state
does (RWO PVC), so recovery is the ordinary resume — HEAD the upload-id
**against the new port** and continue from `Upload-Offset`.

Which layout a cluster is on, without asking:

```bash
kubectl get svc -n glovebox glovebox-glovebox-ingest -o jsonpath='{.spec.ports[*].name}{"\n"}'
# "ingest ingest-bearer" -> split, use 9093 | "ingest" -> shared, use 9091
```

`bearerPort: 0` restores the shared layout but re-opens the P0-7 exposure — a
migration aid, not a supported configuration.

### 8.2 Vault TLS verification on by default — correction to §5

§5 predicted this "401s/503s every upload". Impact right, details wrong:
**the symptom is 503, not 401, and it is not silent.** Token fetch fails at
boot, the archive listener mounts a deliberate 503 fallback, and the cause is
logged:

```
glovebox vault k8s login failed: <err> (archive listener will mount 503 fallback)
```

It does not retry into a good state — the operator sets
`ingest.auth.vault.caSecret` (a Secret holding the CA bundle under `ca.crt`)
and restarts the pod. Operator-side, but confirm it before their upgrade.

### 8.3 `archive/recognizer-scan` finalize — correction to §5

We recorded the fail-closed error as `ErrExtractUnscanned` ("no scanner
configured"). That exists in glovebox's code but is **unreachable in the
shipped binary** — glovebox refuses to start without a scanner. The failure we
will actually hit is **`ErrScanMissingOCR`**: a missing or whitespace-only
`ocr.txt` at the tar root. Both surface as an opaque `500 internal_finalize`,
so the response body cannot distinguish them (upstream ergonomics:
glovebox#71, open by design).

**Done in this repo** (`internal/delivery`):

- `archive/recognizer-scan` added to `AllowedMediaTypes` — it was missing, so
  the scanner lane would have failed our own client-side validation before a
  request was ever sent.
- `ErrFinalizeContent`: a 5xx carrying `internal_finalize` is **not retried**.
  Previously the generic 5xx path burned `MaxRetries` re-sending the final
  chunk, re-running a full server-side untar and scan of a multi-GB tarball
  into a verdict that can never change. A 5xx whose body is not glovebox's
  error envelope stays retryable (a truncated or proxy-generated 5xx really is
  transient); both behaviors are regression-tested.

Two consequences to carry into the scanner producer when it exists:

- **Retrying the same `archive_id` after a fix is safe** — a failed finalize
  publishes nothing and cleans up, so a corrected re-POST returns 201, not 409.
- **A 2xx does not mean the text was published.** If the scanner quarantines
  the extracted text, finalize still succeeds but `content.extracted.md` holds
  a stub naming the score and firing signals; the raw text stays at
  `tree/ocr.txt`. Read `content.extracted.md` to know which happened.

### 8.4 Our upstream issues are resolved

All three landed in v0.8.0 and are closed. The chart now exposes
`ingest.tls.producers`, so `spiffe://glovebox/producer/recognizer` can be
minted by the chart if archives ever move to mTLS — closing the §4.3 gap.

Also fixed upstream: the handoff doc previously documented two error codes that
**do not exist in glovebox's source** — `tar_unsupported_entry` and
`quota_exhausted`. Checked: no recognizer code was written against either
(we reference only the real `tar_unsafe_entry`), so we had no dead code. The
real codes are `tar_unsafe_entry` (400) and `storage_hard_cap` (503).

Still open upstream, neither a regression: glovebox#68 (latent cert
Secret-name collision) and glovebox#71 (client-caused finalize failures return
500). App floor is unchanged at **0.6.4**; v0.8.0 is well past it.

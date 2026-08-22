# Glovebox integration review — 2026-08-22

**Scope:** what changed on the glovebox side (release notes, specs, security
assessment) and what recognizer must do to keep up its end of the integration.
Reviewed against glovebox `main` @ `b862f28` (PR #56), the GitHub releases
list, and this repo @ `68b6883` (chart 0.6.2).

**Companion documents:**
- [architecture-implementation-review.md](architecture-implementation-review.md) — designed-vs-built audit of recognizer itself
- [action-plan.md](action-plan.md) — work packets derived from both reviews

---

## 1. Version reality — read this before acting on anything else

The GitHub release **v0.7.0 (2026-08-05) does NOT contain the mTLS work.**
That tag's `CHANGELOG.md` was fetched and diffed: its `[Unreleased]` section
holds only the `/healthz`+`/readyz` probes, operator-supplied registry files,
and the silent-delivery-stall fix. The v0.7.0 tag number tracks the *Helm
chart* version (`charts/glovebox/Chart.yaml: version: 0.7.0`,
`appVersion: "0.6.1"`); the newest app changelog section is **0.6.4
(2026-06-26)**.

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
- [ ] Keep `gloveboxIngest.url` fully operator-configurable (already true); add an explicit values comment about the coming `bearerPort` migration and the `required`-mode precondition.
- [ ] Move `GLOVEBOX_INGEST_TOKEN` from env injection to a read-only file mount, re-read per delivery (rotation + `/proc/<pid>/environ` exposure).
- [ ] Add optional TLS support to the delivery client (accept `https://` URLs + optional CA bundle file) so the bearer listener can be encrypted the day glovebox offers it — today the transport is plaintext HTTP carrying PHI and a bearer token.
- [ ] Fix our own CiliumNetworkPolicy so egress to glovebox:9091 is actually permitted-and-constrained (today the policy selects zero pods — see the architecture review).
- [ ] Coordinate with the glovebox operator before their Vault `tlsSkipVerify` flip (confirm `caSecret`).
- [ ] Register walhelm subject principals glovebox-side before `subjects.json` enforcement turns on (`archiver-vry`).
- [ ] Design the `archive/recognizer-scan` producer (source-id `recognizer-scanner`, `ocr.txt` at tar root) with fail-closed finalize in mind — blocked on the scanner actually working (see architecture review §3).
- [ ] File the three upstream issues in §6 (needs user/operator sign-off; outward-facing).

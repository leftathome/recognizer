# Archive Importer Pattern -- Design Specification

**Version 1.0 -- April 2026**

*This document extends archiver's physical-media capture pattern to digital archive sources. It defines the taxonomy of layered handlers (container unpacker, layout recognizer, format handler, destination), the notification event schema extension that carries archive events across the system, and the repo boundaries that determine where each piece of work lives. This is the architectural foundation for mbox, Google Takeout, Slack export, WhatsApp export, and other finished-source ingest paths.*

---

## 1. Problem Statement

Archiver today captures from **physical external sources**: USB-attached Blu-ray drives, document scanners. A workload runs on-node when the device is plugged in, writes raw output to the NAS, and emits a versioned notification event. Downstream consumers (Plex, OpenClaw agents, OCR pipelines) receive the events and process the raw content independently.

Real-world archive capture also involves **digital finished sources**: a Google Takeout zipfile, a Slack workspace export, a WhatsApp chat archive, an old `.pst` file converted to mbox, a Thunderbird profile. These share archiver's capture/processing separation at a conceptual level -- the content is finite, the container was finished when it arrived, the consumer doesn't trust the content but does trust that the container came from somewhere knowable.

Three properties make the digital case harder than the physical case:

1. **Archives are often composite.** A Google Takeout is a zipfile containing dozens of service subtrees (Mail, Calendar, Keep, YouTube, Chat, Gemini, NotebookLM, ...), each with its own format (mbox, ics, HTML+JSON, per-service JSON schemas). There is no single "format" to handle.

2. **Destinations diverge.** Mail-shaped content flows to glovebox; photo-shaped content flows to Immich; location timelines go to a purpose-built service; many sub-archives have no subscriber yet. A single handler cannot know every destination.

3. **Scale is bursty.** A 12 GB mbox with 500,000 messages arriving in one batch is qualitatively different from 50 new messages per day arriving through an IMAP connector. Ingest-side pipelines sized for a steady trickle can be overwhelmed by a single imported archive.

The goal of this spec is to establish a pattern that handles composite archives, routes content to appropriate destinations, and cleanly separates concerns so each layer can evolve and be added independently.

## 2. Prior Art: Archiver's Physical Capture Pattern

Archiver's existing pattern (see `plan.md`, Section 3.3) is:

```
[Physical media plugged in]
       |
       v
[Capture workload on node]
       |
       v
[Raw content written to NAS at output_path]
       |
       v
[Versioned notification event POSTed to relay]
       |
       v
[Relay fans out to subscribers by media_type]
       |
       v
[Downstream processors read raw from NAS]
```

Key design decisions preserved in the digital extension:

- **Capture writes raw, processing reads raw.** The capturer never transforms; every lossy operation happens downstream. Re-processing the same archive with a new handler is always possible because the original is preserved.
- **Events are pointers, not payloads.** Notifications carry `output_path`, `media_type`, and metadata; the content stays on storage. Keeps events small, lets many consumers process the same raw archive independently.
- **Schemas are versioned.** Every notification and every side-car manifest declares `schema_version`; consumers check before parsing.
- **Versioned media types are the routing key.** Consumers subscribe by `media_type`. Adding a new source type means adding enum values; adding a new consumer means adding a subscriber.

The digital extension keeps all four and generalizes the first step: instead of "physical media plugged in -> capture workload runs," the trigger can be "archive file lands on storage -> recognizer runs."

## 3. Taxonomy of Layers

Archive handling decomposes into four independent layers. Each emits events that the next layer consumes; each layer can be built, tested, and replaced without touching the others.

```
[ Container     ]    unpack zip/tar.gz/7z into a directory tree
[ unpacker      ]    -> emits "unpacked archive at path X"
       |
       v
[ Layout        ]    match the tree against known archive layouts
[ recognizer    ]    (Google Takeout, Slack export, WhatsApp export, ...)
                     -> for each recognized subtree, emit a sub-archive
                        event with a specific media_type
       |
       v
[ Format        ]    parse a specific content format (mbox, ics, vcard,
[ handler       ]    slack-channel-json, whatsapp-chat-txt, ...)
                     -> for each content item, push to a destination
       |
       v
[ Destination   ]    consume per-item content (glovebox ingest, Immich,
                     calendar service, ...)
```

### 3.1 Layer Independence

Each layer is optional and independently composable:

- You can skip the **container unpacker** if your archive is already unpacked (user pre-extracted the zip). The layout recognizer runs on the directory directly.
- You can skip the **layout recognizer** if you already know what you have (a standalone mbox file you downloaded from somewhere). The format handler runs directly on the file.
- You can skip the **format handler** for subtrees nobody handles yet (Google Fit JSON, NotebookLM before a subscriber exists). The recognizer still emits an event; nobody consumes it; raw stays on storage; add a handler when you want to.
- The **destination** is whatever subscribes to the format handler's ingest calls. Glovebox is one destination. Immich is another. A new destination is just a new subscriber.

### 3.2 Layer Ownership

Layers live in the repo that owns the problem:

| Layer | Repo | Rationale |
|---|---|---|
| Container unpackers | archiver | Agnostic of content; cross-cutting utility |
| Layout recognizers | archiver | Archive-provider-specific (Google, Slack, Meta); pattern-matching on directory structure; no content parsing |
| Format handlers | *destination repo* | Format parsing is tightly coupled to destination ingest schema and per-item dedup keys. Format handlers for text-shaped content live in glovebox; photo-shaped format handlers live with Immich tooling; etc. |
| Destinations | *destination repo* | Whatever service consumes the per-item ingest calls |

**Why format handlers don't live in archiver:** archiver would become a polyglot of mbox + ics + vcard + Slack JSON + WhatsApp text + twenty other parsers, each coupled to a specific destination's ingest format. That mixes "recognize an archive" with "speak a destination's API," which are genuinely different jobs owned by different codebases.

### 3.3 Trust Boundary

The container is finished and came from a knowable source -- the user deliberately downloaded/received it. The **content inside** the container is from low-trust authors (email senders, chat participants, document creators) and retains the full trust posture of any live-source content. Prompt-injection scanning and other content scrutiny apply to archived content identically to live-source content; the container-finishedness only affects operational posture (capture workflow), not trust.

This is why format handlers that feed glovebox-destined content live in glovebox (same codebase, same scrutiny), not in archiver.

## 4. Notification Event Schema: v1.0 -> v1.1

The existing `notification-event.v1.schema.json` hard-codes `source` and `event_type` enums scoped to physical media. Digital archive events need the same envelope but with different values. Schema v1.1 adds archive support as a non-breaking extension.

### 4.1 Extensions

- **`source` enum** adds: `archive-recognizer`, `archive-unpacker`, `archive-format-handler`.
- **`event_type` enum** adds:
  - `archive-unpacked` -- emitted by a container unpacker after extraction.
  - `archive-subtree-recognized` -- emitted by a layout recognizer for each identified sub-archive.
  - `archive-import-complete` -- emitted by a format handler after processing a content archive end-to-end.
- **`media_type` enum** is loosened: the v1.0 closed enum is replaced with a `oneOf` of (the v1.0 enum values) or (the pattern `^archive/.+$`). This makes `archive/*` an open taxonomy without requiring every new archive subtype to ship a schema revision. Examples:
  - `archive/mbox` -- standalone mbox, provenance unknown
  - `archive/google-takeout/mail` -- Gmail mbox inside a Takeout
  - `archive/google-takeout/calendar` -- ics inside a Takeout
  - `archive/google-takeout/chat` -- Google Chat conversations
  - `archive/google-takeout/keep` -- Keep notes
  - `archive/google-takeout/notebooklm` -- NotebookLM notebooks
  - `archive/google-takeout/voice` -- Voice transcripts
  - `archive/google-takeout/my-activity` -- My Activity stream
  - `archive/google-takeout/photos` -- Photos export
  - `archive/google-takeout/timeline` -- Maps location timeline
  - `archive/slack-export/channel` -- Slack channel JSON
  - `archive/slack-export/dm` -- Slack DM JSON
  - `archive/whatsapp-export/chat` -- WhatsApp chat text + media
  - `archive/thunderbird-profile/mail` -- Thunderbird mbox folders
  - ...

The `archive/*` prefix is deliberately open-ended at the schema level so new archive providers and subtypes can be added without schema revisions. Consumers match by prefix.

### 4.1.1 Compatibility Note

Adding enum values to `source` and `event_type`, and loosening `media_type` to accept `archive/*` patterns, is **backward-compatible for event producers** (v1.0 events parse cleanly against the v1.1 schema). For **strict consumers** that use `additionalProperties: false` and closed-enum validation, this is technically a breaking change -- such consumers must update their validation logic to accept v1.1. We judge this acceptable because:

- The only current consumer is archiver's notification-relay, which does prefix-matching, not strict schema validation.
- Forcing a major version bump (v2.0) on every new archive subtype would defeat the extensibility we're trying to achieve.
- Producers declare `schema_version: "1.1"` in events using the new values; consumers that don't yet understand v1.1 can detect and log the mismatch.

### 4.2 Metadata Extensions

The existing `metadata` object gains optional fields relevant to archives (all nullable, additive):

- `archive_format` -- container format of the source (`zip`, `tar.gz`, `7z`, `none`).
- `item_count` -- number of individual items inside the archive (messages, notes, files, etc.) if known at recognition time.
- `byte_size` -- size of the raw archive in bytes.
- `origin` -- free-form string identifying where the archive came from (`"Google Takeout 2026-04-11"`, `"Slack export workspace=foo 2026-02-01"`).
- `parent_event_id` -- if this event was emitted as a result of another (e.g., a layout recognizer emitting subtree events after receiving an `archive-unpacked` event), the originating event's id. Enables event-chain tracing.

### 4.3 Side-Car Manifests

For each archive that goes through a format handler, a side-car manifest sits alongside the raw content on storage. The manifest records what was in the archive, what was processed, what was filtered out, what errored, and enough resume state to restart an interrupted import. Per-format manifests extend a common base schema; mbox's specific manifest is detailed in glovebox's spec 09.

Base side-car manifest: `<archive>.<kind>-manifest.v1.json`
- Common fields: `schema_version`, `source_path`, `source_size`, `source_mtime`, `timestamp_start`, `timestamp_end`, `counts`, `resume_state`.
- Per-kind fields layered on top (mbox adds `message_ids_ingested`, `filter_rules_applied`; chat adds `conversation_ids_ingested`; etc.).

## 5. V1 Scope for This Spec

V1 of the archive importer pattern ships **architecture and schema only, no code**:

- This document (`01-archive-importer-pattern.md`) captures the taxonomy, ownership, and trust boundary.
- `notification-event.v1.1.schema.json` adds the archive source/event/media_type extensions.
- The `archive/*` media_type prefix is documented as open taxonomy.
- Side-car manifest conventions are documented; per-format manifest schemas are defined in the spec for each format handler (starting with glovebox's mbox spec).

No recognizer code, no unpacker code, and no format handler code lives in archiver in V1. The only archiver-repo artifact is the schema bump and this spec.

**Why schema-only for V1:** glovebox's mbox importer (spec 09) is the first concrete use case and it doesn't need a recognizer or an unpacker -- Steve has a standalone mbox file extracted from Takeout. Building the recognizer and unpacker now would be speculative; building them against a real second use case (WhatsApp export, Slack export, or automatic Takeout recognition) will produce better code. The schema extension is enough to not have to retrofit later.

## 6. V2+ Deferred Scope

Not in V1; named so they aren't forgotten:

- **Google Takeout outer recognizer.** Walks an unpacked Takeout directory, emits `archive-subtree-recognized` events for each known service subtree. Graceful skip for absent/empty services. Unhandled subtrees emit events with no subscribers; raw stays on storage.
- **Takeout subrecognizers** per service (Mail, Calendar, Contacts, Chat, Keep, Gemini, NotebookLM, Voice, YouTube, My Activity, ...). Each is independent, lands one rainy afternoon at a time.
- **Container unpackers** for zip, tar.gz, 7z. Simple wrappers around Go's `archive/zip` etc.; emit `archive-unpacked` events.
- **Other archive layouts:** Slack export, WhatsApp export, Thunderbird profile, .pst-to-mbox conversion, Outlook .ost, Signal backup, ...
- **Event transport evolution:** V1 uses archiver's existing webhook relay. V2 may introduce NATS subjects per `plan.md`, Section 3.3.2. Subscribers change minimally because the event envelope stays the same.
- **Inbox watcher:** a K8s workload watching an `incoming/archives/raw/` directory and triggering the appropriate unpacker/recognizer automatically. V1 is manually triggered (you run the handler against a path); V2 automates.

## 7. Relationship to Glovebox

Glovebox is one consumer of archive events -- specifically, of events carrying text-shaped content (mail, chat transcripts, notes, activity logs, documents). Glovebox grows a sibling family to its existing `connectors/` directory, called `importers/`, holding format handlers that subscribe to specific `archive/*` media types.

Details of the glovebox-side handler family, its shared library factoring, and the specific mbox handler are in glovebox's spec 09 (`09-mbox-importer-design.md`).

Glovebox is not privileged in this pattern. Other destinations (Immich for photos, calendar services for ics events, a future Signal archive consumer) attach the same way: by subscribing to events with the matching media types.

## 8. Repo Topology Summary

```
archiver/                                  archiver repo
  docs/specs/
    01-archive-importer-pattern.md         (this doc)
  schemas/
    notification-event.v1.1.schema.json    (V1 deliverable)
    scan-session-manifest.v1.schema.json   (unchanged, existing)
  manifests/                               (future: recognizer/unpacker workloads)

glovebox/                                  glovebox repo
  docs/specs/
    09-mbox-importer-design.md             (first format handler spec)
    10-external-ingest-auth-design.md      (follow-up auth project stub)
  importers/                               (new sibling to connectors/)
    mbox/                                  (V1 deliverable: mbox format handler)
  connector/                               (refactored to be a pure library)
  connectors/                              (existing live-source connectors)

immich/                                    (hypothetical; not in our scope)
  importers/
    google-photos/                         (subscribes to archive/google-takeout/photos)
```

## 9. Open Questions

None blocking V1. Questions worth revisiting before V2:

- **Event transport choice.** V1 continues using archiver's webhook relay. NATS is on archiver's roadmap (plan.md Section 3.3.2). If NATS lands before V2 archive work starts, the subscriber shape changes slightly (subject subscribe vs. webhook receive). Event envelope stays identical.
- **Handler discovery.** How does a newly deployed format handler register its subscription? V1 has no automation -- handlers are configured statically. V2 may want a registry if the number of handlers grows.
- **Failed-handler semantics.** If a handler is deployed but repeatedly fails to process events, what happens to the raw archive? V1 relies on manual intervention (check logs, fix, rerun); V2 may want dead-letter patterns parallel to archiver's existing `/incoming/notifications/dead-letter/`.
- **Archive retention.** Raw archives are preserved indefinitely by default. A retention policy (keep 2 years, archive to cold storage, etc.) is a cluster-operator concern, not a schema concern, but it's worth noting that the pattern assumes raw is cheap.

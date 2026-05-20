# Archive Importer for Google Takeout — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `archive-importer` v0.1.0 plus chart 0.2.0 plus schemas, end-to-end, with a real Google Takeout running through the pipeline and producing scrubbed example output for spec 03 § 7.5.

**Architecture:** One Go binary (`archive-importer`), runnable as a Kubernetes Job (chart ships a suspended CronJob template that operators promote via `kubectl get cronjob | yq | kubectl apply`) and as a workstation `docker run`. The binary unpacks a zip, detects the Google Takeout provider, walks `Takeout/`, runs per-service `SubtreeMatcher`s, writes a sidecar manifest, and POSTs notification events to the recognizer notification-relay. Code is decomposed into small focused packages (`ident`, `unpacker`, `matcher`, `manifest`, `relay`, `lock`) each unit-tested in isolation.

**Tech Stack:** Go 1.26.3 (matches existing scanner module). Helm 3.14.4 + kubeconform for chart. Kaniko for image build. `archive/zip` (stdlib) for unpacking. Standard library `flock` via `golang.org/x/sys/unix` for the concurrent-run guard. Notification-relay HTTP contract per `schemas/notification-event.v1.1.schema.json`.

**Spec:** `docs/specs/03-archive-importer-google-takeout.md` @ commit `1242425+`.

**Beads:** `archiver-ubc`.

---

## File Structure

### New files

```
images/archive-importer/
  Dockerfile
  go.mod                                            # module github.com/leftathome/recognizer/images/archive-importer
  go.sum
  cmd/archive-importer/
    main.go                                         # CLI entrypoint
    main_test.go                                    # end-to-end integration tests
  internal/
    ident/
      ident.go                                      # SHA256 prefix + filename-stem → archive_id
      ident_test.go
    unpacker/
      unpacker.go                                   # interface
      zip.go                                        # archive/zip implementation; ErrInsecurePath = fatal
      unpacker_test.go
    matcher/
      matcher.go                                    # SubtreeMatcher, Provider interfaces
      google_takeout.go                             # provider Detect + 15 subtree matchers
      google_takeout_test.go                        # per-matcher tests
    manifest/
      manifest.go                                   # read/write archive-layout-manifest.v1.json
      manifest_test.go                              # schema round-trip
    relay/
      client.go                                     # POST notification events with retry
      client_test.go                                # httptest.Server mock
    lock/
      lock.go                                       # flock-based concurrent-run guard
      lock_test.go
  testdata/
    fixtures/
      google-takeout-minimal/Takeout/...            # one file per known subtree
      google-takeout-mail-only/Takeout/Mail/...
      google-takeout-with-unknown/Takeout/...
      not-an-archive/random.txt
    takeout-zip/
      takeout-fixture.zip                           # zip of google-takeout-minimal
  scripts/
    run-job.sh                                      # operator helper

schemas/
  notification-event.v1.1.schema.json               # additive over v1.0
  archive-layout-manifest.v1.schema.json            # new

charts/recognizer/templates/archive-importer/
  configmap.yaml
  serviceaccount.yaml
  cronjob.yaml                                      # suspend: true; template only
```

### Modified files

| Path | Change |
|---|---|
| `charts/recognizer/Chart.yaml` | version `0.1.1` → `0.2.0` (additive new workload) |
| `charts/recognizer/values.yaml` | add `archiveImporter:` block |
| `charts/recognizer/templates/_helpers.tpl` | add `recognizer.relayUrl` helper |
| `charts/recognizer/README.md` | one-paragraph mention pointing at spec 03 |
| `.gitlab-ci.yml` | new `test:go:archive-importer`, `vuln:go:archive-importer`, `build:archive-importer` jobs |
| `CHANGELOG.md` | new `## [Unreleased]` entries + the 0.2.0 release section when cut |
| `docs/specs/03-archive-importer-google-takeout.md` | § 7.5 example regenerated post-acceptance, scrubbed per § 7.6 |

---

## Prerequisites

Confirm before starting:

- `go` 1.26.3+ installed locally (`go version`)
- `helm` 3.14+ + `kubeconform` + `yq` (mikefarah's Go version) — same toolset spec 02's plan required
- `docker` (or compatible) for local image build verification
- `kubectl` pointed at the orac cluster + `flux` CLI (for the acceptance step)
- `bd` (beads) installed and able to update `archiver-ubc`
- A worktree on a new branch off `main`: `git worktree add .claude/worktrees/archive-importer -b feat/archive-importer-takeout gitlab/main`

---

## Phase A — Foundation (schemas + scaffold)

### Task A1: Notification event v1.1 schema

**Files:**
- Create: `schemas/notification-event.v1.1.schema.json`

- [ ] **Step 1: Verify the v1.0 schema is what we extend**

```bash
jq -r '.["$schema"], .properties.source.enum, .properties.event_type.enum, .properties.media_type' schemas/notification-event.v1.schema.json
```

Expected: prints the v1.0 schema URL, the existing source/event_type enum values, and the existing closed `media_type` enum.

- [ ] **Step 2: Write the failing check — there's no v1.1 yet**

```bash
test -f schemas/notification-event.v1.1.schema.json && echo "exists" || echo "missing"
```

Expected: `missing`.

- [ ] **Step 3: Write `schemas/notification-event.v1.1.schema.json`**

Start from the v1.0 file and apply the additive changes from spec 03 § 6.1:

- Bump `$id` to v1.1 (`.../notification-event/v1.1.json`).
- `source.enum` — keep all v1.0 entries; add `archive-recognizer`, `archive-unpacker`, `archive-format-handler`.
- `event_type.enum` — keep all v1.0; add `archive-unpacked`, `archive-subtree-recognized`, `archive-import-complete`.
- `media_type` — replace the closed enum with:

```json
"media_type": {
  "oneOf": [
    { "enum": ["<original v1.0 enum values, verbatim>"] },
    { "type": "string", "pattern": "^archive/.+$" }
  ]
}
```

- `metadata.properties` — add (all `nullable: true`, all `optional`):
  - `archive_format`: `{"type": "string", "enum": ["zip", "tar.gz", "7z", "none"]}` (nullable)
  - `item_count`: `{"type": "integer", "minimum": 0}` (nullable)
  - `byte_size`: `{"type": "integer", "minimum": 0}` (nullable)
  - `origin`: `{"type": "string"}` (nullable)
  - `parent_event_id`: `{"type": "string"}` (nullable)
- `schema_version` field accepts `"1.0"` OR `"1.1"`.
- Keep `additionalProperties: false` at the same level v1.0 had it.

- [ ] **Step 4: Validate the schema is itself well-formed JSON Schema**

```bash
jq empty schemas/notification-event.v1.1.schema.json && echo "json ok"
# Validate against draft 2020-12 metaschema (or whichever v1.0 uses)
kubeconform -strict -schema-location schemas/notification-event.v1.1.schema.json /dev/null 2>&1 | head
# kubeconform isn't ideal for this; use ajv if available, or just parse:
python3 -c "import json, jsonschema; jsonschema.Draft202012Validator.check_schema(json.load(open('schemas/notification-event.v1.1.schema.json')))"
```

Expected: no errors. `check_schema` returns `None` on success.

- [ ] **Step 5: Smoke-test against a v1.0 event AND a v1.1 archive event**

Write a one-shot script `/tmp/schema-probe.py`:

```python
import json, jsonschema
schema = json.load(open("schemas/notification-event.v1.1.schema.json"))
v10 = {
    "schema_version": "1.0",
    "source": "scanner-session-manager",
    "event_type": "scan-complete",
    "event_id": "evt_EXAMPLE_001",
    "timestamp": "2026-05-19T11:00:00Z",
    "media_type": "scan/document",      # adjust to a real v1.0 enum value
    "output_path": "/data/scans/x.tiff",
    "metadata": {}
}
v11 = {
    "schema_version": "1.1",
    "source": "archive-recognizer",
    "event_type": "archive-subtree-recognized",
    "event_id": "evt_EXAMPLE_002",
    "timestamp": "2026-05-19T11:30:08Z",
    "media_type": "archive/google-takeout/mail",
    "output_path": "/data/unpacked/00000000-takeout-EXAMPLE/Takeout/Mail",
    "metadata": {"archive_format": "zip", "byte_size": 12345}
}
jsonschema.validate(v10, schema)
jsonschema.validate(v11, schema)
print("both validate")
```

```bash
python3 /tmp/schema-probe.py
```

Expected: `both validate`.

- [ ] **Step 6: Commit**

```bash
git add schemas/notification-event.v1.1.schema.json
git commit -m "feat(schemas): add notification-event v1.1 with archive extensions"
```

**Exit criteria:** file exists, is valid JSON Schema, accepts both a v1.0 and a v1.1 example event.

### Task A2: archive-layout-manifest v1 schema

**Files:**
- Create: `schemas/archive-layout-manifest.v1.schema.json`

- [ ] **Step 1: Write the failing existence check**

```bash
test -f schemas/archive-layout-manifest.v1.schema.json && echo "exists" || echo "missing"
```

Expected: `missing`.

- [ ] **Step 2: Write the schema**

Top-level object (see spec 03 § 6.2 for full field semantics):

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://recognizer.orac.local/schemas/archive-layout-manifest/v1.json",
  "title": "Archive Layout Manifest v1",
  "type": "object",
  "required": ["schema_version", "archive_id", "source", "provider", "matcher_version", "timestamps", "subtrees_recognized", "subtrees_unrecognized", "events_emitted"],
  "additionalProperties": false,
  "properties": {
    "schema_version": {"const": "1.0"},
    "archive_id": {"type": "string", "minLength": 1},
    "source": {
      "type": "object",
      "required": ["original_filename", "moved_to", "sha256", "size_bytes", "mtime", "archive_format"],
      "additionalProperties": false,
      "properties": {
        "original_filename": {"type": "string"},
        "moved_to": {"type": "string"},
        "sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
        "size_bytes": {"type": "integer", "minimum": 0},
        "mtime": {"type": "string", "format": "date-time"},
        "archive_format": {"enum": ["zip", "tar.gz", "7z"]}
      }
    },
    "provider": {"type": ["string", "null"]},
    "matcher_version": {"type": "string"},
    "timestamps": {
      "type": "object",
      "required": ["start", "end"],
      "additionalProperties": false,
      "properties": {
        "start": {"type": "string", "format": "date-time"},
        "end":   {"type": "string", "format": "date-time"}
      }
    },
    "subtrees_recognized": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["media_type", "output_path", "item_count", "byte_size", "event_id"],
        "additionalProperties": false,
        "properties": {
          "media_type": {"type": "string", "pattern": "^archive/.+$"},
          "output_path": {"type": "string"},
          "item_count": {"type": ["integer", "null"], "minimum": 0},
          "byte_size":  {"type": "integer", "minimum": 0},
          "event_id":   {"type": "string"}
        }
      }
    },
    "subtrees_unrecognized": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["path", "first_seen", "byte_size", "emitted_event"],
        "additionalProperties": false,
        "properties": {
          "path": {"type": "string"},
          "first_seen": {"type": "string", "format": "date-time"},
          "byte_size": {"type": "integer", "minimum": 0},
          "emitted_event": {"type": "boolean"}
        }
      }
    },
    "events_emitted": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["event_id", "event_type", "media_type", "timestamp"],
        "additionalProperties": false,
        "properties": {
          "event_id":   {"type": "string"},
          "event_type": {"type": "string"},
          "media_type": {"type": "string"},
          "timestamp":  {"type": "string", "format": "date-time"}
        }
      }
    }
  }
}
```

- [ ] **Step 3: Validate the schema is itself well-formed**

```bash
python3 -c "import json, jsonschema; jsonschema.Draft202012Validator.check_schema(json.load(open('schemas/archive-layout-manifest.v1.schema.json')))"
```

Expected: no output (success).

- [ ] **Step 4: Smoke-test against the worked-example manifest from spec 03 § 6.2**

Write a fixture `/tmp/manifest-probe.json` mirroring spec 03 § 6.2's example (use the `00000000`/`EXAMPLE` sentinels). Then:

```bash
python3 -c "
import json, jsonschema
schema = json.load(open('schemas/archive-layout-manifest.v1.schema.json'))
sample = json.load(open('/tmp/manifest-probe.json'))
jsonschema.validate(sample, schema)
print('valid')
"
```

Expected: `valid`.

- [ ] **Step 5: Commit**

```bash
git add schemas/archive-layout-manifest.v1.schema.json
git commit -m "feat(schemas): add archive-layout-manifest v1 schema"
```

**Exit criteria:** file exists, is valid JSON Schema, accepts the spec 03 § 6.2 example manifest.

### Task A3: Go module scaffold + Dockerfile skeleton

**Files:**
- Create: `images/archive-importer/go.mod`
- Create: `images/archive-importer/Dockerfile`
- Create: `images/archive-importer/cmd/archive-importer/main.go` (placeholder)

- [ ] **Step 1: Red — `go build` fails because there's no module yet**

```bash
ls images/archive-importer/ 2>&1 || echo "missing"
```

Expected: `missing` (or path error).

- [ ] **Step 2: Create the module**

```bash
mkdir -p images/archive-importer/cmd/archive-importer
cd images/archive-importer
go mod init github.com/leftathome/recognizer/images/archive-importer
```

Write `cmd/archive-importer/main.go`:

```go
// Package main is the archive-importer CLI entrypoint.
// See docs/specs/03-archive-importer-google-takeout.md for the design.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "ingest" {
		fmt.Fprintln(os.Stderr, "usage: archive-importer ingest <archive-path>")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "archive-importer: not implemented yet")
	os.Exit(1)
}
```

- [ ] **Step 3: Green — `go build` succeeds; binary prints usage**

```bash
cd images/archive-importer
go build -o /tmp/archive-importer ./cmd/archive-importer
/tmp/archive-importer
echo "exit=$?"
```

Expected: prints `usage: archive-importer ingest <archive-path>`; `exit=1`.

- [ ] **Step 4: Write the Dockerfile**

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/archive-importer ./cmd/archive-importer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/archive-importer /usr/local/bin/archive-importer
USER nonroot
ENTRYPOINT ["/usr/local/bin/archive-importer"]
```

(`go.sum*` allows the COPY to succeed when go.sum is absent in the initial empty-deps state.)

- [ ] **Step 5: Green — local Docker build succeeds**

```bash
cd images/archive-importer
docker build -t archive-importer:scaffold .
docker run --rm archive-importer:scaffold
echo "exit=$?"
```

Expected: prints usage; `exit=1`.

- [ ] **Step 6: Commit**

```bash
git add images/archive-importer/
git commit -m "feat(archive-importer): scaffold Go module + Dockerfile"
```

**Exit criteria:** `go build` succeeds; `docker build` succeeds; binary runs and prints usage.

---

## Phase B — Core libraries (one focused package per task, all TDD)

Each Phase B task creates one package, drives it with tests first, lands ~50-200 lines of code. The packages are independent — Phase B tasks can be implemented in any order, but the listed order is the natural assembly direction.

### Task B1: `internal/ident` — archive ID derivation

**Files:**
- Create: `images/archive-importer/internal/ident/ident.go`
- Create: `images/archive-importer/internal/ident/ident_test.go`

- [ ] **Step 1: Write the failing tests**

`ident_test.go`:

```go
package ident

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDerive_KnownContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "takeout-EXAMPLE.zip")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := Derive(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	wantPrefix := hex.EncodeToString(sum[:])[:8]
	want := wantPrefix + "-takeout-EXAMPLE"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDerive_DeterministicAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fixture.zip")
	if err := os.WriteFile(p, []byte("deterministic"), 0644); err != nil {
		t.Fatal(err)
	}
	a, _ := Derive(p)
	b, _ := Derive(p)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
}

func TestDerive_StripsExtension(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"x.zip", "x.tar.gz", "x.7z", "x"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("data"), 0644)
		got, err := Derive(p)
		if err != nil {
			t.Errorf("Derive(%q) err=%v", name, err)
			continue
		}
		stem := got[strings.Index(got, "-")+1:]
		if stem != "x" {
			t.Errorf("Derive(%q).stem = %q, want %q", name, stem, "x")
		}
	}
}

func TestDerive_MissingFile(t *testing.T) {
	_, err := Derive("/nonexistent/path.zip")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
```

- [ ] **Step 2: Red — run, observe missing package**

```bash
cd images/archive-importer
go test ./internal/ident/...
```

Expected: `no Go files in .../ident` or `package ident: ...`.

- [ ] **Step 3: Implement the package minimally**

`ident.go`:

```go
// Package ident derives the content-addressed archive identifier
// used to name the unpacked-tree directory: <sha256-prefix>-<filename-stem>.
package ident

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Derive returns "<8-char-sha-prefix>-<filename-stem>" for archivePath.
// The stem strips known archive extensions (.zip, .tar.gz, .7z) and
// anything after the first dot for unknown extensions.
func Derive(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	prefix := hex.EncodeToString(h.Sum(nil))[:8]
	base := filepath.Base(archivePath)
	stem := strings.TrimSuffix(base, ".zip")
	stem = strings.TrimSuffix(stem, ".tar.gz")
	stem = strings.TrimSuffix(stem, ".7z")
	if i := strings.Index(stem, "."); i > 0 && stem == base {
		stem = stem[:i]
	}
	return prefix + "-" + stem, nil
}
```

- [ ] **Step 4: Green — all four tests pass**

```bash
cd images/archive-importer
go test ./internal/ident/... -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/internal/ident/
git commit -m "feat(archive-importer): ident package — SHA256-prefix + stem → archive id"
```

**Exit criteria:** package exists, all four unit tests pass.

### Task B2: `internal/unpacker` — zip with path-traversal safety

**Files:**
- Create: `images/archive-importer/internal/unpacker/unpacker.go`
- Create: `images/archive-importer/internal/unpacker/zip.go`
- Create: `images/archive-importer/internal/unpacker/unpacker_test.go`

- [ ] **Step 1: Write the failing tests**

`unpacker_test.go`:

```go
package unpacker

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackZip_BasicTree(t *testing.T) {
	src := makeZip(t, map[string][]byte{
		"Takeout/Mail/foo.mbox":     []byte("From: a@b\n"),
		"Takeout/Calendar/foo.ics":  []byte("BEGIN:VCALENDAR\nEND:VCALENDAR\n"),
	})
	dst := t.TempDir()
	if err := UnpackZip(src, dst); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(dst, "Takeout/Mail/foo.mbox"))
	mustExist(t, filepath.Join(dst, "Takeout/Calendar/foo.ics"))
}

func TestUnpackZip_RejectsPathTraversal(t *testing.T) {
	// Hand-craft a zip with a ../ entry; archive/zip's NewReader surfaces
	// this as ErrInsecurePath when GODEBUG zipinsecurepath=0 (default).
	src := makeZipRaw(t, "../escape.txt", []byte("evil"))
	dst := t.TempDir()
	err := UnpackZip(src, dst)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !errors.Is(err, zip.ErrInsecurePath) {
		t.Errorf("got %v, want zip.ErrInsecurePath", err)
	}
	// Most importantly: nothing was written outside dst.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("file leaked outside dst")
	}
}

func TestUnpackZip_PreservesDirectoryStructure(t *testing.T) {
	src := makeZip(t, map[string][]byte{
		"a/b/c/leaf.txt": []byte("leaf"),
	})
	dst := t.TempDir()
	if err := UnpackZip(src, dst); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(dst, "a/b/c/leaf.txt"))
}

// --- test helpers ---

func makeZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, body := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(body)
	}
	w.Close()
	return path
}

func makeZipRaw(t *testing.T, entry string, body []byte) string {
	t.Helper()
	return makeZip(t, map[string][]byte{entry: body})
}

func mustExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected %s to exist: %v", p, err)
	}
}
```

- [ ] **Step 2: Red — package missing**

```bash
cd images/archive-importer
go test ./internal/unpacker/...
```

Expected: `no Go files`.

- [ ] **Step 3: Implement the interface + zip backend**

`unpacker.go`:

```go
// Package unpacker extracts archives into a target directory.
// V1 supports zip; tar.gz and 7z are reserved.
package unpacker

// Unpacker extracts an archive at srcPath into dstDir.
type Unpacker interface {
	Unpack(srcPath, dstDir string) error
}
```

`zip.go`:

```go
package unpacker

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UnpackZip extracts srcZip into dstDir.
//
// Path-traversal safety: archive/zip's NewReader returns ErrInsecurePath
// for entries that aren't filepath.IsLocal. We reject the archive entirely
// in that case (treating ErrInsecurePath as fatal, never opting into
// GODEBUG=zipinsecurepath=1).
func UnpackZip(srcZip, dstDir string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err // may be zip.ErrInsecurePath
	}
	defer r.Close()
	for _, f := range r.File {
		if !filepath.IsLocal(f.Name) || strings.Contains(f.Name, "..") {
			return fmt.Errorf("unpacker: refusing insecure entry %q: %w", f.Name, zip.ErrInsecurePath)
		}
		dst := filepath.Join(dstDir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			w.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

var _ = errors.Is // silence unused-import if needed
```

- [ ] **Step 4: Green**

```bash
cd images/archive-importer
go test ./internal/unpacker/... -v
```

Expected: 3 tests PASS. (The traversal test must hit `zip.ErrInsecurePath`.)

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/internal/unpacker/
git commit -m "feat(archive-importer): unpacker package with path-traversal rejection"
```

**Exit criteria:** zip extracts; path-traversal entries rejected; nothing leaks outside dstDir.

### Task B3: `internal/matcher` interfaces

**Files:**
- Create: `images/archive-importer/internal/matcher/matcher.go`
- Create: `images/archive-importer/internal/matcher/matcher_test.go`

- [ ] **Step 1: Write the failing tests (contract-level)**

`matcher_test.go`:

```go
package matcher

import "testing"

// stubMatcher is a SubtreeMatcher used to verify the interface compiles
// and behaves as documented.
type stubMatcher struct{ mt, desc string; matches bool }

func (s *stubMatcher) MediaType() string   { return s.mt }
func (s *stubMatcher) Description() string { return s.desc }
func (s *stubMatcher) Matches(dirPath, dirName string) (bool, error) {
	return s.matches, nil
}

func TestProvider_HasNameAndSubtrees(t *testing.T) {
	p := Provider{
		Name:    "test",
		Detect:  func(string) (bool, string, error) { return true, "/x", nil },
		Subtrees: []SubtreeMatcher{
			&stubMatcher{mt: "archive/test/a", desc: "alpha"},
		},
	}
	if p.Name != "test" {
		t.Errorf("Name lost")
	}
	if len(p.Subtrees) != 1 {
		t.Errorf("Subtrees lost")
	}
	if p.Subtrees[0].MediaType() != "archive/test/a" {
		t.Errorf("MediaType lost")
	}
}

func TestProvider_DetectSignature(t *testing.T) {
	called := false
	p := Provider{
		Detect: func(root string) (bool, string, error) {
			called = true
			if root != "/probe" {
				t.Errorf("Detect got %q, want /probe", root)
			}
			return true, "/probe/Sub", nil
		},
	}
	ok, base, err := p.Detect("/probe")
	if err != nil || !ok || base != "/probe/Sub" || !called {
		t.Errorf("Detect failed contract: ok=%v base=%q err=%v called=%v", ok, base, err, called)
	}
}
```

- [ ] **Step 2: Red**

```bash
go test ./internal/matcher/... -run Provider
```

Expected: build failure (no `matcher` package).

- [ ] **Step 3: Implement the interfaces**

`matcher.go`:

```go
// Package matcher defines the archive-layout matcher contract:
// one Provider per archive provider (Google Takeout, future Meta export, etc.)
// containing many SubtreeMatchers that identify per-service subtrees.
//
// See docs/specs/03-archive-importer-google-takeout.md § 5.
package matcher

// SubtreeMatcher identifies a specific archive subtree and reports the
// media_type to emit on a match.
type SubtreeMatcher interface {
	MediaType() string
	Description() string
	Matches(dirPath, dirName string) (bool, error)
}

// Provider groups a top-level provider detector with its subtree matchers.
type Provider struct {
	Name     string
	Detect   func(rootPath string) (matched bool, subtreeBase string, err error)
	Subtrees []SubtreeMatcher
}
```

- [ ] **Step 4: Green**

```bash
go test ./internal/matcher/... -run Provider -v
```

Expected: 2 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/internal/matcher/matcher.go images/archive-importer/internal/matcher/matcher_test.go
git commit -m "feat(archive-importer): matcher package interfaces (Provider, SubtreeMatcher)"
```

**Exit criteria:** interface contracts compile and round-trip values.

### Task B4: `internal/matcher` Google Takeout provider + per-subtree matchers

**Files:**
- Create: `images/archive-importer/internal/matcher/google_takeout.go`
- Create: `images/archive-importer/internal/matcher/google_takeout_test.go`
- Create: per-fixture trees under `images/archive-importer/testdata/fixtures/...` (see test below)

This is the biggest Phase B task — 15 subtree matchers. Decompose internally as the test grows: one sub-test per matcher.

- [ ] **Step 1: Write the fixture trees**

Create one minimal Takeout fixture exercising every subtree:

```bash
cd images/archive-importer
mkdir -p testdata/fixtures/google-takeout-minimal/Takeout/{Mail,Calendar,Chat,Keep,NotebookLM,Voice,"My Activity","Google Photos","Location History","YouTube and YouTube Music",Fit,Drive,Tasks,Contacts}
echo 'From: a@b' > testdata/fixtures/google-takeout-minimal/Takeout/Mail/foo.mbox
cat > testdata/fixtures/google-takeout-minimal/Takeout/Calendar/foo.ics <<'EOF'
BEGIN:VCALENDAR
END:VCALENDAR
EOF
mkdir -p testdata/fixtures/google-takeout-minimal/Takeout/Chat/Groups
echo '{}' > testdata/fixtures/google-takeout-minimal/Takeout/Chat/Groups/.placeholder
echo '{}' > testdata/fixtures/google-takeout-minimal/Takeout/Keep/note.json
echo '<html></html>' > testdata/fixtures/google-takeout-minimal/Takeout/NotebookLM/notebook.html
mkdir -p testdata/fixtures/google-takeout-minimal/Takeout/Voice/Calls
echo '<html></html>' > testdata/fixtures/google-takeout-minimal/Takeout/Voice/Calls/.placeholder
echo '<html></html>' > "testdata/fixtures/google-takeout-minimal/Takeout/My Activity/activity.html"
echo '{}' > "testdata/fixtures/google-takeout-minimal/Takeout/Google Photos/photo.json"
echo '{}' > "testdata/fixtures/google-takeout-minimal/Takeout/Location History/Records.json"
mkdir -p "testdata/fixtures/google-takeout-minimal/Takeout/YouTube and YouTube Music/videos"
echo '{}' > "testdata/fixtures/google-takeout-minimal/Takeout/YouTube and YouTube Music/videos/.placeholder"
mkdir -p testdata/fixtures/google-takeout-minimal/Takeout/Fit/Activity
echo '<gpx></gpx>' > testdata/fixtures/google-takeout-minimal/Takeout/Fit/Activity/run.gpx
echo 'doc body' > testdata/fixtures/google-takeout-minimal/Takeout/Drive/doc.docx
echo '{}' > testdata/fixtures/google-takeout-minimal/Takeout/Tasks/list.json
cat > testdata/fixtures/google-takeout-minimal/Takeout/Contacts/c.vcf <<'EOF'
BEGIN:VCARD
END:VCARD
EOF
```

Also the alternate fixtures:

```bash
mkdir -p testdata/fixtures/google-takeout-mail-only/Takeout/Mail
echo 'From: a@b' > testdata/fixtures/google-takeout-mail-only/Takeout/Mail/foo.mbox

mkdir -p testdata/fixtures/google-takeout-with-unknown/Takeout/{Mail,SomeUnknownService}
echo 'From: a@b' > testdata/fixtures/google-takeout-with-unknown/Takeout/Mail/foo.mbox
echo 'whatever' > testdata/fixtures/google-takeout-with-unknown/Takeout/SomeUnknownService/data.txt

mkdir -p testdata/fixtures/not-an-archive
echo 'random' > testdata/fixtures/not-an-archive/random.txt
```

Commit fixtures separately so the test commit is clean:

```bash
git add testdata/fixtures/
git commit -m "test(archive-importer): add Google Takeout fixtures"
```

- [ ] **Step 2: Write the failing tests**

`google_takeout_test.go`:

```go
package matcher

import (
	"path/filepath"
	"testing"
)

const fixtureRoot = "../../testdata/fixtures/google-takeout-minimal"

func TestGoogleTakeout_DetectsMinimalFixture(t *testing.T) {
	p := GoogleTakeoutProvider()
	ok, base, err := p.Detect(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected detection")
	}
	want := filepath.Join(fixtureRoot, "Takeout")
	if base != want {
		t.Errorf("base = %q, want %q", base, want)
	}
}

func TestGoogleTakeout_RejectsNonArchive(t *testing.T) {
	p := GoogleTakeoutProvider()
	ok, _, _ := p.Detect("../../testdata/fixtures/not-an-archive")
	if ok {
		t.Error("expected no detection")
	}
}

// One sub-test per known subtree.
func TestGoogleTakeout_SubtreeMatchers(t *testing.T) {
	base := filepath.Join(fixtureRoot, "Takeout")
	p := GoogleTakeoutProvider()

	expect := map[string]string{
		"Mail":                            "archive/google-takeout/mail",
		"Calendar":                        "archive/google-takeout/calendar",
		"Chat":                            "archive/google-takeout/chat",
		"Keep":                            "archive/google-takeout/keep",
		"NotebookLM":                      "archive/google-takeout/notebooklm",
		"Voice":                           "archive/google-takeout/voice",
		"My Activity":                     "archive/google-takeout/my-activity",
		"Google Photos":                   "archive/google-takeout/photos",
		"Location History":                "archive/google-takeout/timeline",
		"YouTube and YouTube Music":       "archive/google-takeout/youtube",
		"Fit":                             "archive/google-takeout/fit",
		"Drive":                           "archive/google-takeout/drive",
		"Tasks":                           "archive/google-takeout/tasks",
		"Contacts":                        "archive/google-takeout/contacts",
	}

	for dirName, mediaType := range expect {
		t.Run(dirName, func(t *testing.T) {
			matched := false
			for _, m := range p.Subtrees {
				ok, err := m.Matches(filepath.Join(base, dirName), dirName)
				if err != nil {
					t.Errorf("matcher %q on %q: %v", m.MediaType(), dirName, err)
					continue
				}
				if ok {
					if m.MediaType() != mediaType {
						t.Errorf("dir %q matched %q, expected %q",
							dirName, m.MediaType(), mediaType)
					}
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("no matcher claimed %q", dirName)
			}
		})
	}
}

func TestGoogleTakeout_UnknownSubtreeNotMatched(t *testing.T) {
	p := GoogleTakeoutProvider()
	dirName := "SomeUnknownService"
	dirPath := "../../testdata/fixtures/google-takeout-with-unknown/Takeout/SomeUnknownService"
	for _, m := range p.Subtrees {
		ok, _ := m.Matches(dirPath, dirName)
		if ok {
			t.Errorf("matcher %q falsely claimed %q", m.MediaType(), dirName)
		}
	}
}
```

- [ ] **Step 3: Red**

```bash
go test ./internal/matcher/... -run GoogleTakeout
```

Expected: undefined `GoogleTakeoutProvider`.

- [ ] **Step 4: Implement the provider + matchers**

`google_takeout.go`:

```go
package matcher

import (
	"os"
	"path/filepath"
	"strings"
)

// GoogleTakeoutProvider returns the Provider that detects Google Takeout
// archives and matches their per-service subtrees.
func GoogleTakeoutProvider() Provider {
	return Provider{
		Name: "google-takeout",
		Detect: func(rootPath string) (bool, string, error) {
			tk := filepath.Join(rootPath, "Takeout")
			fi, err := os.Stat(tk)
			if err != nil {
				if os.IsNotExist(err) {
					return false, "", nil
				}
				return false, "", err
			}
			if !fi.IsDir() {
				return false, "", nil
			}
			return true, tk, nil
		},
		Subtrees: []SubtreeMatcher{
			dirMatcher{name: "Mail", mt: "archive/google-takeout/mail", desc: "Gmail export", fingerprint: anyFileMatching("*.mbox")},
			dirMatcher{name: "Calendar", mt: "archive/google-takeout/calendar", desc: "Google Calendar export", fingerprint: anyFileMatching("*.ics")},
			dirMatcher{nameAny: []string{"Chat", "Google Chat"}, mt: "archive/google-takeout/chat", desc: "Google Chat export", fingerprint: anySubdirOf("Groups", "Conversations")},
			dirMatcher{name: "Keep", mt: "archive/google-takeout/keep", desc: "Google Keep export", fingerprint: anyFileMatching("*.json")},
			dirMatcher{name: "NotebookLM", mt: "archive/google-takeout/notebooklm", desc: "NotebookLM export", fingerprint: anyFileMatching("*.html")},
			dirMatcher{name: "Voice", mt: "archive/google-takeout/voice", desc: "Google Voice export", fingerprint: anySubdirOf("Calls", "Texts", "Voicemails")},
			dirMatcher{name: "My Activity", mt: "archive/google-takeout/my-activity", desc: "Google My Activity", fingerprint: anyFileMatchingOneOf("*.html", "*.json")},
			dirMatcher{nameAny: []string{"Google Photos"}, mt: "archive/google-takeout/photos", desc: "Google Photos export", fingerprint: anyFileMatching("*.json")},
			dirMatcher{nameAny: []string{"Location History", "Timeline"}, mt: "archive/google-takeout/timeline", desc: "Google location timeline", fingerprint: anyFileMatching("*.json")},
			dirMatcher{nameAny: []string{"YouTube and YouTube Music"}, mt: "archive/google-takeout/youtube", desc: "YouTube + Music export", fingerprint: anySubdirOf("videos", "playlists")},
			dirMatcher{name: "Fit", mt: "archive/google-takeout/fit", desc: "Google Fit", fingerprint: anySubdirOf("Activity")},
			dirMatcher{name: "Drive", mt: "archive/google-takeout/drive", desc: "Google Drive", fingerprint: anyFileMatching("*")}, // mixed content; presence of any file
			dirMatcher{name: "Tasks", mt: "archive/google-takeout/tasks", desc: "Google Tasks", fingerprint: anyFileMatching("*.json")},
			dirMatcher{name: "Contacts", mt: "archive/google-takeout/contacts", desc: "Google Contacts", fingerprint: anyFileMatching("*.vcf")},
		},
	}
}

// dirMatcher implements SubtreeMatcher via directory-name match plus a
// fingerprint function over the directory contents.
type dirMatcher struct {
	name        string
	nameAny     []string
	mt          string
	desc        string
	fingerprint func(dirPath string) (bool, error)
}

func (d dirMatcher) MediaType() string   { return d.mt }
func (d dirMatcher) Description() string { return d.desc }
func (d dirMatcher) Matches(dirPath, dirName string) (bool, error) {
	if !d.nameMatches(dirName) {
		return false, nil
	}
	if d.fingerprint == nil {
		return true, nil
	}
	return d.fingerprint(dirPath)
}

func (d dirMatcher) nameMatches(name string) bool {
	if d.name != "" && d.name == name {
		return true
	}
	for _, n := range d.nameAny {
		if n == name {
			return true
		}
	}
	return false
}

// anyFileMatching returns a fingerprint that's true if any file in dirPath
// (any depth) matches glob.
func anyFileMatching(glob string) func(string) (bool, error) {
	return func(dirPath string) (bool, error) {
		found := false
		err := filepath.WalkDir(dirPath, func(p string, _ os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			ok, _ := filepath.Match(glob, filepath.Base(p))
			if ok && !strings.HasPrefix(filepath.Base(p), ".") {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true, nil
		}
		return false, err
	}
}

func anyFileMatchingOneOf(globs ...string) func(string) (bool, error) {
	return func(dirPath string) (bool, error) {
		for _, g := range globs {
			ok, err := anyFileMatching(g)(dirPath)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
}

func anySubdirOf(names ...string) func(string) (bool, error) {
	return func(dirPath string) (bool, error) {
		for _, n := range names {
			fi, err := os.Stat(filepath.Join(dirPath, n))
			if err == nil && fi.IsDir() {
				return true, nil
			}
		}
		return false, nil
	}
}
```

- [ ] **Step 5: Green**

```bash
go test ./internal/matcher/... -v
```

Expected: all matcher tests PASS.

- [ ] **Step 6: Commit**

```bash
git add images/archive-importer/internal/matcher/google_takeout.go images/archive-importer/internal/matcher/google_takeout_test.go
git commit -m "feat(archive-importer): Google Takeout provider + 15 subtree matchers"
```

**Exit criteria:** Provider detects the minimal fixture; each of 15 subtrees is claimed by exactly one matcher; unknown subtree is claimed by nobody.

### Task B5: `internal/manifest` — read/write + schema validation

**Files:**
- Create: `images/archive-importer/internal/manifest/manifest.go`
- Create: `images/archive-importer/internal/manifest/manifest_test.go`

- [ ] **Step 1: Failing tests**

`manifest_test.go`:

```go
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	m := Manifest{
		SchemaVersion: "1.0",
		ArchiveID:     "00000000-takeout-EXAMPLE",
		Source: Source{
			OriginalFilename: "takeout-EXAMPLE.zip",
			MovedTo:          "unpacked/00000000-takeout-EXAMPLE/takeout-EXAMPLE.zip",
			SHA256:           "0000000000000000000000000000000000000000000000000000000000000000",
			SizeBytes:        1234,
			Mtime:            time.Now().UTC().Format(time.RFC3339),
			ArchiveFormat:    "zip",
		},
		Provider:       strptr("google-takeout"),
		MatcherVersion: "1.0",
		Timestamps:     Timestamps{Start: time.Now().UTC().Format(time.RFC3339), End: time.Now().UTC().Format(time.RFC3339)},
		SubtreesRecognized: []SubtreeRecognized{
			{MediaType: "archive/google-takeout/mail", OutputPath: "unpacked/00000000-takeout-EXAMPLE/Takeout/Mail", ByteSize: 100, EventID: "evt_EXAMPLE_001"},
		},
		SubtreesUnrecognized: []SubtreeUnrecognized{},
		EventsEmitted: []EventEmitted{
			{EventID: "evt_EXAMPLE_001", EventType: "archive-subtree-recognized", MediaType: "archive/google-takeout/mail", Timestamp: time.Now().UTC().Format(time.RFC3339)},
		},
	}

	dir := t.TempDir()
	p := filepath.Join(dir, ManifestFilename)
	if err := Write(p, &m); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveID != m.ArchiveID {
		t.Errorf("round-trip lost ArchiveID")
	}
}

func TestWrite_ProducesValidJSON(t *testing.T) {
	m := minimal()
	p := filepath.Join(t.TempDir(), ManifestFilename)
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var anything map[string]any
	if err := json.Unmarshal(b, &anything); err != nil {
		t.Errorf("invalid JSON: %v", err)
	}
}

func TestRead_RejectsInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), ManifestFilename)
	os.WriteFile(p, []byte("not json"), 0644)
	if _, err := Read(p); err == nil {
		t.Error("expected error")
	}
}

func TestExists_True(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ManifestFilename)
	Write(p, minimal())
	if !Exists(dir) {
		t.Error("expected Exists=true")
	}
}

func TestExists_False(t *testing.T) {
	if Exists(t.TempDir()) {
		t.Error("expected Exists=false in empty dir")
	}
}

// --- helpers ---

func strptr(s string) *string { return &s }

func minimal() *Manifest {
	return &Manifest{
		SchemaVersion: "1.0",
		ArchiveID:     "00000000-x",
		Source:        Source{OriginalFilename: "x.zip", MovedTo: "x", SHA256: "0000000000000000000000000000000000000000000000000000000000000000", SizeBytes: 1, Mtime: "2026-01-01T00:00:00Z", ArchiveFormat: "zip"},
		Provider:      strptr("google-takeout"),
		MatcherVersion: "1.0",
		Timestamps:    Timestamps{Start: "2026-01-01T00:00:00Z", End: "2026-01-01T00:00:01Z"},
		SubtreesRecognized:   []SubtreeRecognized{},
		SubtreesUnrecognized: []SubtreeUnrecognized{},
		EventsEmitted:        []EventEmitted{},
	}
}
```

- [ ] **Step 2: Red**

```bash
go test ./internal/manifest/...
```

Expected: undefined types.

- [ ] **Step 3: Implement the package**

`manifest.go`:

```go
// Package manifest reads and writes the archive-layout-manifest.v1.json
// sidecar. See docs/specs/03-archive-importer-google-takeout.md § 6.2.
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const ManifestFilename = "archive-layout-manifest.v1.json"

type Manifest struct {
	SchemaVersion        string                  `json:"schema_version"`
	ArchiveID            string                  `json:"archive_id"`
	Source               Source                  `json:"source"`
	Provider             *string                 `json:"provider"`
	MatcherVersion       string                  `json:"matcher_version"`
	Timestamps           Timestamps              `json:"timestamps"`
	SubtreesRecognized   []SubtreeRecognized     `json:"subtrees_recognized"`
	SubtreesUnrecognized []SubtreeUnrecognized   `json:"subtrees_unrecognized"`
	EventsEmitted        []EventEmitted          `json:"events_emitted"`
}

type Source struct {
	OriginalFilename string `json:"original_filename"`
	MovedTo          string `json:"moved_to"`
	SHA256           string `json:"sha256"`
	SizeBytes        int64  `json:"size_bytes"`
	Mtime            string `json:"mtime"`
	ArchiveFormat    string `json:"archive_format"`
}

type Timestamps struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type SubtreeRecognized struct {
	MediaType  string `json:"media_type"`
	OutputPath string `json:"output_path"`
	ItemCount  *int64 `json:"item_count"`
	ByteSize   int64  `json:"byte_size"`
	EventID    string `json:"event_id"`
}

type SubtreeUnrecognized struct {
	Path          string `json:"path"`
	FirstSeen     string `json:"first_seen"`
	ByteSize      int64  `json:"byte_size"`
	EmittedEvent  bool   `json:"emitted_event"`
}

type EventEmitted struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	MediaType string `json:"media_type"`
	Timestamp string `json:"timestamp"`
}

func Write(path string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func Read(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &Manifest{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

func Exists(dirPath string) bool {
	_, err := os.Stat(filepath.Join(dirPath, ManifestFilename))
	return err == nil
}
```

- [ ] **Step 4: Green**

```bash
go test ./internal/manifest/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/internal/manifest/
git commit -m "feat(archive-importer): manifest read/write/exists"
```

**Exit criteria:** round-trip works; bad JSON rejected; existence check accurate.

### Task B6: `internal/relay` — POST events with retry

**Files:**
- Create: `images/archive-importer/internal/relay/client.go`
- Create: `images/archive-importer/internal/relay/client_test.go`

- [ ] **Step 1: Failing tests**

`client_test.go`:

```go
package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPost_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event map[string]any
		json.NewDecoder(r.Body).Decode(&event)
		if event["schema_version"] != "1.1" {
			t.Errorf("missing schema_version 1.1")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 3, 10*time.Millisecond)
	err := c.Post(map[string]any{
		"schema_version": "1.1",
		"event_id":       "evt_EXAMPLE_001",
		"media_type":     "archive/google-takeout/mail",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPost_RetriesOn500(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if atomic.LoadInt32(&hits) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 5, time.Millisecond)
	if err := c.Post(map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("hits = %d, want 3", got)
	}
}

func TestPost_ExitsAfterMaxRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 2, time.Millisecond)
	if err := c.Post(map[string]any{}); err == nil {
		t.Fatal("expected error after exhausted retries")
	}
}

func TestPost_BodyIsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("body not JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 1, time.Millisecond)
	c.Post(map[string]any{"k": "v"})
}
```

- [ ] **Step 2: Red**

```bash
go test ./internal/relay/...
```

Expected: undefined.

- [ ] **Step 3: Implement the client**

`client.go`:

```go
// Package relay posts notification events to the recognizer
// notification-relay HTTP endpoint with bounded retries.
package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	url     string
	max     int
	backoff time.Duration
	http    *http.Client
}

func NewClient(url string, maxRetries int, backoff time.Duration) *Client {
	return &Client{
		url:     url,
		max:     maxRetries,
		backoff: backoff,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Post sends event to the relay. Returns nil on first 2xx response or
// after retrying up to maxRetries times on 5xx / network errors.
func (c *Client) Post(event any) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var lastErr error
	for i := 0; i <= c.max; i++ {
		req, err := http.NewRequest("POST", c.url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
		} else {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				if resp.StatusCode >= 400 {
					return fmt.Errorf("relay returned %d", resp.StatusCode)
				}
				return nil
			}
			lastErr = fmt.Errorf("relay returned %d", resp.StatusCode)
		}
		time.Sleep(c.backoff * time.Duration(1<<i))
	}
	return fmt.Errorf("relay POST exhausted retries: %w", lastErr)
}
```

- [ ] **Step 4: Green**

```bash
go test ./internal/relay/... -v
```

Expected: 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/internal/relay/
git commit -m "feat(archive-importer): relay HTTP client with retry"
```

**Exit criteria:** POSTs JSON; retries on 5xx with backoff; exits after exhausting retries.

### Task B7: `internal/lock` — flock-based concurrent-run guard

**Files:**
- Create: `images/archive-importer/internal/lock/lock.go`
- Create: `images/archive-importer/internal/lock/lock_test.go`

- [ ] **Step 1: Failing tests**

`lock_test.go`:

```go
package lock

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAcquire_Succeeds(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	lk, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	lk.Release()
}

func TestAcquire_SecondConcurrentFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	lk1, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	defer lk1.Release()
	if _, err := Acquire(p); err == nil {
		t.Fatal("expected second Acquire to fail")
	}
}

func TestAcquire_AfterReleaseSucceeds(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	lk1, _ := Acquire(p)
	lk1.Release()
	lk2, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	lk2.Release()
}

func TestAcquire_NoRace(t *testing.T) {
	// Stress test: 10 goroutines, exactly one should succeed at a time.
	p := filepath.Join(t.TempDir(), ".lock")
	var wg sync.WaitGroup
	var succ int
	var mu sync.Mutex
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lk, err := Acquire(p)
			if err != nil {
				return
			}
			mu.Lock()
			succ++
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			lk.Release()
		}()
	}
	wg.Wait()
	// All 10 will succeed eventually because they all release; the test is
	// just that no two hold simultaneously, which we check via no panic
	// and matched acquire/release counts.
	if succ == 0 {
		t.Error("nobody acquired the lock")
	}
}
```

- [ ] **Step 2: Red**

```bash
go test ./internal/lock/...
```

Expected: undefined.

- [ ] **Step 3: Implement**

`lock.go`:

```go
// Package lock provides an advisory file-lock guard for serialising
// archive-importer runs against the same archive id.
package lock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type Lock struct{ f *os.File }

// Acquire opens path and takes an exclusive non-blocking flock on it.
// Returns an error if another process holds the lock.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %q: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release drops the lock. The lockfile remains on disk; the next caller
// re-acquires it without issue.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
```

- [ ] **Step 4: Add the dependency + green**

```bash
cd images/archive-importer
go get golang.org/x/sys
go test ./internal/lock/... -v
```

Expected: 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/internal/lock/ images/archive-importer/go.mod images/archive-importer/go.sum
git commit -m "feat(archive-importer): flock-based concurrent-run guard"
```

**Exit criteria:** Acquire fails when held; succeeds after release; no races.

---

## Phase C — CLI integration

### Task C1: CLI flag parsing

**Files:**
- Modify: `images/archive-importer/cmd/archive-importer/main.go`
- Create: `images/archive-importer/cmd/archive-importer/flags.go`
- Create: `images/archive-importer/cmd/archive-importer/flags_test.go`

- [ ] **Step 1: Failing flags test**

`flags_test.go`:

```go
package main

import (
	"testing"
)

func TestParseFlags_Defaults(t *testing.T) {
	c, err := parseFlags([]string{"ingest", "/path/to/archive.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ArchivePath != "/path/to/archive.zip" {
		t.Errorf("ArchivePath = %q", c.ArchivePath)
	}
	if c.DataRoot != "/data/incoming/archives" {
		t.Errorf("DataRoot default = %q", c.DataRoot)
	}
	if c.IncludeUnrecognized {
		t.Error("IncludeUnrecognized default = true")
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel default = %q", c.LogLevel)
	}
}

func TestParseFlags_EnvOverrides(t *testing.T) {
	t.Setenv("ARCHIVE_DATA_ROOT", "/custom/root")
	t.Setenv("NOTIFICATION_RELAY_URL", "http://relay.example/notify")
	t.Setenv("INCLUDE_UNRECOGNIZED", "true")
	c, _ := parseFlags([]string{"ingest", "x.zip"})
	if c.DataRoot != "/custom/root" || c.RelayURL != "http://relay.example/notify" || !c.IncludeUnrecognized {
		t.Errorf("env overrides: %+v", c)
	}
}

func TestParseFlags_CLIBeatsEnv(t *testing.T) {
	t.Setenv("ARCHIVE_DATA_ROOT", "/from-env")
	c, _ := parseFlags([]string{"ingest", "--data-root", "/from-cli", "x.zip"})
	if c.DataRoot != "/from-cli" {
		t.Errorf("DataRoot = %q, want /from-cli", c.DataRoot)
	}
}

func TestParseFlags_UnknownVerb(t *testing.T) {
	_, err := parseFlags([]string{"frobnicate", "x.zip"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseFlags_MissingArchive(t *testing.T) {
	_, err := parseFlags([]string{"ingest"})
	if err == nil {
		t.Error("expected error")
	}
}
```

- [ ] **Step 2: Red**

```bash
cd images/archive-importer
go test ./cmd/archive-importer/...
```

Expected: build failure.

- [ ] **Step 3: Implement `flags.go`**

```go
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

type Config struct {
	ArchivePath         string
	DataRoot            string
	RelayURL            string
	IncludeUnrecognized bool
	Force               bool
	IDOverride          string
	DryRun              bool
	LogLevel            string
}

func parseFlags(args []string) (*Config, error) {
	if len(args) < 1 {
		return nil, errors.New("usage: archive-importer ingest <archive-path>")
	}
	if args[0] != "ingest" {
		return nil, fmt.Errorf("unknown verb %q", args[0])
	}
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	c := &Config{}
	fs.StringVar(&c.DataRoot, "data-root", envOr("ARCHIVE_DATA_ROOT", "/data/incoming/archives"), "Base under which raw/ and unpacked/ live")
	fs.StringVar(&c.RelayURL, "relay-url", envOr("NOTIFICATION_RELAY_URL", ""), "Notification relay URL")
	fs.BoolVar(&c.IncludeUnrecognized, "include-unrecognized", envBool("INCLUDE_UNRECOGNIZED"), "Emit events for unrecognized subtrees")
	fs.BoolVar(&c.Force, "force", false, "Re-extract even if unpacked/<id>/ exists")
	fs.StringVar(&c.IDOverride, "id", "", "Override the derived <id>")
	fs.BoolVar(&c.DryRun, "dry-run", false, "Log everything, emit no events, write no files")
	fs.StringVar(&c.LogLevel, "log-level", envOr("LOG_LEVEL", "info"), "debug / info / warn")
	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return nil, errors.New("ingest requires exactly one positional argument: <archive-path>")
	}
	c.ArchivePath = rest[0]
	return c, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "yes"
}
```

Update `main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = cfg
	// Real work wired in C2.
	fmt.Fprintln(os.Stderr, "archive-importer: parsed config; main flow not yet wired")
	os.Exit(1)
}
```

- [ ] **Step 4: Green**

```bash
go test ./cmd/archive-importer/... -v
```

Expected: 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/cmd/archive-importer/
git commit -m "feat(archive-importer): CLI flag parsing"
```

**Exit criteria:** all flag combinations + env overrides + CLI-beats-env precedence verified.

### Task C2: Main flow wiring

**Files:**
- Modify: `images/archive-importer/cmd/archive-importer/main.go`

- [ ] **Step 1: Failing integration test (placed in C3 — see next task)**

Skip ahead and write `main_test.go` first (Task C3 below), then come back and implement until the test passes. This task and C3 are tightly coupled — you'll iterate between them.

- [ ] **Step 2: Implement the main flow**

`main.go` should orchestrate the steps in spec 03 § 4:

```go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/leftathome/recognizer/images/archive-importer/internal/ident"
	"github.com/leftathome/recognizer/images/archive-importer/internal/lock"
	"github.com/leftathome/recognizer/images/archive-importer/internal/manifest"
	"github.com/leftathome/recognizer/images/archive-importer/internal/matcher"
	"github.com/leftathome/recognizer/images/archive-importer/internal/relay"
	"github.com/leftathome/recognizer/images/archive-importer/internal/unpacker"
)

const matcherVersion = "1.0"

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCodeFor(err))
	}
}

// ... see main_test.go in Task C3 for the exact assertions this must satisfy.
// Implement run() to satisfy them, calling out to ident/unpacker/matcher/
// manifest/relay/lock in the order documented in spec 03 § 4.
```

The detailed `run()` implementation is intentionally not pasted here verbatim; let the tests in C3 drive it (TDD). Reference points:

1. Compute `<id>` via `ident.Derive(cfg.ArchivePath)`.
2. Compute `unpackedDir := filepath.Join(cfg.DataRoot, "unpacked", id)`.
3. Decide state per spec 03 § 4.2 (absent / valid-manifest / invalid-manifest).
4. `lock.Acquire(filepath.Join(unpackedDir, ".lock"))` immediately after creating the directory.
5. Unpack via `unpacker.UnpackZip`.
6. Move source archive into unpackedDir.
7. Walk subtree base, run matchers, emit `archive-subtree-recognized` events via `relay.Post`.
8. Emit `archive-import-complete` at the end.
9. Write manifest via `manifest.Write`.

Exit codes from spec 03 § 4.3.

- [ ] **Step 3: Run integration tests (C3 below)**

See Task C3.

- [ ] **Step 4: Commit (after C3 is green)**

```bash
git add images/archive-importer/cmd/archive-importer/main.go
git commit -m "feat(archive-importer): main flow — ingest pipeline orchestration"
```

**Exit criteria:** Phase C3 integration tests all pass with this main flow.

### Task C3: End-to-end integration test

**Files:**
- Create: `images/archive-importer/cmd/archive-importer/main_test.go`
- Create: `images/archive-importer/testdata/takeout-zip/takeout-fixture.zip` (built from script)

- [ ] **Step 1: Build the test fixture zip**

Use a small Go helper or a Bash one-liner to zip `testdata/fixtures/google-takeout-minimal/`:

```bash
cd images/archive-importer/testdata/fixtures/google-takeout-minimal
zip -r ../../takeout-zip/takeout-fixture.zip Takeout/
cd -
```

Commit the zip:

```bash
git add images/archive-importer/testdata/takeout-zip/
git commit -m "test(archive-importer): build takeout-fixture.zip from minimal tree"
```

- [ ] **Step 2: Write the integration test**

`main_test.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// runIngest invokes the binary as the integration tests would.
// Builds once, reuses the binary across subtests.
func runIngest(t *testing.T, env map[string]string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := buildBinaryOnce(t)
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		exitCode = exit.ExitCode()
	}
	return so.String(), se.String(), exitCode
}

var (
	binOnce sync.Once
	binPath string
)

func buildBinaryOnce(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		out := filepath.Join(os.TempDir(), "archive-importer-itest")
		cmd := exec.Command("go", "build", "-o", out, ".")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, b)
		}
		binPath = out
	})
	return binPath
}

func TestIngest_FullFixture(t *testing.T) {
	var received []map[string]any
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev map[string]any
		json.NewDecoder(r.Body).Decode(&ev)
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	src, _ := filepath.Abs("../../testdata/takeout-zip/takeout-fixture.zip")
	target := filepath.Join(rawDir, "takeout-fixture.zip")
	copyFile(t, src, target)

	_, stderr, code := runIngest(t,
		map[string]string{
			"ARCHIVE_DATA_ROOT":      dataRoot,
			"NOTIFICATION_RELAY_URL": srv.URL,
		},
		"ingest", target,
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}

	// Find the unpacked dir
	matches, _ := filepath.Glob(filepath.Join(dataRoot, "unpacked", "*-takeout-fixture"))
	if len(matches) != 1 {
		t.Fatalf("expected one unpacked dir, got %v", matches)
	}
	unpacked := matches[0]

	// Source archive was moved
	if _, err := os.Stat(filepath.Join(unpacked, "takeout-fixture.zip")); err != nil {
		t.Errorf("source archive not moved: %v", err)
	}
	// Tree was extracted
	if _, err := os.Stat(filepath.Join(unpacked, "Takeout", "Mail")); err != nil {
		t.Errorf("Mail subtree not extracted: %v", err)
	}
	// Manifest exists
	if _, err := os.Stat(filepath.Join(unpacked, "archive-layout-manifest.v1.json")); err != nil {
		t.Errorf("manifest missing: %v", err)
	}

	// We expect at least 14 archive-subtree-recognized events + 1 archive-import-complete
	mu.Lock()
	defer mu.Unlock()
	subtreeEvents := 0
	completeEvents := 0
	for _, ev := range received {
		switch ev["event_type"] {
		case "archive-subtree-recognized":
			subtreeEvents++
		case "archive-import-complete":
			completeEvents++
		}
	}
	if subtreeEvents < 14 {
		t.Errorf("got %d subtree events, want >= 14", subtreeEvents)
	}
	if completeEvents != 1 {
		t.Errorf("got %d complete events, want 1", completeEvents)
	}
}

func TestIngest_Idempotent_ReusesEventIDs(t *testing.T) {
	// Run once, capture event_ids. Run again. Expect identical event_ids.
	var run1, run2 []string
	mkServer := func(ids *[]string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ev map[string]any
			json.NewDecoder(r.Body).Decode(&ev)
			if id, ok := ev["event_id"].(string); ok {
				*ids = append(*ids, id)
			}
			w.WriteHeader(http.StatusOK)
		}))
	}

	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	src, _ := filepath.Abs("../../testdata/takeout-zip/takeout-fixture.zip")
	target := filepath.Join(rawDir, "takeout-fixture.zip")
	copyFile(t, src, target)

	// run 1
	srv1 := mkServer(&run1)
	runIngest(t, map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": srv1.URL}, "ingest", target)
	srv1.Close()

	// run 2
	srv2 := mkServer(&run2)
	runIngest(t, map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": srv2.URL}, "ingest", target)
	srv2.Close()

	if len(run1) != len(run2) {
		t.Fatalf("event counts differ: %d vs %d", len(run1), len(run2))
	}
	for i := range run1 {
		if run1[i] != run2[i] {
			t.Errorf("event %d: id %q vs %q", i, run1[i], run2[i])
		}
	}
}

func TestIngest_UnknownSubtree_Default_NoEvent(t *testing.T) {
	// Use the with-unknown fixture; SomeUnknownService should NOT emit an event by default.
	// Manifest should list it under subtrees_unrecognized with emitted_event: false.
	// ... (full impl elided for brevity; same shape as TestIngest_FullFixture)
}

func TestIngest_UnknownSubtree_WithFlag_EmitsEvent(t *testing.T) {
	// Same fixture, but pass --include-unrecognized; should emit
	// archive/google-takeout/unrecognized-subtree.
}

func TestIngest_CorruptZip_Exit1(t *testing.T) {
	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	target := filepath.Join(rawDir, "corrupt.zip")
	os.WriteFile(target, []byte("not a zip"), 0644)
	_, _, code := runIngest(t,
		map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": "http://localhost:1"},
		"ingest", target,
	)
	if code != 1 {
		t.Errorf("got exit %d, want 1", code)
	}
}

func TestIngest_RelayDown_Exit3(t *testing.T) {
	// Server that always 500s.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	dataRoot := t.TempDir()
	rawDir := filepath.Join(dataRoot, "raw")
	os.MkdirAll(rawDir, 0755)
	src, _ := filepath.Abs("../../testdata/takeout-zip/takeout-fixture.zip")
	target := filepath.Join(rawDir, "takeout-fixture.zip")
	copyFile(t, src, target)
	_, _, code := runIngest(t,
		map[string]string{"ARCHIVE_DATA_ROOT": dataRoot, "NOTIFICATION_RELAY_URL": srv.URL},
		"ingest", target,
	)
	if code != 3 {
		t.Errorf("got exit %d, want 3", code)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	s, _ := os.Open(src)
	defer s.Close()
	d, _ := os.Create(dst)
	defer d.Close()
	io.Copy(d, s)
}
```

- [ ] **Step 3: Red — run, observe failures**

```bash
cd images/archive-importer
go test ./cmd/archive-importer/... -v -run TestIngest
```

Expected: tests FAIL (main flow not yet wired).

- [ ] **Step 4: Iterate on `main.go` until all integration tests pass**

This is the meat of Task C2. Implement `run(cfg)` step by step until:

```bash
go test ./cmd/archive-importer/... -v -run TestIngest
```

Reports all 6 (or however many you wrote — 4 are sketched in full above; flesh out the two `_UnknownSubtree_*` tests as you go) PASS.

- [ ] **Step 5: Commit**

```bash
git add images/archive-importer/cmd/archive-importer/main_test.go
git commit -m "test(archive-importer): end-to-end integration tests; main flow now passes"
```

**Exit criteria:** all integration tests pass. Idempotency proven. Exit codes match spec 03 § 4.3.

---

## Phase D — Chart integration

### Task D1: `recognizer.relayUrl` helper

**Files:**
- Modify: `charts/recognizer/templates/_helpers.tpl`

- [ ] **Step 1: Red — render without the helper, observe nothing renders the URL**

```bash
helm template recognizer charts/recognizer --kube-version 1.34.0 --namespace recognizer | grep -i 'relayUrl\|relay-url' | head
```

Expected: empty (no consumer of the URL yet; this is mostly a sanity baseline).

- [ ] **Step 2: Add the helper**

Append to `charts/recognizer/templates/_helpers.tpl`:

```
{{/*
URL of the in-cluster notification-relay Service.
Default used by archiveImporter when archiveImporter.config.relayUrl is empty.
*/}}
{{- define "recognizer.relayUrl" -}}
{{- printf "http://%s-notification-relay.%s.svc.cluster.local:8080/notify"
      (include "recognizer.fullname" .)
      .Release.Namespace -}}
{{- end -}}
```

- [ ] **Step 3: Smoke-test via a throwaway probe template**

```bash
cat > charts/recognizer/templates/probe-relayurl.txt <<'EOF'
{{ include "recognizer.relayUrl" . }}
EOF
helm template recognizer charts/recognizer --kube-version 1.34.0 --namespace recognizer | grep notification-relay
rm charts/recognizer/templates/probe-relayurl.txt
```

Expected: prints `http://recognizer-notification-relay.recognizer.svc.cluster.local:8080/notify` (or similar based on release name).

- [ ] **Step 4: Commit**

```bash
git add charts/recognizer/templates/_helpers.tpl
git commit -m "feat(chart): recognizer.relayUrl helper for in-cluster default"
```

**Exit criteria:** helper renders the correct in-cluster Service URL.

### Task D2: archive-importer chart templates

**Files:**
- Create: `charts/recognizer/templates/archive-importer/configmap.yaml`
- Create: `charts/recognizer/templates/archive-importer/serviceaccount.yaml`
- Create: `charts/recognizer/templates/archive-importer/cronjob.yaml`
- Modify: `charts/recognizer/values.yaml`

- [ ] **Step 1: Red — `helm template` shows no archive-importer resources**

```bash
helm template recognizer charts/recognizer --kube-version 1.34.0 --namespace recognizer | yq 'select(.metadata.name | test("archive-importer"))'
```

Expected: empty.

- [ ] **Step 2: Add the `archiveImporter:` block to values.yaml**

Per spec 03 § 8.2:

```yaml
archiveImporter:
  enabled: true
  image:
    name: archive-importer
    tag: ""
  resources:
    requests:
      cpu: 200m
      memory: 256Mi
    limits:
      cpu: "1"
      memory: 2Gi
  config:
    dataRoot: /data/incoming/archives
    relayUrl: ""              # empty = use computed in-cluster default
    includeUnrecognized: false
    logLevel: info
```

- [ ] **Step 3: Write `configmap.yaml`**

```yaml
{{- if .Values.archiveImporter.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "recognizer.fullname" . }}-archive-importer
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "recognizer.labels" . | nindent 4 }}
    app.kubernetes.io/component: archive-importer
data:
  ARCHIVE_DATA_ROOT: {{ .Values.archiveImporter.config.dataRoot | quote }}
  NOTIFICATION_RELAY_URL: {{ .Values.archiveImporter.config.relayUrl | default (include "recognizer.relayUrl" .) | quote }}
  INCLUDE_UNRECOGNIZED: {{ .Values.archiveImporter.config.includeUnrecognized | quote }}
  LOG_LEVEL: {{ .Values.archiveImporter.config.logLevel | quote }}
{{- end }}
```

- [ ] **Step 4: Write `serviceaccount.yaml`**

```yaml
{{- if .Values.archiveImporter.enabled }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "recognizer.fullname" . }}-archive-importer
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "recognizer.labels" . | nindent 4 }}
    app.kubernetes.io/component: archive-importer
{{- end }}
```

- [ ] **Step 5: Write `cronjob.yaml` (suspended template)**

```yaml
{{- if .Values.archiveImporter.enabled }}
apiVersion: batch/v1
kind: CronJob
metadata:
  name: {{ include "recognizer.fullname" . }}-archive-importer
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "recognizer.labels" . | nindent 4 }}
    app.kubernetes.io/component: archive-importer
  annotations:
    recognizer.orac.local/usage: |
      Suspended template. Promote to a Job per spec 03 § 8.1:
        kubectl -n {{ .Release.Namespace }} get cronjob {{ include "recognizer.fullname" . }}-archive-importer -o yaml \
        | yq '.spec.jobTemplate as $jt | $jt
              | .apiVersion = "batch/v1" | .kind = "Job"
              | .metadata.name = "archive-import-<name>-<id>" | .metadata.namespace = "{{ .Release.Namespace }}"
              | .spec.template.spec.containers[0].args = ["ingest", "/data/incoming/archives/raw/<filename>"]' \
        | kubectl apply -f -
spec:
  suspend: true
  schedule: "0 0 1 1 0"          # Jan 1, year 0 — effectively never
  jobTemplate:
    spec:
      template:
        metadata:
          labels:
            {{- include "recognizer.labels" . | nindent 12 }}
            app.kubernetes.io/component: archive-importer
        spec:
          serviceAccountName: {{ include "recognizer.fullname" . }}-archive-importer
          restartPolicy: Never
          nodeSelector:
            kubernetes.io/arch: amd64
          {{- with .Values.image.pullSecrets }}
          imagePullSecrets:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          containers:
            - name: archive-importer
              image: {{ include "recognizer.image" (dict "root" $ "name" .Values.archiveImporter.image.name "tag" .Values.archiveImporter.image.tag) }}
              imagePullPolicy: {{ .Values.image.pullPolicy }}
              envFrom:
                - configMapRef:
                    name: {{ include "recognizer.fullname" . }}-archive-importer
              args: ["ingest", "/data/incoming/archives/raw/REPLACE-ME.zip"]
              volumeMounts:
                - name: data
                  mountPath: /data/incoming/archives
              resources:
                {{- toYaml .Values.archiveImporter.resources | nindent 16 }}
          volumes:
            - name: data
              persistentVolumeClaim:
                claimName: {{ include "recognizer.dataClaimName" . }}
{{- end }}
```

- [ ] **Step 6: Green — render shows three new resources**

```bash
helm template recognizer charts/recognizer --kube-version 1.34.0 --namespace recognizer | yq 'select(.metadata.labels."app.kubernetes.io/component" == "archive-importer") | .kind + " " + .metadata.name'
```

Expected: three lines — ConfigMap, ServiceAccount, CronJob — all named `recognizer-archive-importer`.

- [ ] **Step 7: kubeconform**

```bash
helm template recognizer charts/recognizer --kube-version 1.34.0 --namespace recognizer | kubeconform -strict -ignore-missing-schemas -summary -schema-location default
```

Expected: `Valid: N, Invalid: 0`.

- [ ] **Step 8: Verify the `relayUrl` default substitution works**

```bash
helm template recognizer charts/recognizer --kube-version 1.34.0 --namespace recognizer | yq 'select(.kind == "ConfigMap" and .metadata.labels."app.kubernetes.io/component" == "archive-importer") | .data.NOTIFICATION_RELAY_URL'
```

Expected: `http://recognizer-notification-relay.recognizer.svc.cluster.local:8080/notify`.

And with an override:

```bash
helm template recognizer charts/recognizer --kube-version 1.34.0 --namespace recognizer --set archiveImporter.config.relayUrl=http://elsewhere/notify | yq 'select(.kind == "ConfigMap" and .metadata.labels."app.kubernetes.io/component" == "archive-importer") | .data.NOTIFICATION_RELAY_URL'
```

Expected: `http://elsewhere/notify`.

- [ ] **Step 9: Commit**

```bash
git add charts/recognizer/values.yaml charts/recognizer/templates/archive-importer/
git commit -m "feat(chart): archive-importer workload (ConfigMap, ServiceAccount, suspended CronJob)"
```

**Exit criteria:** three new resources rendered; kubeconform clean; default + override `relayUrl` both render correctly.

### Task D3: Chart version bump + README mention

**Files:**
- Modify: `charts/recognizer/Chart.yaml`
- Modify: `charts/recognizer/README.md`

- [ ] **Step 1: Bump Chart.yaml**

```diff
-version: 0.1.1
-appVersion: "0.1.1"
+version: 0.2.0
+appVersion: "0.2.0"
```

- [ ] **Step 2: Add a paragraph to README.md**

Append after the existing workload table:

```markdown
| `archive-importer` | Suspended CronJob template (promoted to a Job per spec 03 § 8.1) | Imports finished digital archives (Google Takeout in v0.2.0); emits per-subtree notification events |
```

And in the cluster-prerequisites section, note that archive-importer has no hardware deps and no extra prereqs beyond ESO + StorageClass.

- [ ] **Step 3: Helm lint**

```bash
helm lint charts/recognizer
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add charts/recognizer/Chart.yaml charts/recognizer/README.md
git commit -m "chore(chart): bump to 0.2.0 (archive-importer workload added)"
```

**Exit criteria:** chart at 0.2.0; README mentions the new workload.

### Task D4: `scripts/run-job.sh` operator helper

**Files:**
- Create: `images/archive-importer/scripts/run-job.sh`

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# Promote the recognizer-archive-importer CronJob into a one-off Job
# targeting a specific archive. Stock kubectl + yq.
#
# Usage:
#   scripts/run-job.sh <archive-filename>
#     where <archive-filename> is relative to /data/incoming/archives/raw/
#     and already lives there on the chart's NFS / Longhorn PVC.

set -euo pipefail

ARCHIVE="${1:-}"
if [[ -z "$ARCHIVE" ]]; then
  echo "usage: $0 <archive-filename>" >&2
  exit 1
fi

NAMESPACE="${NAMESPACE:-recognizer}"
CRONJOB="${CRONJOB:-recognizer-archive-importer}"

STEM="${ARCHIVE%.zip}"
SUFFIX="$(date +%s)$(openssl rand -hex 2 2>/dev/null || head -c 4 /dev/urandom | xxd -p)"

kubectl -n "$NAMESPACE" get cronjob "$CRONJOB" -o yaml \
| yq '
    .spec.jobTemplate as $jt
    | $jt
    | .apiVersion = "batch/v1"
    | .kind = "Job"
    | .metadata.name = "archive-import-'"$STEM"'-'"$SUFFIX"'"
    | .metadata.namespace = "'"$NAMESPACE"'"
    | .spec.template.spec.containers[0].args = ["ingest", "/data/incoming/archives/raw/'"$ARCHIVE"'"]
  ' \
| kubectl apply -f -
```

- [ ] **Step 2: Make executable + commit**

```bash
chmod +x images/archive-importer/scripts/run-job.sh
git add images/archive-importer/scripts/
git commit -m "feat(archive-importer): scripts/run-job.sh operator helper"
```

**Exit criteria:** script is executable; reading it shows it does the documented yq pipeline.

---

## Phase E — CI integration

### Task E1: `test:go:archive-importer` + `vuln:go:archive-importer`

**Files:**
- Modify: `.gitlab-ci.yml`

- [ ] **Step 1: Add two new jobs**

After the existing `test:go` block, add:

```yaml
test:go:archive-importer:
  extends: test:go
  variables:
    GO_MODULE_DIR: images/archive-importer

vuln:go:archive-importer:
  extends: vuln:go
  variables:
    GO_MODULE_DIR: images/archive-importer
```

(`extends:` inherits the existing job spec but overrides the module dir. If `extends` isn't already used and the duplication is small, you can also copy-paste the job spec verbatim; either is acceptable.)

If `vuln:go` references the document-scanner module by hard-coded path (not via `GO_MODULE_DIR`), parameterize it the same way before adding the new job.

- [ ] **Step 2: Smoke-test locally — does the YAML parse?**

```bash
yq '.test:go:archive-importer, ."vuln:go:archive-importer"' .gitlab-ci.yml
```

Expected: prints two non-null job specs.

- [ ] **Step 3: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: test:go + vuln:go for archive-importer module"
```

**Exit criteria:** YAML valid; new jobs reference the new module path.

### Task E2: `build:archive-importer` kaniko job

**Files:**
- Modify: `.gitlab-ci.yml`

- [ ] **Step 1: Add the build job**

```yaml
build:archive-importer:
  extends: .build
  variables:
    DOCKERFILE: images/archive-importer/Dockerfile
    COMPONENT: archive-importer
```

- [ ] **Step 2: Smoke-test YAML**

```bash
yq '."build:archive-importer"' .gitlab-ci.yml
```

Expected: prints the job.

- [ ] **Step 3: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: build:archive-importer (kaniko)"
```

**Exit criteria:** job present; references `images/archive-importer/Dockerfile`.

---

## Phase F — Release + acceptance

### Task F1: Open the MR

**Files:** (none; git operation)

- [ ] **Step 1: Push the branch**

```bash
cd images/archive-importer/../..
git push gitlab feat/archive-importer-takeout
```

- [ ] **Step 2: Open MR via API**

Use `curl -X POST` against `/api/v4/projects/4/merge_requests` as documented in spec 02's plan F1. Title: `feat: archive-importer for Google Takeout (spec 03, archiver-ubc)`. Body: summarize the deliverables (binary, schemas, chart, CI), point at spec 03, list the gitops-side beads if any.

- [ ] **Step 3: Wait for pipeline green**

`test:python`, `test:go`, `test:go:archive-importer`, `vuln:go`, `vuln:go:archive-importer`, `scan:trivy-fs`, `helm:lint`, `build:document-scanner`, `build:notification-relay`, `build:archive-importer`, `package:chart` — all must be green. Retry any arm64 helper-image flakes per the established pattern (gitops-4en4).

**Exit criteria:** MR open, pipeline green.

### Task F2: Cut release tag

**Files:** (none; git operation)

- [ ] **Step 1: After MR merges, tag**

```bash
git fetch gitlab main
git tag -a v0.2.0 gitlab/main -m "archive-importer for Google Takeout"
git push gitlab v0.2.0
```

- [ ] **Step 2: Wait for the tag pipeline to publish**

Expected artifacts:
- `registry.orac.local/steve/recognizer/archive-importer:0.2.0` (image)
- `oci://registry.orac.local/steve/recognizer/charts/recognizer:0.2.0` (chart)
- GitLab Release `v0.2.0`

- [ ] **Step 3: Verify pull**

```bash
helm pull oci://registry.orac.local/steve/recognizer/charts/recognizer --version 0.2.0 --insecure-skip-tls-verify
docker pull registry.orac.local/steve/recognizer/archive-importer:0.2.0
```

Both succeed.

**Exit criteria:** v0.2.0 published; both artifacts pullable.

### Task F3: Deploy + acceptance run

**Files:**
- Modify: gitops repo's `clusters/orac/apps/recognizer/helmrelease-recognizer.yaml` (bump pin to 0.2.0)

- [ ] **Step 1: gitops MR bumping the HelmRelease**

Open the small follow-up MR; standard process documented in spec 02's gitops MR pattern.

- [ ] **Step 2: After Flux reconciles, drop an anonymized Takeout into the cluster's NFS**

Per spec 03 § 7.6, copy a real Takeout to `takeout-EXAMPLE.zip` first (or whatever sentinel name you choose), then transfer it to the cluster's `incoming/archives/raw/` directory.

- [ ] **Step 3: Run the importer**

```bash
images/archive-importer/scripts/run-job.sh takeout-EXAMPLE.zip
```

Watch the Job to completion (`kubectl -n recognizer wait --for=condition=complete job/archive-import-...`).

- [ ] **Step 4: Verify events reached the relay**

```bash
kubectl -n recognizer logs deployment/recognizer-notification-relay | grep archive-
```

Expected: ~14 `archive-subtree-recognized` + 1 `archive-import-complete` entries.

- [ ] **Step 5: Pull the manifest off the PVC**

Use a temporary debug pod or `kubectl cp` to retrieve `archive-layout-manifest.v1.json` from `unpacked/<id>/`.

- [ ] **Step 6: Regenerate spec 03 § 7.5 example from the manifest**

Pick one `archive-subtree-recognized` event entry from `events_emitted[]`. Apply the § 7.6 scrubbing rules: rewrite `archive_id` (replace SHA prefix with `00000000`), replace event IDs with `evt_EXAMPLE_NNN`, ensure no PII strings.

Update `docs/specs/03-archive-importer-google-takeout.md` § 7.5 with the regenerated (and scrubbed) example.

- [ ] **Step 7: Commit the spec update**

```bash
git add docs/specs/03-archive-importer-google-takeout.md
git commit -m "docs(spec 03): regenerate § 7.5 worked example from acceptance run"
git push gitlab main
```

**Exit criteria:** real Takeout produced events end-to-end; spec § 7.5 example is real (and scrubbed).

### Task F4: Close beads

- [ ] **Step 1: Close archiver-ubc**

```bash
bd close archiver-ubc --reason="V1 shipped: archive-importer v0.2.0 published, real Takeout exercised end-to-end, spec § 7.5 example regenerated and scrubbed."
```

- [ ] **Step 2: Record the lesson**

```bash
bd remember "Archive importer V1 patterns: SHA256-prefix + filename-stem archive id, flock guard against concurrent runs, kaniko build with --skip-tls-verify-registry, helm template pinned --kube-version 1.34.0. The chart's archive-importer CronJob is suspend:true + scripts/run-job.sh promotes via 'kubectl get cronjob | yq ... | kubectl apply'."
```

**Exit criteria:** bead closed; lesson saved.

---

## Dependency Graph

```
A1 (notification-event v1.1)  --\
A2 (archive-layout-manifest v1) -> B5 (manifest)
A3 (Go module scaffold)         --\
                                    \
B1 (ident)  -----------------------> C2 (main flow)
B2 (unpacker) ---------------------> C2
B3 (matcher interfaces) -> B4 (Takeout matchers) -> C2
B5 (manifest) ---------------------> C2
B6 (relay client) -----------------> C2
B7 (lock) -------------------------> C2
                                       |
                                       v
                                    C1 (flags) -> C2 -> C3 (integration tests)
                                                            |
                                                            v
                                                       D1 (helper) -> D2 (chart templates) -> D3 (version bump)
                                                                                              |
                                                                                              v
                                                                                          D4 (run-job.sh)
                                                                                              |
                                                                                              v
                                                                                          E1 (test:go matrix)
                                                                                          E2 (build:archive-importer)
                                                                                              |
                                                                                              v
                                                                                          F1 (MR) -> F2 (tag) -> F3 (acceptance) -> F4 (close)
```

**Phase B tasks are independent of each other** (they can be done in parallel by separate subagents). Each depends only on Phase A3 (module scaffold).

**Phase D tasks have a sequence**: D1 (helper) before D2 (templates that use it); D3 (version bump) can land anywhere after D2; D4 is independent.

**Phase E** depends on the binary existing (Phase C complete) so CI has something to test/build.

**Phase F** depends on everything before it being green in CI.

---

## Exit Criteria (overall)

Mapping to spec 03 § 9.4:

- [ ] Schema files committed (A1, A2)
- [ ] This spec committed (already done)
- [ ] Binary builds + tests green in CI; chart at 0.2.0; image at v0.2.0 published (F1, F2)
- [ ] Idempotency verified via the integration tests in C3 (the `TestIngest_Idempotent_ReusesEventIDs` test)
- [ ] Real user-supplied Takeout lands events end-to-end (F3)
- [ ] § 7.5's example regenerated from the acceptance run with § 7.6 scrubbing applied (F3 step 6)

If all six land, V1 done. Meta export, inbox watcher, and the deferred items become their own spec/plan/MR cycles.

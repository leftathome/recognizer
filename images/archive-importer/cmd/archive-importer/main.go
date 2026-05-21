// Package main is the archive-importer CLI entrypoint.
// See docs/specs/03-archive-importer-google-takeout.md for the design.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// run dispatches by file extension to the right handler.
//
// Google Takeout delivers a mix of file types: zip volumes plus
// standalone content files for any single subtree too large to fit
// inside the zips (most commonly a .mbox split out when Mail is huge).
// Recognizer needs to handle whatever Google drops in the delivery
// folder, so the same `ingest` verb accepts both.
func run(cfg *Config) error {
	ext := strings.ToLower(filepath.Ext(cfg.ArchivePath))
	switch ext {
	case ".zip":
		return runZipImport(cfg)
	case ".mbox":
		return runMboxImport(cfg)
	default:
		return fmt.Errorf("open archive: unsupported file extension %q (supported: .zip, .mbox)", ext)
	}
}

// runZipImport unpacks a zip archive, runs the provider matcher over
// the unpacked tree, and emits per-subtree + archive-import-complete
// events. See spec 03 § 4.
func runZipImport(cfg *Config) error {
	id, err := deriveID(cfg)
	if err != nil {
		return err
	}
	unpackedDir := filepath.Join(cfg.DataRoot, "unpacked", id)
	relayClient := relay.NewClient(cfg.RelayURL, 3, time.Second)

	priorManifest, err := ensureStateAndManifest(cfg, unpackedDir)
	if err != nil {
		return err
	}

	lk, lockPath, err := acquireLock(unpackedDir, id)
	if err != nil {
		return err
	}
	defer lk.Release()
	defer os.Remove(lockPath)

	startTime := time.Now().UTC()

	var sourcePath string
	if priorManifest == nil {
		if err := unpacker.UnpackZip(cfg.ArchivePath, unpackedDir); err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
		sourcePath = filepath.Join(unpackedDir, filepath.Base(cfg.ArchivePath))
		if err := os.Rename(cfg.ArchivePath, sourcePath); err != nil {
			return fmt.Errorf("move source: %w", err)
		}
	} else {
		sourcePath = filepath.Join(cfg.DataRoot, priorManifest.Source.MovedTo)
	}

	hash, size, mtime, err := hashSize(sourcePath)
	if err != nil {
		return fmt.Errorf("hash source: %w", err)
	}

	provider := matcher.GoogleTakeoutProvider()
	detected, subtreeBase, err := provider.Detect(unpackedDir)
	if err != nil {
		return fmt.Errorf("matcher: provider detection: %w", err)
	}
	if !detected {
		return fmt.Errorf("matcher: no provider matched in %s", unpackedDir)
	}

	entries, err := os.ReadDir(subtreeBase)
	if err != nil {
		return fmt.Errorf("read subtree base: %w", err)
	}

	var recognized []manifest.SubtreeRecognized
	var unrecognized []manifest.SubtreeUnrecognized
	var events []manifest.EventEmitted

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		dirPath := filepath.Join(subtreeBase, dirName)
		outputPath := dirPath
		matched := false
		for _, m := range provider.Subtrees {
			ok, err := m.Matches(dirPath, dirName)
			if err != nil {
				return fmt.Errorf("matcher %s: %w", m.MediaType(), err)
			}
			if ok {
				mt := m.MediaType()
				eid := deriveEventID(id, mt, outputPath)
				byteSize, _ := dirSize(dirPath)
				if !cfg.DryRun {
					ev := newEvent("archive-subtree-recognized", mt, outputPath, eid)
					ev.Metadata["archive_format"] = "zip"
					ev.Metadata["byte_size"] = byteSize
					if err := relayClient.Post(ev); err != nil {
						return err
					}
					events = append(events, manifest.EventEmitted{
						EventID: eid, EventType: ev.EventType, MediaType: mt, Timestamp: ev.Timestamp,
					})
				}
				recognized = append(recognized, manifest.SubtreeRecognized{
					MediaType: mt, OutputPath: outputPath, ItemCount: nil, ByteSize: byteSize, EventID: eid,
				})
				matched = true
				break
			}
		}
		if !matched {
			byteSize, _ := dirSize(dirPath)
			emitted := false
			if cfg.IncludeUnrecognized && !cfg.DryRun {
				mt := "archive/google-takeout/unrecognized-subtree"
				eid := deriveEventID(id, mt, outputPath)
				ev := newEvent("archive-subtree-recognized", mt, outputPath, eid)
				ev.Metadata["byte_size"] = byteSize
				if err := relayClient.Post(ev); err != nil {
					return err
				}
				events = append(events, manifest.EventEmitted{
					EventID: eid, EventType: ev.EventType, MediaType: mt, Timestamp: ev.Timestamp,
				})
				emitted = true
			}
			unrecognized = append(unrecognized, manifest.SubtreeUnrecognized{
				Path: filepath.Join("Takeout", dirName), FirstSeen: time.Now().UTC().Format(time.RFC3339),
				ByteSize: byteSize, EmittedEvent: emitted,
			})
		}
	}

	if !cfg.DryRun {
		eid := deriveEventID(id, "archive/google-takeout", unpackedDir)
		ev := newEvent("archive-import-complete", "archive/google-takeout", unpackedDir, eid)
		ev.Metadata["archive_format"] = "zip"
		ev.Metadata["byte_size"] = size
		if err := relayClient.Post(ev); err != nil {
			return err
		}
		events = append(events, manifest.EventEmitted{
			EventID: eid, EventType: ev.EventType, MediaType: ev.MediaType, Timestamp: ev.Timestamp,
		})
	}

	return writeManifest(cfg, unpackedDir, id, sourcePath, hash, size, mtime, "zip",
		provider.Name, startTime, recognized, unrecognized, events)
}

// runMboxImport handles a standalone .mbox file delivered alongside
// (or instead of) Takeout zip volumes when Mail is too large to fit
// inside a zip. No unpack; just hash, move into unpacked/<id>/, emit
// the archive/google-takeout/mail event pointing at the file, and
// write a single-entry manifest. Glovebox's mbox-importer (spec 09)
// is the downstream consumer; it reads the file directly and handles
// per-message parsing.
func runMboxImport(cfg *Config) error {
	id, err := deriveID(cfg)
	if err != nil {
		return err
	}
	unpackedDir := filepath.Join(cfg.DataRoot, "unpacked", id)
	relayClient := relay.NewClient(cfg.RelayURL, 3, time.Second)

	priorManifest, err := ensureStateAndManifest(cfg, unpackedDir)
	if err != nil {
		return err
	}

	lk, lockPath, err := acquireLock(unpackedDir, id)
	if err != nil {
		return err
	}
	defer lk.Release()
	defer os.Remove(lockPath)

	startTime := time.Now().UTC()

	var sourcePath string
	if priorManifest == nil {
		sourcePath = filepath.Join(unpackedDir, filepath.Base(cfg.ArchivePath))
		if err := os.Rename(cfg.ArchivePath, sourcePath); err != nil {
			return fmt.Errorf("move source: %w", err)
		}
	} else {
		sourcePath = filepath.Join(cfg.DataRoot, priorManifest.Source.MovedTo)
	}

	hash, size, mtime, err := hashSize(sourcePath)
	if err != nil {
		return fmt.Errorf("hash source: %w", err)
	}

	mt := "archive/google-takeout/mail"
	eid := deriveEventID(id, mt, sourcePath)
	var events []manifest.EventEmitted
	if !cfg.DryRun {
		ev := newEvent("archive-subtree-recognized", mt, sourcePath, eid)
		ev.Metadata["archive_format"] = "none"
		ev.Metadata["byte_size"] = size
		if err := relayClient.Post(ev); err != nil {
			return err
		}
		events = append(events, manifest.EventEmitted{
			EventID: eid, EventType: ev.EventType, MediaType: mt, Timestamp: ev.Timestamp,
		})
	}
	recognized := []manifest.SubtreeRecognized{{
		MediaType: mt, OutputPath: sourcePath, ItemCount: nil, ByteSize: size, EventID: eid,
	}}

	if !cfg.DryRun {
		completeEID := deriveEventID(id, "archive/google-takeout", unpackedDir)
		ev := newEvent("archive-import-complete", "archive/google-takeout", unpackedDir, completeEID)
		ev.Metadata["archive_format"] = "none"
		ev.Metadata["byte_size"] = size
		if err := relayClient.Post(ev); err != nil {
			return err
		}
		events = append(events, manifest.EventEmitted{
			EventID: completeEID, EventType: ev.EventType, MediaType: ev.MediaType, Timestamp: ev.Timestamp,
		})
	}

	return writeManifest(cfg, unpackedDir, id, sourcePath, hash, size, mtime, "none",
		"google-takeout", startTime, recognized, nil, events)
}

// --- shared helpers ---

func deriveID(cfg *Config) (string, error) {
	if cfg.IDOverride != "" {
		return cfg.IDOverride, nil
	}
	id, err := ident.Derive(cfg.ArchivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	return id, nil
}

func ensureStateAndManifest(cfg *Config, unpackedDir string) (*manifest.Manifest, error) {
	switch detectState(unpackedDir) {
	case stateAbsent:
		if err := os.MkdirAll(unpackedDir, 0755); err != nil {
			return nil, err
		}
		return nil, nil
	case stateManifestValid:
		m, err := manifest.Read(filepath.Join(unpackedDir, manifest.ManifestFilename))
		if err != nil {
			return nil, fmt.Errorf("read prior manifest: %w", err)
		}
		return m, nil
	case stateManifestMissingOrInvalid:
		if !cfg.Force {
			return nil, fmt.Errorf("partial state at %s; re-run with --force or remove the directory", unpackedDir)
		}
		os.RemoveAll(unpackedDir)
		if err := os.MkdirAll(unpackedDir, 0755); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return nil, nil
}

func acquireLock(unpackedDir, id string) (*lock.Lock, string, error) {
	lockPath := filepath.Join(unpackedDir, ".lock")
	lk, err := lock.Acquire(lockPath)
	if err != nil {
		return nil, "", fmt.Errorf("another import is in progress for %s: %w", id, err)
	}
	return lk, lockPath, nil
}

func writeManifest(
	cfg *Config,
	unpackedDir, id, sourcePath, hash string,
	size int64, mtime time.Time, archiveFormat string,
	providerName string, startTime time.Time,
	recognized []manifest.SubtreeRecognized,
	unrecognized []manifest.SubtreeUnrecognized,
	events []manifest.EventEmitted,
) error {
	endTime := time.Now().UTC()
	movedToRel, err := filepath.Rel(cfg.DataRoot, sourcePath)
	if err != nil {
		return fmt.Errorf("manifest write: compute moved_to relpath: %w", err)
	}
	m := &manifest.Manifest{
		SchemaVersion: "1.0",
		ArchiveID:     id,
		Source: manifest.Source{
			OriginalFilename: filepath.Base(cfg.ArchivePath),
			MovedTo:          movedToRel,
			SHA256:           hash,
			SizeBytes:        size,
			Mtime:            mtime.UTC().Format(time.RFC3339),
			ArchiveFormat:    archiveFormat,
		},
		Provider:             &providerName,
		MatcherVersion:       matcherVersion,
		Timestamps:           manifest.Timestamps{Start: startTime.Format(time.RFC3339), End: endTime.Format(time.RFC3339)},
		SubtreesRecognized:   recognized,
		SubtreesUnrecognized: unrecognized,
		EventsEmitted:        events,
	}
	if recognized == nil {
		m.SubtreesRecognized = []manifest.SubtreeRecognized{}
	}
	if unrecognized == nil {
		m.SubtreesUnrecognized = []manifest.SubtreeUnrecognized{}
	}
	if events == nil {
		m.EventsEmitted = []manifest.EventEmitted{}
	}
	if !cfg.DryRun {
		if err := manifest.Write(filepath.Join(unpackedDir, manifest.ManifestFilename), m); err != nil {
			return fmt.Errorf("manifest write: %w", err)
		}
	}
	return nil
}

type unpackedState int

const (
	stateAbsent unpackedState = iota
	stateManifestValid
	stateManifestMissingOrInvalid
)

func detectState(dir string) unpackedState {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return stateAbsent
	}
	if !manifest.Exists(dir) {
		return stateManifestMissingOrInvalid
	}
	if _, err := manifest.Read(filepath.Join(dir, manifest.ManifestFilename)); err != nil {
		return stateManifestMissingOrInvalid
	}
	return stateManifestValid
}

func deriveEventID(archiveID, mediaType, outputPath string) string {
	h := sha256.New()
	io.WriteString(h, archiveID)
	io.WriteString(h, "|")
	io.WriteString(h, mediaType)
	io.WriteString(h, "|")
	io.WriteString(h, outputPath)
	return "evt_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func hashSize(p string) (sha string, size int64, mtime time.Time, err error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, time.Time{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, time.Time{}, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, time.Time{}, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, st.ModTime(), nil
}

func dirSize(p string) (int64, error) {
	var total int64
	err := filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		return nil
	})
	return total, err
}

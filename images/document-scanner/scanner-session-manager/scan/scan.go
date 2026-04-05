// Package scan wraps the SANE scanimage CLI for controlling the Epson DS-1630 scanner.
package scan

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Status represents the scanner's current state.
type Status string

const (
	StatusIdle     Status = "idle"
	StatusScanning Status = "scanning"
	StatusError    Status = "error"
)

// Params holds scan parameters for a single page capture.
type Params struct {
	Resolution int    // DPI (default 600)
	Mode       string // "Color", "Gray", "Lineart" (default "Color")
	Source     string // "ADF Duplex", "ADF Front", "Flatbed" (default "Flatbed")
	Format     string // "tiff", "png" (default "tiff")
	OutputPath string // file path for the scanned image
	Device     string // SANE device name (e.g. "epsonscan2:DS-1630:usb:...")
}

// DefaultParams returns scan parameters matching the spec: 600 DPI, color, TIFF.
func DefaultParams() Params {
	return Params{
		Resolution: 600,
		Mode:       "Color",
		Source:     "Flatbed",
		Format:     "tiff",
	}
}

// Commander abstracts command execution for testing.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, stderr []byte, err error)
}

// ExecCommander runs real OS commands.
type ExecCommander struct{}

func (ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Scanner controls the SANE scanimage CLI.
type Scanner struct {
	cmd Commander
}

// New creates a Scanner with the given command executor.
func New(cmd Commander) *Scanner {
	return &Scanner{cmd: cmd}
}

// DetectDevice checks if a scanner is available and returns its SANE device string.
func (s *Scanner) DetectDevice(ctx context.Context) (string, error) {
	stdout, stderr, err := s.cmd.Run(ctx, "scanimage", "-L")
	if err != nil {
		return "", fmt.Errorf("scanimage -L failed: %w: %s", err, string(stderr))
	}
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	for scanner.Scan() {
		line := scanner.Text()
		// scanimage -L output: device `epsonscan2:...' is a Epson ...
		if strings.Contains(line, "epsonscan2") || strings.Contains(line, "epson") {
			start := strings.Index(line, "`")
			end := strings.Index(line, "'")
			if start >= 0 && end > start {
				return line[start+1 : end], nil
			}
		}
	}
	return "", fmt.Errorf("no Epson scanner found in scanimage output: %s", string(stdout))
}

// ScanPage captures a single page with the given parameters.
func (s *Scanner) ScanPage(ctx context.Context, p Params) error {
	if p.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}
	if p.Device == "" {
		return fmt.Errorf("device is required")
	}

	args := []string{
		"--device-name", p.Device,
		"--resolution", fmt.Sprintf("%d", p.Resolution),
		"--mode", p.Mode,
		"--source", p.Source,
		"--format", p.Format,
		"--output-file", p.OutputPath,
	}

	_, stderr, err := s.cmd.Run(ctx, "scanimage", args...)
	if err != nil {
		return fmt.Errorf("scanimage failed: %w: %s", err, string(stderr))
	}
	return nil
}

// BuildArgs returns the scanimage CLI arguments for the given params (exported for testing).
func BuildArgs(p Params) []string {
	return []string{
		"--device-name", p.Device,
		"--resolution", fmt.Sprintf("%d", p.Resolution),
		"--mode", p.Mode,
		"--source", p.Source,
		"--format", p.Format,
		"--output-file", p.OutputPath,
	}
}

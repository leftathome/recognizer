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

// ScanMode represents SANE color modes.
type ScanMode string

const (
	ModeColor   ScanMode = "Color"
	ModeGray    ScanMode = "Gray"
	ModeLineart ScanMode = "Lineart"
)

// ScanSource represents SANE input sources.
type ScanSource string

const (
	SourceFlatbed   ScanSource = "Flatbed"
	SourceADFDuplex ScanSource = "ADF Duplex"
	SourceADFFront  ScanSource = "ADF Front"
)

// OutputFormat represents scan output file formats.
type OutputFormat string

const (
	FormatTIFF OutputFormat = "tiff"
	FormatPNG  OutputFormat = "png"
)

// Params holds scan parameters for a single page capture.
type Params struct {
	Resolution int
	Mode       ScanMode
	Source     ScanSource
	Format     OutputFormat
	OutputPath string
	Device     string
}

// DefaultParams returns scan parameters matching the spec: 600 DPI, color, TIFF.
func DefaultParams() Params {
	return Params{
		Resolution: 600,
		Mode:       ModeColor,
		Source:     SourceFlatbed,
		Format:     FormatTIFF,
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
		// scanimage -L output format: device `epsonscan2:...' is a Epson ...
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

// BuildArgs returns the scanimage CLI arguments for the given params.
func BuildArgs(p Params) []string {
	return []string{
		"--device-name", p.Device,
		"--resolution", fmt.Sprintf("%d", p.Resolution),
		"--mode", string(p.Mode),
		"--source", string(p.Source),
		"--format", string(p.Format),
		"--output-file", p.OutputPath,
	}
}

// ScanPage captures a single page with the given parameters.
func (s *Scanner) ScanPage(ctx context.Context, p Params) error {
	if p.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}
	if p.Device == "" {
		return fmt.Errorf("device is required")
	}

	_, stderr, err := s.cmd.Run(ctx, "scanimage", BuildArgs(p)...)
	if err != nil {
		return fmt.Errorf("scanimage failed: %w: %s", err, string(stderr))
	}
	return nil
}

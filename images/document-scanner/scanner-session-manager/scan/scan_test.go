package scan

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// mockCommander records calls and returns canned responses.
type mockCommander struct {
	calls   []mockCall
	results []mockResult
}

type mockCall struct {
	Name string
	Args []string
}

type mockResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

func (m *mockCommander) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	m.calls = append(m.calls, mockCall{Name: name, Args: args})
	if len(m.results) == 0 {
		return nil, nil, nil
	}
	r := m.results[0]
	m.results = m.results[1:]
	return r.Stdout, r.Stderr, r.Err
}

func TestDetectDevice_Found(t *testing.T) {
	mock := &mockCommander{
		results: []mockResult{{
			Stdout: []byte("device `epsonscan2:DS-1630:usb:04b8:0154' is a Epson DS-1630\n"),
		}},
	}
	s := New(mock)
	dev, err := s.DetectDevice(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dev != "epsonscan2:DS-1630:usb:04b8:0154" {
		t.Errorf("got device %q, want epsonscan2:DS-1630:usb:04b8:0154", dev)
	}
	if len(mock.calls) != 1 || mock.calls[0].Name != "scanimage" {
		t.Errorf("expected scanimage -L call, got %+v", mock.calls)
	}
}

func TestDetectDevice_NotFound(t *testing.T) {
	mock := &mockCommander{
		results: []mockResult{{
			Stdout: []byte("No scanners were identified.\n"),
		}},
	}
	s := New(mock)
	_, err := s.DetectDevice(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no Epson scanner found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDetectDevice_CommandError(t *testing.T) {
	mock := &mockCommander{
		results: []mockResult{{
			Stderr: []byte("scanimage: sane_get_devices() failed\n"),
			Err:    fmt.Errorf("exit status 1"),
		}},
	}
	s := New(mock)
	_, err := s.DetectDevice(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "scanimage -L failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestScanPage_Success(t *testing.T) {
	mock := &mockCommander{results: []mockResult{{}}}
	s := New(mock)
	p := DefaultParams()
	p.Device = "epsonscan2:DS-1630:usb:04b8:0154"
	p.OutputPath = "/tmp/page_001.tiff"

	err := s.ScanPage(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	call := mock.calls[0]
	if call.Name != "scanimage" {
		t.Errorf("expected scanimage, got %s", call.Name)
	}

	args := strings.Join(call.Args, " ")
	checks := []string{
		"--resolution 600",
		"--mode Color",
		"--source Flatbed",
		"--format tiff",
		"--output-file /tmp/page_001.tiff",
		"--device-name epsonscan2:DS-1630:usb:04b8:0154",
	}
	for _, c := range checks {
		if !strings.Contains(args, c) {
			t.Errorf("expected args to contain %q, got %q", c, args)
		}
	}
}

func TestScanPage_ADF_Duplex(t *testing.T) {
	mock := &mockCommander{results: []mockResult{{}}}
	s := New(mock)
	p := DefaultParams()
	p.Device = "epsonscan2:DS-1630:usb:04b8:0154"
	p.OutputPath = "/tmp/page_001.tiff"
	p.Source = "ADF Duplex"

	err := s.ScanPage(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(mock.calls[0].Args, " ")
	if !strings.Contains(args, "--source ADF Duplex") {
		t.Errorf("expected ADF Duplex source, got %q", args)
	}
}

func TestScanPage_MissingOutputPath(t *testing.T) {
	s := New(&mockCommander{})
	p := DefaultParams()
	p.Device = "test"
	err := s.ScanPage(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "output path is required") {
		t.Errorf("expected output path error, got: %v", err)
	}
}

func TestScanPage_MissingDevice(t *testing.T) {
	s := New(&mockCommander{})
	p := DefaultParams()
	p.OutputPath = "/tmp/test.tiff"
	err := s.ScanPage(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "device is required") {
		t.Errorf("expected device error, got: %v", err)
	}
}

func TestScanPage_ScanError(t *testing.T) {
	mock := &mockCommander{
		results: []mockResult{{
			Stderr: []byte("scanimage: no paper in ADF\n"),
			Err:    fmt.Errorf("exit status 7"),
		}},
	}
	s := New(mock)
	p := DefaultParams()
	p.Device = "test"
	p.OutputPath = "/tmp/test.tiff"

	err := s.ScanPage(context.Background(), p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "scanimage failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildArgs_DefaultParams(t *testing.T) {
	p := DefaultParams()
	p.Device = "testdev"
	p.OutputPath = "/tmp/out.tiff"
	args := BuildArgs(p)

	expected := []string{
		"--device-name", "testdev",
		"--resolution", "600",
		"--mode", "Color",
		"--source", "Flatbed",
		"--format", "tiff",
		"--output-file", "/tmp/out.tiff",
	}

	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, args[i], expected[i])
		}
	}
}

package lsp

import (
	"path/filepath"
	"testing"
)

func TestSupportedTargetsAreAskedOncePerServer(t *testing.T) {
	s := &Server{}
	first, err := s.supportedTargets(t.Context())
	if err != nil || len(first) == 0 {
		t.Fatalf("supportedTargets() = %d targets, %v", len(first), err)
	}

	s.opts.GoBin = filepath.Join(t.TempDir(), "no-such-go")
	again, err := s.supportedTargets(t.Context())
	if err != nil || len(again) != len(first) {
		t.Errorf("second call = %d targets, %v; want the remembered %d", len(again), err, len(first))
	}
}

func TestSupportedTargetsFailureIsNotRemembered(t *testing.T) {
	s := &Server{opts: Options{GoBin: filepath.Join(t.TempDir(), "no-such-go")}}
	if _, err := s.supportedTargets(t.Context()); err == nil {
		t.Fatal("supportedTargets() with a missing go: want an error")
	}

	s.opts.GoBin = ""
	if list, err := s.supportedTargets(t.Context()); err != nil || len(list) == 0 {
		t.Errorf("after the toolchain is found: %d targets, %v", len(list), err)
	}
}

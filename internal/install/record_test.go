package install_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/install"
)

// What WriteRecord writes, ReadRecord reads back unchanged.
func TestWriteRecordRoundTrips(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "letsgo")
	want := &install.Record{
		Schema: install.RecordSchema, Via: "selfupdate",
		Project: "letsgo", Version: "1.0.0", Tag: "v1.0.0",
		Archive: "letsgo_1.0.0_linux_amd64.tar.gz", ArchiveSHA256: "aa",
		Binary: "letsgo", BinarySHA256: "bb",
	}
	if err := install.WriteRecord(bin, want); err != nil {
		t.Fatal(err)
	}
	got, err := install.ReadRecord(bin)
	if err != nil || got == nil || *got != *want {
		t.Fatalf("ReadRecord = (%+v, %v), want %+v", got, err, want)
	}
}

func TestWriteRecordReportsAnUnwritableDirectory(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "missing", "letsgo")
	if err := install.WriteRecord(bin, &install.Record{}); err == nil {
		t.Fatal("WriteRecord into a missing directory = nil, want an error")
	}
}

func TestReadRecordWithNoRecordIsNil(t *testing.T) {
	got, err := install.ReadRecord(filepath.Join(t.TempDir(), "letsgo"))
	if got != nil || err != nil {
		t.Fatalf("ReadRecord = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestReadRecordRefusesAMalformedRecord(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "letsgo")
	if err := os.WriteFile(install.RecordPath(bin), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := install.ReadRecord(bin); err == nil {
		t.Fatal("ReadRecord of a malformed record = nil error")
	}
}

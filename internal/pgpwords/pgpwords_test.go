package pgpwords

import (
	"encoding/hex"
	"slices"
	"strings"
	"testing"
)

func TestEncodeAlternatesBetweenTheTwoLists(t *testing.T) {
	// PW-1, PW-2: position 0 is even and position 1 is odd, whatever the byte.
	if got := Encode([]byte{0x00, 0x00}); !slices.Equal(got, []string{"aardvark", "adroitness"}) {
		t.Errorf("Encode(00 00) = %v", got)
	}
	if got := Encode([]byte{0xFF, 0xFF}); !slices.Equal(got, []string{"Zulu", "Yucatan"}) {
		t.Errorf("Encode(FF FF) = %v", got)
	}
}

// The published example: a PGP fingerprint and the words Wikipedia lists for
// it (PW-2).
func TestEncodeMatchesThePublishedFingerprintExample(t *testing.T) {
	fingerprint, err := hex.DecodeString("E58294F2E9A227486E8B061B31CC528FD7FA3F19")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields("topmost Istanbul Pluto vagabond treadmill Pacific brackish dictator goldfish Medusa " +
		"afflict bravado chatter revolver Dupont midsummer stopwatch whimsical cowbell bottomless")

	if got := Encode(fingerprint); !slices.Equal(got, want) {
		t.Errorf("Encode = %v, want %v", got, want)
	}
}

func TestEncodeKeepsAllThirtyTwoBytes(t *testing.T) {
	if got := len(Encode(make([]byte, 32))); got != 32 {
		t.Errorf("len(Encode(32 bytes)) = %d, want 32", got)
	}
	if got := Encode(nil); len(got) != 0 {
		t.Errorf("Encode(nil) = %v, want none", got)
	}
}

// Every word in a table must be distinct, or two bytes would read the same.
func TestTablesHoldDistinctWords(t *testing.T) {
	for name, table := range map[string][256]string{"even": even, "odd": odd} {
		seen := map[string]bool{}
		for i, w := range table {
			if w == "" || seen[w] {
				t.Errorf("%s[%d] = %q is empty or repeated", name, i, w)
			}
			seen[w] = true
		}
	}
}

func TestRowsNumbersEveryFourthWord(t *testing.T) {
	words := Encode(make([]byte, 8))
	got := strings.Split(strings.TrimSuffix(Rows(words), "\n"), "\n")

	if len(got) != 2 {
		t.Fatalf("Rows = %q, want 2 rows", got)
	}
	if !strings.HasPrefix(got[0], "  1  aardvark") || !strings.HasPrefix(got[1], "  5  aardvark") {
		t.Errorf("Rows = %q, want rows numbered 1 and 5", got)
	}
}

func TestRowsOfAPartialLastRow(t *testing.T) {
	if got := Rows([]string{"a", "b", "c", "d", "e"}); strings.Count(got, "\n") != 2 {
		t.Errorf("Rows = %q, want 2 rows", got)
	}
}

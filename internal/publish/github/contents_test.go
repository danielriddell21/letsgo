package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReadFileDecodesWrappedBase64(t *testing.T) {
	content := strings.Repeat("formula line\n", 40)

	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/you/homebrew-tap/contents/Formula/my-tool.rb" {
			t.Errorf("path = %q", got)
		}
		// GitHub wraps the payload at 60 columns, which the strict decoder
		// rejects if the newlines are not stripped first.
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		var wrapped strings.Builder
		for len(encoded) > 60 {
			wrapped.WriteString(encoded[:60] + "\n")
			encoded = encoded[60:]
		}
		wrapped.WriteString(encoded)

		json.NewEncoder(w).Encode(map[string]string{
			"sha": "abc123", "encoding": "base64", "content": wrapped.String(),
		})
	})

	file, err := c.ReadFile(context.Background(), Repo{"you", "homebrew-tap"}, "Formula/my-tool.rb")
	if err != nil {
		t.Fatal(err)
	}
	if file.SHA != "abc123" {
		t.Errorf("sha = %q", file.SHA)
	}
	if string(file.Content) != content {
		t.Errorf("content did not round-trip")
	}
}

// A formula that is not there yet is the normal state of a first release, not
// an error to report.
func TestReadFileTreatsAbsenceAsAbsence(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	})

	file, err := c.ReadFile(context.Background(), Repo{"you", "homebrew-tap"}, "Formula/x.rb")
	if err != nil {
		t.Fatalf("a missing file should not be an error: %v", err)
	}
	if file != nil {
		t.Errorf("file = %+v, want nil", file)
	}
}

func TestWriteFileSendsBase64AndBlobSHA(t *testing.T) {
	var got struct {
		Message string `json:"message"`
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}

	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(`{}`))
	})

	err := c.WriteFile(context.Background(), Repo{"you", "homebrew-tap"}, FileInput{
		Path: "Formula/x.rb", Message: "x 1.0.0", Content: []byte("class X\nend\n"), SHA: "old",
	})
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := base64.StdEncoding.DecodeString(got.Content)
	if err != nil {
		t.Fatalf("content was not base64: %v", err)
	}
	if string(decoded) != "class X\nend\n" {
		t.Errorf("content = %q", decoded)
	}
	if got.SHA != "old" || got.Message != "x 1.0.0" {
		t.Errorf("body = %+v", got)
	}
}

// An unidentifiable licence must not be copied into a formula verbatim.
func TestRepositoryDropsNoassertion(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"description":"a tool","license":{"spdx_id":"NOASSERTION"}}`))
	})

	info, err := c.Repository(context.Background(), Repo{"you", "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Description != "a tool" {
		t.Errorf("description = %q", info.Description)
	}
	if info.License != "" {
		t.Errorf("license = %q, want empty", info.License)
	}
}

func TestDownloadURL(t *testing.T) {
	got := DownloadURL(Repo{"you", "tool"}, "v1.2.3", "tool_1.2.3_linux_amd64.tar.gz")
	want := "https://github.com/you/tool/releases/download/v1.2.3/tool_1.2.3_linux_amd64.tar.gz"
	if got != want {
		t.Errorf("DownloadURL = %q, want %q", got, want)
	}
}

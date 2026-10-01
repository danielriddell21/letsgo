package lsp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/danielriddell21/letsgo/internal/lsp"
	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

const toolBody = "#!/bin/sh\nexit 0\n"

func digestOfBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// storeInstaller stands in for the forge: it "downloads" body into the plugin
// store, or fails with err, and records what it was asked for.
func storeInstaller(t *testing.T, body string, err error, asked *[]string) lsp.PinInstaller {
	t.Helper()
	return func(_ context.Context, command, version string) error {
		*asked = append(*asked, command+"@"+version)
		if err != nil {
			return err
		}
		store, openErr := pluginstore.Open("", "")
		if openErr != nil {
			return openErr
		}
		_, putErr := store.Put(digestOfBody(body), command, []byte(body))
		return putErr
	}
}

func pinFor(command, digest string) string {
	return "plugin ldflags " + command + " v1.0.0 " + digest
}

type shownMessage struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

func execute(t *testing.T, c *client, args any) {
	t.Helper()
	c.request("workspace/executeCommand", map[string]any{"command": "letsgo.installPins", "arguments": []any{args}})
}

func awaitMessage(t *testing.T, c *client) shownMessage {
	t.Helper()
	var msg shownMessage
	if err := json.Unmarshal(c.awaitNotification("window/showMessage"), &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestInstallActionIsOfferedForAMissingPin(t *testing.T) {
	lsp.IsolatePlugins(t)
	var asked []string
	opts := lsp.Options{InstallPin: storeInstaller(t, toolBody, nil, &asked)}
	c, uri := pinEditor(t, opts, "letsgo.mod", pinFor("letsgo-env", digestOfBody(toolBody))+"\n")

	actions := codeActions(t, c, uri)
	if len(actions) != 1 {
		t.Fatalf("actions = %+v, want one", actions)
	}
	cmd := actions[0].Command
	if actions[0].Title != "Install pinned plugin: letsgo-env v1.0.0" || actions[0].Kind != "quickfix" || cmd == nil || cmd.Command != "letsgo.installPins" || len(cmd.Arguments) != 1 {
		t.Fatalf("action = %+v, want an install quickfix running letsgo.installPins", actions[0])
	}
	if len(asked) != 0 {
		t.Errorf("offering the action installed %v", asked)
	}
}

func TestInstallActionIsNotOfferedWhereNothingNeedsInstalling(t *testing.T) {
	storeDir, _ := lsp.IsolatePlugins(t)
	store, err := pluginstore.Open(storeDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(digestOfBody(toolBody), "letsgo-env", []byte(toolBody)); err != nil {
		t.Fatal(err)
	}
	var asked []string
	installer := storeInstaller(t, toolBody, nil, &asked)
	missing := pinFor("letsgo-env", newDigest) + "\n"
	installed := pinFor("letsgo-env", digestOfBody(toolBody)) + "\n"

	tests := []struct {
		name, file, text string
		opts             lsp.Options
	}{
		{"installed", "letsgo.mod", installed, lsp.Options{InstallPin: installer}},
		{"restricted", "letsgo.mod", missing, lsp.Options{Restricted: true, InstallPin: installer}},
		{"no installer", "letsgo.mod", missing, lsp.Options{}},
		{"global config", "config.mod", missing, lsp.Options{InstallPin: installer}},
		{"no pin on the line", "letsgo.mod", "build linux/amd64\n", lsp.Options{InstallPin: installer}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, uri := pinEditor(t, tt.opts, tt.file, tt.text)
			if actions := codeActions(t, c, uri); len(actions) != 0 {
				t.Errorf("actions = %+v, want none", actions)
			}
		})
	}
}

func TestInstallAllIsOfferedOnlyForSeveralMissingPins(t *testing.T) {
	lsp.IsolatePlugins(t)
	var asked []string
	opts := lsp.Options{InstallPin: storeInstaller(t, toolBody, nil, &asked)}
	one := pinFor("letsgo-env", newDigest) + "\n"
	two := one + pinFor("letsgo-cask", newDigest) + "\n"
	sameRelease := one + pinFor("letsgo-env", newDigest) + "\n"

	tests := []struct {
		name, text string
		titles     []string
	}{
		{"one pin", one, []string{"Install pinned plugin: letsgo-env v1.0.0"}},
		{"two pins", two, []string{"Install pinned plugin: letsgo-env v1.0.0", "Install pinned plugin: letsgo-cask v1.0.0", "Install all missing pinned plugins"}},
		{"one release on two hooks", sameRelease, []string{"Install pinned plugin: letsgo-env v1.0.0", "Install pinned plugin: letsgo-env v1.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, uri := pinEditor(t, opts, "letsgo.mod", tt.text)
			var got []string
			for _, a := range codeActions(t, c, uri) {
				got = append(got, a.Title)
			}
			if len(got) != len(tt.titles) {
				t.Fatalf("titles = %q, want %q", got, tt.titles)
			}
			for i := range got {
				if got[i] != tt.titles[i] {
					t.Errorf("titles = %q, want %q", got, tt.titles)
					break
				}
			}
		})
	}
}

func TestExecuteInstallReportsTheOutcome(t *testing.T) {
	tests := []struct {
		name     string
		body     string // what the "release" holds
		err      error
		line     int
		wantType int
		want     string
		asked    []string
	}{
		{"installed", toolBody, nil, 0, 3, "letsgo: installed letsgo-env v1.0.0", []string{"letsgo-env@v1.0.0"}},
		{"all", toolBody, nil, -1, 3, "letsgo: installed letsgo-env v1.0.0", []string{"letsgo-env@v1.0.0"}},
		{"forge fails", toolBody, errors.New("offline"), 0, 2, "letsgo: could not install letsgo-env v1.0.0: offline", []string{"letsgo-env@v1.0.0"}},
		{"release differs from the pin", "#!/bin/sh\nexit 1\n", nil, 0, 2, "letsgo: installed letsgo-env v1.0.0, but the release is not the digest the pin names; update the pin", []string{"letsgo-env@v1.0.0"}},
		{"line without a pin", toolBody, nil, 1, 3, "letsgo: nothing to install", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lsp.IsolatePlugins(t)
			var asked []string
			opts := lsp.Options{InstallPin: storeInstaller(t, tt.body, tt.err, &asked)}
			c, uri := pinEditor(t, opts, "letsgo.mod", pinFor("letsgo-env", digestOfBody(toolBody))+"\nbuild linux/amd64\n")

			execute(t, c, map[string]any{"uri": uri, "line": tt.line})
			msg := awaitMessage(t, c)
			if msg.Type != tt.wantType || msg.Message != tt.want {
				t.Errorf("message = %+v, want type %d %q", msg, tt.wantType, tt.want)
			}
			if len(asked) != len(tt.asked) || (len(asked) > 0 && asked[0] != tt.asked[0]) {
				t.Errorf("installer asked for %v, want %v", asked, tt.asked)
			}
		})
	}
}

func TestExecuteInstallDoesNothingWhereInstallingIsNotAllowed(t *testing.T) {
	lsp.IsolatePlugins(t)
	var asked []string
	installer := storeInstaller(t, toolBody, nil, &asked)
	text := pinFor("letsgo-env", newDigest) + "\n"

	tests := []struct {
		name string
		opts lsp.Options
		file string
		open bool
	}{
		{"restricted", lsp.Options{Restricted: true, InstallPin: installer}, "letsgo.mod", true},
		{"no installer", lsp.Options{}, "letsgo.mod", true},
		{"global config", lsp.Options{InstallPin: installer}, "config.mod", true},
		{"unopened", lsp.Options{InstallPin: installer}, "letsgo.mod", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, uri := pinEditor(t, tt.opts, tt.file, text)
			if !tt.open {
				uri += "x"
			}
			execute(t, c, map[string]any{"uri": uri, "line": 0})
			if len(asked) != 0 {
				t.Errorf("installer asked for %v, want nothing", asked)
			}
		})
	}
}

func TestInitializeAdvertisesInstallOnlyWithAnInstaller(t *testing.T) {
	installer := func(context.Context, string, string) error { return nil }
	tests := []struct {
		name string
		opts lsp.Options
		want bool
	}{
		{"installer", lsp.Options{InstallPin: installer}, true},
		{"restricted", lsp.Options{Restricted: true, InstallPin: installer}, false},
		{"none", lsp.Options{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var init struct {
				Capabilities struct {
					ExecuteCommandProvider *struct {
						Commands []string `json:"commands"`
					} `json:"executeCommandProvider"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal(newClient(t, tt.opts).request("initialize", map[string]any{}), &init); err != nil {
				t.Fatal(err)
			}
			p := init.Capabilities.ExecuteCommandProvider
			if got := p != nil && len(p.Commands) == 1 && p.Commands[0] == "letsgo.installPins"; got != tt.want {
				t.Errorf("install advertised = %v, want %v", got, tt.want)
			}
		})
	}
}

package launchd

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

// plistTexts returns the trimmed, unescaped text nodes of a plist in document
// order and fails the test when the document is not well-formed XML.
func plistTexts(t *testing.T, plist string) []string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(plist))
	dec.Strict = true
	var texts []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return texts
		}
		if err != nil {
			t.Fatalf("plist is not well-formed XML: %v", err)
		}
		if cd, ok := tok.(xml.CharData); ok {
			if s := strings.TrimSpace(string(cd)); s != "" {
				texts = append(texts, s)
			}
		}
	}
}

func TestRenderAgentPlist(t *testing.T) {
	out, err := RenderAgentPlist(Params{Binary: "/opt/bin/ticktack", LogPath: "/home/u/.local/state/ticktack/ticktack.log"})
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + strings.Join(plistTexts(t, out), "\n") + "\n"
	for _, want := range []string{Label, "/opt/bin/ticktack", "agent", "/home/u/.local/state/ticktack/ticktack.log"} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Errorf("rendered plist lacks %q", want)
		}
	}
	for _, want := range []string{"<key>RunAtLoad</key>\n\t<true/>", "<key>KeepAlive</key>\n\t<true/>"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered plist lacks %q", want)
		}
	}
}

func TestRenderAgentPlistEscapesValues(t *testing.T) {
	out, err := RenderAgentPlist(Params{Binary: "/a&b/<x>", LogPath: "/log"})
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal([]byte(out), new(struct{})); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	if !strings.Contains(out, "/a&amp;b/&lt;x&gt;") {
		t.Fatalf("binary path not escaped:\n%s", out)
	}
}

func TestRenderAgentPlistPassesXDGDirectories(t *testing.T) {
	tests := []struct {
		name   string
		params Params
	}{
		{"plain directories", Params{
			Binary: "/opt/homebrew/opt/ticktack/bin/ticktack", LogPath: "/x/state/ticktack/ticktack.log",
			ConfigHome: "/x/config", DataHome: "/x/data", StateHome: "/x/state",
		}},
		{"directories needing XML escapes", Params{
			Binary: "/bin/ticktack", LogPath: "/log",
			ConfigHome: "/x/<config>", DataHome: "/x/a&b", StateHome: "/x/it's \"state\"",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := RenderAgentPlist(tt.params)
			if err != nil {
				t.Fatal(err)
			}
			texts := plistTexts(t, out)
			want := map[string]string{
				"XDG_CONFIG_HOME": tt.params.ConfigHome,
				"XDG_DATA_HOME":   tt.params.DataHome,
				"XDG_STATE_HOME":  tt.params.StateHome,
			}
			for key, value := range want {
				found := false
				for i, text := range texts {
					if text == key && i+1 < len(texts) && texts[i+1] == value {
						found = true
					}
				}
				if !found {
					t.Errorf("plist does not set %s to %q:\n%s", key, value, out)
				}
			}
			if !strings.Contains(out, "<key>EnvironmentVariables</key>") {
				t.Errorf("plist lacks EnvironmentVariables:\n%s", out)
			}
		})
	}
}

func TestRenderAgentPlistOmitsUnknownDirectories(t *testing.T) {
	out, err := RenderAgentPlist(Params{Binary: "/bin/ticktack", LogPath: "/log", DataHome: "/x/data"})
	if err != nil {
		t.Fatal(err)
	}
	texts := strings.Join(plistTexts(t, out), "\n")
	if !strings.Contains(texts, "XDG_DATA_HOME\n/x/data") {
		t.Errorf("known directory missing:\n%s", out)
	}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		if strings.Contains(out, key) {
			t.Errorf("plist sets %s without a value:\n%s", key, out)
		}
	}
}

func TestProgramPath(t *testing.T) {
	rendered, err := RenderAgentPlist(Params{Binary: "/a&b/ticktack", LogPath: "/log", ConfigHome: "/c", DataHome: "/d", StateHome: "/s"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		plist   string
		want    string
		wantErr bool
	}{
		{"rendered plist, unescaped", rendered, "/a&b/ticktack", false},
		{
			"strings before the arguments are ignored",
			"<plist><dict><key>Label</key><string>x</string><key>ProgramArguments</key><array><string>/bin/t</string><string>agent</string></array></dict></plist>",
			"/bin/t", false,
		},
		{"no program arguments", "<plist><dict><key>Label</key><string>x</string></dict></plist>", "", true},
		{"empty argument list", "<plist><dict><key>ProgramArguments</key><array></array><key>Label</key><string>x</string></dict></plist>", "", true},
		{"empty program", "<plist><dict><key>ProgramArguments</key><array><string></string></array></dict></plist>", "", true},
		{"not XML", "plist", "", true},
		{"truncated", "<plist><dict><key>ProgramArguments</key><array><string>/bin/t", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProgramPath([]byte(tt.plist))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("program = %q, want %q", got, tt.want)
			}
		})
	}
}

package launchd

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRenderAgentPlist(t *testing.T) {
	out, err := RenderAgentPlist(Params{Binary: "/opt/bin/ticktack", LogPath: "/home/u/.local/state/ticktack/ticktack.log"})
	if err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(strings.NewReader(out))
	dec.Strict = true
	var strs []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("plist is not well-formed XML: %v", err)
		}
		if cd, ok := tok.(xml.CharData); ok {
			if s := strings.TrimSpace(string(cd)); s != "" {
				strs = append(strs, s)
			}
		}
	}
	joined := "\n" + strings.Join(strs, "\n") + "\n"
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

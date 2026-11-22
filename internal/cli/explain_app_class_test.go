package cli

import (
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/config"
)

func TestMsgExplainAppClassNamesClassAndApp(t *testing.T) {
	got := msgExplainAppClass(config.ClassNeutral, "com.example.chat")
	for _, want := range []string{"neutral", "com.example.chat", "app_classes", "confirm it in review"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q does not contain %q", got, want)
		}
	}
	if strings.Contains(msgExplainAppClass(config.ClassNeutral, ""), "foreground app  ") {
		t.Error("message with an unknown app must not leave a double space")
	}
}

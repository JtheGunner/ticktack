package shellhook

import "testing"

func TestAliasLine(t *testing.T) {
	tests := []struct {
		shell   string
		want    string
		wantErr bool
	}{
		{"zsh", "alias tt=ticktack", false},
		{"bash", "alias tt=ticktack", false},
		{"fish", "alias tt ticktack", false},
		{"nushell", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			got, err := AliasLine(tt.shell)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("AliasLine(%q) = %q, %v; want %q, error %v", tt.shell, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

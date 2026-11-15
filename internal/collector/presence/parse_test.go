package presence

import (
	"testing"
	"time"
)

func TestParseIdle(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"typical", "    | |   \"HIDIdleTime\" = 4123456789\n    | | }", 4123456789 * time.Nanosecond, false},
		{"zero", "\"HIDIdleTime\" = 0", 0, false},
		{"multiple takes first", "\"HIDIdleTime\" = 5\n\"HIDIdleTime\" = 9", 5 * time.Nanosecond, false},
		{"missing", "nothing here", 0, true},
		{"not a number", "\"HIDIdleTime\" = abc", 0, true},
		{"empty", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIdle(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestParseBundleID(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"typical", "\"CFBundleIdentifier\"=\"com.apple.Terminal\"\n", "com.apple.Terminal", false},
		{"spaces", "\"CFBundleIdentifier\" = \"com.example.App-1\"", "com.example.App-1", false},
		{"null", "\"CFBundleIdentifier\"=[ NULL ]", "", true},
		{"empty", "", "", true},
		{"garbage", "something else", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBundleID(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

package paths

import (
	"path/filepath"
	"testing"
)

func TestPaths(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		fn   func() (string, error)
		want string
	}{
		{"data default", map[string]string{"XDG_DATA_HOME": "", "HOME": "/home/u"}, DataDir, "/home/u/.local/share/ticktack"},
		{"data xdg", map[string]string{"XDG_DATA_HOME": "/x/data", "HOME": "/home/u"}, DataDir, "/x/data/ticktack"},
		{"state default", map[string]string{"XDG_STATE_HOME": "", "HOME": "/home/u"}, StateDir, "/home/u/.local/state/ticktack"},
		{"state xdg", map[string]string{"XDG_STATE_HOME": "/x/state", "HOME": "/home/u"}, StateDir, "/x/state/ticktack"},
		{"db", map[string]string{"XDG_DATA_HOME": "/x/data"}, DBPath, "/x/data/ticktack/events.db"},
		{"booking lock", map[string]string{"XDG_DATA_HOME": "/x/data"}, BookingLockPath, "/x/data/ticktack/booking.lock"},
		{"pause", map[string]string{"XDG_STATE_HOME": "/x/state"}, PausePath, "/x/state/ticktack/paused"},
		{"log", map[string]string{"XDG_STATE_HOME": "/x/state"}, LogPath, "/x/state/ticktack/ticktack.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := tt.fn()
			if err != nil {
				t.Fatal(err)
			}
			if got != filepath.FromSlash(tt.want) {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestPathsFailWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	for name, fn := range map[string]func() (string, error){
		"DataDir": DataDir, "StateDir": StateDir, "DBPath": DBPath, "BookingLockPath": BookingLockPath, "PausePath": PausePath, "LogPath": LogPath,
	} {
		if _, err := fn(); err == nil {
			t.Errorf("%s: expected error without home directory", name)
		}
	}
}

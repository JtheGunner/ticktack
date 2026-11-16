package render

import "testing"

func TestLayoutWidthExported(t *testing.T) {
	tests := []struct {
		name string
		r    Renderer
		want int
	}{
		{"zero value uses the default", Renderer{}, defaultLayoutWidth},
		{"plain uses the default", Plain(), defaultLayoutWidth},
		{"explicit width", Renderer{Width: 60}, 60},
		{"negative width falls back", Renderer{Width: -1}, defaultLayoutWidth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.LayoutWidth(); got != tt.want {
				t.Errorf("LayoutWidth() = %d, want %d", got, tt.want)
			}
		})
	}
}

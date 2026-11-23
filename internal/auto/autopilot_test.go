package auto

import (
	"reflect"
	"testing"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
)

func TestAutopilotDaysAreCompletedDaysOldestFirst(t *testing.T) {
	today := civil.Date{Year: 2026, Month: 3, Day: 2}
	tests := []struct {
		name     string
		lookback int
		want     []civil.Date
	}{
		{"one day is yesterday", 1, []civil.Date{{Year: 2026, Month: 3, Day: 1}}},
		{"three days cross a month boundary", 3, []civil.Date{
			{Year: 2026, Month: 2, Day: 27}, {Year: 2026, Month: 2, Day: 28}, {Year: 2026, Month: 3, Day: 1},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AutopilotDays(today, tt.lookback)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("days = %v, want %v", got, tt.want)
			}
			for _, d := range got {
				if !Completed(d, today) {
					t.Errorf("%v is not completed", d)
				}
			}
		})
	}
}

func TestCompletedExcludesTodayAndTheFuture(t *testing.T) {
	today := civil.Date{Year: 2026, Month: 1, Day: 5}
	if Completed(today, today) || Completed(today.AddDays(1), today) || !Completed(today.AddDays(-1), today) {
		t.Fatal("only days before today are completed")
	}
}

func TestLookbackDaysFallsBackToTheDefaultOutsideTheRange(t *testing.T) {
	tests := []struct{ in, want int }{
		{0, 7}, {-3, 7}, {32, 7}, {1, 1}, {31, 31}, {14, 14},
	}
	for _, tt := range tests {
		if got := LookbackDays(config.Auto{AutopilotLookbackDays: tt.in}); got != tt.want {
			t.Errorf("LookbackDays(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

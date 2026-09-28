package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestGetWorkWeekMF(t *testing.T) {
	tests := []struct {
		name       string
		now        time.Time
		wantMonday time.Time
	}{
		{"monday", date(2026, time.September, 28), date(2026, time.September, 28)},
		{"wednesday afternoon", time.Date(2026, time.September, 30, 15, 30, 0, 0, time.UTC), date(2026, time.September, 28)},
		{"friday", date(2026, time.October, 2), date(2026, time.September, 28)},
		{"saturday gives the week that just ended", date(2026, time.October, 3), date(2026, time.September, 28)},
		{"sunday gives the week that just ended", date(2026, time.October, 4), date(2026, time.September, 28)},
		{"week crossing a month boundary", date(2026, time.October, 1), date(2026, time.September, 28)},
		{"week crossing a year boundary", date(2026, time.December, 31), date(2026, time.December, 28)},
	}
	wantNames := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := getWorkWeekMF(tt.now)
			if len(days) != 5 {
				t.Fatalf("got %d days, want 5", len(days))
			}
			for i, d := range days {
				wantStart := tt.wantMonday.AddDate(0, 0, i)
				wantEnd := wantStart.AddDate(0, 0, 1)
				if !d.Start.Equal(wantStart) {
					t.Errorf("day %d start = %v, want %v", i, d.Start, wantStart)
				}
				if !d.End.Equal(wantEnd) {
					t.Errorf("day %d end = %v, want %v", i, d.End, wantEnd)
				}
				if d.DayName != wantNames[i] {
					t.Errorf("day %d name = %q, want %q", i, d.DayName, wantNames[i])
				}
			}
		})
	}
}

// hours builds timesheets with the given durations in hours.
func hours(h ...float64) []TimeSheet {
	sheets := make([]TimeSheet, len(h))
	for i, v := range h {
		sheets[i] = TimeSheet{Duration: v * 3600}
	}
	return sheets
}

func TestSummarizeWeek(t *testing.T) {
	fullWeek := map[string][]TimeSheet{
		"Monday":    hours(8),
		"Tuesday":   hours(8),
		"Wednesday": hours(8),
		"Thursday":  hours(8),
		"Friday":    hours(8),
	}

	tests := []struct {
		name       string
		data       map[string][]TimeSheet
		now        time.Time
		wantToday  float64
		wantWeek   float64
		wantTarget int
	}{
		{
			name:       "no data",
			data:       nil,
			now:        date(2026, time.September, 28), // Monday
			wantTarget: 8,
		},
		{
			name: "wednesday sums several sheets for today",
			data: map[string][]TimeSheet{
				"Monday":    hours(8),
				"Tuesday":   hours(7.5),
				"Wednesday": hours(2, 1.5, 0.5),
			},
			now:        date(2026, time.September, 30),
			wantToday:  4,
			wantWeek:   19.5,
			wantTarget: 24,
		},
		{
			name:       "friday full week",
			data:       fullWeek,
			now:        date(2026, time.October, 2),
			wantToday:  8,
			wantWeek:   40,
			wantTarget: 40,
		},
		{
			name:       "saturday caps target and has no hours today",
			data:       fullWeek,
			now:        date(2026, time.October, 3),
			wantToday:  0,
			wantWeek:   40,
			wantTarget: 40,
		},
		{
			name:       "sunday caps target and has no hours today",
			data:       fullWeek,
			now:        date(2026, time.October, 4),
			wantToday:  0,
			wantWeek:   40,
			wantTarget: 40,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			today, week, target := summarizeWeek(tt.data, tt.now)
			if today != tt.wantToday || week != tt.wantWeek || target != tt.wantTarget {
				t.Errorf("summarizeWeek() = (%v, %v, %v), want (%v, %v, %v)",
					today, week, target, tt.wantToday, tt.wantWeek, tt.wantTarget)
			}
		})
	}
}

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer secret")
		}
		switch r.URL.Path {
		case "/projects/1":
			w.Write([]byte(`{"parentTitle":"Acme","name":"Website","customer":7}`))
		case "/broken":
			w.Write([]byte(`{not json`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newClient(srv.URL+"/", "secret")

	t.Run("decodes response", func(t *testing.T) {
		got, err := getJSON[Project](c, c.baseURL+"projects/1")
		if err != nil {
			t.Fatal(err)
		}
		want := Project{CustomerName: "Acme", ProjectName: "Website", CustomerID: 7}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("error on bad status", func(t *testing.T) {
		_, err := getJSON[Project](c, c.baseURL+"missing")
		if err == nil || !strings.Contains(err.Error(), "got status 404") {
			t.Errorf("err = %v, want status 404 error", err)
		}
	})

	t.Run("error on invalid json", func(t *testing.T) {
		_, err := getJSON[Project](c, c.baseURL+"broken")
		if err == nil || !strings.Contains(err.Error(), "decode") {
			t.Errorf("err = %v, want decode error", err)
		}
	})
}

func TestGetProjectCaches(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"parentTitle":"Acme","name":"Website","customer":7}`))
	}))
	defer srv.Close()
	c := newClient(srv.URL+"/", "secret")

	for range 3 {
		if _, err := c.getProject(1); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Errorf("got %d requests, want 1", requests)
	}
}

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestParseDate(t *testing.T) {
	now := time.Date(2026, time.September, 28, 15, 30, 0, 0, time.UTC)
	tests := []struct {
		input   string
		want    time.Time
		wantErr bool
	}{
		{"", date(2026, time.September, 28), false},
		{"-1", date(2026, time.September, 27), false},
		{"-28", date(2026, time.August, 31), false},
		{"2026-09-01", date(2026, time.September, 1), false},
		{"-x", time.Time{}, true},
		{"--1", time.Time{}, true},
		{"28/09/2026", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDate(tt.input, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseDate(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseDate(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseWorkDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"1.5", 90 * time.Minute, false},
		{"8", 8 * time.Hour, false},
		{"0.25", 15 * time.Minute, false},
		{"1:30", 90 * time.Minute, false},
		{"0:45", 45 * time.Minute, false},
		{"90m", 90 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"1:60", 0, true},
		{"1:xx", 0, true},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseWorkDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseWorkDuration(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseWorkDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestPostTimesheet(t *testing.T) {
	var got NewTimesheet
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/timesheets" {
			t.Errorf("got %s %s, want POST /timesheets", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Project == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":400,"message":"Validation Failed"}`))
			return
		}
		w.Write([]byte(`{"id":42,"project":3,"activity":5,"duration":5400,"description":"Planning"}`))
	}))
	defer srv.Close()
	c := newClient(srv.URL+"/", "secret")

	want := NewTimesheet{Begin: "2026-09-28T08:00:00", End: "2026-09-28T09:30:00", Project: 3, Activity: 5, Description: "Planning"}
	created, err := c.postTimesheet(want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sent %+v, want %+v", got, want)
	}
	if created.ID != 42 {
		t.Errorf("created ID = %d, want 42", created.ID)
	}

	_, err = c.postTimesheet(NewTimesheet{})
	if err == nil || !strings.Contains(err.Error(), "Validation Failed") {
		t.Errorf("err = %v, want error containing the response body", err)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{90 * time.Minute, "1h 30m (1.5 h)"},
		{8 * time.Hour, "8h 00m (8 h)"},
		{15 * time.Minute, "0h 15m (0.25 h)"},
		{10*time.Hour + 30*time.Minute, "10h 30m (10.5 h)"},
		{61 * time.Minute, "1h 01m (1.02 h)"},
	}
	for _, tt := range tests {
		if got := formatDuration(tt.in); got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"\n", true},
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"maybe\n", false},
	}
	for _, tt := range tests {
		got, err := confirm(bufio.NewReader(strings.NewReader(tt.input)), "Continue?")
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("confirm(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestChoose(t *testing.T) {
	items := []string{"a", "b", "c"}
	label := func(s string) string { return s }
	choice := func(input string) (string, error) {
		return choose(bufio.NewReader(strings.NewReader(input)), items, label, "item")
	}

	if got, err := choice("2\n"); err != nil || got != "b" {
		t.Errorf("choose 2 = %q, %v, want b", got, err)
	}
	for _, bad := range []string{"0\n", "4\n", "x\n"} {
		if _, err := choice(bad); err == nil {
			t.Errorf("choose %q: want error", bad)
		}
	}
	if got, err := choose(nil, []string{"only"}, label, "item"); err != nil || got != "only" {
		t.Errorf("single item = %q, %v, want only", got, err)
	}
	if _, err := choose(nil, []string{}, label, "item"); err == nil {
		t.Error("empty list: want error")
	}
}

// fakeKimaiWeb mimics the web timesheet form: it needs the session cookie,
// hands out a CSRF token and redirects after a valid submit.
func fakeKimaiWeb(t *testing.T, posted *url.Values) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/en/timesheet/create" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		cookie, err := r.Cookie("KIMAI_SESSION")
		if err != nil || cookie.Value != "valid" {
			w.Header().Set("Location", "/en/login")
			w.WriteHeader(http.StatusFound)
			return
		}
		if r.Method == http.MethodGet {
			w.Write([]byte(`<form name="timesheet_edit_form"><input type="hidden" id="timesheet_edit_form__token" name="timesheet_edit_form[_token]" value="tok&amp;en"></form>`))
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		*posted = r.PostForm
		if r.PostForm.Get("timesheet_edit_form[_token]") != "tok&en" {
			w.Write([]byte(`<div class="invalid-feedback d-block">The CSRF token is invalid.</div>`))
			return
		}
		if r.PostForm.Get("timesheet_edit_form[project]") == "0" {
			w.Write([]byte(`<div class="invalid-feedback d-block"><span>This value should not be blank.</span></div>
				<div class="invalid-feedback d-block">This value should not be blank.</div>`))
			return
		}
		w.Header().Set("Location", "/en/timesheet/")
		w.WriteHeader(http.StatusFound)
	}))
}

func TestSubmitTimesheetForm(t *testing.T) {
	var posted url.Values
	srv := fakeKimaiWeb(t, &posted)
	defer srv.Close()
	c := newClient(srv.URL+"/api/", "secret")
	c.session = "valid"

	ts := WebTimesheet{
		Date:        date(2026, time.September, 27),
		Duration:    90 * time.Minute,
		Customer:    24,
		Project:     12,
		Activity:    21,
		Description: "Planning",
	}
	if err := c.submitTimesheetForm(ts); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"timesheet_edit_form[begin_date]":  "2026-09-27",
		"timesheet_edit_form[duration]":    "1:30",
		"timesheet_edit_form[customer]":    "24",
		"timesheet_edit_form[project]":     "12",
		"timesheet_edit_form[activity]":    "21",
		"timesheet_edit_form[description]": "Planning",
		"timesheet_edit_form[_token]":      "tok&en",
	}
	for field, value := range want {
		if got := posted.Get(field); got != value {
			t.Errorf("%s = %q, want %q", field, got, value)
		}
	}

	t.Run("validation errors are reported once each", func(t *testing.T) {
		ts := ts
		ts.Project = 0
		err := c.submitTimesheetForm(ts)
		if err == nil || !strings.HasSuffix(err.Error(), ": This value should not be blank.") {
			t.Errorf("err = %v, want the validation message once", err)
		}
	})

	t.Run("expired session", func(t *testing.T) {
		c := newClient(srv.URL+"/api/", "secret")
		c.session = "expired"
		if err := c.submitTimesheetForm(ts); !errors.Is(err, errSessionExpired) {
			t.Errorf("err = %v, want errSessionExpired", err)
		}
	})

	t.Run("missing session", func(t *testing.T) {
		c := newClient(srv.URL+"/api/", "secret")
		err := c.submitTimesheetForm(ts)
		if err == nil || !strings.Contains(err.Error(), "KIMAI_SESSION is not set") {
			t.Errorf("err = %v, want missing session error", err)
		}
	})
}

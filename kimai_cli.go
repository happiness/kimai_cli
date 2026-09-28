package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

type Client struct {
	baseURL      string
	token        string
	http         *http.Client
	projectCache map[int]Project
	// session is the KIMAI_SESSION cookie from a browser login. It is only
	// needed for the web form, which is used to create timesheets.
	session string
}

type TimeSheet struct {
	ID          int     `json:"id"`
	Activity    int     `json:"activity"`
	Project     int     `json:"project"`
	Duration    float64 `json:"duration"`
	Description string  `json:"description"`
	Begin       string  `json:"begin"`
	End         string  `json:"end"`
}

// NewTimesheet is the body for POST /api/timesheets. Begin and End use
// customLayout, local time without a timezone.
type NewTimesheet struct {
	Begin       string `json:"begin"`
	End         string `json:"end"`
	Project     int    `json:"project"`
	Activity    int    `json:"activity"`
	Description string `json:"description,omitempty"`
}

type Project struct {
	ID           int    `json:"id"`
	CustomerName string `json:"parentTitle"`
	ProjectName  string `json:"name"`
	CustomerID   int    `json:"customer"`
}

type DayRange struct {
	DayName string
	Start   time.Time
	End     time.Time
}

type Activity struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Customer struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
	Name        string `json:"name"`
}

const customLayout = "2006-01-02T15:04:05"

// Config is read from <user config dir>/kimai_cli/config.toml.
type Config struct {
	URL string `toml:"url"`
}

func getWorkWeekMF(t time.Time) []DayRange {
	currentDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	weekday := int(currentDate.Weekday())
	if weekday == 0 {
		weekday = 7 // Treat Sunday as day 7
	}
	mondayStart := currentDate.AddDate(0, 0, -weekday+1)
	workDays := make([]DayRange, 5)

	for i := 0; i < 5; i++ {
		dayStart := mondayStart.AddDate(0, 0, i)
		dayEnd := dayStart.AddDate(0, 0, 1) // Next day midnight
		workDays[i] = DayRange{
			DayName: dayStart.Weekday().String(),
			Start:   dayStart,
			End:     dayEnd,
		}
	}
	return workDays
}

func newClient(baseURL, token string) *Client {
	return &Client{
		baseURL:      baseURL,
		token:        token,
		http:         &http.Client{Timeout: 10 * time.Second},
		projectCache: map[int]Project{},
	}
}

func getToken() string {
	token := os.Getenv("KIMAI_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "No KIMAI_TOKEN environment variable. This is a required variable.")
		os.Exit(3)
	}
	return token
}

func loadConfig() (Config, error) {
	var cfg Config

	dir, err := os.UserConfigDir()
	if err != nil {
		return cfg, fmt.Errorf("find config dir: %w", err)
	}
	path := filepath.Join(dir, "kimai_cli", "config.toml")
	_, err = toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, fmt.Errorf("no config file found, create %s with: url = \"https://<your-kimai>/api/\"", path)
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if cfg.URL == "" {
		return cfg, fmt.Errorf("config %s: url is not set", path)
	}

	// Endpoints are appended directly, so make sure the URL ends with a slash.
	if !strings.HasSuffix(cfg.URL, "/") {
		cfg.URL += "/"
	}
	return cfg, nil
}

func (c *Client) fetchWeekData() (map[string][]TimeSheet, error) {
	workDays := getWorkWeekMF(time.Now())
	weeklyMap := make(map[string][]TimeSheet)
	// Each goroutine writes only to its own index, so errs needs no mutex.
	errs := make([]error, len(workDays))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, d := range workDays {
		wg.Add(1)
		go func() {
			defer wg.Done()
			endpoint := c.baseURL + "timesheets?begin=" + d.Start.Format(customLayout) + "&end=" + d.End.Format(customLayout)
			timesheets, err := c.getTimeSheets(endpoint)
			if err != nil {
				errs[i] = fmt.Errorf("fetch %s: %w", d.DayName, err)
				return
			}
			mu.Lock()
			weeklyMap[d.DayName] = timesheets
			mu.Unlock()
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return weeklyMap, nil
}

func (c *Client) getWeek() error {
	weeklyMap, err := c.fetchWeekData()
	if err != nil {
		return err
	}
	totalDuration := 0.0
	workDays := getWorkWeekMF(time.Now())
	for _, day := range workDays {
		dayName := day.Start.Weekday().String()
		sheets := weeklyMap[dayName] // Fetch the already-fetched sheets from our map
		fmt.Println(dayName)
		dayDuration := 0.0

		for _, sheet := range sheets {
			duration := sheet.Duration / 3600.0
			totalDuration = totalDuration + duration
			dayDuration = dayDuration + duration
			if err := c.outputSheet(sheet, duration); err != nil {
				return err
			}
		}
		fmt.Printf("%s time reported total: %.1f\n", dayName, dayDuration)
	}

	fmt.Printf("Time reported all week: %.1f of 40\n", totalDuration)
	return nil
}

func (c *Client) getTimeSheets(endpoint string) ([]TimeSheet, error) {
	return getJSON[[]TimeSheet](c, endpoint)
}

func (c *Client) getToday() error {
	now := time.Now()
	year, month, day := now.Date()
	loc := now.Location()
	currentDateStart := time.Date(year, month, day, 0, 0, 0, 0, loc)
	currentDateEnd := currentDateStart.AddDate(0, 0, 1)
	endpoint := c.baseURL + "timesheets?begin=" + currentDateStart.Format(customLayout) + "&end=" + currentDateEnd.Format(customLayout)
	timesheets, err := c.getTimeSheets(endpoint)
	if err != nil {
		return err
	}
	for _, sheet := range timesheets {
		duration := sheet.Duration / 3600
		if err := c.outputSheet(sheet, duration); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) getProject(projectID int) (Project, error) {
	if project, ok := c.projectCache[projectID]; ok {
		return project, nil
	}
	project, err := getJSON[Project](c, c.baseURL+"projects/"+strconv.Itoa(projectID))
	if err != nil {
		return Project{}, fmt.Errorf("get project %d: %w", projectID, err)
	}
	c.projectCache[projectID] = project
	return project, nil
}

func (c *Client) getShortWeekInfo() error {
	weeklyData, err := c.fetchWeekData()
	if err != nil {
		return err
	}
	today, week, target := summarizeWeek(weeklyData, time.Now())
	fmt.Printf("t: %.1f (of 8), w: %.1f (of %d/40)\n", today, week, target)
	return nil
}

func summarizeWeek(data map[string][]TimeSheet, now time.Time) (today, week float64, target int) {
	workDays := getWorkWeekMF(now)
	dayNumber := (int(now.Weekday())+6)%7 + 1 // Monday = 1 ... Sunday = 7
	target = 8 * min(dayNumber, 5)

	for daynum, day := range workDays {
		for _, sheet := range data[day.DayName] {
			duration := sheet.Duration / 3600
			week += duration
			if daynum+1 == dayNumber {
				today += duration
			}
		}
	}
	return today, week, target
}

func (c *Client) outputSheet(sheet TimeSheet, duration float64) error {
	project, err := c.getProject(sheet.Project)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s %d - %s %.1f\n", project.CustomerName, project.ProjectName, sheet.Activity, sheet.Description, duration)
	return nil
}

func (c *Client) searchCustomers(customer string) ([]Customer, error) {
	customers, err := getJSON[[]Customer](c, c.baseURL+"customers?term="+url.QueryEscape(customer))
	if err != nil {
		return nil, fmt.Errorf("search customers: %w", err)
	}
	return customers, nil
}

func (c *Client) searchTimesheets(term string) ([]TimeSheet, error) {
	sheets, err := c.getTimeSheets(c.baseURL + "timesheets?term=" + url.QueryEscape(term))
	if err != nil {
		return nil, fmt.Errorf("search timesheets: %w", err)
	}
	return sheets, nil
}

func (c *Client) searchCustomerAndDescription(customer string, description string) error {
	customers, err := c.searchCustomers(customer)
	if err != nil {
		return err
	}
	timesheets, err := c.searchTimesheets(description)
	if err != nil {
		return err
	}
	var customerIDs []int
	for _, customerHit := range customers {
		customerIDs = append(customerIDs, customerHit.ID)
	}
	totalHitDuration := 0.0
	for _, timesheet := range timesheets {
		project, err := c.getProject(timesheet.Project)
		if err != nil {
			return err
		}
		if slices.Contains(customerIDs, project.CustomerID) {
			duration := timesheet.Duration / 3600
			totalHitDuration = totalHitDuration + duration
			if err := c.outputSheet(timesheet, duration); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Total duration of all hits: %.1f\n", totalHitDuration)
	return nil
}

func getJSON[T any](c *Client, endpoint string) (T, error) {
	return doJSON[T](c, http.MethodGet, endpoint, nil)
}

// postJSON sends body as JSON to endpoint and decodes the response into T.
func postJSON[T any](c *Client, endpoint string, body any) (T, error) {
	return doJSON[T](c, http.MethodPost, endpoint, body)
}

func doJSON[T any](c *Client, method, endpoint string, body any) (T, error) {
	var result T

	var reqBody io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return result, fmt.Errorf("encode request: %w", err)
		}
		reqBody = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, endpoint, reqBody)
	if err != nil {
		return result, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return result, fmt.Errorf("request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Kimai explains validation errors in the body, so include it.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return result, fmt.Errorf("could not reach kimai endpoint %s: got status %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return result, nil
}

func (c *Client) listProjectsByCustomer(customerID int) ([]Project, error) {
	projects, err := getJSON[[]Project](c, c.baseURL+"projects?customer="+strconv.Itoa(customerID))
	if err != nil {
		return nil, fmt.Errorf("list projects for customer %d: %w", customerID, err)
	}
	return projects, nil
}

// prompt prints msg and returns the trimmed line the user typed.
func prompt(r *bufio.Reader, msg string) (string, error) {
	fmt.Print(msg)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// confirm asks a yes/no question. An empty answer counts as yes.
func confirm(r *bufio.Reader, msg string) (bool, error) {
	answer, err := prompt(r, msg+" [Y/n]: ")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "", "y", "yes":
		return true, nil
	}
	return false, nil
}

// choose lets the user pick one of items, numbered from 1. A single item is
// picked without asking.
func choose[T any](r *bufio.Reader, items []T, label func(T) string, what string) (T, error) {
	var zero T
	switch len(items) {
	case 0:
		return zero, fmt.Errorf("no matching %s", what)
	case 1:
		fmt.Printf("Using the only %s: %s\n", what, label(items[0]))
		return items[0], nil
	}
	fmt.Printf("\nMatching %s:\n", what)
	for i, item := range items {
		fmt.Printf("  %2d) %s\n", i+1, label(item))
	}
	input, err := prompt(r, fmt.Sprintf("Choose %s [1-%d]: ", what, len(items)))
	if err != nil {
		return zero, err
	}
	n, err := strconv.Atoi(input)
	if err != nil || n < 1 || n > len(items) {
		return zero, fmt.Errorf("invalid %s choice %q", what, input)
	}
	return items[n-1], nil
}

func (c *Client) selectCustomer(r *bufio.Reader) (Customer, error) {
	term, err := prompt(r, "Customer (search): ")
	if err != nil {
		return Customer{}, err
	}
	customers, err := c.searchCustomers(term)
	if err != nil {
		return Customer{}, err
	}
	return choose(r, customers, func(cu Customer) string { return cu.Name }, "customer")
}

func (c *Client) selectProject(r *bufio.Reader, customerID int) (Project, error) {
	projects, err := c.listProjectsByCustomer(customerID)
	if err != nil {
		return Project{}, err
	}
	return choose(r, projects, func(p Project) string { return p.ProjectName }, "project")
}

// listActivitiesByProject returns the activities that can be used on a
// project, including global activities.
func (c *Client) listActivitiesByProject(projectID int) ([]Activity, error) {
	activities, err := getJSON[[]Activity](c, c.baseURL+"activities?project="+strconv.Itoa(projectID))
	if err != nil {
		return nil, fmt.Errorf("list activities for project %d: %w", projectID, err)
	}
	return activities, nil
}

func (c *Client) selectActivity(r *bufio.Reader, projectID int) (Activity, error) {
	activities, err := c.listActivitiesByProject(projectID)
	if err != nil {
		return Activity{}, err
	}
	return choose(r, activities, func(a Activity) string { return a.Name }, "activity")
}

func (c *Client) createTimesheet() error {
	fmt.Println("Create a new timesheet")
	// Check the session before asking anything, so an expired cookie is
	// reported before the user has typed in the whole entry.
	if _, err := c.fetchFormToken(); err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}
	r := bufio.NewReader(os.Stdin)

	customer, err := c.selectCustomer(r)
	if err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}
	project, err := c.selectProject(r, customer.ID)
	if err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}
	activity, err := c.selectActivity(r, project.ID)
	if err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}

	description, err := prompt(r, "\nDescription: ")
	if err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}
	begin, end, err := selectTimeRange(r, time.Now())
	if err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}
	fmt.Println()
	fmt.Printf("Customer:    %s\n", customer.Name)
	fmt.Printf("Project:     %s\n", project.ProjectName)
	fmt.Printf("Activity:    %s\n", activity.Name)
	fmt.Printf("Description: %s\n", description)
	fmt.Printf("Date:        %s\n", begin.Format("Mon 2006-01-02"))
	fmt.Printf("Duration:    %s\n", formatDuration(end.Sub(begin)))
	fmt.Println()

	ok, err := confirm(r, "Create this timesheet?")
	if err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}
	if !ok {
		fmt.Println("Cancelled, nothing was reported.")
		return nil
	}

	err = c.submitTimesheetForm(WebTimesheet{
		Date:        begin,
		Duration:    end.Sub(begin),
		Customer:    customer.ID,
		Project:     project.ID,
		Activity:    activity.ID,
		Description: description,
	})
	if err != nil {
		return fmt.Errorf("create timesheet: %w", err)
	}
	fmt.Println("Timesheet created")
	return nil
}

// postTimesheet creates a timesheet through the API. It is not used right now:
// in the duration_fixed_begin tracking mode the API rejects begin and end
// unless the user has the view_other_timesheet permission, so the web form
// is used instead (see submitTimesheetForm).
func (c *Client) postTimesheet(ts NewTimesheet) (TimeSheet, error) {
	created, err := postJSON[TimeSheet](c, c.baseURL+"timesheets", ts)
	if err != nil {
		return TimeSheet{}, fmt.Errorf("post timesheet: %w", err)
	}
	return created, nil
}

// WebTimesheet holds the fields of Kimai's web form for creating a timesheet.
type WebTimesheet struct {
	Date        time.Time
	Duration    time.Duration
	Customer    int
	Project     int
	Activity    int
	Description string
}

// webLocale is the language part of Kimai's web URLs. It decides the language
// of the validation errors shown by submitTimesheetForm.
const webLocale = "en"

var (
	formTokenRe = regexp.MustCompile(`name="timesheet_edit_form\[_token\]"[^>]*value="([^"]+)"`)
	formErrorRe = regexp.MustCompile(`(?s)class="[^"]*invalid-feedback[^"]*"[^>]*>(.*?)</(?:div|span)>`)
	htmlTagRe   = regexp.MustCompile(`<[^>]+>`)
)

var errSessionExpired = errors.New("the Kimai session is not valid, log in to Kimai in the browser and copy a fresh KIMAI_SESSION cookie")

// webURL returns the URL of a page in Kimai's web interface. The configured
// URL points at the API, so its "api/" suffix is removed.
func (c *Client) webURL(path string) string {
	return strings.TrimSuffix(c.baseURL, "api/") + webLocale + "/" + path
}

// webRequest sends a request to the web interface with the session cookie.
// Redirects are not followed, because they tell whether a form was accepted.
func (c *Client) webRequest(method, path string, form url.Values) (*http.Response, error) {
	if c.session == "" {
		return nil, errors.New("KIMAI_SESSION is not set, log in to Kimai in the browser and copy the KIMAI_SESSION cookie")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, c.webURL(path), body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.AddCookie(&http.Cookie{Name: "KIMAI_SESSION", Value: c.session})
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	client := &http.Client{
		Timeout:   c.http.Timeout,
		Transport: c.http.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", req.URL, err)
	}
	return resp, nil
}

// isLoginRedirect reports whether resp sends the browser to the login page,
// which is what Kimai does when the session has expired.
func isLoginRedirect(resp *http.Response) bool {
	return resp.StatusCode >= 300 && resp.StatusCode < 400 && strings.Contains(resp.Header.Get("Location"), "/login")
}

// fetchFormToken loads the create form and returns its CSRF token.
func (c *Client) fetchFormToken() (string, error) {
	resp, err := c.webRequest(http.MethodGet, "timesheet/create", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if isLoginRedirect(resp) {
		return "", errSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("load timesheet form: got status %d", resp.StatusCode)
	}
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read timesheet form: %w", err)
	}
	m := formTokenRe.FindSubmatch(page)
	if m == nil {
		return "", errors.New("could not find the form token on the timesheet form")
	}
	return html.UnescapeString(string(m[1])), nil
}

// submitTimesheetForm creates a timesheet by filling in the web form, the same
// way as the browser does.
func (c *Client) submitTimesheetForm(ts WebTimesheet) error {
	token, err := c.fetchFormToken()
	if err != nil {
		return err
	}

	minutes := int(ts.Duration.Round(time.Minute) / time.Minute)
	form := url.Values{
		"timesheet_edit_form[begin_date]":  {ts.Date.Format("2006-01-02")},
		"timesheet_edit_form[duration]":    {fmt.Sprintf("%d:%02d", minutes/60, minutes%60)},
		"timesheet_edit_form[project]":     {strconv.Itoa(ts.Project)},
		"timesheet_edit_form[activity]":    {strconv.Itoa(ts.Activity)},
		"timesheet_edit_form[description]": {ts.Description},
		"timesheet_edit_form[_token]":      {token},
	}
	if ts.Customer != 0 {
		form.Set("timesheet_edit_form[customer]", strconv.Itoa(ts.Customer))
	}

	resp, err := c.webRequest(http.MethodPost, "timesheet/create", form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case isLoginRedirect(resp):
		return errSessionExpired
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// Kimai redirects to the timesheet list when the entry was saved.
		return nil
	case resp.StatusCode == http.StatusOK:
		// The form is shown again when it was not accepted.
		page, _ := io.ReadAll(resp.Body)
		return formErrors(page)
	default:
		return fmt.Errorf("submit timesheet form: got status %d", resp.StatusCode)
	}
}

// formErrors collects the validation messages from a rejected form.
func formErrors(page []byte) error {
	var msgs []string
	for _, m := range formErrorRe.FindAllSubmatch(page, -1) {
		msg := strings.TrimSpace(html.UnescapeString(htmlTagRe.ReplaceAllString(string(m[1]), "")))
		if msg != "" && !slices.Contains(msgs, msg) {
			msgs = append(msgs, msg)
		}
	}
	if len(msgs) == 0 {
		return errors.New("kimai did not accept the timesheet form")
	}
	return fmt.Errorf("kimai did not accept the timesheet form: %s", strings.Join(msgs, "; "))
}

// defaultBeginHour is the start time used for entries sent through the API.
// When Kimai tracks durations only (duration_fixed_begin) the exact start time
// does not matter, as long as overlapping entries are allowed. The web form
// only takes a date and sets the start time itself.
const defaultBeginHour = 8

// parseDate reads a date as YYYY-MM-DD, or as -N for N days before now.
// An empty input means today.
func parseDate(input string, now time.Time) (time.Time, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if input == "" {
		return today, nil
	}
	if strings.HasPrefix(input, "-") {
		days, err := strconv.Atoi(input[1:])
		if err != nil || days < 0 {
			return time.Time{}, fmt.Errorf("invalid date %q", input)
		}
		return today.AddDate(0, 0, -days), nil
	}
	d, err := time.ParseInLocation("2006-01-02", input, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, use YYYY-MM-DD or -N", input)
	}
	return d, nil
}

// parseWorkDuration reads a duration as decimal hours (1.5), hours and
// minutes (1:30) or a Go duration (90m, 1h30m).
func parseWorkDuration(input string) (time.Duration, error) {
	var d time.Duration
	if hours, err := strconv.ParseFloat(input, 64); err == nil {
		d = time.Duration(hours * float64(time.Hour))
	} else if h, m, ok := strings.Cut(input, ":"); ok {
		hours, errH := strconv.Atoi(h)
		minutes, errM := strconv.Atoi(m)
		if errH != nil || errM != nil || minutes < 0 || minutes >= 60 {
			return 0, fmt.Errorf("invalid duration %q", input)
		}
		d = time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute
	} else if d, err = time.ParseDuration(input); err != nil {
		return 0, fmt.Errorf("invalid duration %q, use 1.5, 1:30 or 90m", input)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive, got %q", input)
	}
	return d.Round(time.Minute), nil
}

// formatDuration shows d as hours and minutes plus decimal hours, e.g.
// "1h 30m (1.5 h)".
func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	hours := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", d.Hours()), "0"), ".")
	return fmt.Sprintf("%dh %02dm (%s h)", h, m, hours)
}

// selectTimeRange asks for a date and a duration and returns the begin and end
// times to report, starting at defaultBeginHour on that date.
func selectTimeRange(r *bufio.Reader, now time.Time) (time.Time, time.Time, error) {
	dateInput, err := prompt(r, "Date (YYYY-MM-DD or -N days back, empty for today): ")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	day, err := parseDate(dateInput, now)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	durationInput, err := prompt(r, "Duration (e.g. 1.5, 1:30 or 90m): ")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	duration, err := parseWorkDuration(durationInput)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	begin := day.Add(defaultBeginHour * time.Hour)
	return begin, begin.Add(duration), nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	c := newClient(cfg.URL, getToken())
	c.session = os.Getenv("KIMAI_SESSION")
	cmd := "short"
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "short":
		err = c.getShortWeekInfo()

	case "today":
		err = c.getToday()

	case "week":
		fmt.Println("Get week data")
		err = c.getWeek()

	case "s", "search":
		searchCmd := flag.NewFlagSet("search", flag.ExitOnError)
		customerPtr := searchCmd.String("customer", "", "The name of the customer to search for")
		descriptionPtr := searchCmd.String("description", "", "The description keywords to search for")

		// Parse only the arguments AFTER the word "search"
		searchCmd.Parse(os.Args[2:])
		customerValue := *customerPtr
		descriptionValue := *descriptionPtr
		if len(customerValue) == 0 || len(descriptionValue) == 0 {
			fmt.Println("You need to enter customer and description to use search. e.g \"-customer custom\" and \"-description description\".")
			return
		}
		err = c.searchCustomerAndDescription(customerValue, descriptionValue)

	case "c", "create":
		err = c.createTimesheet()

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		fmt.Println("Expected commands: short, today, week, search, create")
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

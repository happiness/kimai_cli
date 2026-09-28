package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

type Client struct {
	baseURL      string
	token        string
	http         *http.Client
	projectCache map[int]Project
}

type TimeSheet struct {
	ID          int     `json:"id"`
	Activity    int     `json:"activity"`
	Project     int     `json:"project"`
	Duration    float64 `json:"duration"`
	Description string  `json:"description"`
}

type Project struct {
	CustomerName string `json:"parentTitle"`
	ProjectName  string `json:"name"`
	CustomerID   int    `json:"customer"`
}

type DayRange struct {
	DayName string
	Start   time.Time
	End     time.Time
}

type Customer struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
}

const defaultBaseURL = "https://kimai.hpns.dev/api/"
const customLayout = "2006-01-02T15:04:05"

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
	workDays := getWorkWeekMF(time.Now())
	weekDuration := 0.0
	dayDuration := 0.0

	// Week defaults to US system, where Sunday is 1.
	w := time.Now().Weekday()
	dayNumber := (int(w)+6)%7 + 1
	weekLength := 8 * min(dayNumber, 5)
	for daynum, day := range workDays {
		dayName := day.Start.Weekday().String()
		sheets := weeklyData[dayName] // Fetch the already-fetched sheets from our map
		for _, sheet := range sheets {
			duration := sheet.Duration / 3600
			weekDuration = weekDuration + duration
			if daynum+1 == dayNumber {
				dayDuration = dayDuration + duration
			}
		}
	}
	fmt.Printf("t: %.1f (of 8), w: %.1f (of %d/40)\n", dayDuration, weekDuration, weekLength)
	return nil
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
	var result T

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return result, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return result, fmt.Errorf("request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return result, fmt.Errorf("could not reach kimai endpoint %s: got status %d", endpoint, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return result, nil
}

func main() {
	c := newClient(defaultBaseURL, getToken())
	cmd := "short"
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}

	var err error
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

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		fmt.Println("Expected commands: short, today, week, search")
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

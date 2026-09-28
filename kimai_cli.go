package main

import (
	"fmt"
	"net/http"
	"os"
	"log"
	"time"
	"io"
	"encoding/json"
	"strings"
	"strconv"
	"slices"
	"sync"
	"flag"
)

type TimeSheet struct {
	ID 				   int 	       `json:"id"`
	Activity     int 	       `json:"activitiy"`
	Project      int         `json:"project"`
	Duration     float64     `json:"duration"`
	Description  string      `json:"description"`
}

type Project struct {
	CustomerName 			 string      `json:"parentTitle"`
	ProjectName				 string      `json:"name"`
	CustomerId         int      `json:"customer"`
}

type DayRange struct {
	DayName  string
	Start    time.Time
	End      time.Time
}

type Customer struct {
	ID           int    `json:"id"` 
	Description  string `json:"description"`
}

var token string
const BaseUrl = "https://kimai.hpns.dev/api/"

func setToken() {
	token = os.Getenv("KIMAI_TOKEN");
	if token == "" {
		fmt.Println("No KIMAI_TOKEN enviorment variable. This is a required variable.")
		os.Exit(3)
	}
}


func GetWorkWeekMF(t time.Time) []DayRange {
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


func fetchWeekData() map[string][]TimeSheet {
	now := time.Now()
	workDays := GetWorkWeekMF(now)
	customLayout := "2006-01-02T15:04:05"
	weeklyMap := make(map[string][]TimeSheet)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, day := range workDays {
		wg.Add(1)
		go func(d DayRange) {
			defer wg.Done()
			url := BaseUrl + "timesheets?begin=" + day.Start.Format(customLayout) + "&end=" + day.End.Format(customLayout)
			timesheets := getTimeSheets(url)
			mu.Lock()
			weeklyMap[d.Start.Weekday().String()] = timesheets            
			mu.Unlock()
		}(day)
	}
	wg.Wait()
	return weeklyMap
}

func getWeek() {
	weeklyMap := fetchWeekData()
	totalDuration := 0.0
	now := time.Now()
	workDays := GetWorkWeekMF(now)
	for _, day := range workDays {
		dayName := day.Start.Weekday().String()
		sheets := weeklyMap[dayName] // Fetch the already-fetched sheets from our map
		fmt.Println(dayName)
		dayDuration := 0.0
		
		for _, sheet := range sheets {
			duration := float64(sheet.Duration) / 3600.0
			totalDuration = totalDuration + duration
			dayDuration = dayDuration + duration
			outputSheet(sheet, duration)
		}
		dayDurationStr := fmt.Sprintf("%s time reported total: %.1f", dayName, dayDuration)
		fmt.Println(dayDurationStr)
	}
	
	totalDurationStr := fmt.Sprintf("Time reported all week: %.1f of 40", totalDuration)
	fmt.Println(totalDurationStr)

}

func getTimeSheets(url string) []TimeSheet {
        body := makeRequest(url)        
        var timesheets []TimeSheet
        err := json.NewDecoder(strings.NewReader(body)).Decode(&timesheets)
        if err != nil {
                panic(err)
        }
        return timesheets
}

func getToday() {
        now := time.Now()
        year, month, day := now.Date()
        loc := now.Location()
        CurrentDateStart := time.Date(year,month,day,0,0,0,0,loc)
        CurrentDateEnd := time.Date(year,month,day,23,59,59,0,loc)
        customLayout := "2006-01-02T15:04:05"
        url := BaseUrl  + "timesheets?begin=" + CurrentDateStart.Format(customLayout)  + "&end=" + CurrentDateEnd.Format(customLayout)
        body := makeRequest(url)
        var timesheets []TimeSheet
        err := json.NewDecoder(strings.NewReader(body)).Decode(&timesheets)
        if err != nil {
                panic(err)
        }
        for _,sheet := range timesheets {
                duration := sheet.Duration / 3600
								outputSheet(sheet, duration)
        }
}

func getProject(projectId int) Project {
	url := BaseUrl + "projects/" + strconv.Itoa(projectId)
	body := makeRequest(url)
	var project Project
	err := json.NewDecoder(strings.NewReader(body)).Decode(&project)
	if err != nil {
		panic(err)
	}
	return project
}

func getShortWeekInfo() {
	weeklyData := fetchWeekData()
	workDays := GetWorkWeekMF(time.Now())
	weekDuration := 0.0
	dayDuration := 0.0
	weekLength := 0

	// Week defaults to US system, where Sunday is 1.
	w := time.Now().Weekday()
	dayNumber := (int(w)+6)%7 + 1
	weekLength = 8 * dayNumber
	for daynum, day := range workDays {
		dayName := day.Start.Weekday().String()
		sheets := weeklyData[dayName] // Fetch the already-fetched sheets from our map
		for _, sheet := range sheets {
			duration := sheet.Duration / 3600
			weekDuration = weekDuration +  duration
			if daynum + 1 == dayNumber {
				dayDuration = dayDuration + duration
			}	
		}
	}
	message := fmt.Sprintf("t: %.1f (of 8), w: %.1f (of %d/40)", dayDuration, weekDuration, weekLength)
	fmt.Println(message)
}

func outputSheet(sheet TimeSheet, duration float64) {
	project := getProject(sheet.Project)
	message := fmt.Sprintf("%s %s %d - %s %.1f", project.CustomerName, project.ProjectName , sheet.Activity, sheet.Description, duration)
	fmt.Println(message)
}

func searchCustomers(customer string) []Customer {
	url := BaseUrl + "customers?terms" + customer
	body := makeRequest(url)
	var customers []Customer
  err := json.NewDecoder(strings.NewReader(body)).Decode(&customers)
  if err != nil {
  	panic(err)
  }
	return customers
}

func searchTimesheets(term string) []TimeSheet {
	url := BaseUrl + "timesheets?term=" + term
	sheets := getTimeSheets(url)
	return sheets
}

func searchCustomerAndDescription(customer string, description string) {
	customers := searchCustomers(customer)
	timesheets := searchTimesheets(description)
	duration := 0.0
	var customer_ids []int
	for _, customerHits := range customers {
		customer_ids = append(customer_ids, customerHits.ID)
	}
	totalHitDuration := 0.0
	for _, timesheet := range timesheets {
		project := getProject(timesheet.Project)
		if slices.Contains(customer_ids, project.CustomerId) {
			duration = timesheet.Duration / 3600 
			totalHitDuration = totalHitDuration + duration
			outputSheet(timesheet, duration)
		}
		msg := fmt.Sprintf("Total duration of all hits: %.1f", totalHitDuration)
		fmt.Println(msg)
	}
}

func makeRequest(url string) string {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer " + token)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	bodyString := string(bodyBytes)
	return bodyString
}

func main() {
	setToken()
	if len(os.Args) < 2 {
		getShortWeekInfo()
		return
	}
	switch os.Args[1] {
		case "short":
			getShortWeekInfo()
			fmt.Println("About to search")

		case "today":
			getToday()

		case "week":
			fmt.Println("Get week data")
			getWeek()

		case "s", "search":
			searchCmd := flag.NewFlagSet("search", flag.ExitOnError)
			customerPtr := searchCmd.String("customer", "", "The name of the customer to search for")
			descriptionPtr := searchCmd.String("description", "", "The description keywords to search for")

			// Parse only the arguments AFTER the word "search"
			searchCmd.Parse(os.Args[2:])
			customerValue := *customerPtr
			descriptionValue := *descriptionPtr
			if len(customerValue) == 0 ||  len(descriptionValue) == 0 {
				fmt.Println("You need to enter customer and description to use search. e.g \"-customer custom\" and \"-description description\".")
				return
			}
			searchCustomerAndDescription(customerValue, descriptionValue)

		default:
			fmt.Printf("Unknown command: %s\n", os.Args[1])
			fmt.Println("Expected commands: short, today, week, search")
	}
}


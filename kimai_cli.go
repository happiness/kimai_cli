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
		project := getProject(sheet.Project)
		message := fmt.Sprintf("%s %s %d - %s %.1f", project.CustomerName, project.ProjectName , sheet.Activity, sheet.Description, duration)
		fmt.Println(message)
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
	getToday()
}


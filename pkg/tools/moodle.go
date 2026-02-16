package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// MoodleTool provides access to Moodle (QM+) Web Services API.
type MoodleTool struct {
	baseURL string
	token   string
}

func NewMoodleTool(baseURL, token string) *MoodleTool {
	return &MoodleTool{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

func (t *MoodleTool) Name() string {
	return "moodle"
}

func (t *MoodleTool) Description() string {
	return "Access Moodle (QM+) learning platform. Can list enrolled courses, show upcoming/overdue assignments, view calendar events, and browse course contents."
}

func (t *MoodleTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"courses", "assignments", "calendar", "course_contents", "site_info"},
			},
			"course_id": map[string]interface{}{
				"type":        "integer",
				"description": "Course ID (required for course_contents)",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "Number of days to look ahead for assignments/calendar (default: 30)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MoodleTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	days := 30
	if d, ok := args["days"].(float64); ok && int(d) > 0 {
		days = int(d)
	}

	switch action {
	case "site_info":
		return t.siteInfo(ctx)
	case "courses":
		return t.courses(ctx)
	case "assignments":
		return t.assignments(ctx, days)
	case "calendar":
		return t.calendar(ctx, days)
	case "course_contents":
		courseID, ok := args["course_id"].(float64)
		if !ok || courseID == 0 {
			return ErrorResult("course_id is required for course_contents")
		}
		return t.courseContents(ctx, int(courseID))
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s (use: courses, assignments, calendar, course_contents, site_info)", action))
	}
}

// -- Moodle API helpers --

func (t *MoodleTool) call(ctx context.Context, wsfunction string, params url.Values) (json.RawMessage, error) {
	params.Set("wstoken", t.token)
	params.Set("wsfunction", wsfunction)
	params.Set("moodlewsrestformat", "json")

	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/webservice/rest/server.php", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Check for Moodle error response
	var moodleErr struct {
		Exception string `json:"exception"`
		ErrorCode string `json:"errorcode"`
		Message   string `json:"message"`
	}
	if json.Unmarshal(body, &moodleErr) == nil && moodleErr.Exception != "" {
		return nil, fmt.Errorf("moodle error %s: %s", moodleErr.ErrorCode, moodleErr.Message)
	}

	return body, nil
}

func (t *MoodleTool) getUserID(ctx context.Context) (int, string, error) {
	data, err := t.call(ctx, "core_webservice_get_site_info", url.Values{})
	if err != nil {
		return 0, "", err
	}
	var info struct {
		UserID   int    `json:"userid"`
		FullName string `json:"fullname"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return 0, "", err
	}
	return info.UserID, info.FullName, nil
}

func (t *MoodleTool) getCourseIDs(ctx context.Context, uid int) ([]int, error) {
	data, err := t.call(ctx, "core_enrol_get_users_courses", url.Values{"userid": {fmt.Sprintf("%d", uid)}})
	if err != nil {
		return nil, err
	}
	var courses []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(data, &courses); err != nil {
		return nil, err
	}
	ids := make([]int, len(courses))
	for i, c := range courses {
		ids[i] = c.ID
	}
	return ids, nil
}

func fmtTime(ts int64) string {
	if ts == 0 {
		return "No date"
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04")
}

// -- Actions --

func (t *MoodleTool) siteInfo(ctx context.Context) *ToolResult {
	data, err := t.call(ctx, "core_webservice_get_site_info", url.Values{})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get site info: %v", err))
	}
	var info struct {
		UserID   int    `json:"userid"`
		FullName string `json:"fullname"`
		Username string `json:"username"`
		SiteName string `json:"sitename"`
		SiteURL  string `json:"siteurl"`
	}
	json.Unmarshal(data, &info)
	result := fmt.Sprintf("Logged in as: %s (%s)\nUser ID: %d\nSite: %s\nURL: %s",
		info.FullName, info.Username, info.UserID, info.SiteName, info.SiteURL)
	return SilentResult(result)
}

func (t *MoodleTool) courses(ctx context.Context) *ToolResult {
	uid, _, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}
	data, err := t.call(ctx, "core_enrol_get_users_courses", url.Values{"userid": {fmt.Sprintf("%d", uid)}})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get courses: %v", err))
	}
	var courses []struct {
		ID        int    `json:"id"`
		ShortName string `json:"shortname"`
		FullName  string `json:"fullname"`
	}
	json.Unmarshal(data, &courses)

	sort.Slice(courses, func(i, j int) bool {
		return courses[i].ShortName < courses[j].ShortName
	})

	var lines []string
	lines = append(lines, fmt.Sprintf("Enrolled courses (%d):", len(courses)))
	for _, c := range courses {
		lines = append(lines, fmt.Sprintf("  [%d] %s — %s", c.ID, c.ShortName, c.FullName))
	}
	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) assignments(ctx context.Context, days int) *ToolResult {
	uid, _, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}
	cids, err := t.getCourseIDs(ctx, uid)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get courses: %v", err))
	}

	params := url.Values{}
	for i, id := range cids {
		params.Set(fmt.Sprintf("courseids[%d]", i), fmt.Sprintf("%d", id))
	}
	data, err := t.call(ctx, "mod_assign_get_assignments", params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get assignments: %v", err))
	}

	var result struct {
		Courses []struct {
			ShortName   string `json:"shortname"`
			FullName    string `json:"fullname"`
			Assignments []struct {
				Name    string `json:"name"`
				DueDate int64  `json:"duedate"`
			} `json:"assignments"`
		} `json:"courses"`
	}
	json.Unmarshal(data, &result)

	now := time.Now().Unix()
	cutoff := now + int64(days)*86400

	type entry struct {
		Due    int64
		Course string
		Name   string
	}

	var overdue, upcoming []entry
	for _, c := range result.Courses {
		cname := c.ShortName
		if cname == "" {
			cname = c.FullName
		}
		for _, a := range c.Assignments {
			if a.DueDate == 0 {
				continue
			}
			e := entry{Due: a.DueDate, Course: cname, Name: a.Name}
			if a.DueDate < now {
				overdue = append(overdue, e)
			} else if a.DueDate <= cutoff {
				upcoming = append(upcoming, e)
			}
		}
	}

	sort.Slice(overdue, func(i, j int) bool { return overdue[i].Due > overdue[j].Due })
	sort.Slice(upcoming, func(i, j int) bool { return upcoming[i].Due < upcoming[j].Due })

	var lines []string

	if len(upcoming) > 0 {
		lines = append(lines, fmt.Sprintf("Upcoming assignments (next %d days):", days))
		for _, e := range upcoming {
			daysLeft := (e.Due - now) / 86400
			lines = append(lines, fmt.Sprintf("  %s  %-25s %s  (%dd left)", fmtTime(e.Due), e.Course, e.Name, daysLeft))
		}
	} else {
		lines = append(lines, fmt.Sprintf("No upcoming assignments in the next %d days.", days))
	}

	if len(overdue) > 0 {
		lines = append(lines, fmt.Sprintf("\nOverdue assignments (%d):", len(overdue)))
		for _, e := range overdue {
			daysLate := (now - e.Due) / 86400
			lines = append(lines, fmt.Sprintf("  %s  %-25s %s  (%dd late)", fmtTime(e.Due), e.Course, e.Name, daysLate))
		}
	}

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) calendar(ctx context.Context, days int) *ToolResult {
	uid, _, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}
	cids, err := t.getCourseIDs(ctx, uid)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get courses: %v", err))
	}

	now := time.Now().Unix()
	end := now + int64(days)*86400

	params := url.Values{}
	for i, id := range cids {
		params.Set(fmt.Sprintf("events[courseids][%d]", i), fmt.Sprintf("%d", id))
	}
	params.Set("options[timestart]", fmt.Sprintf("%d", now))
	params.Set("options[timeend]", fmt.Sprintf("%d", end))

	data, err := t.call(ctx, "core_calendar_get_calendar_events", params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get calendar: %v", err))
	}

	var result struct {
		Events []struct {
			Name       string `json:"name"`
			TimeStart  int64  `json:"timestart"`
			EventType  string `json:"eventtype"`
			CourseName string `json:"coursename"`
		} `json:"events"`
	}
	json.Unmarshal(data, &result)

	sort.Slice(result.Events, func(i, j int) bool {
		return result.Events[i].TimeStart < result.Events[j].TimeStart
	})

	var lines []string
	lines = append(lines, fmt.Sprintf("Calendar events (next %d days): %d", days, len(result.Events)))
	for _, e := range result.Events {
		suffix := ""
		if e.CourseName != "" {
			suffix = " (" + e.CourseName + ")"
		}
		lines = append(lines, fmt.Sprintf("  %s  [%-8s] %s%s", fmtTime(e.TimeStart), e.EventType, e.Name, suffix))
	}
	if len(result.Events) == 0 {
		lines = append(lines, fmt.Sprintf("  No events in the next %d days.", days))
	}
	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) courseContents(ctx context.Context, courseID int) *ToolResult {
	data, err := t.call(ctx, "core_course_get_contents", url.Values{"courseid": {fmt.Sprintf("%d", courseID)}})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get course contents: %v", err))
	}

	var sections []struct {
		Name    string `json:"name"`
		Modules []struct {
			Name    string `json:"name"`
			ModName string `json:"modname"`
			URL     string `json:"url"`
		} `json:"modules"`
	}
	json.Unmarshal(data, &sections)

	var lines []string
	lines = append(lines, fmt.Sprintf("Course %d contents:", courseID))
	for _, s := range sections {
		if len(s.Modules) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("\n== %s ==", s.Name))
		for _, m := range s.Modules {
			lines = append(lines, fmt.Sprintf("  [%s] %s", m.ModName, m.Name))
			if m.URL != "" {
				lines = append(lines, fmt.Sprintf("        %s", m.URL))
			}
		}
	}
	return SilentResult(strings.Join(lines, "\n"))
}

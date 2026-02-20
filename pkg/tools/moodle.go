package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/utils"
)

// MoodleTool provides access to Moodle (QM+) Web Services API with auto-refresh.
type MoodleTool struct {
	baseURL        string
	token          string
	m365User       string
	m365Pass       string
	scriptPath     string
	workspace      string
	mu             sync.Mutex
	refreshed      bool                   // tracks if we already tried refresh this call
	onTokenRefresh func(newToken string)   // callback to persist new token
}

type MoodleToolOptions struct {
	BaseURL        string
	Token          string
	M365Username   string
	M365Password   string
	ScriptPath     string // path to moodle_sso_refresh.py
	Workspace      string
	OnTokenRefresh func(newToken string)
}

func NewMoodleTool(opts MoodleToolOptions) *MoodleTool {
	scriptPath := opts.ScriptPath
	if scriptPath == "" {
		// Default locations to search
		candidates := []string{
			"/usr/local/lib/picoclaw/scripts/moodle_sso_refresh.py",
			"scripts/moodle_sso_refresh.py",
		}
		for _, p := range candidates {
			if _, err := exec.LookPath("python3"); err == nil {
				scriptPath = p
				break
			}
		}
		if scriptPath == "" {
			scriptPath = candidates[0]
		}
	}
	return &MoodleTool{
		baseURL:        strings.TrimRight(opts.BaseURL, "/"),
		token:          opts.Token,
		m365User:       opts.M365Username,
		m365Pass:       opts.M365Password,
		scriptPath:     scriptPath,
		workspace:      opts.Workspace,
		onTokenRefresh: opts.OnTokenRefresh,
	}
}

func (t *MoodleTool) Name() string {
	return "moodle"
}

func (t *MoodleTool) Description() string {
	return "Full access to Moodle (QM+) learning platform. Convenience actions for courses, assignments, grades, forums, quizzes, completion, notifications, calendar, files. Plus a generic api_call action to invoke ANY of the 350+ Moodle Web Service functions directly. Token auto-refreshes via M365 SSO."
}

func (t *MoodleTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform. Use api_call to call any Moodle WS function not covered by convenience actions.",
				"enum": []string{
					// Convenience actions
					"courses", "assignments", "calendar", "course_contents", "site_info",
					"grades", "grade_details", "submission_status",
					"forums", "forum_posts",
					"quizzes", "quiz_attempts",
					"completion", "notifications", "messages",
					"enrolled_users", "search_courses",
					// File actions
					"download_file", "get_file_content",
					// Generic passthrough (100% API coverage)
					"api_call", "list_functions",
				},
			},
			"course_id": map[string]interface{}{
				"type":        "integer",
				"description": "Course ID (used by course_contents, grade_details, forums, quizzes, completion, enrolled_users)",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "Number of days to look ahead for assignments/calendar (default: 30)",
			},
			"file_url": map[string]interface{}{
				"type":        "string",
				"description": "Moodle file URL from course_contents output (required for download_file and get_file_content)",
			},
			"filename": map[string]interface{}{
				"type":        "string",
				"description": "Optional filename override for download_file",
			},
			// IDs for specific resources
			"assign_id": map[string]interface{}{
				"type":        "integer",
				"description": "Assignment ID (for submission_status)",
			},
			"forum_id": map[string]interface{}{
				"type":        "integer",
				"description": "Forum ID (for forum_posts — get forum IDs from the forums action)",
			},
			"discussion_id": map[string]interface{}{
				"type":        "integer",
				"description": "Discussion ID (for forum_posts — get IDs from forum_posts with forum_id)",
			},
			"quiz_id": map[string]interface{}{
				"type":        "integer",
				"description": "Quiz ID (for quiz_attempts)",
			},
			"attempt_id": map[string]interface{}{
				"type":        "integer",
				"description": "Attempt ID (for reviewing a specific quiz attempt)",
			},
			"user_id": map[string]interface{}{
				"type":        "integer",
				"description": "User ID (0 or omit for current user)",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query (for search_courses)",
			},
			// Generic API passthrough
			"wsfunction": map[string]interface{}{
				"type":        "string",
				"description": "Moodle WS function name for api_call (e.g. core_message_send_instant_messages, mod_data_get_entries)",
			},
			"params": map[string]interface{}{
				"type":        "object",
				"description": "Parameters for api_call as key-value pairs. Arrays use bracket notation in keys: {\"courseids[0]\": 123}. Nested: {\"events[courseids][0]\": 123}.",
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

	// Reset refresh flag for each top-level Execute call
	t.mu.Lock()
	t.refreshed = false
	t.mu.Unlock()

	switch action {
	// -- Original convenience actions --
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

	// -- Grades --
	case "grades":
		return t.grades(ctx)
	case "grade_details":
		courseID, ok := args["course_id"].(float64)
		if !ok || courseID == 0 {
			return ErrorResult("course_id is required for grade_details")
		}
		return t.gradeDetails(ctx, int(courseID))

	// -- Assignments --
	case "submission_status":
		assignID, ok := args["assign_id"].(float64)
		if !ok || assignID == 0 {
			return ErrorResult("assign_id is required for submission_status")
		}
		return t.submissionStatus(ctx, int(assignID))

	// -- Forums --
	case "forums":
		courseID, _ := args["course_id"].(float64)
		return t.forums(ctx, int(courseID))
	case "forum_posts":
		forumID, _ := args["forum_id"].(float64)
		discussionID, _ := args["discussion_id"].(float64)
		if forumID == 0 && discussionID == 0 {
			return ErrorResult("either forum_id (to list discussions) or discussion_id (to get posts) is required")
		}
		if discussionID > 0 {
			return t.discussionPosts(ctx, int(discussionID))
		}
		return t.forumDiscussions(ctx, int(forumID))

	// -- Quizzes --
	case "quizzes":
		courseID, _ := args["course_id"].(float64)
		return t.quizzes(ctx, int(courseID))
	case "quiz_attempts":
		quizID, _ := args["quiz_id"].(float64)
		attemptID, _ := args["attempt_id"].(float64)
		if quizID == 0 && attemptID == 0 {
			return ErrorResult("quiz_id (to list attempts) or attempt_id (to review one) is required")
		}
		if attemptID > 0 {
			return t.quizAttemptReview(ctx, int(attemptID))
		}
		return t.quizAttempts(ctx, int(quizID))

	// -- Completion --
	case "completion":
		courseID, ok := args["course_id"].(float64)
		if !ok || courseID == 0 {
			return ErrorResult("course_id is required for completion")
		}
		return t.completion(ctx, int(courseID))

	// -- Messages & Notifications --
	case "notifications":
		return t.notifications(ctx)
	case "messages":
		return t.recentMessages(ctx)

	// -- Users & Search --
	case "enrolled_users":
		courseID, ok := args["course_id"].(float64)
		if !ok || courseID == 0 {
			return ErrorResult("course_id is required for enrolled_users")
		}
		return t.enrolledUsers(ctx, int(courseID))
	case "search_courses":
		query, _ := args["query"].(string)
		if query == "" {
			return ErrorResult("query is required for search_courses")
		}
		return t.searchCourses(ctx, query)

	// -- File actions --
	case "download_file":
		fileURL, ok := args["file_url"].(string)
		if !ok || fileURL == "" {
			return ErrorResult("file_url is required for download_file")
		}
		filename, _ := args["filename"].(string)
		return t.downloadFile(ctx, fileURL, filename)
	case "get_file_content":
		fileURL, ok := args["file_url"].(string)
		if !ok || fileURL == "" {
			return ErrorResult("file_url is required for get_file_content")
		}
		return t.getFileContent(ctx, fileURL)

	// -- Generic API passthrough --
	case "api_call":
		wsfunction, ok := args["wsfunction"].(string)
		if !ok || wsfunction == "" {
			return ErrorResult("wsfunction is required for api_call (e.g. core_message_send_instant_messages)")
		}
		params, _ := args["params"].(map[string]interface{})
		return t.apiCall(ctx, wsfunction, params)
	case "list_functions":
		return t.listFunctions(ctx)

	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s — use api_call with wsfunction to call any Moodle WS function", action))
	}
}

// ============================================================================
// Token refresh
// ============================================================================

func (t *MoodleTool) isTokenError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalidtoken") ||
		strings.Contains(msg, "accessexception") ||
		strings.Contains(msg, "invalidsesskey")
}

func (t *MoodleTool) tryRefresh() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.refreshed {
		return fmt.Errorf("already tried refresh this call")
	}
	t.refreshed = true

	if t.m365User == "" || t.m365Pass == "" {
		return fmt.Errorf("no M365 credentials configured for auto-refresh")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", t.scriptPath, t.m365User, t.m365Pass)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("SSO refresh failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("SSO refresh failed: %w", err)
	}

	newToken := strings.TrimSpace(string(out))
	if newToken == "" {
		return fmt.Errorf("SSO refresh returned empty token")
	}

	t.token = newToken

	if t.onTokenRefresh != nil {
		t.onTokenRefresh(newToken)
	}

	return nil
}

// ============================================================================
// Moodle API helpers
// ============================================================================

func (t *MoodleTool) call(ctx context.Context, wsfunction string, params url.Values) (json.RawMessage, error) {
	data, err := t.rawCall(ctx, wsfunction, params)
	if err != nil && t.isTokenError(err) {
		if refreshErr := t.tryRefresh(); refreshErr != nil {
			return nil, fmt.Errorf("%v (auto-refresh also failed: %v)", err, refreshErr)
		}
		return t.rawCall(ctx, wsfunction, params)
	}
	return data, err
}

func (t *MoodleTool) rawCall(ctx context.Context, wsfunction string, params url.Values) (json.RawMessage, error) {
	params.Set("wstoken", t.token)
	params.Set("wsfunction", wsfunction)
	params.Set("moodlewsrestformat", "json")

	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/webservice/rest/server.php", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
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

// flattenParams converts a JSON object to url.Values for the Moodle API.
// Handles: simple values, arrays (bracket notation), nested objects.
func flattenParams(m map[string]interface{}) url.Values {
	v := url.Values{}
	if m == nil {
		return v
	}
	flattenInto(v, "", m)
	return v
}

func flattenInto(v url.Values, prefix string, m map[string]interface{}) {
	for key, val := range m {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "[" + key + "]"
		}
		switch typed := val.(type) {
		case map[string]interface{}:
			flattenInto(v, fullKey, typed)
		case []interface{}:
			for i, item := range typed {
				indexKey := fmt.Sprintf("%s[%d]", fullKey, i)
				v.Set(indexKey, fmtParam(item))
			}
		default:
			v.Set(fullKey, fmtParam(val))
		}
	}
}

// fmtParam formats a parameter value for URL encoding.
// Handles float64 integers (from JSON) without scientific notation.
func fmtParam(val interface{}) string {
	switch v := val.(type) {
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", val)
	}
}

func fmtTime(ts int64) string {
	if ts == 0 {
		return "No date"
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04")
}

// ============================================================================
// Generic API passthrough — gives 100% Moodle API coverage
// ============================================================================

func (t *MoodleTool) apiCall(ctx context.Context, wsfunction string, params map[string]interface{}) *ToolResult {
	v := flattenParams(params)
	data, err := t.call(ctx, wsfunction, v)
	if err != nil {
		return ErrorResult(fmt.Sprintf("api_call %s failed: %v", wsfunction, err))
	}

	// Pretty-print the JSON response
	var pretty json.RawMessage
	if json.Unmarshal(data, &pretty) == nil {
		indented, err := json.MarshalIndent(pretty, "", "  ")
		if err == nil {
			// Truncate if very large (>50KB)
			result := string(indented)
			if len(result) > 50000 {
				result = result[:50000] + "\n... (truncated, response was " + formatFileSize(int64(len(indented))) + ")"
			}
			return SilentResult(fmt.Sprintf("[%s] response:\n%s", wsfunction, result))
		}
	}
	return SilentResult(fmt.Sprintf("[%s] response:\n%s", wsfunction, string(data)))
}

func (t *MoodleTool) listFunctions(ctx context.Context) *ToolResult {
	data, err := t.call(ctx, "core_webservice_get_site_info", url.Values{})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get site info: %v", err))
	}

	var info struct {
		Functions []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"functions"`
	}
	json.Unmarshal(data, &info)

	// Group by prefix
	groups := map[string][]string{}
	for _, f := range info.Functions {
		parts := strings.SplitN(f.Name, "_", 3)
		prefix := "other"
		if len(parts) >= 2 {
			prefix = parts[0] + "_" + parts[1]
		}
		groups[prefix] = append(groups[prefix], f.Name)
	}

	// Sort group names
	groupNames := make([]string, 0, len(groups))
	for g := range groups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)

	var lines []string
	lines = append(lines, fmt.Sprintf("Available Moodle WS functions (%d total):", len(info.Functions)))
	lines = append(lines, "Use api_call with wsfunction to call any of these.\n")

	for _, g := range groupNames {
		fns := groups[g]
		sort.Strings(fns)
		lines = append(lines, fmt.Sprintf("== %s (%d) ==", g, len(fns)))
		for _, fn := range fns {
			lines = append(lines, "  "+fn)
		}
		lines = append(lines, "")
	}

	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================================
// Convenience actions — original
// ============================================================================

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
		Release  string `json:"release"`
	}
	json.Unmarshal(data, &info)
	result := fmt.Sprintf("Logged in as: %s (%s)\nUser ID: %d\nSite: %s (%s)\nURL: %s",
		info.FullName, info.Username, info.UserID, info.SiteName, info.Release, info.SiteURL)
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
		ID        int     `json:"id"`
		ShortName string  `json:"shortname"`
		FullName  string  `json:"fullname"`
		Progress  float64 `json:"progress"`
	}
	json.Unmarshal(data, &courses)

	sort.Slice(courses, func(i, j int) bool {
		return courses[i].ShortName < courses[j].ShortName
	})

	var lines []string
	lines = append(lines, fmt.Sprintf("Enrolled courses (%d):", len(courses)))
	for _, c := range courses {
		progress := ""
		if c.Progress > 0 {
			progress = fmt.Sprintf(" (%.0f%% complete)", c.Progress)
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s — %s%s", c.ID, c.ShortName, c.FullName, progress))
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
				ID      int    `json:"id"`
				Name    string `json:"name"`
				DueDate int64  `json:"duedate"`
				Intro   string `json:"intro"`
			} `json:"assignments"`
		} `json:"courses"`
	}
	json.Unmarshal(data, &result)

	now := time.Now().Unix()
	cutoff := now + int64(days)*86400

	type entry struct {
		ID     int
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
			e := entry{ID: a.ID, Due: a.DueDate, Course: cname, Name: a.Name}
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
			lines = append(lines, fmt.Sprintf("  %s  %-25s %s  (id:%d, %dd left)", fmtTime(e.Due), e.Course, e.Name, e.ID, daysLeft))
		}
	} else {
		lines = append(lines, fmt.Sprintf("No upcoming assignments in the next %d days.", days))
	}

	if len(overdue) > 0 {
		lines = append(lines, fmt.Sprintf("\nOverdue assignments (%d):", len(overdue)))
		for _, e := range overdue {
			daysLate := (now - e.Due) / 86400
			lines = append(lines, fmt.Sprintf("  %s  %-25s %s  (id:%d, %dd late)", fmtTime(e.Due), e.Course, e.Name, e.ID, daysLate))
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
			ID      int    `json:"id"`
			Name    string `json:"name"`
			ModName string `json:"modname"`
			URL     string `json:"url"`
			Contents []struct {
				Type     string `json:"type"`
				Filename string `json:"filename"`
				Filesize int64  `json:"filesize"`
				FileURL  string `json:"fileurl"`
				MimeType string `json:"mimetype"`
			} `json:"contents"`
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
			lines = append(lines, fmt.Sprintf("  [%s] %s (cmid:%d)", m.ModName, m.Name, m.ID))
			if m.URL != "" {
				lines = append(lines, fmt.Sprintf("        %s", m.URL))
			}
			for _, c := range m.Contents {
				if c.Type == "file" || c.Type == "url" {
					lines = append(lines, fmt.Sprintf("        file: %s (%s, %s)", c.Filename, formatFileSize(c.Filesize), c.MimeType))
					lines = append(lines, fmt.Sprintf("              %s", c.FileURL))
				}
			}
		}
	}
	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================================
// Convenience actions — new: Grades
// ============================================================================

func (t *MoodleTool) grades(ctx context.Context) *ToolResult {
	uid, name, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}

	data, err := t.call(ctx, "gradereport_overview_get_course_grades", url.Values{
		"userid": {fmt.Sprintf("%d", uid)},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get grades: %v", err))
	}

	var result struct {
		Grades []struct {
			CourseID int    `json:"courseid"`
			Grade    string `json:"grade"`
			RawGrade string `json:"rawgrade"`
			Rank     int    `json:"rank"`
		} `json:"grades"`
	}
	json.Unmarshal(data, &result)

	// Get course names
	courseData, _ := t.call(ctx, "core_enrol_get_users_courses", url.Values{"userid": {fmt.Sprintf("%d", uid)}})
	courseNames := map[int]string{}
	var courses []struct {
		ID        int    `json:"id"`
		ShortName string `json:"shortname"`
	}
	json.Unmarshal(courseData, &courses)
	for _, c := range courses {
		courseNames[c.ID] = c.ShortName
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Course grades for %s:", name))
	for _, g := range result.Grades {
		cname := courseNames[g.CourseID]
		if cname == "" {
			cname = fmt.Sprintf("course:%d", g.CourseID)
		}
		grade := g.Grade
		if grade == "" {
			grade = "-"
		}
		lines = append(lines, fmt.Sprintf("  %-25s %s", cname, grade))
	}
	if len(result.Grades) == 0 {
		lines = append(lines, "  No grades available yet.")
	}

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) gradeDetails(ctx context.Context, courseID int) *ToolResult {
	uid, _, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}

	data, err := t.call(ctx, "gradereport_user_get_grade_items", url.Values{
		"courseid": {fmt.Sprintf("%d", courseID)},
		"userid":   {fmt.Sprintf("%d", uid)},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get grade details: %v", err))
	}

	var result struct {
		UserGrades []struct {
			CourseID int    `json:"courseid"`
			UserID   int    `json:"userid"`
			GradeItems []struct {
				ItemName             string  `json:"itemname"`
				ItemType             string  `json:"itemtype"`
				ItemModule           string  `json:"itemmodule"`
				GradeFormatted       string  `json:"gradeformatted"`
				GradeRaw             float64 `json:"graderaw"`
				GradeMin             float64 `json:"grademin"`
				GradeMax             float64 `json:"grademax"`
				PercentageFormatted  string  `json:"percentageformatted"`
				WeightFormatted      string  `json:"weightformatted"`
				Feedback             string  `json:"feedback"`
			} `json:"gradeitems"`
		} `json:"usergrades"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Grade details for course %d:", courseID))

	for _, ug := range result.UserGrades {
		for _, gi := range ug.GradeItems {
			name := gi.ItemName
			if name == "" {
				name = fmt.Sprintf("[%s]", gi.ItemType)
			}
			grade := gi.GradeFormatted
			if grade == "" {
				grade = "-"
			}
			extra := ""
			if gi.PercentageFormatted != "" {
				extra = " (" + gi.PercentageFormatted + ")"
			}
			weight := ""
			if gi.WeightFormatted != "" {
				weight = " weight:" + gi.WeightFormatted
			}
			lines = append(lines, fmt.Sprintf("  %-35s %s%s%s", name, grade, extra, weight))
		}
	}

	if len(lines) == 1 {
		lines = append(lines, "  No grade items found.")
	}

	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================================
// Convenience actions — new: Assignment submission status
// ============================================================================

func (t *MoodleTool) submissionStatus(ctx context.Context, assignID int) *ToolResult {
	data, err := t.call(ctx, "mod_assign_get_submission_status", url.Values{
		"assignid": {fmt.Sprintf("%d", assignID)},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get submission status: %v", err))
	}

	// Return the full JSON since the structure is complex and varies
	var pretty json.RawMessage
	json.Unmarshal(data, &pretty)
	indented, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return SilentResult(string(data))
	}

	result := string(indented)
	if len(result) > 30000 {
		result = result[:30000] + "\n... (truncated)"
	}

	return SilentResult(fmt.Sprintf("Submission status for assignment %d:\n%s", assignID, result))
}

// ============================================================================
// Convenience actions — new: Forums
// ============================================================================

func (t *MoodleTool) forums(ctx context.Context, courseID int) *ToolResult {
	params := url.Values{}
	if courseID > 0 {
		params.Set("courseids[0]", fmt.Sprintf("%d", courseID))
	}

	data, err := t.call(ctx, "mod_forum_get_forums_by_courses", params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get forums: %v", err))
	}

	var forums []struct {
		ID              int    `json:"id"`
		Course          int    `json:"course"`
		Name            string `json:"name"`
		Type            string `json:"type"`
		NumDiscussions  int    `json:"numdiscussions"`
	}
	json.Unmarshal(data, &forums)

	var lines []string
	lines = append(lines, fmt.Sprintf("Forums (%d):", len(forums)))
	for _, f := range forums {
		lines = append(lines, fmt.Sprintf("  [%d] %s (course:%d, type:%s, discussions:%d)", f.ID, f.Name, f.Course, f.Type, f.NumDiscussions))
	}
	if len(forums) == 0 {
		lines = append(lines, "  No forums found.")
	}
	lines = append(lines, "\nUse forum_posts with forum_id to list discussions, or discussion_id to get posts.")

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) forumDiscussions(ctx context.Context, forumID int) *ToolResult {
	data, err := t.call(ctx, "mod_forum_get_forum_discussions", url.Values{
		"forumid": {fmt.Sprintf("%d", forumID)},
		"perpage": {"25"},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get discussions: %v", err))
	}

	var result struct {
		Discussions []struct {
			ID            int    `json:"id"`
			Name          string `json:"name"`
			UserFullName  string `json:"firstuserfullname"`
			NumReplies    int    `json:"numreplies"`
			TimeModified  int64  `json:"timemodified"`
			Pinned        bool   `json:"pinned"`
			Locked        bool   `json:"locked"`
		} `json:"discussions"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Forum %d discussions (%d):", forumID, len(result.Discussions)))
	for _, d := range result.Discussions {
		flags := ""
		if d.Pinned {
			flags += " [pinned]"
		}
		if d.Locked {
			flags += " [locked]"
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s — by %s, %d replies, modified %s%s",
			d.ID, d.Name, d.UserFullName, d.NumReplies, fmtTime(d.TimeModified), flags))
	}
	if len(result.Discussions) == 0 {
		lines = append(lines, "  No discussions.")
	}
	lines = append(lines, "\nUse forum_posts with discussion_id to read posts.")

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) discussionPosts(ctx context.Context, discussionID int) *ToolResult {
	data, err := t.call(ctx, "mod_forum_get_discussion_posts", url.Values{
		"discussionid": {fmt.Sprintf("%d", discussionID)},
		"sortby":       {"created"},
		"sortdirection": {"ASC"},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get posts: %v", err))
	}

	var result struct {
		Posts []struct {
			ID       int    `json:"id"`
			ParentID int    `json:"parentid"`
			Subject  string `json:"subject"`
			Message  string `json:"message"`
			Author   struct {
				FullName string `json:"fullname"`
			} `json:"author"`
			TimeCreated int64 `json:"timecreated"`
			Attachments []struct {
				Filename string `json:"filename"`
				FileURL  string `json:"fileurl"`
				Filesize int64  `json:"filesize"`
				MimeType string `json:"mimetype"`
			} `json:"attachments"`
		} `json:"posts"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Discussion %d posts (%d):", discussionID, len(result.Posts)))
	for _, p := range result.Posts {
		lines = append(lines, fmt.Sprintf("\n--- Post %d by %s (%s) ---", p.ID, p.Author.FullName, fmtTime(p.TimeCreated)))
		if p.Subject != "" {
			lines = append(lines, "Subject: "+p.Subject)
		}
		// Strip HTML tags for readability
		msg := stripHTML(p.Message)
		if len(msg) > 2000 {
			msg = msg[:2000] + "..."
		}
		lines = append(lines, msg)
		for _, a := range p.Attachments {
			lines = append(lines, fmt.Sprintf("  attachment: %s (%s) %s", a.Filename, formatFileSize(a.Filesize), a.FileURL))
		}
	}

	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================================
// Convenience actions — new: Quizzes
// ============================================================================

func (t *MoodleTool) quizzes(ctx context.Context, courseID int) *ToolResult {
	params := url.Values{}
	if courseID > 0 {
		params.Set("courseids[0]", fmt.Sprintf("%d", courseID))
	}

	data, err := t.call(ctx, "mod_quiz_get_quizzes_by_courses", params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get quizzes: %v", err))
	}

	var result struct {
		Quizzes []struct {
			ID         int    `json:"id"`
			Course     int    `json:"course"`
			Name       string `json:"name"`
			TimeOpen   int64  `json:"timeopen"`
			TimeClose  int64  `json:"timeclose"`
			TimeLimit  int    `json:"timelimit"`
			Attempts   int    `json:"attempts"`
			Grade      float64 `json:"grade"`
		} `json:"quizzes"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Quizzes (%d):", len(result.Quizzes)))
	for _, q := range result.Quizzes {
		timeInfo := ""
		if q.TimeOpen > 0 {
			timeInfo += " opens:" + fmtTime(q.TimeOpen)
		}
		if q.TimeClose > 0 {
			timeInfo += " closes:" + fmtTime(q.TimeClose)
		}
		if q.TimeLimit > 0 {
			timeInfo += fmt.Sprintf(" limit:%dm", q.TimeLimit/60)
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s (course:%d, max_grade:%.0f, max_attempts:%d%s)",
			q.ID, q.Name, q.Course, q.Grade, q.Attempts, timeInfo))
	}
	if len(result.Quizzes) == 0 {
		lines = append(lines, "  No quizzes found.")
	}
	lines = append(lines, "\nUse quiz_attempts with quiz_id to see attempts.")

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) quizAttempts(ctx context.Context, quizID int) *ToolResult {
	data, err := t.call(ctx, "mod_quiz_get_user_attempts", url.Values{
		"quizid": {fmt.Sprintf("%d", quizID)},
		"status": {"all"},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get quiz attempts: %v", err))
	}

	var result struct {
		Attempts []struct {
			ID          int     `json:"id"`
			Attempt     int     `json:"attempt"`
			State       string  `json:"state"`
			TimeStart   int64   `json:"timestart"`
			TimeFinish  int64   `json:"timefinish"`
			SumGrades   float64 `json:"sumgrades"`
		} `json:"attempts"`
	}
	json.Unmarshal(data, &result)

	// Also get best grade
	bestData, _ := t.call(ctx, "mod_quiz_get_user_best_grade", url.Values{
		"quizid": {fmt.Sprintf("%d", quizID)},
	})
	var best struct {
		Grade     float64 `json:"grade"`
		HasGrade  bool    `json:"hasgrade"`
	}
	json.Unmarshal(bestData, &best)

	var lines []string
	lines = append(lines, fmt.Sprintf("Quiz %d attempts (%d):", quizID, len(result.Attempts)))
	if best.HasGrade {
		lines = append(lines, fmt.Sprintf("Best grade: %.2f", best.Grade))
	}
	for _, a := range result.Attempts {
		finish := "in progress"
		if a.TimeFinish > 0 {
			finish = fmtTime(a.TimeFinish)
		}
		lines = append(lines, fmt.Sprintf("  attempt #%d (id:%d) state:%s started:%s finished:%s grade:%.2f",
			a.Attempt, a.ID, a.State, fmtTime(a.TimeStart), finish, a.SumGrades))
	}
	if len(result.Attempts) == 0 {
		lines = append(lines, "  No attempts.")
	}
	lines = append(lines, "\nUse quiz_attempts with attempt_id to review a specific attempt.")

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) quizAttemptReview(ctx context.Context, attemptID int) *ToolResult {
	data, err := t.call(ctx, "mod_quiz_get_attempt_review", url.Values{
		"attemptid": {fmt.Sprintf("%d", attemptID)},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get attempt review: %v", err))
	}

	var result struct {
		Grade     string `json:"grade"`
		Attempt   struct {
			ID        int     `json:"id"`
			State     string  `json:"state"`
			SumGrades float64 `json:"sumgrades"`
		} `json:"attempt"`
		Questions []struct {
			Slot       int    `json:"slot"`
			Type       string `json:"type"`
			HTML       string `json:"html"`
			Mark       string `json:"mark"`
			State      string `json:"state"`
			FlagState  bool   `json:"flagged"`
		} `json:"questions"`
		AdditionalData []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Content string `json:"content"`
		} `json:"additionaldata"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Attempt %d review (grade: %s, state: %s):", attemptID, result.Grade, result.Attempt.State))

	for _, q := range result.Questions {
		lines = append(lines, fmt.Sprintf("\n  Q%d [%s] state:%s mark:%s", q.Slot, q.Type, q.State, q.Mark))
		// Strip HTML for readability
		text := stripHTML(q.HTML)
		if len(text) > 1000 {
			text = text[:1000] + "..."
		}
		lines = append(lines, "    "+text)
	}

	for _, d := range result.AdditionalData {
		lines = append(lines, fmt.Sprintf("\n%s: %s", d.Title, stripHTML(d.Content)))
	}

	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================================
// Convenience actions — new: Completion
// ============================================================================

func (t *MoodleTool) completion(ctx context.Context, courseID int) *ToolResult {
	uid, _, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}

	data, err := t.call(ctx, "core_completion_get_activities_completion_status", url.Values{
		"courseid": {fmt.Sprintf("%d", courseID)},
		"userid":   {fmt.Sprintf("%d", uid)},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get completion: %v", err))
	}

	var result struct {
		Statuses []struct {
			CMID          int    `json:"cmid"`
			ModName       string `json:"modname"`
			Instance      int    `json:"instance"`
			State         int    `json:"state"`
			TimeCompleted int64  `json:"timecompleted"`
			Tracking      int    `json:"tracking"`
			HasCompletion bool   `json:"hascompletion"`
		} `json:"statuses"`
	}
	json.Unmarshal(data, &result)

	completed := 0
	total := 0
	var lines []string
	for _, s := range result.Statuses {
		if !s.HasCompletion {
			continue
		}
		total++
		stateStr := "incomplete"
		switch s.State {
		case 1:
			stateStr = "complete"
			completed++
		case 2:
			stateStr = "complete (pass)"
			completed++
		case 3:
			stateStr = "complete (fail)"
			completed++
		}
		trackStr := "manual"
		if s.Tracking == 2 {
			trackStr = "auto"
		}
		timeStr := ""
		if s.TimeCompleted > 0 {
			timeStr = " at " + fmtTime(s.TimeCompleted)
		}
		lines = append(lines, fmt.Sprintf("  cmid:%d [%s] %s (%s)%s", s.CMID, s.ModName, stateStr, trackStr, timeStr))
	}

	header := fmt.Sprintf("Course %d completion: %d/%d activities complete", courseID, completed, total)
	if total > 0 {
		header += fmt.Sprintf(" (%.0f%%)", float64(completed)/float64(total)*100)
	}

	return SilentResult(header + "\n" + strings.Join(lines, "\n"))
}

// ============================================================================
// Convenience actions — new: Messages & Notifications
// ============================================================================

func (t *MoodleTool) notifications(ctx context.Context) *ToolResult {
	uid, _, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}

	data, err := t.call(ctx, "core_message_get_messages", url.Values{
		"useridto":    {fmt.Sprintf("%d", uid)},
		"useridfrom":  {"0"},
		"type":        {"notifications"},
		"read":        {"0"}, // unread only
		"newestfirst": {"1"},
		"limitnum":    {"20"},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get notifications: %v", err))
	}

	var result struct {
		Messages []struct {
			ID              int    `json:"id"`
			Subject         string `json:"subject"`
			SmallMessage    string `json:"smallmessage"`
			FullMessage     string `json:"fullmessage"`
			UserFromFullName string `json:"userfromfullname"`
			TimeCreated     int64  `json:"timecreated"`
			ContextURL      string `json:"contexturl"`
			Component       string `json:"component"`
		} `json:"messages"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Unread notifications (%d):", len(result.Messages)))
	for _, m := range result.Messages {
		subject := m.Subject
		if subject == "" {
			subject = m.SmallMessage
		}
		if len(subject) > 100 {
			subject = subject[:100] + "..."
		}
		lines = append(lines, fmt.Sprintf("  %s  [%s] %s — %s", fmtTime(m.TimeCreated), m.Component, subject, m.UserFromFullName))
		if m.ContextURL != "" {
			lines = append(lines, fmt.Sprintf("        %s", m.ContextURL))
		}
	}
	if len(result.Messages) == 0 {
		lines = append(lines, "  No unread notifications.")
	}

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) recentMessages(ctx context.Context) *ToolResult {
	uid, _, err := t.getUserID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get user: %v", err))
	}

	data, err := t.call(ctx, "core_message_get_conversations", url.Values{
		"userid":   {fmt.Sprintf("%d", uid)},
		"limitnum": {"20"},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get conversations: %v", err))
	}

	var result struct {
		Conversations []struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			Type        int    `json:"type"`
			UnreadCount int    `json:"unreadcount"`
			IsMuted     bool   `json:"ismuted"`
			Members     []struct {
				FullName string `json:"fullname"`
			} `json:"members"`
			Messages []struct {
				Text        string `json:"text"`
				TimeCreated int64  `json:"timecreated"`
				UserIDFrom  int    `json:"useridfrom"`
			} `json:"messages"`
		} `json:"conversations"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Recent conversations (%d):", len(result.Conversations)))
	for _, c := range result.Conversations {
		name := c.Name
		if name == "" && len(c.Members) > 0 {
			names := make([]string, 0, len(c.Members))
			for _, m := range c.Members {
				names = append(names, m.FullName)
			}
			name = strings.Join(names, ", ")
		}
		unread := ""
		if c.UnreadCount > 0 {
			unread = fmt.Sprintf(" (%d unread)", c.UnreadCount)
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s%s", c.ID, name, unread))
		if len(c.Messages) > 0 {
			msg := stripHTML(c.Messages[0].Text)
			if len(msg) > 80 {
				msg = msg[:80] + "..."
			}
			lines = append(lines, fmt.Sprintf("        last: %s — %s", fmtTime(c.Messages[0].TimeCreated), msg))
		}
	}
	if len(result.Conversations) == 0 {
		lines = append(lines, "  No conversations.")
	}

	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================================
// Convenience actions — new: Users & Search
// ============================================================================

func (t *MoodleTool) enrolledUsers(ctx context.Context, courseID int) *ToolResult {
	data, err := t.call(ctx, "core_enrol_get_enrolled_users", url.Values{
		"courseid": {fmt.Sprintf("%d", courseID)},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get enrolled users: %v", err))
	}

	var users []struct {
		ID         int    `json:"id"`
		FullName   string `json:"fullname"`
		Email      string `json:"email"`
		Roles      []struct {
			ShortName string `json:"shortname"`
		} `json:"roles"`
		LastAccess int64 `json:"lastcourseaccess"`
	}
	json.Unmarshal(data, &users)

	var lines []string
	lines = append(lines, fmt.Sprintf("Enrolled users in course %d (%d):", courseID, len(users)))
	for _, u := range users {
		roles := make([]string, 0, len(u.Roles))
		for _, r := range u.Roles {
			roles = append(roles, r.ShortName)
		}
		roleStr := strings.Join(roles, ",")
		if roleStr == "" {
			roleStr = "student"
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s (%s) — %s", u.ID, u.FullName, roleStr, u.Email))
	}

	return SilentResult(strings.Join(lines, "\n"))
}

func (t *MoodleTool) searchCourses(ctx context.Context, query string) *ToolResult {
	data, err := t.call(ctx, "core_course_search_courses", url.Values{
		"criterianame":  {"search"},
		"criteriavalue": {query},
		"perpage":       {"20"},
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to search courses: %v", err))
	}

	var result struct {
		Total   int `json:"total"`
		Courses []struct {
			ID        int    `json:"id"`
			ShortName string `json:"shortname"`
			FullName  string `json:"fullname"`
			Summary   string `json:"summary"`
		} `json:"courses"`
	}
	json.Unmarshal(data, &result)

	var lines []string
	lines = append(lines, fmt.Sprintf("Search results for '%s' (%d total):", query, result.Total))
	for _, c := range result.Courses {
		summary := stripHTML(c.Summary)
		if len(summary) > 80 {
			summary = summary[:80] + "..."
		}
		lines = append(lines, fmt.Sprintf("  [%d] %s — %s", c.ID, c.ShortName, c.FullName))
		if summary != "" {
			lines = append(lines, fmt.Sprintf("        %s", summary))
		}
	}
	if len(result.Courses) == 0 {
		lines = append(lines, "  No courses found.")
	}

	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================================
// File helpers
// ============================================================================

func formatFileSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func (t *MoodleTool) injectToken(fileURL string) (string, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return "", fmt.Errorf("invalid file URL: %w", err)
	}
	q := u.Query()
	q.Set("token", t.token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (t *MoodleTool) downloadDir() string {
	if t.workspace != "" {
		return filepath.Join(t.workspace, "moodle_downloads")
	}
	return filepath.Join(os.TempDir(), "moodle_downloads")
}

var textMimeTypes = map[string]bool{
	"application/json":       true,
	"application/xml":        true,
	"application/csv":        true,
	"application/javascript": true,
}

func isTextMime(mime string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	return textMimeTypes[mime]
}

// stripHTML removes HTML tags for plain-text display.
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	// Collapse whitespace
	result := strings.TrimSpace(b.String())
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result
}

// ============================================================================
// File actions
// ============================================================================

const (
	maxDownloadSize      = 100 << 20 // 100 MB
	maxInlineContentSize = 1 << 20   // 1 MB
	downloadTimeout      = 2 * time.Minute
)

func (t *MoodleTool) downloadFile(ctx context.Context, fileURL string, filename string) *ToolResult {
	authURL, err := t.injectToken(fileURL)
	if err != nil {
		return ErrorResult(err.Error())
	}

	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, "GET", authURL, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create request: %v", err))
	}

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		if t.isHTTPAuthError(resp) || strings.Contains(err.Error(), "invalidtoken") {
			if refreshErr := t.tryRefresh(); refreshErr != nil {
				return ErrorResult(fmt.Sprintf("download failed: %v (refresh also failed: %v)", err, refreshErr))
			}
			authURL, _ = t.injectToken(fileURL)
			req, _ = http.NewRequestWithContext(dlCtx, "GET", authURL, nil)
			resp, err = client.Do(req)
			if err != nil {
				return ErrorResult(fmt.Sprintf("download failed after refresh: %v", err))
			}
		} else {
			return ErrorResult(fmt.Sprintf("download failed: %v", err))
		}
	}
	defer resp.Body.Close()

	if t.isHTTPAuthError(resp) {
		if refreshErr := t.tryRefresh(); refreshErr != nil {
			return ErrorResult(fmt.Sprintf("auth error %d (refresh also failed: %v)", resp.StatusCode, refreshErr))
		}
		resp.Body.Close()
		authURL, _ = t.injectToken(fileURL)
		req, _ = http.NewRequestWithContext(dlCtx, "GET", authURL, nil)
		resp, err = client.Do(req)
		if err != nil {
			return ErrorResult(fmt.Sprintf("download failed after refresh: %v", err))
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		return ErrorResult(fmt.Sprintf("download returned HTTP %d", resp.StatusCode))
	}

	if filename == "" {
		filename = filenameFromURL(fileURL)
	}
	filename = utils.SanitizeFilename(filename)
	if filename == "" || filename == "." {
		filename = "moodle_file"
	}

	dir := t.downloadDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ErrorResult(fmt.Sprintf("create download dir: %v", err))
	}

	outPath := filepath.Join(dir, filename)

	limited := io.LimitReader(resp.Body, maxDownloadSize+1)
	f, err := os.Create(outPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create file: %v", err))
	}
	defer f.Close()

	n, err := io.Copy(f, limited)
	if err != nil {
		os.Remove(outPath)
		return ErrorResult(fmt.Sprintf("write file: %v", err))
	}
	if n > maxDownloadSize {
		os.Remove(outPath)
		return ErrorResult(fmt.Sprintf("file exceeds 100 MB limit (%s)", formatFileSize(n)))
	}

	return SilentResult(fmt.Sprintf("Downloaded to: %s (%s)\nUse read_file or exec to process this file.", outPath, formatFileSize(n)))
}

func (t *MoodleTool) getFileContent(ctx context.Context, fileURL string) *ToolResult {
	authURL, err := t.injectToken(fileURL)
	if err != nil {
		return ErrorResult(err.Error())
	}

	dlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, "GET", authURL, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create request: %v", err))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("fetch failed: %v", err))
	}
	defer resp.Body.Close()

	if t.isHTTPAuthError(resp) {
		if refreshErr := t.tryRefresh(); refreshErr != nil {
			return ErrorResult(fmt.Sprintf("auth error %d (refresh also failed: %v)", resp.StatusCode, refreshErr))
		}
		resp.Body.Close()
		authURL, _ = t.injectToken(fileURL)
		req, _ = http.NewRequestWithContext(dlCtx, "GET", authURL, nil)
		resp, err = client.Do(req)
		if err != nil {
			return ErrorResult(fmt.Sprintf("fetch failed after refresh: %v", err))
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		return ErrorResult(fmt.Sprintf("fetch returned HTTP %d", resp.StatusCode))
	}

	contentType := resp.Header.Get("Content-Type")
	mime := strings.SplitN(contentType, ";", 2)[0]
	mime = strings.TrimSpace(mime)

	if !isTextMime(mime) {
		return ErrorResult(fmt.Sprintf("Cannot read binary file inline (Content-Type: %s). Use download_file + exec instead, e.g. for PDFs: download_file then exec with pdftotext.", mime))
	}

	limited := io.LimitReader(resp.Body, maxInlineContentSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read body: %v", err))
	}
	if int64(len(body)) > maxInlineContentSize {
		return ErrorResult(fmt.Sprintf("file too large for inline reading (%s > 1 MB). Use download_file instead.", formatFileSize(int64(len(body)))))
	}

	fname := filenameFromURL(fileURL)
	return SilentResult(fmt.Sprintf("File: %s (%s, %s)\n\n%s", fname, mime, formatFileSize(int64(len(body))), string(body)))
}

func (t *MoodleTool) isHTTPAuthError(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
}

func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}
	parts := strings.Split(u.Path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return "unknown"
}

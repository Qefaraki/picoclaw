---
name: moodle
description: Full access to QM+ Moodle — courses, assignments, grades, forums, quizzes, completion, notifications, messages, files, and a generic api_call for any of the 350+ Moodle WS functions. Auto-refreshes token via M365 SSO.
---

# Moodle (QM+) Integration

You have a `moodle` tool that connects to QM+ (qmplus.qmul.ac.uk), the Moodle instance for Queen Mary University of London. This tool gives you **100% access** to the Moodle Web Services API.

## Available Actions

### Core
| Action | Description | Params |
|--------|-------------|--------|
| `courses` | List all enrolled courses with completion % | — |
| `site_info` | Show logged-in user info + Moodle version | — |
| `search_courses` | Search for courses by name | `query` |

### Academics
| Action | Description | Params |
|--------|-------------|--------|
| `assignments` | Upcoming + overdue assignments (shows assign IDs) | `days` (default 30) |
| `submission_status` | Full submission + grading status for an assignment | `assign_id` |
| `grades` | Overview grades across all courses | — |
| `grade_details` | Detailed grade items for one course | `course_id` |
| `quizzes` | List quizzes (optionally for one course) | `course_id` (optional) |
| `quiz_attempts` | List attempts for a quiz, or review one attempt | `quiz_id` or `attempt_id` |
| `completion` | Activity completion status for a course | `course_id` |
| `calendar` | Calendar events | `days` (default 30) |

### Content & Files
| Action | Description | Params |
|--------|-------------|--------|
| `course_contents` | Browse sections/resources with file details | `course_id` |
| `download_file` | Download a Moodle file to local disk (100MB max) | `file_url`, `filename` (optional) |
| `get_file_content` | Read a text file inline (1MB, text/* only) | `file_url` |

### Communication
| Action | Description | Params |
|--------|-------------|--------|
| `forums` | List forums (optionally for one course) | `course_id` (optional) |
| `forum_posts` | List discussions (by `forum_id`) or read posts (by `discussion_id`) | `forum_id` or `discussion_id` |
| `notifications` | Unread notifications (last 20) | — |
| `messages` | Recent conversations with last message | — |
| `enrolled_users` | List users in a course with roles | `course_id` |

### Generic API Passthrough (100% Coverage)
| Action | Description | Params |
|--------|-------------|--------|
| `api_call` | Call ANY Moodle WS function directly | `wsfunction`, `params` (object) |
| `list_functions` | List all available WS functions on this server | — |

## Examples

```json
{"action": "assignments", "days": 14}
{"action": "grades"}
{"action": "grade_details", "course_id": 28907}
{"action": "submission_status", "assign_id": 12345}
{"action": "forums", "course_id": 28907}
{"action": "forum_posts", "forum_id": 456}
{"action": "forum_posts", "discussion_id": 789}
{"action": "quizzes", "course_id": 28907}
{"action": "quiz_attempts", "quiz_id": 100}
{"action": "quiz_attempts", "attempt_id": 555}
{"action": "completion", "course_id": 28907}
{"action": "notifications"}
{"action": "messages"}
{"action": "enrolled_users", "course_id": 28907}
{"action": "search_courses", "query": "finance"}
{"action": "download_file", "file_url": "https://qmplus.qmul.ac.uk/pluginfile.php/.../lecture1.pdf"}
{"action": "get_file_content", "file_url": "https://qmplus.qmul.ac.uk/pluginfile.php/.../data.csv"}
```

### Generic api_call examples

```json
{"action": "api_call", "wsfunction": "core_message_send_instant_messages", "params": {"messages[0][touserid]": 123, "messages[0][text]": "Hello"}}
{"action": "api_call", "wsfunction": "mod_assign_get_submissions", "params": {"assignmentids[0]": 456}}
{"action": "api_call", "wsfunction": "core_user_get_users_by_field", "params": {"field": "email", "values[0]": "user@qmul.ac.uk"}}
{"action": "api_call", "wsfunction": "core_completion_update_activity_completion_status_manually", "params": {"cmid": 789, "completed": true}}
{"action": "api_call", "wsfunction": "mod_forum_add_discussion", "params": {"forumid": 100, "subject": "Question", "message": "<p>My question</p>"}}
```

### Discovering API functions

Use `list_functions` to see all 350+ available functions grouped by module. Then use `api_call` with any function name. Common modules:

- **core_enrol_*** — Enrollment management
- **core_course_*** — Course data, search, structure
- **core_user_*** — User profiles, lookup
- **core_message_*** — Messaging, notifications, conversations
- **core_calendar_*** — Calendar events
- **core_completion_*** — Activity/course completion
- **core_files_*** — File system browsing
- **core_group_*** — Groups management
- **core_badges_*** — User badges
- **core_notes_*** — Course notes
- **core_comment_*** — Comments
- **core_rating_*** — Ratings
- **core_search_*** — Global search
- **core_blog_*** — Blog entries
- **gradereport_*** — Grade reports (overview, user, items)
- **mod_assign_*** — Assignments (submissions, grades, extensions)
- **mod_quiz_*** — Quizzes (attempts, questions, review)
- **mod_forum_*** — Forums (discussions, posts, replies)
- **mod_feedback_*** — Feedback/surveys
- **mod_chat_*** — Real-time chat
- **mod_choice_*** — Choice activities (polls)
- **mod_data_*** — Database activities
- **mod_glossary_*** — Glossaries
- **mod_wiki_*** — Wikis
- **mod_lesson_*** — Lessons
- **mod_workshop_*** — Workshops (peer assessment)
- **mod_scorm_*** — SCORM packages
- **mod_resource_*** — File resources
- **mod_url_*** — URL resources
- **mod_page_*** — Page resources
- **mod_book_*** — Book resources
- **mod_lti_*** — External tools (LTI)

## File Access

1. `course_contents` shows file details: name, size, MIME type, and download URL for each file
2. Use `download_file` to save files locally (saves to `workspace/moodle_downloads/`). Max 100 MB, 2-minute timeout
3. Use `get_file_content` to read text files inline (text/*, JSON, XML, CSV). Max 1 MB
4. For binary files (PDF, DOCX), use `download_file` then `exec` with a converter (e.g. `pdftotext`)
5. The token is automatically appended to file URLs

## How Authentication Works

1. The tool uses a **Moodle mobile service token** stored in config
2. If the token expires, the tool **automatically refreshes** it by running a SAML2 SSO flow:
   - Starts SAML at QM+ -> redirects to Microsoft login
   - Submits M365 credentials (stored in config) -> gets SAML assertion
   - Posts assertion back to QM+ -> extracts new mobile token
3. The Python script lives at `/usr/local/lib/picoclaw/scripts/moodle_sso_refresh.py`
4. The whole process takes ~5-10 seconds and happens transparently

## Troubleshooting

### "invalidtoken" error even after refresh
The M365 password may have changed. Ask the user to update it:
- Config location on VPS: `/data/picoclaw/config.local.json` -> `tools.moodle.m365_password`
- Or set env var: `PICOCLAW_TOOLS_MOODLE_M365_PASSWORD`

### "AADSTS" errors from Microsoft
- `AADSTS50126`: Wrong password
- `AADSTS50076`: MFA required — user needs to do a manual SSO flow from laptop
- `AADSTS700016`: App not found — unlikely but would need client ID change

### SSO script not found
The refresh script should be at `/usr/local/lib/picoclaw/scripts/moodle_sso_refresh.py`. If missing, the Docker image needs rebuilding.

### Python/requests not available
The Dockerfile installs `python3` and `py3-requests` in the Alpine runtime image. If missing, `apk add python3 py3-requests` in the container.

### "accessexception" on a function
The token's service (`moodle_mobile_app`) may not have that function enabled. Use `list_functions` to see what's available on this specific server.

## Key Course IDs (2025/26)

| ID | Code | Name |
|----|------|------|
| 28907 | ECN209 | International Finance |
| 28910 | ECN228 | International Trade |
| 28912 | ECN239 | Managerial Strategy |
| 28913 | ECN242 | Corporate Finance and Valuation |
| 27773 | ECN005 | Personal and Career Development Plan 2 |

## Architecture

- **Go tool**: `pkg/tools/moodle.go` — implements `Tool` interface, handles all actions + auto-refresh + generic API passthrough
- **Python SSO**: `scripts/moodle_sso_refresh.py` — standalone script, takes username+password as args, prints token to stdout
- **Config**: `pkg/config/config.go` -> `MoodleConfig` struct with `url`, `token`, `m365_username`, `m365_password`
- **Registration**: `pkg/agent/loop.go` -> `createToolRegistry()`, enabled when `tools.moodle.enabled = true`
- **Moodle API**: Standard Web Services REST API at `{url}/webservice/rest/server.php` using `moodle_mobile_app` service

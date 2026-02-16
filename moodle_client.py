#!/usr/bin/env python3
"""Moodle Web Services client for QM+ (qmplus.qmul.ac.uk)."""
from __future__ import annotations

import getpass
import json
import os
import sys
import time
from datetime import datetime, timedelta
from pathlib import Path

import requests

MOODLE_URL = "https://qmplus.qmul.ac.uk"
TOKEN_PATH = Path.home() / ".moodle_token.json"
SERVICE = "moodle_mobile_app"


class MoodleError(Exception):
    pass


class MoodleClient:
    def __init__(self, token: str):
        self.token = token
        self._user_id = None
        self._fullname = None

    def _call(self, wsfunction: str, **params):
        params["wstoken"] = self.token
        params["wsfunction"] = wsfunction
        params["moodlewsrestformat"] = "json"
        resp = requests.post(f"{MOODLE_URL}/webservice/rest/server.php", data=params)
        resp.raise_for_status()
        data = resp.json()
        if isinstance(data, dict) and "exception" in data:
            raise MoodleError(f"{data.get('errorcode', '?')}: {data.get('message', 'Unknown error')}")
        return data

    def _call_array(self, wsfunction: str, array_params: list[tuple], **params):
        """Call with PHP-style array parameters passed as list of tuples."""
        payload = list(array_params)
        payload.append(("wstoken", self.token))
        payload.append(("wsfunction", wsfunction))
        payload.append(("moodlewsrestformat", "json"))
        for k, v in params.items():
            payload.append((k, v))
        resp = requests.post(f"{MOODLE_URL}/webservice/rest/server.php", data=payload)
        resp.raise_for_status()
        data = resp.json()
        if isinstance(data, dict) and "exception" in data:
            raise MoodleError(f"{data.get('errorcode', '?')}: {data.get('message', 'Unknown error')}")
        return data

    def get_site_info(self) -> dict:
        return self._call("core_webservice_get_site_info")

    def get_user_id(self) -> int:
        if self._user_id is None:
            info = self.get_site_info()
            self._user_id = info["userid"]
            self._fullname = info.get("fullname", "")
        return self._user_id

    def get_courses(self) -> list[dict]:
        uid = self.get_user_id()
        return self._call("core_enrol_get_users_courses", userid=uid)

    def get_assignments(self, course_ids: list[int]) -> dict:
        arr = [(f"courseids[{i}]", cid) for i, cid in enumerate(course_ids)]
        return self._call_array("mod_assign_get_assignments", arr)

    def get_course_contents(self, course_id: int) -> list[dict]:
        return self._call("core_course_get_contents", courseid=course_id)

    def get_calendar_events(self, course_ids: list[int] | None = None,
                            time_start: int | None = None,
                            time_end: int | None = None) -> dict:
        arr = []
        if course_ids:
            for i, cid in enumerate(course_ids):
                arr.append((f"events[courseids][{i}]", cid))
        opts = {}
        if time_start is not None:
            opts["options[timestart]"] = time_start
        if time_end is not None:
            opts["options[timeend]"] = time_end
        return self._call_array("core_calendar_get_calendar_events", arr, **opts)


def get_token() -> str:
    """Load cached token or authenticate."""
    if TOKEN_PATH.exists():
        data = json.loads(TOKEN_PATH.read_text())
        token = data.get("token")
        if token:
            saved = datetime.fromtimestamp(data.get("timestamp", 0))
            print(f"Using saved token (authenticated {saved:%Y-%m-%d %H:%M})")
            return token

    username = os.environ.get("MOODLE_USERNAME") or input("QM+ username: ")
    password = os.environ.get("MOODLE_PASSWORD") or getpass.getpass("QM+ password: ")

    resp = requests.post(f"{MOODLE_URL}/login/token.php", data={
        "username": username,
        "password": password,
        "service": SERVICE,
    })
    resp.raise_for_status()
    data = resp.json()

    if "token" not in data:
        error = data.get("error", "Unknown authentication error")
        print(f"Login failed: {error}")
        sys.exit(1)

    token = data["token"]
    TOKEN_PATH.write_text(json.dumps({"token": token, "timestamp": time.time()}))
    print("Authenticated successfully. Token saved.")
    return token


def fmt_ts(ts: int) -> str:
    """Format a Unix timestamp nicely."""
    if ts == 0:
        return "No date"
    return datetime.fromtimestamp(ts).strftime("%Y-%m-%d %H:%M")


def show_courses(client: MoodleClient):
    courses = client.get_courses()
    if not courses:
        print("No enrolled courses found.")
        return
    print(f"\n{'ID':<8} {'Short name':<20} Course name")
    print("-" * 70)
    for c in sorted(courses, key=lambda x: x.get("shortname", "")):
        print(f"{c['id']:<8} {c.get('shortname', ''):<20} {c.get('fullname', '')}")
    print(f"\nTotal: {len(courses)} courses")


def show_assignments(client: MoodleClient):
    courses = client.get_courses()
    if not courses:
        print("No courses found.")
        return
    cids = [c["id"] for c in courses]
    result = client.get_assignments(cids)

    now = time.time()
    cutoff = now + 30 * 86400
    upcoming = []

    for course_data in result.get("courses", []):
        cname = course_data.get("shortname", course_data.get("fullname", "?"))
        for a in course_data.get("assignments", []):
            due = a.get("duedate", 0)
            if 0 < due <= cutoff:
                upcoming.append((due, cname, a.get("name", "?")))

    if not upcoming:
        print("No assignments due in the next 30 days.")
        return

    upcoming.sort()
    print(f"\nUpcoming assignments (next 30 days):\n")
    print(f"{'Due date':<20} {'Course':<20} Assignment")
    print("-" * 70)
    for due, cname, aname in upcoming:
        status = " [OVERDUE]" if due < now else ""
        print(f"{fmt_ts(due):<20} {cname:<20} {aname}{status}")


def show_course_contents(client: MoodleClient):
    courses = client.get_courses()
    if not courses:
        print("No courses found.")
        return

    sorted_courses = sorted(courses, key=lambda x: x.get("shortname", ""))
    print("\nSelect a course:")
    for i, c in enumerate(sorted_courses, 1):
        print(f"  {i}. [{c.get('shortname', '')}] {c.get('fullname', '')}")

    try:
        choice = int(input("\nCourse number: ")) - 1
        course = sorted_courses[choice]
    except (ValueError, IndexError):
        print("Invalid choice.")
        return

    contents = client.get_course_contents(course["id"])
    print(f"\nContents of: {course.get('fullname', '')}\n")
    for section in contents:
        sname = section.get("name", "Untitled")
        print(f"== {sname} ==")
        for module in section.get("modules", []):
            mtype = module.get("modname", "")
            mname = module.get("name", "")
            url = module.get("url", "")
            print(f"  [{mtype}] {mname}")
            if url:
                print(f"         {url}")
        print()


def show_calendar(client: MoodleClient):
    now = int(time.time())
    end = now + 30 * 86400
    courses = client.get_courses()
    cids = [c["id"] for c in courses] if courses else None

    result = client.get_calendar_events(course_ids=cids, time_start=now, time_end=end)
    events = result.get("events", [])

    if not events:
        print("No calendar events in the next 30 days.")
        return

    events.sort(key=lambda e: e.get("timestart", 0))
    print(f"\nCalendar events (next 30 days):\n")
    print(f"{'Date':<20} {'Type':<12} Event")
    print("-" * 70)
    for e in events:
        ts = fmt_ts(e.get("timestart", 0))
        etype = e.get("eventtype", "?")
        name = e.get("name", "?")
        course = e.get("course", {})
        cname = course.get("shortname", "") if isinstance(course, dict) else ""
        suffix = f" ({cname})" if cname else ""
        print(f"{ts:<20} {etype:<12} {name}{suffix}")


def logout():
    if TOKEN_PATH.exists():
        TOKEN_PATH.unlink()
        print("Token deleted. You will need to log in again.")
    else:
        print("No saved token found.")


def main():
    print("=== QM+ Moodle Client ===\n")
    token = get_token()
    client = MoodleClient(token)

    # Verify token works
    try:
        uid = client.get_user_id()
        print(f"Logged in as: {client._fullname} (id={uid})\n")
    except MoodleError as e:
        print(f"Token invalid: {e}")
        TOKEN_PATH.unlink(missing_ok=True)
        print("Saved token removed. Please run again to re-authenticate.")
        sys.exit(1)

    while True:
        print("1. List enrolled courses")
        print("2. Show upcoming assignments (30 days)")
        print("3. Show course contents")
        print("4. Show calendar events (30 days)")
        print("5. Logout (delete saved token)")
        print("6. Exit")

        choice = input("\nChoice: ").strip()
        print()

        try:
            if choice == "1":
                show_courses(client)
            elif choice == "2":
                show_assignments(client)
            elif choice == "3":
                show_course_contents(client)
            elif choice == "4":
                show_calendar(client)
            elif choice == "5":
                logout()
                break
            elif choice == "6":
                break
            else:
                print("Invalid choice.")
        except MoodleError as e:
            print(f"Moodle API error: {e}")
        except requests.RequestException as e:
            print(f"Network error: {e}")

        print()


if __name__ == "__main__":
    main()

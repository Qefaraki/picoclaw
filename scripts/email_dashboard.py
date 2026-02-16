#!/usr/bin/env python3
"""M365 Email Dashboard — CLI tool for QMUL Microsoft 365 mailbox via IMAP."""

from __future__ import annotations

import argparse
import email
import email.header
import email.utils
import getpass
import imaplib
import json
import logging
import os
import shutil
import sys
import time
import traceback
from datetime import datetime, timedelta, timezone
from pathlib import Path

import re

import requests

logger = logging.getLogger("email_dashboard")

IMAP_HOST = "outlook.office365.com"
IMAP_PORT = 993
DEFAULT_CLIENT_ID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
TENANT = "organizations"
SCOPES = "https://outlook.office365.com/IMAP.AccessAsUser.All offline_access"
TOKEN_EXPIRY_MARGIN = 300  # 5 minutes


# ---------------------------------------------------------------------------
# TokenManager
# ---------------------------------------------------------------------------

class TokenManager:
    """Handles authentication credentials and OAuth2 token lifecycle."""

    def __init__(self, email_address: str):
        self.email_address = email_address
        self.client_id = os.environ.get("EMAIL_DASHBOARD_CLIENT_ID", DEFAULT_CLIENT_ID)
        self.cred_dir = Path.home() / ".email_dashboard"
        self.cred_file = self.cred_dir / "credentials.json"
        self._creds = self._load()

    # -- persistence --

    def _load(self) -> dict:
        if self.cred_file.exists():
            try:
                data = json.loads(self.cred_file.read_text())
                return data.get(self.email_address, {})
            except (json.JSONDecodeError, OSError):
                return {}
        return {}

    def _save(self):
        self.cred_dir.mkdir(parents=True, exist_ok=True)
        os.chmod(self.cred_dir, 0o700)

        all_creds = {}
        if self.cred_file.exists():
            try:
                all_creds = json.loads(self.cred_file.read_text())
            except (json.JSONDecodeError, OSError):
                pass

        all_creds[self.email_address] = self._creds
        self.cred_file.write_text(json.dumps(all_creds, indent=2))
        os.chmod(self.cred_file, 0o600)

    def clear(self):
        self._creds = {}
        if self.cred_file.exists():
            try:
                all_creds = json.loads(self.cred_file.read_text())
                all_creds.pop(self.email_address, None)
                if all_creds:
                    self.cred_file.write_text(json.dumps(all_creds, indent=2))
                else:
                    self.cred_file.unlink()
            except (json.JSONDecodeError, OSError):
                self.cred_file.unlink(missing_ok=True)

    # -- auth type detection --

    @property
    def auth_type(self) -> str | None:
        return self._creds.get("auth_type")

    @property
    def has_saved_creds(self) -> bool:
        return bool(self._creds.get("auth_type"))

    # -- basic auth --

    def get_basic_password(self) -> str | None:
        if self.auth_type == "basic":
            return self._creds.get("password")
        return None

    def save_basic(self, password: str):
        self._creds = {"auth_type": "basic", "password": password}
        self._save()

    # -- OAuth2 --

    def get_access_token(self) -> str | None:
        if self.auth_type != "oauth2":
            return None
        expires_at = self._creds.get("expires_at", 0)
        if time.time() < expires_at - TOKEN_EXPIRY_MARGIN:
            return self._creds.get("access_token")
        # try refresh
        return self._refresh_token()

    def is_token_expired(self) -> bool:
        if self.auth_type != "oauth2":
            return True
        expires_at = self._creds.get("expires_at", 0)
        return time.time() >= expires_at - TOKEN_EXPIRY_MARGIN

    def _refresh_token(self) -> str | None:
        refresh = self._creds.get("refresh_token")
        if not refresh:
            return None
        logger.info("Refreshing OAuth2 access token...")
        url = f"https://login.microsoftonline.com/{TENANT}/oauth2/v2.0/token"
        data = {
            "client_id": self.client_id,
            "grant_type": "refresh_token",
            "refresh_token": refresh,
            "scope": SCOPES,
        }
        try:
            resp = requests.post(url, data=data, timeout=30)
            resp.raise_for_status()
            token_data = resp.json()
            self._creds.update({
                "access_token": token_data["access_token"],
                "refresh_token": token_data.get("refresh_token", refresh),
                "expires_at": time.time() + token_data.get("expires_in", 3600),
            })
            self._save()
            logger.info("Token refreshed successfully.")
            return self._creds["access_token"]
        except requests.RequestException as e:
            logger.error("Token refresh failed: %s", e)
            return None

    def run_device_code_flow(self) -> str:
        """Run OAuth2 device code flow. Returns access token."""
        url = f"https://login.microsoftonline.com/{TENANT}/oauth2/v2.0/devicecode"
        data = {"client_id": self.client_id, "scope": SCOPES}
        resp = requests.post(url, data=data, timeout=30)
        resp.raise_for_status()
        dc = resp.json()

        print(f"\n  To sign in, open: {dc['verification_uri']}", flush=True)
        print(f"  Enter the code:   {dc['user_code']}\n", flush=True)

        token_url = f"https://login.microsoftonline.com/{TENANT}/oauth2/v2.0/token"
        interval = dc.get("interval", 5)
        expires_at_poll = time.time() + dc.get("expires_in", 900)

        while time.time() < expires_at_poll:
            time.sleep(interval)
            token_data = {
                "client_id": self.client_id,
                "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
                "device_code": dc["device_code"],
            }
            tr = requests.post(token_url, data=token_data, timeout=30)
            body = tr.json()

            if "access_token" in body:
                self._creds = {
                    "auth_type": "oauth2",
                    "access_token": body["access_token"],
                    "refresh_token": body.get("refresh_token"),
                    "expires_at": time.time() + body.get("expires_in", 3600),
                }
                self._save()
                print("  Authentication successful!\n", flush=True)
                return body["access_token"]

            err = body.get("error", "")
            if err == "authorization_pending":
                continue
            elif err == "slow_down":
                interval += 5
                continue
            elif "AADSTS65001" in body.get("error_description", ""):
                print(
                    "\nERROR: Admin consent is required for this application.\n"
                    "Ask your IT admin to grant consent for the app, or set\n"
                    "EMAIL_DASHBOARD_CLIENT_ID to a client ID that has been\n"
                    "pre-approved in your tenant.\n"
                )
                sys.exit(1)
            else:
                raise RuntimeError(
                    f"Device code auth failed: {err} — "
                    f"{body.get('error_description', '')}"
                )

        raise TimeoutError("Device code flow expired. Please try again.")


# ---------------------------------------------------------------------------
# EmailDashboard
# ---------------------------------------------------------------------------

class EmailDashboard:
    """IMAP operations against an M365 mailbox."""

    def __init__(self, token_mgr: TokenManager):
        self.tm = token_mgr
        self.conn: imaplib.IMAP4_SSL | None = None
        self._archive_folder: str | None = None

    # -- connection --

    def connect(self):
        """Establish IMAP connection using saved creds, basic auth prompt, or OAuth2."""
        if self.tm.has_saved_creds:
            try:
                self._connect_with_saved()
                return
            except imaplib.IMAP4.error:
                logger.warning("Saved credentials failed, re-authenticating...")
                self.tm.clear()

        # interactive chooser or auto-fallback
        print(f"Authenticating as {self.tm.email_address}")
        if sys.stdin.isatty():
            print("1) Password (basic auth)")
            print("2) Microsoft sign-in (OAuth2 — recommended)")
            choice = input("Choose [2]: ").strip() or "2"
            if choice == "1":
                self._connect_basic_interactive()
                return
        self._connect_oauth2()

    def _connect_with_saved(self):
        if self.tm.auth_type == "basic":
            pw = self.tm.get_basic_password()
            self._imap_login(self.tm.email_address, pw)
        elif self.tm.auth_type == "oauth2":
            token = self.tm.get_access_token()
            if not token:
                raise imaplib.IMAP4.error("Token expired and refresh failed")
            self._imap_oauth2(self.tm.email_address, token)

    def _connect_basic_interactive(self):
        pw = getpass.getpass("Password: ")
        try:
            self._imap_login(self.tm.email_address, pw)
        except imaplib.IMAP4.error as e:
            err = str(e)
            if "AUTHENTICATE" in err.upper() or "LOGIN" in err.upper():
                print(
                    "\nBasic auth failed — your tenant likely requires OAuth2.\n"
                    "Falling back to Microsoft sign-in...\n"
                )
                self._connect_oauth2()
                return
            raise

        save = input("Save password locally? [y/N]: ").strip().lower()
        if save == "y":
            self.tm.save_basic(pw)

    def _connect_oauth2(self):
        token = self.tm.run_device_code_flow()
        self._imap_oauth2(self.tm.email_address, token)

    def _imap_login(self, user: str, password: str):
        self.conn = imaplib.IMAP4_SSL(IMAP_HOST, IMAP_PORT)
        self.conn.login(user, password)
        self.conn.select("INBOX")
        logger.info("Connected via basic auth.")

    def _imap_oauth2(self, user: str, token: str):
        self.conn = imaplib.IMAP4_SSL(IMAP_HOST, IMAP_PORT)
        auth_string = f"user={user}\x01auth=Bearer {token}\x01\x01"

        def _cb(_response):
            return auth_string.encode()

        self.conn.authenticate("XOAUTH2", _cb)
        self.conn.select("INBOX")
        logger.info("Connected via OAuth2.")

    def _ensure_connected(self):
        """Reconnect if the IMAP session has dropped or the token expired."""
        try:
            if self.conn:
                if self.tm.auth_type == "oauth2" and self.tm.is_token_expired():
                    raise imaplib.IMAP4.error("token expired")
                self.conn.noop()
                return
        except (imaplib.IMAP4.error, OSError):
            pass
        self.conn = None
        self._connect_with_saved()

    def _with_retry(self, fn, *args, **kwargs):
        """Run fn, reconnecting once on connection drop."""
        try:
            self._ensure_connected()
            return fn(*args, **kwargs)
        except (imaplib.IMAP4.abort, OSError):
            logger.info("Connection lost, reconnecting...")
            self.conn = None
            self._ensure_connected()
            return fn(*args, **kwargs)

    def close(self):
        if self.conn:
            try:
                self.conn.close()
                self.conn.logout()
            except Exception:
                pass
            self.conn = None

    # -- header helpers --

    @staticmethod
    def _decode_header(raw: str) -> str:
        if not raw:
            return ""
        parts = email.header.decode_header(raw)
        decoded = []
        for data, charset in parts:
            if isinstance(data, bytes):
                decoded.append(data.decode(charset or "utf-8", errors="replace"))
            else:
                decoded.append(data)
        return " ".join(decoded)

    def _fetch_headers(self, uids: list[bytes], cap: int = 50) -> list[dict]:
        """Fetch envelope headers for a list of UIDs (newest first, capped)."""
        if not uids:
            return []
        uid_list = uids[-cap:]  # take most recent
        uid_str = b",".join(uid_list)
        status, data = self.conn.uid("FETCH", uid_str, "(UID BODY.PEEK[HEADER.FIELDS (FROM SUBJECT DATE)])")
        if status != "OK":
            return []

        results = []
        i = 0
        while i < len(data):
            item = data[i]
            if isinstance(item, tuple) and len(item) == 2:
                meta_line = item[0].decode(errors="replace")
                header_bytes = item[1]
                uid_match = re.search(r"UID\s+(\d+)", meta_line)
                uid_val = uid_match.group(1) if uid_match else "?"

                msg = email.message_from_bytes(header_bytes)
                results.append({
                    "uid": uid_val,
                    "from": self._decode_header(msg.get("From", "")),
                    "subject": self._decode_header(msg.get("Subject", "(no subject)")),
                    "date": msg.get("Date", ""),
                })
            i += 1

        results.reverse()  # newest first
        return results

    # -- operations --

    def list_recent(self, days: int = 7) -> list[dict]:
        def _op():
            since = (datetime.now(timezone.utc) - timedelta(days=days)).strftime("%d-%b-%Y")
            status, data = self.conn.uid("SEARCH", None, f"SINCE {since}")
            if status != "OK":
                return []
            uids = data[0].split() if data[0] else []
            return self._fetch_headers(uids)
        return self._with_retry(_op)

    def list_unread(self) -> list[dict]:
        def _op():
            status, data = self.conn.uid("SEARCH", None, "UNSEEN")
            if status != "OK":
                return []
            uids = data[0].split() if data[0] else []
            return self._fetch_headers(uids)
        return self._with_retry(_op)

    def search_emails(self, sender: str | None = None, subject: str | None = None) -> list[dict]:
        def _op():
            criteria = []
            if sender:
                criteria.append(f'FROM "{sender}"')
            if subject:
                criteria.append(f'SUBJECT "{subject}"')
            if not criteria:
                criteria.append("ALL")
            query = " ".join(criteria)
            status, data = self.conn.uid("SEARCH", None, query)
            if status != "OK":
                return []
            uids = data[0].split() if data[0] else []
            return self._fetch_headers(uids)
        return self._with_retry(_op)

    def get_email_body(self, uid: str) -> dict:
        def _op():
            status, data = self.conn.uid("FETCH", uid, "(RFC822)")
            if status != "OK" or not data or data[0] is None:
                return {"error": f"Could not fetch UID {uid}"}
            raw = data[0][1] if isinstance(data[0], tuple) else data[0]
            msg = email.message_from_bytes(raw)

            result = {
                "uid": uid,
                "from": self._decode_header(msg.get("From", "")),
                "to": self._decode_header(msg.get("To", "")),
                "subject": self._decode_header(msg.get("Subject", "")),
                "date": msg.get("Date", ""),
                "text": "",
                "html": "",
                "attachments": [],
            }

            if msg.is_multipart():
                for part in msg.walk():
                    ct = part.get_content_type()
                    disp = str(part.get("Content-Disposition", ""))
                    if "attachment" in disp:
                        result["attachments"].append({
                            "filename": part.get_filename() or "unnamed",
                            "content_type": ct,
                            "size": len(part.get_payload(decode=True) or b""),
                        })
                    elif ct == "text/plain" and not result["text"]:
                        payload = part.get_payload(decode=True)
                        if payload:
                            charset = part.get_content_charset() or "utf-8"
                            result["text"] = payload.decode(charset, errors="replace")
                    elif ct == "text/html" and not result["html"]:
                        payload = part.get_payload(decode=True)
                        if payload:
                            charset = part.get_content_charset() or "utf-8"
                            result["html"] = payload.decode(charset, errors="replace")
            else:
                payload = msg.get_payload(decode=True)
                if payload:
                    charset = msg.get_content_charset() or "utf-8"
                    text = payload.decode(charset, errors="replace")
                    if msg.get_content_type() == "text/html":
                        result["html"] = text
                    else:
                        result["text"] = text

            return result
        return self._with_retry(_op)

    def mark_as_read(self, uid: str) -> bool:
        def _op():
            status, _ = self.conn.uid("STORE", uid, "+FLAGS", "(\\Seen)")
            return status == "OK"
        return self._with_retry(_op)

    def _discover_archive_folder(self) -> str:
        """Find the archive folder name via LIST."""
        if self._archive_folder:
            return self._archive_folder
        status, folders = self.conn.list()
        if status != "OK":
            self._archive_folder = "Archive"
            return self._archive_folder

        candidates = ["Archive", "Archived", "archive"]
        folder_names = []
        for f in folders:
            if isinstance(f, bytes):
                decoded = f.decode(errors="replace")
            else:
                decoded = str(f)
            # parse folder name from IMAP LIST response: (* (\\flags) "delimiter" "name")
            parts = decoded.rsplit('"', 2)
            if len(parts) >= 2:
                name = parts[-2].strip()
                if name:
                    folder_names.append(name)
            # also try last token after delimiter
            tokens = decoded.split(" ")
            if tokens:
                last = tokens[-1].strip().strip('"')
                if last:
                    folder_names.append(last)

        for c in candidates:
            if c in folder_names:
                self._archive_folder = c
                return c

        self._archive_folder = "Archive"
        return self._archive_folder

    def move_to_archive(self, uid: str) -> bool:
        def _op():
            archive = self._discover_archive_folder()
            # try MOVE (RFC 6851)
            try:
                status, _ = self.conn.uid("MOVE", uid, archive)
                if status == "OK":
                    return True
            except imaplib.IMAP4.error:
                pass
            # fallback: COPY + DELETE + EXPUNGE
            status, _ = self.conn.uid("COPY", uid, archive)
            if status != "OK":
                return False
            self.conn.uid("STORE", uid, "+FLAGS", "(\\Deleted)")
            self.conn.expunge()
            return True
        return self._with_retry(_op)

    def list_folders(self) -> list[str]:
        def _op():
            status, folders = self.conn.list()
            if status != "OK":
                return []
            result = []
            for f in folders:
                if isinstance(f, bytes):
                    decoded = f.decode(errors="replace")
                else:
                    decoded = str(f)
                result.append(decoded)
            return result
        return self._with_retry(_op)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _format_table(emails_list: list[dict]):
    """Print emails as a formatted table with dynamic column widths."""
    if not emails_list:
        print("  No emails found.")
        return

    term_width = shutil.get_terminal_size((80, 24)).columns
    # columns: UID, Date, From, Subject
    uid_w = max(len(e["uid"]) for e in emails_list)
    uid_w = max(uid_w, 3)

    # parse and format dates
    for e in emails_list:
        try:
            parsed = email.utils.parsedate_to_datetime(e["date"])
            e["_date"] = parsed.strftime("%b %d %H:%M")
        except Exception:
            e["_date"] = e["date"][:12] if e["date"] else ""

    date_w = 12
    # remaining space for from and subject
    fixed = uid_w + date_w + 6  # separators
    remaining = term_width - fixed
    from_w = min(max(remaining // 3, 15), 30)
    subj_w = remaining - from_w - 2

    # header
    print(f"  {'UID':<{uid_w}}  {'Date':<{date_w}}  {'From':<{from_w}}  {'Subject'}")
    print(f"  {'-'*uid_w}  {'-'*date_w}  {'-'*from_w}  {'-'*max(subj_w, 7)}")

    for e in emails_list:
        uid = e["uid"][:uid_w]
        dt = e["_date"][:date_w]
        fr = e["from"][:from_w]
        subj = e["subject"]
        if subj_w > 3 and len(subj) > subj_w:
            subj = subj[: subj_w - 3] + "..."
        print(f"  {uid:<{uid_w}}  {dt:<{date_w}}  {fr:<{from_w}}  {subj}")

    print(f"\n  {len(emails_list)} email(s)")


def _format_body(body: dict):
    """Print a single email body."""
    if "error" in body:
        print(f"  Error: {body['error']}")
        return
    print(f"  From:    {body['from']}")
    print(f"  To:      {body['to']}")
    print(f"  Subject: {body['subject']}")
    print(f"  Date:    {body['date']}")
    if body["attachments"]:
        print(f"  Attachments:")
        for a in body["attachments"]:
            print(f"    - {a['filename']} ({a['content_type']}, {a['size']} bytes)")
    print(f"  {'─' * 60}")
    text = body["text"] or body["html"] or "(empty body)"
    print(text)


def main():
    parser = argparse.ArgumentParser(
        prog="email_dashboard",
        description="M365 Email Dashboard — access your QMUL mailbox via IMAP",
    )
    parser.add_argument("--email", required=True, help="Your email address (e.g. user@qmul.ac.uk)")
    parser.add_argument("--format", choices=["table", "json"], default="table", help="Output format")
    parser.add_argument("--verbose", action="store_true", help="Enable debug logging")

    sub = parser.add_subparsers(dest="command", required=True)

    p_recent = sub.add_parser("recent", help="List recent inbox emails")
    p_recent.add_argument("--days", type=int, default=7, help="Number of days (default: 7)")

    sub.add_parser("unread", help="List unread emails")

    p_search = sub.add_parser("search", help="Search emails")
    p_search.add_argument("--sender", help="Filter by sender")
    p_search.add_argument("--subject", help="Filter by subject")

    p_read = sub.add_parser("read", help="Show full email body")
    p_read.add_argument("uid", help="Email UID")

    p_mark = sub.add_parser("mark-read", help="Mark email as read")
    p_mark.add_argument("uid", help="Email UID")

    p_archive = sub.add_parser("archive", help="Move email to archive")
    p_archive.add_argument("uid", help="Email UID")

    sub.add_parser("folders", help="List mailbox folders")
    sub.add_parser("logout", help="Clear saved credentials")

    args = parser.parse_args()

    if args.verbose:
        logging.basicConfig(level=logging.DEBUG)
    else:
        logging.basicConfig(level=logging.WARNING)

    tm = TokenManager(args.email)
    output_json = args.format == "json"

    # logout doesn't need IMAP
    if args.command == "logout":
        tm.clear()
        print(f"  Credentials cleared for {args.email}")
        return

    dashboard = EmailDashboard(tm)
    try:
        dashboard.connect()

        if args.command == "recent":
            result = dashboard.list_recent(days=args.days)
            if output_json:
                print(json.dumps(result, indent=2, ensure_ascii=False))
            else:
                _format_table(result)

        elif args.command == "unread":
            result = dashboard.list_unread()
            if output_json:
                print(json.dumps(result, indent=2, ensure_ascii=False))
            else:
                _format_table(result)

        elif args.command == "search":
            result = dashboard.search_emails(sender=args.sender, subject=args.subject)
            if output_json:
                print(json.dumps(result, indent=2, ensure_ascii=False))
            else:
                _format_table(result)

        elif args.command == "read":
            body = dashboard.get_email_body(args.uid)
            if output_json:
                print(json.dumps(body, indent=2, ensure_ascii=False))
            else:
                _format_body(body)

        elif args.command == "mark-read":
            ok = dashboard.mark_as_read(args.uid)
            if output_json:
                print(json.dumps({"uid": args.uid, "marked_read": ok}))
            else:
                print(f"  UID {args.uid}: {'marked as read' if ok else 'FAILED'}")

        elif args.command == "archive":
            ok = dashboard.move_to_archive(args.uid)
            if output_json:
                print(json.dumps({"uid": args.uid, "archived": ok}))
            else:
                print(f"  UID {args.uid}: {'archived' if ok else 'FAILED'}")

        elif args.command == "folders":
            folders = dashboard.list_folders()
            if output_json:
                print(json.dumps(folders, indent=2, ensure_ascii=False))
            else:
                print("  Mailbox folders:")
                for f in folders:
                    print(f"    {f}")

    except KeyboardInterrupt:
        print("\n  Interrupted.")
    except Exception as e:
        print(f"  Error: {e}", file=sys.stderr)
        if args.verbose:
            traceback.print_exc()
        sys.exit(1)
    finally:
        dashboard.close()


if __name__ == "__main__":
    main()

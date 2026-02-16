#!/usr/bin/env python3
"""Refresh Moodle (QM+) mobile token via M365 SAML2 SSO.

Usage:
    python3 moodle_sso_refresh.py <username> <password>

Or via environment variables:
    M365_USERNAME=ml23251@qmul.ac.uk M365_PASSWORD=xxx python3 moodle_sso_refresh.py

Outputs the Moodle mobile token on stdout (and nothing else) on success.
Exit code 0 = success, 1 = failure (error on stderr).
"""
from __future__ import annotations

import json
import os
import re
import sys
import base64
from html.parser import HTMLParser
from urllib.parse import urljoin

import requests

MOODLE_URL = "https://qmplus.qmul.ac.uk"
SAML_IDP = "4eb950c6f0e1110dc8e14b5cf41532d7"
MS_BASE = "https://login.microsoftonline.com"


class FormParser(HTMLParser):
    """Extract hidden form fields and action URL from HTML."""

    def __init__(self):
        super().__init__()
        self.action = None
        self.fields = {}

    def handle_starttag(self, tag, attrs):
        d = dict(attrs)
        if tag == "form" and d.get("method", "").lower() == "post":
            self.action = d.get("action", "")
        if tag == "input" and d.get("type") == "hidden":
            name = d.get("name", "")
            val = d.get("value", "")
            if name:
                self.fields[name] = val


def err(msg: str):
    print(msg, file=sys.stderr)
    sys.exit(1)


def refresh_token(username: str, password: str) -> str:
    """Run the full SAML2 SSO flow and return a Moodle mobile token."""
    s = requests.Session()
    s.trust_env = False  # avoid macOS proxy IDNA bug
    s.headers["User-Agent"] = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)"

    # Step 1: Start SAML2 flow at QM+
    r = s.get(
        f"{MOODLE_URL}/auth/saml2/login.php?wants&idp={SAML_IDP}&passive=off",
        timeout=30,
    )
    if "login.microsoftonline.com" not in r.url:
        err(f"Expected Microsoft login, got: {r.url}")

    # Step 2: Parse Microsoft login page config
    config_match = re.search(r"\$Config\s*=\s*({.*?});", r.text, re.DOTALL)
    if not config_match:
        err("Could not parse Microsoft login page")
    config = json.loads(config_match.group(1))

    # Step 3: Get credential type + flow token
    r2 = s.post(
        f"{MS_BASE}/common/GetCredentialType?mkt=en-US",
        json={
            "username": username,
            "flowToken": config["sFT"],
            "isOtherIdpSupported": True,
            "checkPhones": False,
        },
        timeout=15,
    )
    cred = r2.json()

    # Check for federation redirect (ADFS)
    fed_url = cred.get("Credentials", {}).get("FederationRedirectUrl", "")
    if fed_url:
        err(f"Federated login not supported: {fed_url}")

    flow_token = cred.get("FlowToken", config["sFT"])

    # Step 4: Submit password
    post_url = config.get("urlPost", "")
    if not post_url.startswith("http"):
        post_url = urljoin(MS_BASE + "/", post_url)

    r3 = s.post(
        post_url,
        data={
            "login": username,
            "loginfmt": username,
            "passwd": password,
            "type": "11",
            "LoginOptions": "3",
            "canary": config.get("canary", ""),
            "ctx": config.get("sCtx", ""),
            "flowToken": flow_token,
            "NewUser": "1",
            "fspost": "0",
            "i13": "0",
            "ps": "2",
            "i19": "16369",
        },
        timeout=30,
        allow_redirects=True,
    )

    page = r3.text

    # Check for auth errors
    if "AADSTS" in page:
        aadsts = re.search(r"AADSTS\d+", page)
        desc = re.search(r'"sErrTxt"\s*:\s*"([^"]+)"', page)
        code = aadsts.group() if aadsts else "unknown"
        msg = desc.group(1) if desc else "authentication failed"
        err(f"Microsoft auth error {code}: {msg}")

    # Step 5: Handle "Stay signed in?" prompt
    if "KmsiInterrupt" in page or "StaySignedIn" in page:
        c2_match = re.search(r"\$Config\s*=\s*({.*?});", page, re.DOTALL)
        if c2_match:
            c2 = json.loads(c2_match.group(1))
            post_url2 = c2.get("urlPost", "")
            if not post_url2.startswith("http"):
                post_url2 = urljoin(MS_BASE + "/", post_url2)
            r4 = s.post(
                post_url2,
                data={
                    "LoginOptions": "1",
                    "type": "28",
                    "ctx": c2.get("sCtx", ""),
                    "flowToken": c2.get("sFT", ""),
                    "canary": c2.get("canary", ""),
                    "i19": "2048",
                },
                timeout=30,
                allow_redirects=True,
            )
            page = r4.text
            url = r4.url
        else:
            url = r3.url
    else:
        url = r3.url

    # Step 6: Handle SAML response redirect back to QM+
    if "SAMLResponse" in page:
        fp = FormParser()
        fp.feed(page)
        if fp.action and "SAMLResponse" in fp.fields:
            action = fp.action
            if not action.startswith("http"):
                action = urljoin(url, action)
            r5 = s.post(action, data=fp.fields, timeout=30)
            page = r5.text
            url = r5.url

    if "qmplus.qmul.ac.uk" not in url:
        err(f"SSO flow did not complete. Final URL: {url}")

    # Step 7: Get Moodle mobile token via launch.php
    r6 = s.post(
        f"{MOODLE_URL}/admin/tool/mobile/launch.php",
        data={
            "service": "moodle_mobile_app",
            "passport": "12345",
            "urlscheme": "moodlemobile",
        },
        timeout=15,
        allow_redirects=False,
    )

    if r6.status_code not in (301, 302, 303):
        err(f"Expected redirect from launch.php, got {r6.status_code}")

    loc = r6.headers.get("Location", "")
    token_match = re.search(r"token=([A-Za-z0-9+/=_-]+)", loc)
    if not token_match:
        err(f"No token in redirect URL")

    raw = token_match.group(1)
    # Fix base64 padding
    padding = 4 - len(raw) % 4
    if padding != 4:
        raw += "=" * padding
    decoded = base64.b64decode(raw).decode(errors="replace")

    # Format: PASSPORT:::TOKEN or PASSPORT:::TOKEN:::PRIVATETOKEN
    parts = decoded.split(":::")
    if len(parts) < 2:
        err(f"Unexpected token format: {decoded[:50]}")

    return parts[1]


def main():
    username = None
    password = None

    if len(sys.argv) >= 3:
        username = sys.argv[1]
        password = sys.argv[2]
    else:
        username = os.environ.get("M365_USERNAME", "")
        password = os.environ.get("M365_PASSWORD", "")

    if not username or not password:
        err("Usage: moodle_sso_refresh.py <username> <password>")

    token = refresh_token(username, password)
    # Only output the token on stdout
    print(token)


if __name__ == "__main__":
    main()

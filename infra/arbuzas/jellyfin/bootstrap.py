#!/usr/bin/env python3
"""Bootstrap and verify the small Tailscale-only Jellyfin instance."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import stat
import sys
from typing import Any
from urllib import error, parse, request


SERVER_VERSION = "10.11.11"
DEFAULT_ADMIN_NAME = "JellyfinAdmin"
DEFAULT_MEDIA_NAME = "Media"
LIBRARY_NAME = "Torrent Library"
LIBRARY_PATH = "/media"
TRANSCODE_PATH = "/transcodes"
DOCKER_GATEWAY = "172.29.247.1"
DAILY_SCAN_TICKS = 4 * 60 * 60 * 10_000_000
HEAVY_TASK_KEYS = {
    "DownloadLyrics",
    "DownloadSubtitles",
    "KeyframeExtraction",
    "MoveTrickplayImages",
    "RefreshChapterImages",
    "RefreshTrickplayImages",
    "TaskExtractMediaSegments",
}
AUTHORIZATION_BASE = (
    'MediaBrowser Client="Arbuzas Jellyfin Bootstrap", '
    'Device="kitty-gration", '
    'DeviceId="arbuzas-jellyfin-bootstrap", '
    'Version="1.0.0"'
)
MISSING = object()


class BootstrapError(RuntimeError):
    """A safe, operator-facing bootstrap error."""


class ApiError(BootstrapError):
    """An HTTP error without request bodies, passwords, or tokens."""

    def __init__(self, method: str, path: str, status: int) -> None:
        super().__init__(f"Jellyfin API {method} {path} returned HTTP {status}")
        self.status = status


class JellyfinApi:
    def __init__(self, base_url: str, timeout: float) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

    def call(
        self,
        method: str,
        path: str,
        *,
        token: str | None = None,
        payload: Any = MISSING,
        expect_json: bool = True,
        timeout: float | None = None,
    ) -> Any:
        headers = {
            "Accept": "application/json",
            "Authorization": AUTHORIZATION_BASE
            + (f', Token="{token}"' if token else ""),
        }
        body = None
        if payload is not MISSING:
            body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
            headers["Content-Type"] = "application/json"

        req = request.Request(
            f"{self.base_url}{path}",
            data=body,
            headers=headers,
            method=method,
        )
        try:
            with request.urlopen(req, timeout=timeout or self.timeout) as response:
                raw = response.read()
        except error.HTTPError as exc:
            # Never render a server response body: it could reflect a submitted secret.
            raise ApiError(method, path, exc.code) from None
        except error.URLError as exc:
            raise BootstrapError(
                f"could not reach Jellyfin at {self.base_url}: {exc.reason}"
            ) from None

        if not raw or not expect_json:
            return None
        try:
            return json.loads(raw)
        except json.JSONDecodeError as exc:
            raise BootstrapError(
                f"Jellyfin API {method} {path} returned invalid JSON"
            ) from exc

def read_admin_password(path: Path) -> str:
    if not path.is_absolute():
        raise BootstrapError("--admin-password-file must be an absolute path")
    try:
        metadata = path.lstat()
    except FileNotFoundError:
        raise BootstrapError(f"missing administrator password file: {path}") from None
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
        raise BootstrapError(
            f"administrator password file must be a regular non-symlink: {path}"
        )
    if metadata.st_uid != 0:
        raise BootstrapError(f"administrator password file must be owned by root: {path}")
    if metadata.st_mode & 0o077:
        raise BootstrapError(
            f"administrator password file must not be accessible by group or others: {path}"
        )
    if metadata.st_size > 4096:
        raise BootstrapError("administrator password file is unexpectedly large")

    raw = path.read_bytes().rstrip(b"\r\n")
    if b"\n" in raw or b"\r" in raw or b"\0" in raw:
        raise BootstrapError("administrator password must be a single text line")
    try:
        password = raw.decode("utf-8")
    except UnicodeDecodeError:
        raise BootstrapError("administrator password must be UTF-8 text") from None
    if not 24 <= len(password) <= 256:
        raise BootstrapError(
            "administrator password must contain between 24 and 256 characters"
        )
    return password


def public_info(api: JellyfinApi) -> dict[str, Any]:
    info = api.call("GET", "/System/Info/Public")
    if not isinstance(info, dict):
        raise BootstrapError("Jellyfin public system information is malformed")
    if info.get("Version") != SERVER_VERSION:
        raise BootstrapError(
            f"Jellyfin version is {info.get('Version')!r}, expected {SERVER_VERSION}"
        )
    return info


def complete_startup(
    api: JellyfinApi, admin_name: str, admin_password: str
) -> None:
    info = public_info(api)
    if info.get("StartupWizardCompleted") is True:
        return

    # Jellyfin creates its seed user lazily. This GET must precede Startup/User.
    api.call("GET", "/Startup/User")
    api.call(
        "POST",
        "/Startup/Configuration",
        payload={
            "ServerName": "Kidigration Media",
            "UICulture": "en-US",
            "MetadataCountryCode": "LV",
            "PreferredMetadataLanguage": "en",
        },
        expect_json=False,
    )
    api.call(
        "POST",
        "/Startup/User",
        payload={"Name": admin_name, "Password": admin_password},
        expect_json=False,
    )
    api.call(
        "POST",
        "/Startup/RemoteAccess",
        payload={"EnableRemoteAccess": True, "EnableAutomaticPortMapping": False},
        expect_json=False,
    )
    api.call("POST", "/Startup/Complete", payload={}, expect_json=False)


def authenticate(
    api: JellyfinApi, admin_name: str, admin_password: str
) -> tuple[str, dict[str, Any]]:
    result = api.call(
        "POST",
        "/Users/AuthenticateByName",
        payload={"Username": admin_name, "Pw": admin_password},
    )
    if not isinstance(result, dict):
        raise BootstrapError("Jellyfin authentication response is malformed")
    token = result.get("AccessToken")
    user = result.get("User")
    if not isinstance(token, str) or not token or not isinstance(user, dict):
        raise BootstrapError("Jellyfin authentication did not return a session")
    return token, user


def logout(api: JellyfinApi, token: str) -> None:
    try:
        api.call(
            "POST", "/Sessions/Logout", token=token, payload={}, expect_json=False
        )
    except BootstrapError:
        # Logout is cleanup only; the deploy/check result must describe the real failure.
        pass


def _users_by_name(users: Any) -> dict[str, dict[str, Any]]:
    if not isinstance(users, list) or not all(isinstance(item, dict) for item in users):
        raise BootstrapError("Jellyfin user list is malformed")
    result: dict[str, dict[str, Any]] = {}
    for user in users:
        name = user.get("Name")
        if not isinstance(name, str) or not name:
            raise BootstrapError("Jellyfin returned a user without a name")
        key = name.casefold()
        if key in result:
            raise BootstrapError(f"Jellyfin contains duplicate user name: {name}")
        result[key] = user
    return result


def _policy(user: dict[str, Any]) -> dict[str, Any]:
    policy = user.get("Policy")
    if not isinstance(policy, dict):
        raise BootstrapError(f"Jellyfin user {user.get('Name')!r} has no policy")
    return dict(policy)


def reconcile_users(
    api: JellyfinApi, token: str, admin_name: str, media_name: str
) -> None:
    users = _users_by_name(api.call("GET", "/Users", token=token))
    admin = users.get(admin_name.casefold())
    if admin is None:
        raise BootstrapError(f"missing Jellyfin administrator: {admin_name}")

    media = users.get(media_name.casefold())
    if media is None:
        api.call(
            "POST",
            "/Users/New",
            token=token,
            payload={"Name": media_name},
        )
        users = _users_by_name(api.call("GET", "/Users", token=token))
        media = users.get(media_name.casefold())
    if media is None:
        raise BootstrapError(f"could not create Jellyfin media user: {media_name}")

    admin_policy = _policy(admin)
    admin_policy.update(
        {"IsAdministrator": True, "IsHidden": True, "IsDisabled": False}
    )
    api.call(
        "POST",
        f"/Users/{admin['Id']}/Policy",
        token=token,
        payload=admin_policy,
        expect_json=False,
    )

    media_policy = _policy(media)
    media_policy.update(
        {
            "IsAdministrator": False,
            "IsHidden": False,
            "IsDisabled": False,
            "EnableCollectionManagement": False,
            "EnableSubtitleManagement": False,
            "EnableLyricManagement": False,
            "EnableRemoteControlOfOtherUsers": False,
            "EnableSharedDeviceControl": False,
            "EnableRemoteAccess": True,
            "EnableLiveTvManagement": False,
            "EnableLiveTvAccess": False,
            "EnableMediaPlayback": True,
            "EnableAudioPlaybackTranscoding": True,
            "EnableVideoPlaybackTranscoding": False,
            "EnablePlaybackRemuxing": True,
            "ForceRemoteSourceTranscoding": False,
            "EnableContentDeletion": False,
            "EnableContentDeletionFromFolders": [],
            "EnableContentDownloading": True,
            "EnableSyncTranscoding": False,
            "EnableMediaConversion": False,
            "EnablePublicSharing": False,
            "EnableAllDevices": True,
            "EnableAllFolders": True,
            "EnableAllChannels": True,
        }
    )
    api.call(
        "POST",
        f"/Users/{media['Id']}/Policy",
        token=token,
        payload=media_policy,
        expect_json=False,
    )

    if media.get("HasPassword") is True or media.get("HasConfiguredPassword") is True:
        query = parse.urlencode({"userId": media["Id"]})
        api.call(
            "POST",
            f"/Users/Password?{query}",
            token=token,
            payload={"ResetPassword": True},
            expect_json=False,
        )


def desired_library_options(current: dict[str, Any] | None = None) -> dict[str, Any]:
    options = dict(current or {})
    options.update(
        {
            "Enabled": True,
            "EnablePhotos": False,
            "EnableRealtimeMonitor": True,
            "EnableLUFSScan": False,
            "EnableChapterImageExtraction": False,
            "ExtractChapterImagesDuringLibraryScan": False,
            "EnableTrickplayImageExtraction": False,
            "ExtractTrickplayImagesDuringLibraryScan": False,
            "SaveLocalMetadata": False,
            "SaveSubtitlesWithMedia": False,
            "SaveLyricsWithMedia": False,
            "SaveTrickplayWithMedia": False,
            "SubtitleDownloadLanguages": [],
            "AutomaticallyAddToCollection": False,
            "PathInfos": [{"Path": LIBRARY_PATH}],
        }
    )
    return options


def _libraries(api: JellyfinApi, token: str) -> list[dict[str, Any]]:
    libraries = api.call("GET", "/Library/VirtualFolders", token=token)
    if not isinstance(libraries, list) or not all(
        isinstance(item, dict) for item in libraries
    ):
        raise BootstrapError("Jellyfin library list is malformed")
    return libraries


def reconcile_library(api: JellyfinApi, token: str) -> None:
    libraries = _libraries(api, token)
    matches = [item for item in libraries if item.get("Name") == LIBRARY_NAME]
    if len(matches) > 1:
        raise BootstrapError(f"Jellyfin contains duplicate library: {LIBRARY_NAME}")
    for library in libraries:
        if library.get("Name") != LIBRARY_NAME and LIBRARY_PATH in (
            library.get("Locations") or []
        ):
            raise BootstrapError(
                f"another Jellyfin library already uses {LIBRARY_PATH}: "
                f"{library.get('Name')!r}"
            )

    if not matches:
        query = parse.urlencode(
            {
                "name": LIBRARY_NAME,
                "collectionType": "mixed",
                "paths": LIBRARY_PATH,
                "refreshLibrary": "true",
            }
        )
        api.call(
            "POST",
            f"/Library/VirtualFolders?{query}",
            token=token,
            payload={"LibraryOptions": desired_library_options()},
            expect_json=False,
            timeout=max(api.timeout, 300),
        )
        matches = [
            item for item in _libraries(api, token) if item.get("Name") == LIBRARY_NAME
        ]
    if len(matches) != 1:
        raise BootstrapError(f"could not create Jellyfin library: {LIBRARY_NAME}")

    library = matches[0]
    if library.get("CollectionType") != "mixed":
        raise BootstrapError(
            f"{LIBRARY_NAME} has collection type {library.get('CollectionType')!r}, "
            "expected 'mixed'"
        )
    if library.get("Locations") != [LIBRARY_PATH]:
        raise BootstrapError(
            f"{LIBRARY_NAME} locations are {library.get('Locations')!r}, "
            f"expected [{LIBRARY_PATH!r}]"
        )
    current = library.get("LibraryOptions")
    if not isinstance(current, dict):
        raise BootstrapError(f"{LIBRARY_NAME} options are malformed")
    api.call(
        "POST",
        "/Library/VirtualFolders/LibraryOptions",
        token=token,
        payload={
            "Id": library.get("ItemId"),
            "LibraryOptions": desired_library_options(current),
        },
        expect_json=False,
    )


def reconcile_named_config(api: JellyfinApi, token: str) -> None:
    network = api.call("GET", "/System/Configuration/network", token=token)
    if not isinstance(network, dict):
        raise BootstrapError("Jellyfin network configuration is malformed")
    known_proxies = network.get("KnownProxies")
    if not isinstance(known_proxies, list):
        known_proxies = []
    safe_known_proxies = {
        value for value in known_proxies if isinstance(value, str) and value
    }
    network.update(
        {
            "AutoDiscovery": False,
            "EnableUPnP": False,
            "EnableRemoteAccess": True,
            "EnablePublishedServerUriByRequest": True,
            "KnownProxies": sorted({*safe_known_proxies, DOCKER_GATEWAY}),
        }
    )
    api.call(
        "POST",
        "/System/Configuration/network",
        token=token,
        payload=network,
        expect_json=False,
    )

    encoding = api.call("GET", "/System/Configuration/encoding", token=token)
    if not isinstance(encoding, dict):
        raise BootstrapError("Jellyfin encoding configuration is malformed")
    encoding.update(
        {
            "TranscodingTempPath": TRANSCODE_PATH,
            "HardwareAccelerationType": "none",
            "EnableHardwareEncoding": False,
        }
    )
    api.call(
        "POST",
        "/System/Configuration/encoding",
        token=token,
        payload=encoding,
        expect_json=False,
    )


def _tasks(api: JellyfinApi, token: str) -> dict[str, dict[str, Any]]:
    tasks = api.call("GET", "/ScheduledTasks", token=token)
    if not isinstance(tasks, list) or not all(isinstance(item, dict) for item in tasks):
        raise BootstrapError("Jellyfin scheduled task list is malformed")
    result: dict[str, dict[str, Any]] = {}
    for task in tasks:
        key = task.get("Key")
        if isinstance(key, str):
            result[key] = task
    return result


def reconcile_tasks(api: JellyfinApi, token: str) -> None:
    tasks = _tasks(api, token)
    refresh = tasks.get("RefreshLibrary")
    if refresh is None:
        raise BootstrapError("Jellyfin has no RefreshLibrary scheduled task")
    api.call(
        "POST",
        f"/ScheduledTasks/{refresh['Id']}/Triggers",
        token=token,
        payload=[{"Type": "DailyTrigger", "TimeOfDayTicks": DAILY_SCAN_TICKS}],
        expect_json=False,
    )
    for key in sorted(HEAVY_TASK_KEYS):
        task = tasks.get(key)
        if task is None:
            raise BootstrapError(f"Jellyfin has no expected scheduled task: {key}")
        api.call(
            "POST",
            f"/ScheduledTasks/{task['Id']}/Triggers",
            token=token,
            payload=[],
            expect_json=False,
        )


def verify_state(
    api: JellyfinApi, token: str, admin_name: str, media_name: str
) -> None:
    problems: list[str] = []
    info = public_info(api)
    if info.get("StartupWizardCompleted") is not True:
        problems.append("startup wizard is not complete")

    users = _users_by_name(api.call("GET", "/Users", token=token))
    admin = users.get(admin_name.casefold())
    media = users.get(media_name.casefold())
    if admin is None:
        problems.append(f"missing administrator {admin_name!r}")
    else:
        policy = _policy(admin)
        if policy.get("IsAdministrator") is not True:
            problems.append("administrator is not an administrator")
        if policy.get("IsHidden") is not True:
            problems.append("administrator is not hidden")
        if admin.get("HasPassword") is not True:
            problems.append("administrator has no password")
    if media is None:
        problems.append(f"missing media user {media_name!r}")
    else:
        expected_media_policy = {
            "IsAdministrator": False,
            "IsHidden": False,
            "IsDisabled": False,
            "EnableMediaPlayback": True,
            "EnableAudioPlaybackTranscoding": True,
            "EnableVideoPlaybackTranscoding": False,
            "EnablePlaybackRemuxing": True,
            "EnableContentDeletion": False,
            "EnableSyncTranscoding": False,
            "EnableMediaConversion": False,
            "EnablePublicSharing": False,
        }
        policy = _policy(media)
        for key, expected in expected_media_policy.items():
            if policy.get(key) != expected:
                problems.append(
                    f"media policy {key} is {policy.get(key)!r}, expected {expected!r}"
                )
        if media.get("HasPassword") is not False:
            problems.append("media user is not passwordless")

    public_users = api.call("GET", "/Users/Public")
    if not isinstance(public_users, list):
        problems.append("public user list is malformed")
    else:
        visible = {
            item.get("Name")
            for item in public_users
            if isinstance(item, dict) and isinstance(item.get("Name"), str)
        }
        if media_name not in visible:
            problems.append("media user is not visible on the login screen")
        if admin_name in visible:
            problems.append("administrator is visible on the login screen")

    libraries = _libraries(api, token)
    matches = [item for item in libraries if item.get("Name") == LIBRARY_NAME]
    if len(matches) != 1:
        problems.append(f"expected one {LIBRARY_NAME!r} library")
    else:
        library = matches[0]
        if library.get("CollectionType") != "mixed":
            problems.append("torrent library is not mixed content")
        if library.get("Locations") != [LIBRARY_PATH]:
            problems.append(f"torrent library is not rooted at {LIBRARY_PATH}")
        options = library.get("LibraryOptions")
        expected_options = desired_library_options()
        if not isinstance(options, dict):
            problems.append("torrent library options are malformed")
        else:
            for key, expected in expected_options.items():
                if options.get(key) != expected:
                    problems.append(
                        f"library option {key} is {options.get(key)!r}, "
                        f"expected {expected!r}"
                    )

    network = api.call("GET", "/System/Configuration/network", token=token)
    if not isinstance(network, dict):
        problems.append("network configuration is malformed")
    else:
        for key, expected in {
            "AutoDiscovery": False,
            "EnableUPnP": False,
            "EnableRemoteAccess": True,
            "EnablePublishedServerUriByRequest": True,
        }.items():
            if network.get(key) != expected:
                problems.append(
                    f"network setting {key} is {network.get(key)!r}, expected {expected!r}"
                )
        if DOCKER_GATEWAY not in (network.get("KnownProxies") or []):
            problems.append("Docker gateway is not a known Jellyfin proxy")

    encoding = api.call("GET", "/System/Configuration/encoding", token=token)
    if not isinstance(encoding, dict):
        problems.append("encoding configuration is malformed")
    else:
        if encoding.get("TranscodingTempPath") != TRANSCODE_PATH:
            problems.append(f"transcoding temporary path is not {TRANSCODE_PATH}")
        if encoding.get("HardwareAccelerationType") != "none":
            problems.append("hardware acceleration is not disabled")
        if encoding.get("EnableHardwareEncoding") is not False:
            problems.append("hardware encoding is not disabled")

    tasks = _tasks(api, token)
    refresh = tasks.get("RefreshLibrary")
    if refresh is None:
        problems.append("RefreshLibrary scheduled task is missing")
    else:
        triggers = refresh.get("Triggers")
        if not (
            isinstance(triggers, list)
            and len(triggers) == 1
            and isinstance(triggers[0], dict)
            and triggers[0].get("Type") == "DailyTrigger"
            and triggers[0].get("TimeOfDayTicks") == DAILY_SCAN_TICKS
        ):
            problems.append("library fallback scan is not scheduled once daily at 04:00")
    for key in sorted(HEAVY_TASK_KEYS):
        task = tasks.get(key)
        if task is None:
            problems.append(f"scheduled task {key} is missing")
        elif task.get("Triggers") != []:
            problems.append(f"scheduled task {key} is not disabled")

    if problems:
        rendered = "\n  - ".join(problems)
        raise BootstrapError(f"Jellyfin configuration drift:\n  - {rendered}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="action", required=True)
    for action in ("bootstrap", "check"):
        action_parser = subparsers.add_parser(action)
        action_parser.add_argument("--url", required=True)
        action_parser.add_argument(
            "--admin-password-file", type=Path, required=True
        )
        action_parser.add_argument("--admin-name", default=DEFAULT_ADMIN_NAME)
        action_parser.add_argument("--media-name", default=DEFAULT_MEDIA_NAME)
        action_parser.add_argument("--timeout", type=float, default=30.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.timeout <= 0:
        raise BootstrapError("--timeout must be positive")
    admin_password = read_admin_password(args.admin_password_file)
    api = JellyfinApi(args.url, args.timeout)

    if args.action == "check" and public_info(api).get("StartupWizardCompleted") is not True:
        raise BootstrapError("Jellyfin startup wizard is not complete")
    if args.action == "bootstrap":
        complete_startup(api, args.admin_name, admin_password)

    token = ""
    try:
        token, _ = authenticate(api, args.admin_name, admin_password)
        if args.action == "bootstrap":
            reconcile_users(api, token, args.admin_name, args.media_name)
            reconcile_named_config(api, token)
            reconcile_library(api, token)
            reconcile_tasks(api, token)
        verify_state(api, token, args.admin_name, args.media_name)
    finally:
        if token:
            logout(api, token)

    if args.action == "bootstrap":
        print("Jellyfin bootstrap complete")
    else:
        print("Jellyfin configuration verified")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BootstrapError as exc:
        print(f"Jellyfin bootstrap: {exc}", file=sys.stderr)
        raise SystemExit(1) from None

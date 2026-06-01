from __future__ import annotations

import base64
import json
import os
import socket
import threading
import urllib.parse
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import pytest

from cove_sandbox import CoveClient, CoveComputer, CoveError, CoveFleetClient
from cove_sandbox import backend as backend_module
from cove_sandbox.backend import CoveSandboxClientOptions, CoveSandboxSessionState
from cove_sandbox.computer import _resolve_keys


def test_resolve_keys() -> None:
    key, modifiers = _resolve_keys(["cmd", "shift", "a"])
    assert key == 0
    assert modifiers == (1 << 20) | (1 << 17)


def test_screenshot_reads_typed_image(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": True, "screenshot_result": {"image_data": _b64(b"png")}})
    server.start()
    client = CoveClient(socket_path=sock, token="tok")
    assert client.screenshot() == b"png"
    assert server.request["auth_token"] == "tok"
    assert server.request["screenshot"]["format"] == "png"


def test_exec_result(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(
        sock,
        {
            "success": True,
            "agent_exec_result": {
                "exit_code": 2,
                "stdout": "out",
                "stderr": "err",
                "duration_seconds": 0.25,
            },
        },
    )
    server.start()
    result = CoveClient(socket_path=sock).exec("false")
    assert result.exit_code == 2
    assert result.stdout == "out"
    assert result.stderr == "err"
    with pytest.raises(CoveError):
        result.check_returncode()


def test_computer_screenshot_is_base64(tmp_path: Path) -> None:
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": True, "screenshot_result": {"image_data": _b64(b"image")}})
    server.start()
    screenshots = tmp_path / "screens"
    events = tmp_path / "events.jsonl"
    computer = CoveComputer(CoveClient(socket_path=sock), screenshot_dir=str(screenshots), events_jsonl=str(events))
    assert base64.b64decode(computer.screenshot()) == b"image"
    assert (screenshots / "step-001.png").read_bytes() == b"image"
    row = json.loads(events.read_text().splitlines()[0])
    assert row["action"] == "screenshot"
    assert row["path"].endswith("step-001.png")
    assert computer.environment == "mac"
    assert computer.dimensions == (1024, 768)


def test_control_error(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": False, "error": "nope"})
    server.start()
    with pytest.raises(CoveError, match="nope"):
        CoveClient(socket_path=sock).control({"type": "ping"})


def test_write_file_sends_base64(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": True})
    server.start()
    CoveClient(socket_path=sock).write_file("/tmp/file", b"\x00hello")
    assert server.request["agent_write"]["data"] == _b64(b"\x00hello")
    assert server.request["agent_write"]["mode"] == 0o644


def test_backend_options_round_trip_without_agents() -> None:
    opts = CoveSandboxClientOptions(
        provider="cloud",
        parent="base",
        name="eval-001",
        manifest_bundle="manifests",
        image_platform="darwin/arm64",
        required_capabilities=("ram-overlay", "gui"),
        fleet_url="http://127.0.0.1:9758",
        workspace_root="/tmp/work",
        gui=True,
        extra_run_args=("-disposable",),
    )
    assert opts.model_dump()["type"] == "cove"
    assert opts.model_dump()["provider"] == "cloud"
    assert opts.model_dump()["parent"] == "base"
    assert opts.model_dump()["manifest_bundle"] == "manifests"
    assert opts.model_dump()["image_platform"] == "darwin/arm64"
    assert opts.model_dump()["required_capabilities"] == ("ram-overlay", "gui")
    assert opts.model_dump()["fleet_url"] == "http://127.0.0.1:9758"
    assert opts.model_dump()["extra_run_args"] == ("-disposable",)


def test_backend_state_round_trip_without_agents() -> None:
    kwargs = _state_kwargs()
    state = CoveSandboxSessionState(**kwargs)
    payload = state.model_dump()
    restored = CoveSandboxSessionState.model_validate(payload)
    assert restored.type == "cove"
    assert restored.vm == "eval-001"
    assert restored.workspace_root == "/tmp/work"
    assert restored.owned is True


def test_fleet_client_create_wait_exec_and_delete() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        client = CoveFleetClient.create_sandbox(
            fleet_url=server.url,
            api_key="secret",
            image_ref="base:v1",
            manifest_bundle="manifests",
            image_manifest_digest="sha256:1111111111111111111111111111111111111111111111111111111111111111",
            image_digest_ref="ghcr.io/me/dev-vm@sha256:1111111111111111111111111111111111111111111111111111111111111111",
            image_platform="darwin/arm64",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay", "gui", "ram-overlay", ""),
            sandbox_id="job-1",
            namespace="team-a",
        )
        sandboxes = client.list()
        assert sandboxes[0]["id"] == "job-1"
        assert sandboxes[0]["image_ref"] == "base:v1"
        assert sandboxes[0]["required_capabilities"] == ["ram-overlay"]
        listed = CoveFleetClient.list_sandboxes(fleet_url=server.url, api_key="secret", namespace="team-a")
        assert listed[0]["id"] == "job-1"
        status = client.status()
        assert status["status"] == "ready"
        wait = client.wait(timeout=2.5)
        assert wait["done"] is True
        lease = client.lease(holder="runner-42", ttl=30)
        assert lease["lease"]["holder"] == "runner-42"
        released = client.release_lease(holder="runner-42")
        assert released["sandbox"]["lease"] is None
        client.wait_ready(timeout=1)
        client.restart()
        result = client.exec(["/bin/echo", "ok"], env={"A": "1"}, timeout=2.5)
        assert result.exit_code == 7
        assert result.stdout == "out"
        assert result.stderr == "err"
        metering = client.metering()
        assert metering["summary"]["records"] == 1
        all_metering = client.list_metering(sandbox_id="job-1")
        assert all_metering["summary"]["sandbox_id"] == "job-1"
        client.delete_vm()

        paths = [req["path"] for req in server.requests]
        assert paths == [
            "/v1/sandboxes",
            "/v1/sandboxes",
            "/v1/sandboxes",
            "/v1/sandboxes/job-1",
            "/v1/sandboxes/job-1/wait",
            "/v1/sandboxes/job-1/lease",
            "/v1/sandboxes/job-1/lease",
            "/v1/sandboxes/job-1",
            "/v1/sandboxes/job-1/restart",
            "/v1/sandboxes/job-1/exec",
            "/v1/sandboxes/job-1/metering",
            "/v1/metering/sandboxes",
            "/v1/sandboxes/job-1",
        ]
        create = server.requests[0]
        assert create["authorization"] == "Bearer secret"
        assert create["body"]["image_ref"] == "base:v1"
        assert create["body"]["manifest_bundle"] == "manifests"
        assert create["body"]["image_manifest_digest"].startswith("sha256:")
        assert create["body"]["image_digest_ref"].startswith("ghcr.io/me/dev-vm@sha256:")
        assert create["body"]["image_platform"] == "darwin/arm64"
        assert create["body"]["required_labels"] == {"zone": "desk"}
        assert create["body"]["required_capabilities"] == ["ram-overlay", "gui"]
        assert create["body"]["namespace"] == "team-a"
        assert server.requests[1]["query"]["namespace"] == ["team-a"]
        assert server.requests[2]["query"]["namespace"] == ["team-a"]
        assert server.requests[4]["query"]["timeout"] == ["2.5s"]
        assert server.requests[5]["body"] == {"holder": "runner-42", "ttl": "30s"}
        assert server.requests[6]["query"]["holder"] == ["runner-42"]
        exec_req = server.requests[9]
        assert exec_req["body"]["command"] == ["/bin/echo", "ok"]
        assert exec_req["body"]["env"] == {"A": "1"}
        assert exec_req["body"]["timeout"] == "2.5s"
        assert server.requests[11]["query"]["namespace"] == ["team-a"]
        assert server.requests[11]["query"]["sandbox_id"] == ["job-1"]
    finally:
        server.stop()


def test_fleet_client_plan_sandbox() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        plan = CoveFleetClient.plan_sandbox(
            fleet_url=server.url,
            api_key="secret",
            image_ref="base:v1",
            manifest_bundle="manifests",
            image_platform="darwin/arm64",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay", "asif", ""),
            namespace="team-a",
            limit=3,
        )
        assert plan["id"] == "placement-plan-1"
        assert plan["candidates"][0]["worker_id"] == "worker-1"
        assert plan["skipped"][0]["worker_id"] == "worker-2"
        assert plan["skipped"][0]["reason"] == "capability"

        req = server.requests[-1]
        assert req["path"] == "/v1/placements/plan"
        assert req["authorization"] == "Bearer secret"
        assert req["body"]["namespace"] == "team-a"
        assert req["body"]["image_ref"] == "base:v1"
        assert req["body"]["manifest_bundle"] == "manifests"
        assert req["body"]["image_platform"] == "darwin/arm64"
        assert req["body"]["required_labels"] == {"zone": "desk"}
        assert req["body"]["required_capabilities"] == ["ram-overlay", "asif"]
        assert req["body"]["limit"] == 3

        page = CoveFleetClient.list_placement_plans(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            policy="image-affinity",
            image_ref="base:v1",
            offset=1,
            limit=2,
        )
        assert page["count"] == 1
        assert page["plans"][0]["id"] == "placement-plan-1"
        assert page["plans"][0]["skipped"][0]["missing_capabilities"] == ["asif"]
        got = CoveFleetClient.get_placement_plan(
            fleet_url=server.url,
            api_key="secret",
            plan_id="placement-plan-1",
        )
        assert got["id"] == "placement-plan-1"
        assert got["candidates"][0]["worker_id"] == "worker-1"
        assert [request["path"] for request in server.requests[-3:]] == [
            "/v1/placements/plan",
            "/v1/placements/plans",
            "/v1/placements/plans/placement-plan-1",
        ]
        query = server.requests[-2]["query"]
        assert query["namespace"] == ["team-a"]
        assert query["policy"] == ["image-affinity"]
        assert query["image_ref"] == ["base:v1"]
        assert query["offset"] == ["1"]
        assert query["limit"] == ["2"]

        with pytest.raises(ValueError, match="placement plan limit must be non-negative"):
            CoveFleetClient.list_placement_plans(fleet_url="https://fleet.example", api_key="secret", limit=-1)
        with pytest.raises(ValueError, match="placement plan offset must be non-negative"):
            CoveFleetClient.list_placement_plans(fleet_url="https://fleet.example", api_key="secret", offset=-1)
        with pytest.raises(ValueError, match="placement plan id is required"):
            CoveFleetClient.get_placement_plan(fleet_url="https://fleet.example", api_key="secret", plan_id="")
    finally:
        server.stop()


def test_fleet_client_image_preparation() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        digest = "sha256:" + "1" * 64
        digest_ref = "ghcr.io/me/base@" + digest
        result = CoveFleetClient.prepare_image(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            image_ref="base:v1",
            manifest_bundle="manifests",
            image_manifest_digest=digest,
            image_digest_ref=digest_ref,
            image_platform="darwin/arm64",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay", "asif", ""),
            force=True,
            dry_run=True,
        )
        assert result["id"] == "image-prepare-1"
        assert result["dry_run"] is True
        assert result["assignments"][0]["worker_id"] == "worker-1"
        assert result["skipped"][0] == {"worker_id": "worker-2", "reason": "present"}
        assert result["skipped"][1] == {
            "worker_id": "worker-3",
            "reason": "label",
            "missing_labels": {"zone": "desk"},
        }
        assert result["skipped"][2] == {
            "worker_id": "worker-4",
            "reason": "capability",
            "missing_capabilities": ["asif"],
        }

        page = CoveFleetClient.list_image_preparations(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            source_ref=digest_ref,
            image_ref="base:v1",
            image_manifest_digest=digest,
            offset=2,
            limit=5,
        )
        assert page["count"] == 1
        assert page["offset"] == 2
        assert page["limit"] == 5
        assert page["preparations"][0]["id"] == "image-prepare-1"

        got = CoveFleetClient.get_image_preparation(
            fleet_url=server.url,
            api_key="secret",
            preparation_id="image-prepare-1",
        )
        assert got["id"] == "image-prepare-1"
        assert got["image_digest_ref"] == digest_ref
        assert got["image_platform"] == "darwin/arm64"

        paths = [request["path"] for request in server.requests[-3:]]
        assert paths == [
            "/v1/images/prepare",
            "/v1/images/preparations",
            "/v1/images/preparations/image-prepare-1",
        ]
        prepare = server.requests[-3]["body"]
        assert prepare["namespace"] == "team-a"
        assert "source_ref" not in prepare
        assert prepare["image_ref"] == "base:v1"
        assert prepare["manifest_bundle"] == "manifests"
        assert prepare["image_manifest_digest"] == digest
        assert prepare["image_digest_ref"] == digest_ref
        assert prepare["image_platform"] == "darwin/arm64"
        assert prepare["required_labels"] == {"zone": "desk"}
        assert prepare["required_capabilities"] == ["ram-overlay", "asif"]
        assert prepare["force"] is True
        assert prepare["dry_run"] is True
        query = server.requests[-2]["query"]
        assert query["namespace"] == ["team-a"]
        assert query["source_ref"] == [digest_ref]
        assert query["image_ref"] == ["base:v1"]
        assert query["image_manifest_digest"] == [digest]
        assert query["offset"] == ["2"]
        assert query["limit"] == ["5"]
    finally:
        server.stop()


def test_fleet_client_maintenance_runs() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        gc = CoveFleetClient.push_image_gc(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay", "asif", ""),
            older_than="168h",
            apply=True,
            dry_run=True,
        )
        assert gc["id"] == "image-gc-1"
        assert gc["apply"] is True
        assert gc["dry_run"] is True
        assert gc["assignments"][0]["worker_id"] == "worker-1"
        assert gc["skipped"][0]["reason"] == "status"
        assert gc["skipped"][0]["status"] == "cordoned"
        assert gc["skipped"][1]["missing_labels"] == {"zone": "desk"}
        assert gc["skipped"][2]["missing_capabilities"] == ["asif"]
        gc_page = CoveFleetClient.list_image_gc_runs(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            older_than="168h",
            apply=True,
            offset=1,
            limit=2,
        )
        assert gc_page["runs"][0]["id"] == "image-gc-1"
        assert gc_page["count"] == 1
        got_gc = CoveFleetClient.get_image_gc_run(fleet_url=server.url, api_key="secret", run_id="image-gc-1")
        assert got_gc["older_than"] == "168h"

        policy = CoveFleetClient.push_lifecycle_policy(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            vm_name="ci-runner",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay", "asif"),
            idle_timeout="30m",
            run_budget=100,
            dry_run=True,
        )
        assert policy["id"] == "lifecycle-policy-1"
        assert policy["vm_name"] == "ci-runner"
        assert policy["dry_run"] is True
        policy_page = CoveFleetClient.list_lifecycle_policy_runs(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            vm_name="ci-runner",
            clear=False,
            offset=1,
            limit=2,
        )
        assert policy_page["runs"][0]["id"] == "lifecycle-policy-1"
        got_policy = CoveFleetClient.get_lifecycle_policy_run(
            fleet_url=server.url,
            api_key="secret",
            run_id="lifecycle-policy-1",
        )
        assert got_policy["idle_timeout"] == "30m"

        budget = CoveFleetClient.push_storage_budget(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay",),
            target="750GB",
            warn_pct=70,
            hard_pct=90,
            dry_run=True,
        )
        assert budget["id"] == "storage-budget-1"
        assert budget["target"] == "750GB"
        assert budget["dry_run"] is True
        budget_page = CoveFleetClient.list_storage_budget_runs(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            target="750GB",
            clear=False,
            offset=1,
            limit=2,
        )
        assert budget_page["runs"][0]["id"] == "storage-budget-1"
        got_budget = CoveFleetClient.get_storage_budget_run(
            fleet_url=server.url,
            api_key="secret",
            run_id="storage-budget-1",
        )
        assert got_budget["hard_pct"] == 90

        prune = CoveFleetClient.push_storage_prune(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay",),
            category="build-scratch",
            older_than="48h",
            apply=True,
            dry_run=True,
        )
        assert prune["id"] == "storage-prune-1"
        assert prune["category"] == "build-scratch"
        assert prune["dry_run"] is True
        prune_page = CoveFleetClient.list_storage_prune_runs(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            category="build-scratch",
            older_than="48h",
            apply=True,
            offset=1,
            limit=2,
        )
        assert prune_page["runs"][0]["id"] == "storage-prune-1"
        got_prune = CoveFleetClient.get_storage_prune_run(
            fleet_url=server.url,
            api_key="secret",
            run_id="storage-prune-1",
        )
        assert got_prune["older_than"] == "48h"

        runs = CoveFleetClient.list_controller_runs(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            kind="storage.prune",
            target_type="storage",
            offset=1,
            limit=2,
        )
        assert runs["runs"][0]["kind"] == "storage.prune"
        assert runs["runs"][0]["assignment_count"] == 1
        summary = CoveFleetClient.get_operations_summary(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
        )
        assert summary["namespace"] == "team-a"
        assert summary["workers"]["total"] == 3
        assert summary["workers"]["capabilities"][0]["name"] == "asif"
        assert summary["workers"]["attention"][0]["id"] == "worker-2"
        assert summary["assignments"]["active"] == 1
        assert summary["sandboxes"]["active_sandboxes"][0]["id"] == "job-1"
        assert summary["warm_pools"]["pools"][0]["name"] == "runner"
        assert summary["metering"]["records"] == 2

        paths = [request["path"] for request in server.requests[-14:]]
        assert paths == [
            "/v1/images/gc",
            "/v1/images/gc/runs",
            "/v1/images/gc/runs/image-gc-1",
            "/v1/policies/lifecycle",
            "/v1/policies/lifecycle/runs",
            "/v1/policies/lifecycle/runs/lifecycle-policy-1",
            "/v1/storage/budget",
            "/v1/storage/budget/runs",
            "/v1/storage/budget/runs/storage-budget-1",
            "/v1/storage/prune",
            "/v1/storage/prune/runs",
            "/v1/storage/prune/runs/storage-prune-1",
            "/v1/operations/runs",
            "/v1/operations/summary",
        ]
        assert server.requests[-14]["body"]["required_capabilities"] == ["ram-overlay", "asif"]
        assert server.requests[-14]["body"]["dry_run"] is True
        assert server.requests[-13]["query"]["apply"] == ["true"]
        assert server.requests[-11]["body"]["run_budget"] == 100
        assert server.requests[-11]["body"]["dry_run"] is True
        assert server.requests[-10]["query"]["clear"] == ["false"]
        assert server.requests[-8]["body"]["warn_pct"] == 70
        assert server.requests[-8]["body"]["dry_run"] is True
        assert server.requests[-7]["query"]["target"] == ["750GB"]
        assert server.requests[-5]["body"]["category"] == "build-scratch"
        assert server.requests[-5]["body"]["dry_run"] is True
        assert server.requests[-2]["query"]["kind"] == ["storage.prune"]
        assert server.requests[-1]["query"]["namespace"] == ["team-a"]
    finally:
        server.stop()


def test_fleet_client_audit() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        page = CoveFleetClient.list_audit_events(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            actor="service-account:ci",
            action="assignment.create",
            target_type="assignment",
            target_id="assignment-1",
            worker_id="worker-1",
            assignment_id="assignment-1",
            sandbox_id="job-1",
            offset=1,
            limit=2,
        )
        assert page["count"] == 1
        assert page["events"][0]["id"] == "audit-1"
        assert page["events"][0]["fields"]["reason"] == "created"
        assert page["events"][0]["hash"] == "hash-1"

        verify = CoveFleetClient.verify_audit_log(fleet_url=server.url, api_key="secret")
        assert verify["ok"] is True
        assert verify["events"] == 7
        assert verify["head_hash"] == "hash-1"
        assert verify["issues"] == []

        paths = [request["path"] for request in server.requests[-2:]]
        assert paths == ["/v1/audit", "/v1/audit/verify"]
        query = server.requests[-2]["query"]
        assert query["namespace"] == ["team-a"]
        assert query["actor"] == ["service-account:ci"]
        assert query["action"] == ["assignment.create"]
        assert query["target_type"] == ["assignment"]
        assert query["target_id"] == ["assignment-1"]
        assert query["worker_id"] == ["worker-1"]
        assert query["assignment_id"] == ["assignment-1"]
        assert query["sandbox_id"] == ["job-1"]
        assert query["offset"] == ["1"]
        assert query["limit"] == ["2"]
    finally:
        server.stop()


def test_fleet_client_service_accounts() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        page = CoveFleetClient.list_service_accounts(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
        )
        assert page["count"] == 1
        assert page["service_accounts"][0]["name"] == "ci"
        assert page["service_accounts"][0]["role"] == "operator"

        upsert = CoveFleetClient.upsert_service_account(
            fleet_url=server.url,
            api_key="secret",
            name="ci",
            namespace="team-a",
            role="operator",
            token="next-secret",
        )
        assert upsert["service_account"]["name"] == "ci"
        assert upsert["service_account"]["namespace"] == "team-a"
        assert upsert["service_account"]["role"] == "operator"

        deleted = CoveFleetClient.delete_service_account(
            fleet_url=server.url,
            api_key="secret",
            name="ci",
        )
        assert deleted["service_account"]["name"] == "ci"

        paths = [request["path"] for request in server.requests[-3:]]
        assert paths == ["/v1/service-accounts", "/v1/service-accounts", "/v1/service-accounts/ci"]
        assert server.requests[-3]["query"]["namespace"] == ["team-a"]
        assert server.requests[-2]["body"] == {
            "name": "ci",
            "namespace": "team-a",
            "role": "operator",
            "token": "next-secret",
        }
        assert all(request["authorization"] == "Bearer secret" for request in server.requests[-3:])

        with pytest.raises(ValueError, match="service account name is required"):
            CoveFleetClient.upsert_service_account(fleet_url="https://fleet.example", api_key="secret", name="", token="x")
        with pytest.raises(ValueError, match="service account token is required"):
            CoveFleetClient.upsert_service_account(fleet_url="https://fleet.example", api_key="secret", name="ci", token="")
        with pytest.raises(ValueError, match="service account name is required"):
            CoveFleetClient.delete_service_account(fleet_url="https://fleet.example", api_key="secret", name="")
    finally:
        server.stop()


def test_fleet_client_scoped_observability() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        sandboxes = CoveFleetClient.list_worker_sandboxes(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            namespace="team-a",
            status="ready",
            image_ref="base:v1",
            offset=1,
            limit=2,
        )
        assert sandboxes["count"] == 1
        assert sandboxes["sandboxes"][0]["worker_id"] == "worker-1"
        worker_events = CoveFleetClient.list_worker_events(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            actor="service-account:ci",
            action="assignment.create",
            target_type="assignment",
            target_id="assignment-1",
            sandbox_id="job-1",
            offset=1,
            limit=2,
        )
        assert worker_events["events"][0]["worker_id"] == "worker-1"
        worker_reports = CoveFleetClient.list_worker_reports(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            assignment_id="assignment-1",
            status="complete",
            offset=1,
            limit=2,
        )
        assert worker_reports["reports"][0]["report"]["stdout"] == "out"
        worker_metering = CoveFleetClient.get_worker_metering(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            namespace="team-a",
            sandbox_id="job-1",
            status="ready",
        )
        assert worker_metering["summary"]["worker_id"] == "worker-1"
        assert worker_metering["records"][0]["resources"]["vms"] == 1

        assignment_events = CoveFleetClient.list_assignment_events(
            fleet_url=server.url,
            api_key="secret",
            assignment_id="assignment-1",
            actor="service-account:ci",
            action="assignment.create",
            target_type="assignment",
            target_id="assignment-1",
            worker_id="worker-1",
            sandbox_id="job-1",
            offset=1,
            limit=2,
        )
        assert assignment_events["events"][0]["assignment_id"] == "assignment-1"
        assignment_reports = CoveFleetClient.list_assignment_reports(
            fleet_url=server.url,
            api_key="secret",
            assignment_id="assignment-1",
            worker_id="worker-1",
            status="complete",
            offset=1,
            limit=2,
        )
        assert assignment_reports["reports"][0]["assignment_id"] == "assignment-1"
        assignment_metering = CoveFleetClient.get_assignment_metering(
            fleet_url=server.url,
            api_key="secret",
            assignment_id="assignment-1",
            status="ready",
        )
        assert assignment_metering["summary"]["assignment_id"] == "assignment-1"
        assert assignment_metering["records"][0]["worker_id"] == "worker-1"

        paths = [request["path"] for request in server.requests[-7:]]
        assert paths == [
            "/v1/workers/worker-1/sandboxes",
            "/v1/workers/worker-1/events",
            "/v1/workers/worker-1/reports",
            "/v1/workers/worker-1/metering",
            "/v1/assignments/assignment-1/events",
            "/v1/assignments/assignment-1/reports",
            "/v1/assignments/assignment-1/metering",
        ]
        assert server.requests[-7]["query"]["namespace"] == ["team-a"]
        assert server.requests[-7]["query"]["image_ref"] == ["base:v1"]
        assert server.requests[-6]["query"]["actor"] == ["service-account:ci"]
        assert server.requests[-6]["query"]["sandbox_id"] == ["job-1"]
        assert server.requests[-5]["query"]["assignment_id"] == ["assignment-1"]
        assert server.requests[-4]["query"]["sandbox_id"] == ["job-1"]
        assert server.requests[-3]["query"]["worker_id"] == ["worker-1"]
        assert server.requests[-2]["query"]["status"] == ["complete"]
        assert server.requests[-1]["query"]["status"] == ["ready"]
    finally:
        server.stop()


def test_fleet_client_inventory() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        workers = CoveFleetClient.list_workers(
            fleet_url=server.url,
            api_key="secret",
            status="ready",
            host="mini-1",
            version="dev",
            image_ref="base:v1",
            source_manifest_digest="sha256:base",
            labels={"zone": "desk", "role": "runner"},
            capabilities=("ram-overlay", "asif", "ram-overlay", ""),
            offset=1,
            limit=2,
        )
        assert workers["count"] == 1
        assert workers["workers"][0]["id"] == "worker-1"
        assert workers["workers"][0]["labels"]["zone"] == "desk"
        assert workers["workers"][0]["image_details"][0]["source_manifest_digest"] == "sha256:base"
        worker = CoveFleetClient.get_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
        )
        assert worker["capacity"]["max_vms"] == 4

        assignments = CoveFleetClient.list_assignments(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            status="running",
            worker_id="worker-1",
            leased_to="worker-1",
            verb="cove",
            image_ref="base:v1",
            sandbox_id="job-1",
            warm_pool="runner",
            offset=1,
            limit=2,
        )
        assert assignments["count"] == 1
        assert assignments["assignments"][0]["id"] == "assignment-1"
        assignment = CoveFleetClient.get_assignment(
            fleet_url=server.url,
            api_key="secret",
            assignment_id="assignment-1",
        )
        assert assignment["sandbox_id"] == "job-1"
        assert assignment["warm_pool"] == "runner"

        paths = [request["path"] for request in server.requests[-4:]]
        assert paths == [
            "/v1/workers",
            "/v1/workers/worker-1",
            "/v1/assignments",
            "/v1/assignments/assignment-1",
        ]
        assert server.requests[-4]["query"]["label"] == ["role=runner", "zone=desk"]
        assert server.requests[-4]["query"]["capability"] == ["ram-overlay", "asif"]
        assert server.requests[-4]["query"]["source_manifest_digest"] == ["sha256:base"]
        assert server.requests[-2]["query"]["namespace"] == ["team-a"]
        assert server.requests[-2]["query"]["sandbox_id"] == ["job-1"]
        assert server.requests[-2]["query"]["warm_pool"] == ["runner"]
    finally:
        server.stop()


def test_fleet_client_inventory_validation() -> None:
    with pytest.raises(ValueError, match="worker limit must be non-negative"):
        CoveFleetClient.list_workers(fleet_url="https://fleet.example", api_key="secret", limit=-1)
    with pytest.raises(ValueError, match="worker id is required"):
        CoveFleetClient.get_worker(fleet_url="https://fleet.example", api_key="secret", worker_id="")
    with pytest.raises(ValueError, match="assignment offset must be non-negative"):
        CoveFleetClient.list_assignments(fleet_url="https://fleet.example", api_key="secret", offset=-1)
    with pytest.raises(ValueError, match="assignment id is required"):
        CoveFleetClient.get_assignment(fleet_url="https://fleet.example", api_key="secret", assignment_id="")


def test_fleet_client_audit_validation() -> None:
    with pytest.raises(ValueError, match="audit limit must be non-negative"):
        CoveFleetClient.list_audit_events(fleet_url="https://fleet.example", api_key="secret", limit=-1)
    with pytest.raises(ValueError, match="audit offset must be non-negative"):
        CoveFleetClient.list_audit_events(fleet_url="https://fleet.example", api_key="secret", offset=-1)


def test_fleet_client_scoped_observability_validation() -> None:
    with pytest.raises(ValueError, match="worker id is required"):
        CoveFleetClient.list_worker_events(fleet_url="https://fleet.example", api_key="secret", worker_id="")
    with pytest.raises(ValueError, match="worker reports limit must be non-negative"):
        CoveFleetClient.list_worker_reports(
            fleet_url="https://fleet.example",
            api_key="secret",
            worker_id="worker-1",
            limit=-1,
        )
    with pytest.raises(ValueError, match="worker sandboxes offset must be non-negative"):
        CoveFleetClient.list_worker_sandboxes(
            fleet_url="https://fleet.example",
            api_key="secret",
            worker_id="worker-1",
            offset=-1,
        )
    with pytest.raises(ValueError, match="worker id is required"):
        CoveFleetClient.get_worker_metering(fleet_url="https://fleet.example", api_key="secret", worker_id="")
    with pytest.raises(ValueError, match="assignment id is required"):
        CoveFleetClient.list_assignment_events(
            fleet_url="https://fleet.example",
            api_key="secret",
            assignment_id="",
        )
    with pytest.raises(ValueError, match="assignment reports limit must be non-negative"):
        CoveFleetClient.list_assignment_reports(
            fleet_url="https://fleet.example",
            api_key="secret",
            assignment_id="assignment-1",
            limit=-1,
        )
    with pytest.raises(ValueError, match="assignment id is required"):
        CoveFleetClient.get_assignment_metering(
            fleet_url="https://fleet.example",
            api_key="secret",
            assignment_id="",
        )


def test_fleet_client_assignment_controls() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        canceled = CoveFleetClient.cancel_assignment(
            fleet_url=server.url,
            api_key="secret",
            assignment_id="assignment-1",
            reason="bad input",
            force=True,
        )
        assert canceled["canceled"] is True
        assert canceled["force"] is True
        assert canceled["reason"] == "bad input"
        assert canceled["previous_status"] == "running"
        assert canceled["assignment"]["status"] == "canceled"

        retried = CoveFleetClient.retry_assignment(
            fleet_url=server.url,
            api_key="secret",
            assignment_id="assignment-1",
            reason="transient",
            worker_id="worker-2",
            replan=True,
        )
        assert retried["reason"] == "transient"
        assert retried["previous_status"] == "failed"
        assert retried["previous_worker_id"] == "worker-1"
        assert retried["replanned"] is True
        assert retried["assignment"]["status"] == "pending"
        assert retried["assignment"]["worker_id"] == "worker-2"

        paths = [request["path"] for request in server.requests[-2:]]
        assert paths == [
            "/v1/assignments/assignment-1/cancel",
            "/v1/assignments/assignment-1/retry",
        ]
        assert server.requests[-2]["body"] == {"reason": "bad input", "force": True}
        assert server.requests[-1]["body"] == {"reason": "transient", "worker_id": "worker-2", "replan": True}
    finally:
        server.stop()


def test_fleet_client_assignment_control_validation() -> None:
    with pytest.raises(ValueError, match="assignment id is required"):
        CoveFleetClient.cancel_assignment(fleet_url="https://fleet.example", api_key="secret", assignment_id="")
    with pytest.raises(ValueError, match="assignment id is required"):
        CoveFleetClient.retry_assignment(fleet_url="https://fleet.example", api_key="secret", assignment_id="")


def test_fleet_client_worker_lifecycle() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        cordoned = CoveFleetClient.cordon_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            reason="maintenance",
        )
        assert cordoned["cordoned"] is True
        assert cordoned["cordon_reason"] == "maintenance"
        uncordoned = CoveFleetClient.uncordon_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
        )
        assert uncordoned["cordoned"] is False
        quarantined = CoveFleetClient.quarantine_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            reason="bad disk",
        )
        assert quarantined["quarantined"] is True
        assert quarantined["quarantine_reason"] == "bad disk"
        unquarantined = CoveFleetClient.unquarantine_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
        )
        assert unquarantined["quarantined"] is False

        plan = CoveFleetClient.evacuate_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            reason="maintenance",
        )
        assert plan["apply"] is False
        assert plan["assignments"][0]["action"] == "requeue"
        assert plan["assignments"][0]["target_worker_id"] == "worker-2"
        assert plan["blocked"][0]["assignment_id"] == "assignment-2"
        applied = CoveFleetClient.evacuate_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            reason="maintenance",
            apply=True,
            force=True,
        )
        assert applied["applied"] is True
        assert applied["force"] is True
        assert applied["requeued"][0]["worker_id"] == "worker-2"
        assert applied["canceled"] == ["assignment-3"]

        drained = CoveFleetClient.drain_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            reason="maintenance",
        )
        assert drained["worker"]["cordoned"] is True
        assert drained["sandboxes"][0]["id"] == "job-1"
        assert drained["skipped"][0]["reason"] == "terminal"
        decommissioned = CoveFleetClient.decommission_worker(
            fleet_url=server.url,
            api_key="secret",
            worker_id="worker-1",
            reason="retire",
            force=True,
        )
        assert decommissioned["removed"] is True
        assert decommissioned["force"] is True
        assert decommissioned["canceled"] == ["assignment-1"]

        paths = [request["path"] for request in server.requests[-8:]]
        assert paths == [
            "/v1/workers/worker-1/cordon",
            "/v1/workers/worker-1/uncordon",
            "/v1/workers/worker-1/quarantine",
            "/v1/workers/worker-1/unquarantine",
            "/v1/workers/worker-1/evacuate",
            "/v1/workers/worker-1/evacuate",
            "/v1/workers/worker-1/drain",
            "/v1/workers/worker-1/decommission",
        ]
        assert server.requests[-8]["body"]["reason"] == "maintenance"
        assert server.requests[-7]["body"] == {}
        assert server.requests[-4]["body"] == {"reason": "maintenance"}
        assert server.requests[-3]["body"]["apply"] is True
        assert server.requests[-3]["body"]["force"] is True
        assert server.requests[-1]["body"]["reason"] == "retire"
        assert server.requests[-1]["body"]["force"] is True
    finally:
        server.stop()


def test_fleet_client_worker_lifecycle_validation() -> None:
    with pytest.raises(ValueError, match="worker id is required"):
        CoveFleetClient.cordon_worker(fleet_url="https://fleet.example", api_key="secret", worker_id="")
    with pytest.raises(ValueError, match="worker id is required"):
        CoveFleetClient.evacuate_worker(fleet_url="https://fleet.example", api_key="secret", worker_id="")
    with pytest.raises(ValueError, match="worker id is required"):
        CoveFleetClient.decommission_worker(fleet_url="https://fleet.example", api_key="secret", worker_id="")


def test_fleet_client_maintenance_validation() -> None:
    with pytest.raises(ValueError, match="image gc run limit must be non-negative"):
        CoveFleetClient.list_image_gc_runs(fleet_url="https://fleet.example", api_key="secret", limit=-1)
    with pytest.raises(ValueError, match="image gc run id is required"):
        CoveFleetClient.get_image_gc_run(fleet_url="https://fleet.example", api_key="secret", run_id="")
    with pytest.raises(ValueError, match="vm_name is required"):
        CoveFleetClient.push_lifecycle_policy(fleet_url="https://fleet.example", api_key="secret", vm_name="", idle_timeout="1m")
    with pytest.raises(ValueError, match="threshold is required"):
        CoveFleetClient.push_lifecycle_policy(fleet_url="https://fleet.example", api_key="secret", vm_name="vm")
    with pytest.raises(ValueError, match="target is required"):
        CoveFleetClient.push_storage_budget(fleet_url="https://fleet.example", api_key="secret")
    with pytest.raises(ValueError, match="clear cannot include thresholds"):
        CoveFleetClient.push_storage_budget(fleet_url="https://fleet.example", api_key="secret", clear=True, target="1GB")
    with pytest.raises(ValueError, match="controller run offset must be non-negative"):
        CoveFleetClient.list_controller_runs(fleet_url="https://fleet.example", api_key="secret", offset=-1)


def test_fleet_client_warm_pools() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        result = CoveFleetClient.ensure_warm_pool(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            name="runner",
            image_ref="base:v1",
            manifest_bundle="manifests",
            image_platform="darwin/arm64",
            size=2,
            policy="bin-pack",
            required_labels={"zone": "desk"},
            required_capabilities=("ram-overlay", "asif", ""),
            resources={"vms": 1, "cpus": 4},
            args=("-memory", "8G"),
        )
        assert result["pool"]["name"] == "runner"
        assert result["pool"]["ready"] == 1
        assert result["created"][0]["id"] == "warm-slot-1"

        pools = CoveFleetClient.list_warm_pools(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
        )
        assert pools[0]["name"] == "runner"

        status = CoveFleetClient.get_warm_pool(
            fleet_url=server.url,
            api_key="secret",
            name="runner",
        )
        assert status["assignments"][0]["status"] == "ready"

        claim = CoveFleetClient.claim_warm_pool(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            name="runner",
            command=("/bin/sh", "-lc", "make test"),
            env={"CI": "1"},
        )
        assert claim["pool"] == "runner"
        assert claim["slot"]["id"] == "warm-slot-1"
        assert claim["assignment"]["warm_pool_slot"] == "warm-slot-1"

        events = CoveFleetClient.warm_pool_events(
            fleet_url=server.url,
            api_key="secret",
            name="runner",
            actor="service-account:ci",
            action="warm_pool.claim",
            worker_id="worker-1",
            assignment_id="claim-1",
            offset=1,
            limit=2,
        )
        assert events["events"][0]["action"] == "warm_pool.claim"

        deleted = CoveFleetClient.delete_warm_pool(
            fleet_url=server.url,
            api_key="secret",
            name="runner",
        )
        assert deleted["pool"] == "runner"
        assert deleted["cleanup"][0]["id"] == "cleanup-1"

        paths = [request["path"] for request in server.requests[-6:]]
        assert paths == [
            "/v1/warm-pools",
            "/v1/warm-pools",
            "/v1/warm-pools/runner",
            "/v1/warm-pools/claim",
            "/v1/warm-pools/runner/events",
            "/v1/warm-pools/runner",
        ]
        ensure = server.requests[-6]["body"]
        assert ensure["namespace"] == "team-a"
        assert ensure["name"] == "runner"
        assert ensure["image_ref"] == "base:v1"
        assert ensure["manifest_bundle"] == "manifests"
        assert ensure["image_platform"] == "darwin/arm64"
        assert ensure["size"] == 2
        assert ensure["policy"] == "bin-pack"
        assert ensure["required_labels"] == {"zone": "desk"}
        assert ensure["required_capabilities"] == ["ram-overlay", "asif"]
        assert ensure["resources"] == {"vms": 1, "cpus": 4}
        assert ensure["args"] == ["-memory", "8G"]
        assert server.requests[-5]["query"]["namespace"] == ["team-a"]
        claim_body = server.requests[-3]["body"]
        assert claim_body["namespace"] == "team-a"
        assert claim_body["command"] == ["/bin/sh", "-lc", "make test"]
        assert claim_body["env"] == {"CI": "1"}
        event_query = server.requests[-2]["query"]
        assert event_query["actor"] == ["service-account:ci"]
        assert event_query["action"] == ["warm_pool.claim"]
        assert event_query["worker_id"] == ["worker-1"]
        assert event_query["assignment_id"] == ["claim-1"]
        assert event_query["offset"] == ["1"]
        assert event_query["limit"] == ["2"]
    finally:
        server.stop()


def test_fleet_client_list_filters() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        client = CoveFleetClient(sandbox_id="job-1", fleet_url=server.url, api_key="secret", namespace="team-a")
        page = client.list_page(status="ready", worker_id="worker-1", image_ref="base:v1", offset=2, limit=5)
        assert page["sandboxes"][0]["id"] == "job-1"
        assert page["count"] == 1
        assert page["offset"] == 2
        assert page["limit"] == 5
        query = server.requests[-1]["query"]
        assert query["namespace"] == ["team-a"]
        assert query["status"] == ["ready"]
        assert query["worker_id"] == ["worker-1"]
        assert query["image_ref"] == ["base:v1"]
        assert query["offset"] == ["2"]
        assert query["limit"] == ["5"]

        listed = CoveFleetClient.list_sandboxes(
            fleet_url=server.url,
            api_key="secret",
            namespace="team-a",
            status="ready",
            limit=1,
        )
        assert listed[0]["id"] == "job-1"
        assert server.requests[-1]["query"]["status"] == ["ready"]
        assert server.requests[-1]["query"]["limit"] == ["1"]

        with pytest.raises(ValueError, match="limit must be non-negative"):
            client.list(limit=-1)
        with pytest.raises(ValueError, match="offset must be non-negative"):
            client.list(offset=-1)
    finally:
        server.stop()


def test_fleet_client_events() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        client = CoveFleetClient(sandbox_id="job-1", fleet_url=server.url, api_key="secret", namespace="team-a")
        page = client.events(actor="service-account:ci", action="sandbox.exec", offset=2, limit=5)
        assert page["count"] == 1
        assert page["offset"] == 2
        assert page["limit"] == 5
        assert page["events"][0]["action"] == "sandbox.exec"
        query = server.requests[-1]["query"]
        assert query["actor"] == ["service-account:ci"]
        assert query["action"] == ["sandbox.exec"]
        assert query["offset"] == ["2"]
        assert query["limit"] == ["5"]

        with pytest.raises(ValueError, match="limit must be non-negative"):
            client.events(limit=-1)
        with pytest.raises(ValueError, match="offset must be non-negative"):
            client.events(offset=-1)
    finally:
        server.stop()


def test_fleet_client_reports() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        client = CoveFleetClient(sandbox_id="job-1", fleet_url=server.url, api_key="secret", namespace="team-a")
        page = client.reports(role="exec", status="complete", offset=2, limit=5)
        assert page["count"] == 1
        assert page["offset"] == 2
        assert page["limit"] == 5
        assert page["reports"][0]["report"]["stdout"] == "out"
        query = server.requests[-1]["query"]
        assert query["role"] == ["exec"]
        assert query["status"] == ["complete"]
        assert query["offset"] == ["2"]
        assert query["limit"] == ["5"]

        with pytest.raises(ValueError, match="limit must be non-negative"):
            client.reports(limit=-1)
        with pytest.raises(ValueError, match="offset must be non-negative"):
            client.reports(offset=-1)
    finally:
        server.stop()


def test_fleet_client_passes_lease_holder_to_mutations() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        client = CoveFleetClient(sandbox_id="job-1", fleet_url=server.url, api_key="secret", timeout=1)
        client.lease(holder="runner-42", ttl=1)
        client.restart()
        client.exec(["true"], timeout=1)
        client.screenshot(fmt="png")
        client.delete_vm()

        assert server.requests[1]["body"]["holder"] == "runner-42"
        assert server.requests[2]["body"]["holder"] == "runner-42"
        assert server.requests[3]["body"]["holder"] == "runner-42"
        assert server.requests[4]["query"]["holder"] == ["runner-42"]
    finally:
        server.stop()


def test_fleet_client_control_events() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        client = CoveFleetClient(sandbox_id="job-1", fleet_url=server.url, api_key="secret", timeout=1)
        assert client.screenshot(fmt="png") == b"png"
        client.key(36, modifiers=1 << 20)
        client.text("hi")
        client.mouse(4, 5, "click", button=1)

        control_requests = [req for req in server.requests if req["path"] == "/v1/sandboxes/job-1/control"]
        assert len(control_requests) == 5
        assert control_requests[0]["body"]["screenshot"]["format"] == "png"
        assert control_requests[1]["body"]["key"] == {
            "key_code": 36,
            "key_down": True,
            "modifiers": 1 << 20,
            "use_cg_event": True,
        }
        assert control_requests[2]["body"]["key"]["key_down"] is False
        assert control_requests[3]["body"]["text"] == {"text": "hi"}
        assert control_requests[4]["body"]["mouse"] == {
            "x": 4,
            "y": 5,
            "button": 1,
            "action": "click",
            "absolute": True,
        }
    finally:
        server.stop()


def _state_kwargs() -> dict[str, object]:
    kwargs: dict[str, object] = {
        "vm": "eval-001",
        "workspace_root": "/tmp/work",
        "owned": True,
        "delete_on_close": True,
    }
    if backend_module._AGENTS_AVAILABLE:
        from agents.sandbox.manifest import Manifest
        from agents.sandbox.snapshot import resolve_snapshot

        kwargs["manifest"] = Manifest(root="/tmp/work")
        kwargs["snapshot"] = resolve_snapshot(None, "test")
    return kwargs


def _b64(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii")


def _metering(sandbox_id: str) -> dict[str, object]:
    return {
        "records": [
            {
                "id": "metering-1",
                "sandbox_id": sandbox_id,
                "assignment_id": "assignment-1",
                "status": "ready",
                "duration_millis": 1000,
                "vm_millis": 1000,
            }
        ],
        "summary": {"sandbox_id": sandbox_id, "records": 1, "duration_millis": 1000, "vm_millis": 1000},
    }


def _worker_metering(worker_id: str) -> dict[str, object]:
    data = _metering("job-1")
    data["summary"]["worker_id"] = worker_id
    for record in data["records"]:
        record["worker_id"] = worker_id
        record["resources"] = {"vms": 1, "cpus": 4}
    return data


def _assignment_metering(assignment_id: str) -> dict[str, object]:
    data = _worker_metering("worker-1")
    data["summary"]["assignment_id"] = assignment_id
    for record in data["records"]:
        record["assignment_id"] = assignment_id
    return data


def _placement_plan() -> dict[str, object]:
    return {
        "id": "placement-plan-1",
        "namespace": "team-a",
        "policy": "image-affinity",
        "image_ref": "base:v1",
        "image_platform": "darwin/arm64",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay", "asif"],
        "limit": 3,
        "candidates": [
            {
                "rank": 1,
                "worker_id": "worker-1",
                "load": 1,
                "max_vms": 4,
                "requested_vms": 1,
                "has_image": True,
            }
        ],
        "skipped": [
            {
                "worker_id": "worker-2",
                "reason": "capability",
                "missing_capabilities": ["asif"],
            }
        ],
    }


def _image_prepare_result(*, dry_run: bool = False) -> dict[str, object]:
    digest = "sha256:" + "1" * 64
    digest_ref = "ghcr.io/me/base@" + digest
    assignment = {
        "id": "assignment-prepare-1",
        "namespace": "team-a",
        "worker_id": "worker-1",
        "image_ref": "base:v1",
        "image_manifest_digest": digest,
        "image_digest_ref": digest_ref,
        "image_platform": "darwin/arm64",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay", "asif"],
        "verb": "cove",
        "args": ["image", "pull", "-tag", "base:v1", "-force", digest_ref],
        "status": "pending",
    }
    return {
        "id": "image-prepare-1",
        "namespace": "team-a",
        "source_ref": digest_ref,
        "image_ref": "base:v1",
        "image_manifest_digest": digest,
        "image_digest_ref": digest_ref,
        "image_platform": "darwin/arm64",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay", "asif"],
        "dry_run": dry_run,
        "assignments": [assignment],
        "skipped": [
            {"worker_id": "worker-2", "reason": "present"},
            {"worker_id": "worker-3", "reason": "label", "missing_labels": {"zone": "desk"}},
            {"worker_id": "worker-4", "reason": "capability", "missing_capabilities": ["asif"]},
        ],
    }


def _image_gc_result(*, dry_run: bool = False) -> dict[str, object]:
    return {
        "id": "image-gc-1",
        "created": "2026-05-31T10:00:00Z",
        "namespace": "team-a",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay", "asif"],
        "older_than": "168h",
        "apply": True,
        "dry_run": dry_run,
        "assignments": [_maintenance_assignment("assignment-image-gc-1", ["image", "gc", "-yes", "-older-than", "168h"])],
        "skipped": [
            {"worker_id": "worker-2", "reason": "status", "status": "cordoned"},
            {"worker_id": "worker-3", "reason": "label", "missing_labels": {"zone": "desk"}},
            {"worker_id": "worker-4", "reason": "capability", "missing_capabilities": ["asif"]},
        ],
    }


def _lifecycle_policy_result(*, dry_run: bool = False) -> dict[str, object]:
    return {
        "id": "lifecycle-policy-1",
        "created": "2026-05-31T10:00:00Z",
        "namespace": "team-a",
        "vm_name": "ci-runner",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay", "asif"],
        "idle_timeout": "30m",
        "run_budget": 100,
        "dry_run": dry_run,
        "assignments": [
            _maintenance_assignment(
                "assignment-lifecycle-policy-1",
                ["policy", "ci-runner", "set", "-idle-timeout", "30m", "-run-budget", "100"],
            )
        ],
        "skipped": [
            {"worker_id": "worker-2", "reason": "status", "status": "cordoned"},
            {"worker_id": "worker-3", "reason": "label", "missing_labels": {"zone": "desk"}},
            {"worker_id": "worker-4", "reason": "capability", "missing_capabilities": ["asif"]},
        ],
    }


def _storage_budget_result(*, dry_run: bool = False) -> dict[str, object]:
    return {
        "id": "storage-budget-1",
        "created": "2026-05-31T10:00:00Z",
        "namespace": "team-a",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay"],
        "target": "750GB",
        "warn_pct": 70,
        "hard_pct": 90,
        "dry_run": dry_run,
        "assignments": [
            _maintenance_assignment(
                "assignment-storage-budget-1",
                ["storage", "budget", "set", "-target", "750GB", "-warn", "70", "-hard", "90"],
            )
        ],
        "skipped": [
            {"worker_id": "worker-2", "reason": "status", "status": "cordoned"},
            {"worker_id": "worker-3", "reason": "label", "missing_labels": {"zone": "desk"}},
            {"worker_id": "worker-4", "reason": "capability", "missing_capabilities": ["ram-overlay"]},
        ],
    }


def _storage_prune_result(*, dry_run: bool = False) -> dict[str, object]:
    return {
        "id": "storage-prune-1",
        "created": "2026-05-31T10:00:00Z",
        "namespace": "team-a",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay"],
        "category": "build-scratch",
        "older_than": "48h",
        "apply": True,
        "dry_run": dry_run,
        "assignments": [
            _maintenance_assignment(
                "assignment-storage-prune-1",
                ["storage", "prune", "build-scratch", "-apply", "-older-than", "48h"],
            )
        ],
        "skipped": [
            {"worker_id": "worker-2", "reason": "status", "status": "cordoned"},
            {"worker_id": "worker-3", "reason": "label", "missing_labels": {"zone": "desk"}},
            {"worker_id": "worker-4", "reason": "capability", "missing_capabilities": ["ram-overlay"]},
        ],
    }


def _maintenance_assignment(assignment_id: str, args: list[str]) -> dict[str, object]:
    return {
        "id": assignment_id,
        "namespace": "team-a",
        "worker_id": "worker-1",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay"],
        "verb": "cove",
        "args": args,
        "status": "pending",
    }


def _operations_summary() -> dict[str, object]:
    active = _maintenance_assignment(
        "assignment-storage-prune-1",
        ["storage", "prune", "build-scratch", "-apply", "-older-than", "48h"],
    )
    active["status"] = "running"
    return {
        "time": "2026-05-31T10:05:00Z",
        "namespace": "team-a",
        "workers": {
            "total": 3,
            "ready": 1,
            "cordoned": 1,
            "quarantined": 1,
            "by_status": {"ready": 1, "cordoned": 1, "quarantined": 1},
            "capabilities": [
                {
                    "name": "asif",
                    "total": 2,
                    "ready": 1,
                    "cordoned": 1,
                    "by_status": {"ready": 1, "cordoned": 1},
                    "workers": ["worker-1", "worker-2"],
                },
                {
                    "name": "ram-overlay",
                    "total": 2,
                    "ready": 1,
                    "quarantined": 1,
                    "by_status": {"ready": 1, "quarantined": 1},
                    "workers": ["worker-1", "worker-3"],
                },
            ],
            "attention": [
                {
                    "id": "worker-2",
                    "host": "mini-2",
                    "version": "dev",
                    "capabilities": ["asif"],
                    "status": "cordoned",
                    "cordoned": True,
                    "cordon_reason": "maintenance",
                    "last_seen": "2026-05-31T10:05:00Z",
                    "expires": "2026-05-31T10:06:00Z",
                }
            ],
        },
        "assignments": {
            "total": 2,
            "active": 1,
            "terminal": 1,
            "by_status": {"running": 1, "complete": 1},
            "active_assignments": [active],
        },
        "sandboxes": {
            "total": 1,
            "active": 1,
            "by_status": {"ready": 1},
            "active_sandboxes": [{"namespace": "team-a", "id": "job-1", "status": "ready", "worker_id": "worker-1"}],
        },
        "warm_pools": {
            "total": 1,
            "desired": 2,
            "slots": 1,
            "active": 1,
            "ready": 1,
            "by_status": {"ready": 1},
            "pools": [_warm_pool_status()],
        },
        "metering": {"namespace": "team-a", "records": 2, "duration_millis": 2000, "vm_millis": 2000},
    }


def _audit_event() -> dict[str, object]:
    return {
        "id": "audit-1",
        "time": "2026-05-31T10:00:00Z",
        "namespace": "team-a",
        "actor": "service-account:ci",
        "action": "assignment.create",
        "target_type": "assignment",
        "target_id": "assignment-1",
        "worker_id": "worker-1",
        "assignment_id": "assignment-1",
        "status": "pending",
        "fields": {"reason": "created"},
        "prev_hash": "prev-1",
        "hash": "hash-1",
    }


def _service_account() -> dict[str, object]:
    return {
        "name": "ci",
        "namespace": "team-a",
        "role": "operator",
        "created": "2026-05-31T10:00:00Z",
        "updated": "2026-05-31T10:00:00Z",
    }


def _assignment_report() -> dict[str, object]:
    return {
        "namespace": "team-a",
        "assignment_id": "assignment-1",
        "worker_id": "worker-1",
        "status": "complete",
        "created": "2026-05-31T10:04:00Z",
        "updated": "2026-05-31T10:05:00Z",
        "report": {
            "assignment_id": "assignment-1",
            "status": "complete",
            "exit_code": 7,
            "stdout": "out",
            "stderr": "err",
        },
    }


def _host_record(
    *,
    cordoned: bool = False,
    cordon_reason: str = "",
    quarantined: bool = False,
    quarantine_reason: str = "",
) -> dict[str, object]:
    status = "ready"
    if cordoned:
        status = "cordoned"
    if quarantined:
        status = "quarantined"
    return {
        "id": "worker-1",
        "host": "mini-1",
        "address": "ssh://mini-1",
        "version": "dev",
        "labels": {"zone": "desk", "role": "runner"},
        "capabilities": ["ram-overlay", "asif"],
        "image_refs": ["base:v1"],
        "image_details": [{"ref": "base:v1", "source_manifest_digest": "sha256:base"}],
        "capacity": {"vms": 1, "max_vms": 4},
        "status": status,
        "cordoned": cordoned,
        "cordon_reason": cordon_reason,
        "quarantined": quarantined,
        "quarantine_reason": quarantine_reason,
        "last_seen": "2026-05-31T10:05:00Z",
        "expires": "2026-05-31T10:06:00Z",
        "last_report": {"id": "report-1", "status": "running", "time": "2026-05-31T10:05:00Z"},
    }


def _inventory_assignment() -> dict[str, object]:
    assignment = _maintenance_assignment(
        "assignment-1",
        ["run", "-fork-from", "base:v1", "-ephemeral"],
    )
    assignment["image_ref"] = "base:v1"
    assignment["warm_pool"] = "runner"
    assignment["sandbox_id"] = "job-1"
    assignment["status"] = "running"
    assignment["leased_to"] = "worker-1"
    assignment["lease_expires"] = "2026-05-31T10:06:00Z"
    return assignment


def _assignment_cancel(body: dict[str, object]) -> dict[str, object]:
    assignment = _inventory_assignment()
    assignment["status"] = "canceled"
    assignment["leased_to"] = ""
    assignment.pop("lease_expires", None)
    return {
        "assignment": assignment,
        "reason": str(body.get("reason") or ""),
        "force": body.get("force") is True,
        "canceled": True,
        "previous_status": "running",
    }


def _assignment_retry(body: dict[str, object]) -> dict[str, object]:
    assignment = _inventory_assignment()
    assignment["status"] = "pending"
    assignment["leased_to"] = ""
    assignment.pop("lease_expires", None)
    assignment["worker_id"] = str(body.get("worker_id") or "worker-1")
    return {
        "assignment": assignment,
        "reason": str(body.get("reason") or ""),
        "previous_status": "failed",
        "previous_worker_id": "worker-1",
        "replanned": body.get("replan") is True or assignment["worker_id"] != "worker-1",
    }


def _worker_evacuation(body: dict[str, object]) -> dict[str, object]:
    apply = body.get("apply") is True
    force = body.get("force") is True
    result: dict[str, object] = {
        "worker": _host_record(),
        "reason": str(body.get("reason") or ""),
        "apply": apply,
        "force": force,
        "assignments": [
            {
                "assignment_id": "assignment-1",
                "namespace": "team-a",
                "status": "pending",
                "worker_id": "worker-1",
                "action": "requeue",
                "target_worker_id": "worker-2",
                "candidates": [{"rank": 1, "worker_id": "worker-2", "requested_vms": 1}],
            },
            {
                "assignment_id": "assignment-2",
                "namespace": "team-a",
                "status": "running",
                "worker_id": "worker-1",
                "action": "blocked",
                "reason": "active assignment",
            },
        ],
        "blocked": [
            {
                "assignment_id": "assignment-2",
                "namespace": "team-a",
                "status": "running",
                "worker_id": "worker-1",
                "action": "blocked",
                "reason": "active assignment",
            }
        ],
    }
    if apply:
        requeued = _inventory_assignment()
        requeued["worker_id"] = "worker-2"
        result["applied"] = True
        result["requeued"] = [requeued]
        result["canceled"] = ["assignment-3"]
    return result


def _worker_drain(reason: str) -> dict[str, object]:
    assignment = _inventory_assignment()
    assignment["status"] = "draining"
    return {
        "worker": _host_record(cordoned=True, cordon_reason=reason),
        "sandboxes": [
            {
                "namespace": "team-a",
                "id": "job-1",
                "vm_name": "job-1",
                "status": "draining",
                "canceled": True,
                "assignment": assignment,
            }
        ],
        "skipped": [{"sandbox_id": "job-2", "status": "complete", "reason": "terminal"}],
    }


def _worker_decommission(body: dict[str, object]) -> dict[str, object]:
    return {
        "worker": _host_record(),
        "reason": str(body.get("reason") or ""),
        "force": body.get("force") is True,
        "removed": True,
        "canceled": ["assignment-1"],
    }


def _warm_pool_status() -> dict[str, object]:
    assignment = {
        "id": "warm-slot-1",
        "namespace": "team-a",
        "worker_id": "worker-1",
        "warm_pool": "runner",
        "policy": "bin-pack",
        "image_ref": "base:v1",
        "required_capabilities": ["ram-overlay", "asif"],
        "resources": {"vms": 1, "cpus": 4},
        "verb": "cove",
        "args": ["run", "-fork-from", "base:v1"],
        "status": "ready",
    }
    return {
        "namespace": "team-a",
        "name": "runner",
        "image_ref": "base:v1",
        "image_platform": "darwin/arm64",
        "size": 2,
        "policy": "bin-pack",
        "required_labels": {"zone": "desk"},
        "required_capabilities": ["ram-overlay", "asif"],
        "resources": {"vms": 1, "cpus": 4},
        "args": ["-memory", "8G"],
        "slots": 1,
        "active": 1,
        "ready": 1,
        "by_status": {"ready": 1},
        "assignments": [assignment],
    }


def _short_socket_path() -> Path:
    return Path(f"/tmp/cove-{os.getpid()}-{uuid.uuid4().hex[:8]}.sock")


class _UnixServer:
    def __init__(self, path: Path, response: dict[str, object]) -> None:
        self.path = path
        self.response = response
        self.request: dict[str, object] = {}
        self.ready = threading.Event()
        self.thread = threading.Thread(target=self._serve, daemon=True)

    def start(self) -> None:
        try:
            self.path.unlink()
        except FileNotFoundError:
            pass
        self.thread.start()
        if self.ready.wait(timeout=1):
            return
        raise RuntimeError(f"server did not bind {self.path}")

    def _serve(self) -> None:
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
                sock.bind(str(self.path))
                sock.listen(1)
                self.ready.set()
                conn, _ = sock.accept()
                with conn:
                    line = conn.recv(65536).split(b"\n", 1)[0]
                    self.request = json.loads(line)
                    conn.sendall(json.dumps(self.response).encode() + b"\n")
        finally:
            try:
                self.path.unlink()
            except FileNotFoundError:
                pass


class _FleetHTTPServer:
    def __init__(self) -> None:
        self.requests: list[dict[str, object]] = []
        self.httpd = HTTPServer(("127.0.0.1", 0), self._handler())
        host, port = self.httpd.server_address
        self.url = f"http://{host}:{port}"
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    def start(self) -> None:
        self.thread.start()

    def stop(self) -> None:
        self.httpd.shutdown()
        self.thread.join(timeout=1)
        self.httpd.server_close()

    def _handler(self) -> type[BaseHTTPRequestHandler]:
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802
                path, query = self._path_query()
                owner.requests.append(
                    {
                        "method": "GET",
                        "path": path,
                        "query": query,
                        "authorization": self.headers.get("authorization", ""),
                        "body": {},
                    }
                )
                if path == "/v1/warm-pools":
                    self._write({"warm_pools": [_warm_pool_status()]})
                    return
                if path == "/v1/warm-pools/runner/events":
                    self._write(
                        {
                            "events": [
                                {
                                    "id": "audit-warm-1",
                                    "time": "2026-05-31T10:00:00Z",
                                    "namespace": "team-a",
                                    "actor": "service-account:ci",
                                    "action": "warm_pool.claim",
                                    "target_type": "warm_pool",
                                    "target_id": "runner",
                                    "worker_id": "worker-1",
                                    "assignment_id": "claim-1",
                                }
                            ],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/warm-pools/runner":
                    self._write(_warm_pool_status())
                    return
                if path == "/v1/images/preparations":
                    self._write(
                        {
                            "preparations": [_image_prepare_result()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/images/preparations/image-prepare-1":
                    self._write(_image_prepare_result())
                    return
                if path == "/v1/images/gc/runs":
                    self._write(
                        {
                            "runs": [_image_gc_result()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/images/gc/runs/image-gc-1":
                    self._write(_image_gc_result())
                    return
                if path == "/v1/policies/lifecycle/runs":
                    self._write(
                        {
                            "runs": [_lifecycle_policy_result()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/policies/lifecycle/runs/lifecycle-policy-1":
                    self._write(_lifecycle_policy_result())
                    return
                if path == "/v1/storage/budget/runs":
                    self._write(
                        {
                            "runs": [_storage_budget_result()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/storage/budget/runs/storage-budget-1":
                    self._write(_storage_budget_result())
                    return
                if path == "/v1/storage/prune/runs":
                    self._write(
                        {
                            "runs": [_storage_prune_result()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/storage/prune/runs/storage-prune-1":
                    self._write(_storage_prune_result())
                    return
                if path == "/v1/operations/runs":
                    self._write(
                        {
                            "runs": [
                                {
                                    "id": "storage-prune-1",
                                    "created": "2026-05-31T10:00:00Z",
                                    "namespace": "team-a",
                                    "kind": "storage.prune",
                                    "target_type": "storage",
                                    "target_id": "build-scratch",
                                    "assignment_count": 1,
                                    "skip_count": 1,
                                    "fields": {"older_than": "48h", "apply": "true"},
                                }
                            ],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/operations/summary":
                    self._write(_operations_summary())
                    return
                if path == "/v1/placements/plans":
                    self._write(
                        {
                            "plans": [_placement_plan()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/placements/plans/placement-plan-1":
                    self._write(_placement_plan())
                    return
                if path == "/v1/audit":
                    self._write(
                        {
                            "events": [_audit_event()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/audit/verify":
                    self._write({"ok": True, "events": 7, "head_hash": "hash-1"})
                    return
                if path == "/v1/service-accounts":
                    self._write({"service_accounts": [_service_account()]})
                    return
                if path == "/v1/workers":
                    self._write(
                        {
                            "workers": [_host_record()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/workers/worker-1/sandboxes":
                    self._write(
                        {
                            "sandboxes": [
                                {
                                    "namespace": "team-a",
                                    "id": "job-1",
                                    "vm_name": "cove-sandbox-job-1",
                                    "image_ref": "base:v1",
                                    "status": "ready",
                                    "worker_id": "worker-1",
                                }
                            ],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/workers/worker-1/events":
                    self._write(
                        {
                            "events": [_audit_event()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/workers/worker-1/reports":
                    self._write(
                        {
                            "reports": [_assignment_report()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/workers/worker-1/metering":
                    self._write(_worker_metering("worker-1"))
                    return
                if path == "/v1/workers/worker-1":
                    self._write(_host_record())
                    return
                if path == "/v1/assignments":
                    self._write(
                        {
                            "assignments": [_inventory_assignment()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/assignments/assignment-1":
                    self._write(_inventory_assignment())
                    return
                if path == "/v1/assignments/assignment-1/events":
                    self._write(
                        {
                            "events": [_audit_event()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/assignments/assignment-1/reports":
                    self._write(
                        {
                            "reports": [_assignment_report()],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/assignments/assignment-1/metering":
                    self._write(_assignment_metering("assignment-1"))
                    return
                if path == "/v1/sandboxes":
                    self._write(
                        {
                            "sandboxes": [
                                {
                                    "id": "job-1",
                                    "vm_name": "cove-sandbox-job-1",
                                    "image_ref": "base:v1",
                                    "required_capabilities": ["ram-overlay"],
                                    "status": "ready",
                                }
                            ],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/sandboxes/job-1":
                    self._write(
                        {
                            "id": "job-1",
                            "vm_name": "cove-sandbox-job-1",
                            "required_capabilities": ["ram-overlay"],
                            "status": "ready",
                        }
                    )
                    return
                if path == "/v1/sandboxes/job-1/metering":
                    self._write(_metering("job-1"))
                    return
                if path == "/v1/sandboxes/job-1/events":
                    self._write(
                        {
                            "events": [
                                {
                                    "id": "audit-1",
                                    "time": "2026-05-31T10:00:00Z",
                                    "namespace": "team-a",
                                    "actor": "service-account:ci",
                                    "action": "sandbox.exec",
                                    "target_type": "sandbox",
                                    "target_id": "job-1",
                                    "assignment_id": "assignment-1",
                                    "fields": {"argc": "1"},
                                }
                            ],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/sandboxes/job-1/reports":
                    self._write(
                        {
                            "reports": [
                                {
                                    "namespace": "team-a",
                                    "sandbox_id": "job-1",
                                    "assignment_id": "assignment-1",
                                    "role": "exec",
                                    "worker_id": "worker-1",
                                    "status": "complete",
                                    "report": {
                                        "assignment_id": "assignment-1",
                                        "status": "complete",
                                        "exit_code": 7,
                                        "stdout": "out",
                                        "stderr": "err",
                                    },
                                }
                            ],
                            "count": 1,
                            "offset": int(query.get("offset", ["0"])[0]),
                            "limit": int(query.get("limit", ["0"])[0]),
                        }
                    )
                    return
                if path == "/v1/metering/sandboxes":
                    self._write(_metering(query.get("sandbox_id", ["job-1"])[0]))
                    return
                self.send_error(404)

            def do_POST(self) -> None:  # noqa: N802
                path, query = self._path_query()
                body = self._read_json()
                owner.requests.append(
                    {
                        "method": "POST",
                        "path": path,
                        "query": query,
                        "authorization": self.headers.get("authorization", ""),
                        "body": body,
                    }
                )
                if path == "/v1/sandboxes":
                    self._write({"id": "job-1", "vm_name": "cove-sandbox-job-1", "status": "pending"})
                    return
                if path == "/v1/warm-pools":
                    status = _warm_pool_status()
                    self._write({"pool": status, "created": [status["assignments"][0]]})
                    return
                if path == "/v1/warm-pools/claim":
                    status = _warm_pool_status()
                    self._write(
                        {
                            "namespace": "team-a",
                            "pool": "runner",
                            "vm_name": "cove-warm-runner",
                            "slot": status["assignments"][0],
                            "assignment": {
                                "id": "claim-1",
                                "namespace": "team-a",
                                "worker_id": "worker-1",
                                "warm_pool_slot": "warm-slot-1",
                                "verb": "cove",
                                "args": ["shell"],
                                "status": "pending",
                            },
                        }
                    )
                    return
                if path == "/v1/images/prepare":
                    self._write(_image_prepare_result(dry_run=body.get("dry_run") is True))
                    return
                if path == "/v1/images/gc":
                    self._write(_image_gc_result(dry_run=body.get("dry_run") is True))
                    return
                if path == "/v1/policies/lifecycle":
                    self._write(_lifecycle_policy_result(dry_run=body.get("dry_run") is True))
                    return
                if path == "/v1/storage/budget":
                    self._write(_storage_budget_result(dry_run=body.get("dry_run") is True))
                    return
                if path == "/v1/storage/prune":
                    self._write(_storage_prune_result(dry_run=body.get("dry_run") is True))
                    return
                if path == "/v1/assignments/assignment-1/cancel":
                    self._write(_assignment_cancel(body))
                    return
                if path == "/v1/assignments/assignment-1/retry":
                    self._write(_assignment_retry(body))
                    return
                if path == "/v1/service-accounts":
                    self._write(
                        {
                            "service_account": {
                                "name": str(body.get("name") or ""),
                                "namespace": str(body.get("namespace") or ""),
                                "role": str(body.get("role") or ""),
                                "created": "2026-05-31T10:00:00Z",
                                "updated": "2026-05-31T10:00:00Z",
                            }
                        }
                    )
                    return
                if path == "/v1/workers/worker-1/cordon":
                    self._write(_host_record(cordoned=True, cordon_reason=str(body.get("reason") or "")))
                    return
                if path == "/v1/workers/worker-1/uncordon":
                    self._write(_host_record())
                    return
                if path == "/v1/workers/worker-1/quarantine":
                    self._write(_host_record(quarantined=True, quarantine_reason=str(body.get("reason") or "")))
                    return
                if path == "/v1/workers/worker-1/unquarantine":
                    self._write(_host_record())
                    return
                if path == "/v1/workers/worker-1/evacuate":
                    self._write(_worker_evacuation(body))
                    return
                if path == "/v1/workers/worker-1/drain":
                    self._write(_worker_drain(str(body.get("reason") or "")))
                    return
                if path == "/v1/workers/worker-1/decommission":
                    self._write(_worker_decommission(body))
                    return
                if path == "/v1/placements/plan":
                    self._write(_placement_plan())
                    return
                if path == "/v1/sandboxes/job-1/wait":
                    self._write(
                        {
                            "done": True,
                            "sandbox": {"id": "job-1", "vm_name": "cove-sandbox-job-1", "status": "ready"},
                        }
                    )
                    return
                if path == "/v1/sandboxes/job-1/lease":
                    self._write(
                        {
                            "sandbox": {
                                "id": "job-1",
                                "vm_name": "cove-sandbox-job-1",
                                "status": "ready",
                                "lease": {"holder": "runner-42", "expires": "2026-05-31T10:00:30Z"},
                            },
                            "lease": {"holder": "runner-42", "expires": "2026-05-31T10:00:30Z"},
                        }
                    )
                    return
                if path == "/v1/sandboxes/job-1/restart":
                    self._write({"id": "job-1", "vm_name": "cove-sandbox-job-1", "status": "restarting"})
                    return
                if path == "/v1/sandboxes/job-1/exec":
                    self._write({"done": True, "exit_code": 7, "stdout": "out", "stderr": "err"})
                    return
                if path == "/v1/sandboxes/job-1/control":
                    if body.get("type") == "screenshot":
                        self._write(
                            {
                                "done": True,
                                "data": _b64(b"png"),
                                "response": {"success": True, "screenshot_result": {"image_data": _b64(b"png")}},
                            }
                        )
                        return
                    self._write({"done": True, "response": {"success": True}})
                    return
                self.send_error(404)

            def do_DELETE(self) -> None:  # noqa: N802
                path, query = self._path_query()
                owner.requests.append(
                    {
                        "method": "DELETE",
                        "path": path,
                        "query": query,
                        "authorization": self.headers.get("authorization", ""),
                        "body": {},
                    }
                )
                if path == "/v1/sandboxes/job-1/lease":
                    self._write({"sandbox": {"id": "job-1", "vm_name": "cove-sandbox-job-1", "status": "ready", "lease": None}})
                    return
                if path == "/v1/sandboxes/job-1":
                    self._write({"id": "job-1", "status": "draining"})
                    return
                if path == "/v1/warm-pools/runner":
                    self._write(
                        {
                            "namespace": "team-a",
                            "pool": "runner",
                            "cleanup": [
                                {
                                    "id": "cleanup-1",
                                    "namespace": "team-a",
                                    "worker_id": "worker-1",
                                    "warm_pool_slot": "warm-slot-1",
                                    "verb": "cove",
                                    "args": ["ctl", "stop"],
                                    "status": "pending",
                                }
                            ],
                        }
                    )
                    return
                if path == "/v1/service-accounts/ci":
                    self._write({"service_account": _service_account()})
                    return
                self.send_error(404)

            def log_message(self, format: str, *args: object) -> None:
                del format, args

            def _read_json(self) -> dict[str, object]:
                n = int(self.headers.get("content-length") or "0")
                if n == 0:
                    return {}
                return json.loads(self.rfile.read(n))

            def _write(self, payload: dict[str, object]) -> None:
                data = json.dumps(payload).encode()
                self.send_response(200)
                self.send_header("content-type", "application/json")
                self.send_header("content-length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def _path_query(self) -> tuple[str, dict[str, list[str]]]:
                parsed = urllib.parse.urlsplit(self.path)
                return parsed.path, urllib.parse.parse_qs(parsed.query)

        return Handler

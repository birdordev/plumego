#!/usr/bin/env python3
"""Restore a PostgreSQL scheduler to a named point and reconcile business effects."""

import contextlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import time

from failover_drill import require, reserve_ports, run, wait_for
from load_baseline import DIRECTORY, LoadCluster
from smoke import stop


def main():
    with tempfile.TemporaryDirectory(prefix="plumego-restore-") as directory, contextlib.ExitStack() as stack:
        root = Path(directory)
        archive, backup = root / "archive", root / "restored"
        archive.mkdir()
        source = LoadCluster(root, stack, archive=archive)
        receiver_base, receiver_port, restored_port, restored_api = reserve_ports(4)
        receiver_data = root / "receiver"
        receiver_dsn = (f"user=postgres dbname=postgres host=127.0.0.1 port={receiver_base} "
                        "connect_timeout=2 sslmode=disable")
        restored_dsn = (f"user=postgres dbname=postgres host=127.0.0.1 port={restored_port} "
                        "connect_timeout=2 sslmode=disable")

        def start_database(data, port):
            run("pg_ctl", "-D", data, "-l", root / (data.name + ".log"), "-o",
                f"-p {port} -h 127.0.0.1 -c unix_socket_directories=''", "-w", "start",
                env=source.env, stdout=subprocess.DEVNULL)
            stack.callback(run, "pg_ctl", "-D", data, "-m", "immediate", "-w", "stop",
                           env=source.env, stdout=subprocess.DEVNULL)

        def read(dsn, query, **variables):
            args = [arg for key, value in variables.items() for arg in ("-v", f"{key}={value}")]
            return run("psql", dsn, "-XqAt", "-v", "ON_ERROR_STOP=1", *args, input=query,
                       env=source.env, capture_output=True).stdout.strip()

        run("initdb", "-D", receiver_data, "-U", "postgres", "-A", "trust", "--no-locale",
            env=source.env, stdout=subprocess.DEVNULL)
        start_database(receiver_data, receiver_base)
        delivery_env = {"SCHEDULER_RECEIVER_DATABASE_URL": receiver_dsn, "SCHEDULER_OUTBOX_TOKEN": "restore-drill-token"}
        run(source.binary, "-role", "receiver-migrate", cwd=DIRECTORY, env={**source.env, **delivery_env})
        source.start("receiver", receiver_port, **delivery_env)
        outbox_url = f"http://127.0.0.1:{receiver_port}/events"
        original_sender = source.start("outbox", SCHEDULER_OUTBOX_URL=outbox_url, **delivery_env)

        def submit(key, topic, payload, endpoint="hot"):
            _, result = source.request(endpoint, "/runs/batch", {"runs": [
                {"idempotency_key": key, "topic": topic, "payload": payload},
            ]})
            return result["data"]["ids"][0]

        def stock_payload(operation, quantity):
            return {
                "operation_id": operation, "action": "reserve", "sku": "sku-1", "quantity": quantity,
            }

        def stock(key, operation, quantity):
            run_id = submit(key, "demo.inventory", stock_payload(operation, quantity))
            wait_for(lambda: source.request("hot", "/runs/" + run_id, expected=200)[1]["data"]["queue_status"] == "done",
                     operation + " terminal")
            wait_for(lambda: read(receiver_dsn, "SELECT count(*) FROM scheduler_received_events WHERE event_id=:'id';",
                                  id=run_id) == "1", operation + " receiver effect")
            wait_for(lambda: source.sql("SELECT delivered_at IS NOT NULL FROM scheduled_outbox WHERE run_id=:'id';",
                                        id=run_id) == "t", operation + " outbox confirmation")
            return run_id

        start = time.monotonic()
        run("pg_basebackup", "-d", source.dsn, "-D", backup, "-X", "stream", "-c", "fast", env=source.env)
        source.sql("INSERT INTO scheduler_inventory VALUES ('hot','sku-1',25);")
        prior = stock("before-restore", "order-a", 6)
        _, schedule = source.request("hot", "/schedules", {"id": "pitr-plan", "expression": "@hourly",
                                     "time_zone": "UTC", "topic": "demo.record", "payload": {}}, expected=201)
        del schedule
        schedule_cursor = source.sql("SELECT next_fire_at FROM schedules WHERE tenant_id='hot' AND id='pitr-plan';")
        claimed = submit("in-flight-restore", "demo.io", {"delay_ms": 4000})
        wait_for(lambda: source.request("hot", "/runs/" + claimed, expected=200)[1]["data"]["execution_attempts"] == 1,
                 "live lease")
        marker = source.sql("SELECT pg_walfile_name(pg_create_restore_point('scheduler_reconcile'));")
        after = stock("after-restore", "order-b", 4)
        stop(original_sender)
        source.sql("SELECT pg_switch_wal();")
        wait_for(lambda: (archive / marker).is_file(), "WAL containing the named recovery point")
        restore_command = f"cp {shlex.quote(str(archive))}/%f %p".replace("'", "''")
        with (backup / "postgresql.conf").open("a") as config:
            config.write("\narchive_mode = off\n"
                         f"restore_command = '{restore_command}'\n"
                         "recovery_target_name = 'scheduler_reconcile'\nrecovery_target_action = 'promote'\n")
        (backup / "recovery.signal").touch()
        restore_started = time.monotonic()
        start_database(backup, restored_port)
        wait_for(lambda: read(restored_dsn, "SELECT NOT pg_is_in_recovery();") == "t", "timeline recovery")
        restore_seconds = time.monotonic() - restore_started
        require(read(restored_dsn, "SELECT version >= 39 FROM scheduler_schema;") == "t", "schema fell behind")
        require(read(restored_dsn, "SELECT next_fire_at FROM schedules WHERE tenant_id='hot' AND id='pitr-plan';")
                == schedule_cursor, "schedule cursor changed during restore")
        require(read(restored_dsn, "SELECT status FROM mq_tasks WHERE id=:'id';", id=claimed) == "leased",
                "restored in-flight task lost its lease")
        require(read(restored_dsn, """
            SELECT count(*) FROM scheduled_runs r JOIN scheduled_results s ON s.run_id=r.id
            JOIN scheduled_outbox o ON o.run_id=r.id AND o.delivered_at IS NOT NULL
            WHERE r.id=:'prior';
        """, prior=prior) == "1", "prior receipt or outbox confirmation missing")
        require(read(restored_dsn, "SELECT count(*) FROM scheduled_runs WHERE id=:'id';", id=after) == "0",
                "restore target included a later submitted run")
        require(read(restored_dsn, "SELECT available FROM scheduler_inventory WHERE tenant_id='hot' AND sku='sku-1';")
                == "19", "restored inventory did not match restore time")
        restored_env = {"SCHEDULER_DATABASE_URL": restored_dsn}

        def reconcile():
            result = subprocess.run([source.binary, "-role", "inventory-reconcile"], cwd=DIRECTORY,
                                    capture_output=True, text=True, timeout=20, env={
                                        **source.env, **delivery_env, **restored_env,
                                        "SCHEDULER_INVENTORY_TENANT": "hot", "SCHEDULER_INVENTORY_GRACE": "1us",
                                    })
            return result.returncode, json.loads(result.stdout)

        code, before = reconcile()
        require(code != 0 and before["counts"] == {"matched": 1, "source_missing": 1},
                "independent receiver's post-target effect was not found by reconciliation")
        receiver_stock = "SELECT reserved FROM scheduler_projected_inventory WHERE tenant_id='hot' AND sku='sku-1';"
        require(read(receiver_dsn, receiver_stock) == "10", "pre-replay business balance changed")

        # 恢复点后的实例可以重建，业务操作键仍须与外部账本一致。
        source.start("api", restored_api, SCHEDULER_TENANT="hot", **restored_env)
        source.ports["restored"] = restored_api
        wait_for(lambda: source.request("restored", "/readyz", expected=None)[0] == 200, "restored API")
        require(submit("before-restore", "demo.inventory", stock_payload("order-a", 6), "restored") == prior,
                "restored pre-target idempotency key resolved to a different run")
        replayed = submit("after-restore", "demo.inventory", stock_payload("order-b", 4), "restored")
        require(replayed != after and
                submit("after-restore", "demo.inventory", stock_payload("order-b", 4), "restored") == replayed,
                "post-target replay did not preserve one stable request key")
        source.start("worker", SCHEDULER_WORKER_POOL="short", **restored_env)
        source.start("finalizer", **restored_env)
        wait_for(lambda: source.request("restored", "/runs/" + replayed, expected=200)[1]["data"]["queue_status"] == "done",
                 "replayed business task on restored writer")
        require(read(receiver_dsn, "SELECT count(*) FROM scheduler_received_events WHERE event_id=:'id';",
                     id=replayed) == "0", "restored sender transmitted before approval")
        require(read(restored_dsn, """
            SELECT count(*) FROM scheduler_inventory_effects e JOIN scheduled_runs r ON r.id=e.run_id
            WHERE r.id=:'id' AND r.request_key='after-restore' AND e.operation_id='order-b'
                AND e.action='reserve' AND e.quantity=4;
        """, id=replayed) == "1", "recreated run changed the original business key")
        source.start("outbox", SCHEDULER_OUTBOX_URL=outbox_url, **delivery_env, **restored_env)
        wait_for(lambda: read(restored_dsn, "SELECT delivered_at IS NOT NULL FROM scheduled_outbox WHERE run_id=:'id';",
                              id=replayed) == "t", "restored replay confirmation")
        require(read(receiver_dsn, "SELECT count(*) FROM scheduler_received_events WHERE event_id IN (:'old',:'new');",
                     old=after, new=replayed) == "2", "independent receiver did not record both event receipts")
        require(read(receiver_dsn, receiver_stock) == "10" and read(receiver_dsn, """
            SELECT count(*) FROM scheduler_projected_inventory_effects WHERE tenant_id='hot' AND operation_id='order-b';
        """) == "1", "replay repeated the external business effect")
        code, final = reconcile()
        require(code == 0 and final["counts"] == {"matched": 2}, "restored ledger still has differences")
        require(read(restored_dsn, "SELECT available FROM scheduler_inventory WHERE tenant_id='hot' AND sku='sku-1';")
                == "15", "restored stock did not reconcile with the external balance")
        print(json.dumps({"mode": "local", "restore_seconds": round(restore_seconds, 3),
                          "reconciliation_seconds": round(time.monotonic() - restore_started, 3),
                          "total_seconds": round(time.monotonic() - start, 3),
                          "claims_checked": 1, "schedule_cursors_checked": 1,
                          "post_target_effects_detected": 1, "duplicate_effects": 0,
                          "original_keys_preserved": True, "unresolved_differences": 0,
                          "sender_paused_during_reconciliation": True}, sort_keys=True))


if __name__ == "__main__":
    main()

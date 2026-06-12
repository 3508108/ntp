"""
NTP Dashboard server — Flask + SSE.
Usage: python3 server.py
Open:  http://localhost:8080
"""
import json
import time
import queue
import signal

import psutil
from flask import Flask, Response, send_file, request
from flask_cors import CORS
from ntp_sampler import NTPSampler

app         = Flask(__name__)
CORS(app)

ntp_queue   = queue.Queue(maxsize=100)
sampler     = NTPSampler()
sampler.subscribe(ntp_queue)
sampler.start()   # always running — no manual start/stop


def _graceful_shutdown(signum, frame):
    """On SIGTERM/SIGINT: finish in-flight NTP sample, write final heartbeat.
    Gunicorn's graceful-timeout gives us up to 15s before SIGKILL.
    """
    sampler.graceful_stop(timeout=12)


signal.signal(signal.SIGTERM, _graceful_shutdown)
signal.signal(signal.SIGINT,  _graceful_shutdown)


# ── pages ──────────────────────────────────────────────────────────────────────

@app.route("/")
def index():
    return send_file("dashboard.html")


# ── NTP control ────────────────────────────────────────────────────────────────

@app.route("/ntp/start", methods=["POST", "GET"])
def ntp_start():
    sampler.start()
    return {"status": "started"}


@app.route("/ntp/stop", methods=["POST", "GET"])
def ntp_stop():
    sampler.stop()
    return {"status": "stopped"}


@app.route("/ntp/status")
def ntp_status():
    return sampler.status()


@app.route("/ntp/recent")
def ntp_recent():
    n = min(int(request.args.get("n", 50)), 200)
    return {"samples": sampler.recent(n)}


@app.route("/ntp/servers")
def ntp_servers():
    return {"servers": sampler.servers()}


@app.route("/ntp/db/clear", methods=["POST"])
def ntp_db_clear():
    sampler.db_clear()
    return {"status": "cleared"}


@app.route("/ntp/downtime")
def ntp_downtime():
    n = min(int(request.args.get("n", 20)), 100)
    return {"events": sampler.downtime_recent(n)}


@app.route("/ntp/uptime-stats")
def ntp_uptime_stats():
    return sampler.uptime_stats()


@app.route("/ntp/deploy", methods=["POST"])
def ntp_deploy():
    data        = request.get_json(force=True, silent=True) or {}
    duration_ms = data.get("duration_ms")
    git_hash    = data.get("git_hash", "")[:12]
    message     = data.get("message", "")[:120]
    event       = sampler.log_deploy(duration_ms=duration_ms,
                                     git_hash=git_hash, message=message)
    return event


@app.route("/ntp/deploys")
def ntp_deploys():
    n = min(int(request.args.get("n", 20)), 100)
    return {"deploys": sampler.deploys_recent(n)}


@app.route("/ntp/server-time")
def server_time():
    """Returns current server UTC time + Unix timestamp."""
    from datetime import datetime, timezone
    now    = datetime.now(timezone.utc)
    ts     = time.time()
    return {
        "utc":     now.strftime("%Y-%m-%d %H:%M:%S"),
        "ts":      ts,
        "iso":     now.isoformat(),
        "fetched": now.strftime("%H:%M:%S UTC"),
    }


# ── SSE streams ───────────────────────────────────────────────────────────────────

@app.route("/events/ntp")
def events_ntp():
    """SSE: new sample on every NTP query; ping + status every 3 s otherwise."""
    def generate():
        while True:
            try:
                data = ntp_queue.get(timeout=3)
                yield f"data: {json.dumps(data)}\n\n"
            except queue.Empty:
                st = sampler.status()
                yield f"data: {json.dumps({'ping': True, **st})}\n\n"
    return Response(
        generate(),
        mimetype="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


@app.route("/events/metrics")
def events_metrics():
    """SSE: CPU / RAM / disk every 1 s."""
    def generate():
        while True:
            cpu  = psutil.cpu_percent(interval=0.5)
            mem  = psutil.virtual_memory()
            disk = psutil.disk_usage("/")
            payload = {
                "cpu":          cpu,
                "mem_percent":  mem.percent,
                "mem_used_mb":  mem.used  // (1024 ** 2),
                "mem_total_mb": mem.total // (1024 ** 2),
                "disk_percent": disk.percent,
                "disk_used_gb": round(disk.used  / (1024 ** 3), 1),
                "disk_total_gb":round(disk.total / (1024 ** 3), 1),
            }
            yield f"data: {json.dumps(payload)}\n\n"
            time.sleep(0.5)
    return Response(
        generate(),
        mimetype="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


if __name__ == "__main__":
    print("NTP Dashboard → http://localhost:8080")
    app.run(host="0.0.0.0", port=8080, threaded=True)

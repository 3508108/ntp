"""
deploy_proof.py — proves zero-downtime reload vs hard restart.

Runs two trials back-to-back:
  Trial A: systemctl RESTART  — expect dropped requests
  Trial B: systemctl RELOAD   — expect zero dropped requests

Each trial: hits /ntp/status every 100 ms for 12 s while the
action fires at t=3 s. Prints a timeline and a summary table.
"""
import threading
import time
import urllib.request
import subprocess
import sys

TARGET   = "http://104.248.21.29:8080/ntp/status"
PROBE_MS = 100          # probe interval in ms
TRIAL_S  = 18           # total duration per trial
ACTION_AT = 4           # trigger action at t=4 s


def probe_loop(results: list, stop_event: threading.Event):
    while not stop_event.is_set():
        t0 = time.time()
        try:
            with urllib.request.urlopen(TARGET, timeout=1) as r:
                code = r.status
                ms   = round((time.time() - t0) * 1000)
                results.append((time.time(), code, ms, None))
        except Exception as exc:
            ms = round((time.time() - t0) * 1000)
            results.append((time.time(), 0, ms, str(exc)[:50]))
        time.sleep(PROBE_MS / 1000)


def run_trial(label: str, ssh_cmd: str) -> list:
    print(f"\n{'─'*56}")
    print(f"  TRIAL {label}")
    print(f"  cmd: {ssh_cmd}")
    print(f"{'─'*56}")

    results = []
    stop    = threading.Event()
    t       = threading.Thread(target=probe_loop, args=(results, stop), daemon=True)
    start   = time.time()
    t.start()

    print(f"  t=0   monitor started  ({PROBE_MS}ms interval)")
    time.sleep(ACTION_AT)
    print(f"  t={ACTION_AT}   ► firing: {ssh_cmd.split()[-1]}", flush=True)

    # Fire async — don't block the probe loop
    proc = subprocess.Popen(
        ["ssh", "gr-droplet", ssh_cmd],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )

    # Keep probing for the rest of the trial
    remaining = TRIAL_S - ACTION_AT
    time.sleep(remaining)
    proc.poll()   # check if done (don't block)
    elapsed = time.time() - start
    print(f"  t={elapsed:.1f}  trial ended")
    stop.set()
    t.join(timeout=2)

    return results, start


def analyse(results, trial_start, label):
    total   = len(results)
    ok      = sum(1 for _, c, _, _ in results if c == 200)
    errors  = [(t - trial_start, ms, e) for t, c, ms, e in results if c != 200]
    max_gap = 0

    # compute largest consecutive gap
    times = [r[0] for r in results if r[1] == 200]
    for i in range(1, len(times)):
        gap = (times[i] - times[i-1]) * 1000
        if gap > max_gap:
            max_gap = gap

    # print timeline (one char per probe)
    print(f"\n  Timeline (·=ok  ✗=error  each char={PROBE_MS}ms):")
    print("  ", end="")
    marker_t = trial_start + ACTION_AT
    for ts, code, ms, err in results:
        if abs(ts - marker_t) < 0.06:
            print("▼", end="", flush=True)   # action marker
        elif code == 200:
            print("·", end="", flush=True)
        else:
            print("✗", end="", flush=True)
    print()

    print(f"\n  ┌─ {label} ─────────────────────────────")
    print(f"  │  total probes  : {total}")
    print(f"  │  success       : {ok}  ({round(ok/total*100,1)}%)")
    print(f"  │  errors        : {len(errors)}")
    print(f"  │  max gap (ok→ok): {round(max_gap)} ms")
    if errors:
        print(f"  │  first error at : t={errors[0][0]:.2f}s  ({errors[0][2]})")
    print(f"  └────────────────────────────────────────")


# ── main ──────────────────────────────────────────────────────────────────────

print("=" * 56)
print("  ZERO-DOWNTIME DEPLOY PROOF")
print(f"  target : {TARGET}")
print(f"  probe  : every {PROBE_MS}ms  |  trial: {TRIAL_S}s each")
print("=" * 56)

# Warm-up: verify server is up
try:
    urllib.request.urlopen(TARGET, timeout=3)
    print("  ✓ server reachable")
except Exception as e:
    print(f"  ✗ server unreachable: {e}")
    sys.exit(1)

# ── Trial A: hard restart ─────────────────────────────────────────────────────
res_a, t_a = run_trial(
    "A — systemctl RESTART  (expect downtime)",
    "systemctl restart ntp-dashboard",
)
print("  waiting for server to recover...", flush=True)
time.sleep(10)  # gunicorn graceful-timeout=5s + startup time

# ── Trial B: graceful reload ──────────────────────────────────────────────────
res_b, t_b = run_trial(
    "B — systemctl RELOAD   (expect zero downtime)",
    "systemctl reload ntp-dashboard",
)

# ── Summary ───────────────────────────────────────────────────────────────────
print("\n" + "=" * 56)
print("  RESULTS")
print("=" * 56)
analyse(res_a, t_a, "RESTART")
analyse(res_b, t_b, "RELOAD (deploy)")
print()

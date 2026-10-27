#!/usr/bin/env python3
"""Plan every demo ride against a live serpentine-api and report what it actually rode.

The catalogue names roads. The point of this check is that the plan contains them: an entry whose
coordinates are slightly off still plans successfully, it just quietly rides somewhere else, and
that failure is invisible from the JSON alone.

    python3 tools/check_rides.py [base-url]
"""
import json
import pathlib
import sys
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "https://serpentine.phfactor.net/v1").rstrip("/")
CATALOG = pathlib.Path(__file__).resolve().parent.parent / "server" / "rides.json"


def plan(ride):
    body = {"mode": ride["mode"], "start": ride["start"]}
    if ride["mode"] == "point_to_point":
        body["end"] = ride["end"]
    elif ride["mode"] == "out_and_back":
        body["turnaround"] = ride["turnaround"]
    else:
        body["distance_m"] = ride["distance_m"]
    req = urllib.request.Request(f"{BASE}/plan", method="POST",
                                 data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return json.load(r)


def main():
    rides = json.loads(CATALOG.read_text())["rides"]
    bad = 0
    for ride in rides:
        try:
            p = plan(ride)
        except Exception as e:  # noqa: BLE001 - any failure is a failure to report
            print(f"FAIL  {ride['id']}: {e}")
            bad += 1
            continue
        roads = [r["name"] for r in (p.get("roads") or []) if r.get("name")][:6]
        mi = p["distance_m"] / 1609.344
        mins = p["time_s"] / 60
        # The road the entry names should be in what we rode. Matching is loose because OSM spells
        # a road several ways ("US 129", "Tapoco Road", "Us 129").
        want = ride["road"].split(",")[0].strip().lower()
        hit = any(want in r.lower() or r.lower() in want for r in roads)
        flag = "" if hit else "   <-- named road not in the top roads"
        print(f"{'ok  ' if hit else 'CHECK'} {ride['id']:<26} {mi:6.1f} mi {mins:5.0f} min  "
              f"{', '.join(roads[:4])}{flag}")
        if not hit:
            bad += 1
    print(f"\n{len(rides) - bad}/{len(rides)} rode the road they name")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())

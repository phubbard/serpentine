#!/usr/bin/env python3
"""Does a ride stay fun when errands are bolted onto it?

The errand ride ("I have errands and an afternoon") can be built two ways, and the difference is
not a matter of taste:

  chain   home -> errand -> errand -> home, with max_extra_s paying for curves
  ride    home -> errand -> errand -> T -> home, where T is a far turnaround CHOSEN BY SCORING
          several candidates, the way a loop is scored

Measured 2026-11-18 against production: the chain cannot work. Offered 45 minutes of detour it
spent 0.0, because a detour budget only picks among paths between fixed endpoints - it will never
ride out to a canyon and back, since every kilometre out there is cost with no reward in the cost
function. A budget cannot manufacture a detour. Only a far waypoint can, and only scoring makes it
a good one.

This script measures both against a plain loop of the same length, which is the ceiling.

  tools/errand_ride.py --home LON,LAT --stop LON,LAT [--stop LON,LAT] [--km 120]
"""

import argparse, json, math, sys, time, urllib.error, urllib.request

API = "https://serpentine.phfactor.net/v1/plan"
R_EARTH_KM = 6371.0


def post(body, tries=4, soft=False):
    """soft=True returns None instead of exiting when the route is simply unroutable."""
    data = json.dumps(body).encode()
    for attempt in range(tries):
        req = urllib.request.Request(API, data=data, headers={"content-type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=120) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            # ADR-030: 20 plans/min per caller, burst 8, with Retry-After on a 429.
            if e.code == 429 and attempt < tries - 1:
                time.sleep(float(e.headers.get("Retry-After", 4)))
                continue
            msg = e.read()[:200].decode(errors="replace")
            # A candidate turnaround can land in the desert, off the extract, or behind the border:
            # "Cannot find point N". That is a candidate to drop, not a failure of the run - the
            # real server will have to drop them too (downtown SD cannot loop east or south).
            if soft and e.code == 422:
                return None
            sys.exit(f"{e.code} from the API: {msg}")
    return None


def offset(lon, lat, bearing_deg, km):
    """A point km away from (lon, lat) on the given bearing."""
    b = math.radians(bearing_deg)
    dlat = math.degrees(km / R_EARTH_KM * math.cos(b))
    dlon = math.degrees(km / R_EARTH_KM * math.sin(b) / math.cos(math.radians(lat)))
    return [round(lon + dlon, 6), round(lat + dlat, 6)]


def measure(d):
    st = d.get("stats") or {}
    km = d["distance_m"] / 1000
    curvy = st.get("curvy_km", 0.0)
    return {
        "km": km,
        "min": d["time_s"] / 60,
        "curvy_km": curvy,
        "curvy_pct": 100 * curvy / km if km else 0.0,
        "retraced_km": st.get("repeated_km", 0.0),
    }


def row(name, m):
    print(f"  {name:<34} {m['km']:6.1f} km {m['min']:6.0f} min   "
          f"curvy {m['curvy_km']:6.2f} km ({m['curvy_pct']:4.1f}%)   retraced {m['retraced_km']:5.1f} km")


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--home", required=True, help="LON,LAT")
    ap.add_argument("--stop", action="append", default=[], metavar="LON,LAT",
                    help="an errand; repeatable")
    ap.add_argument("--km", type=float, default=120.0, help="target total distance (default 120)")
    ap.add_argument("--twistiness", type=float, default=0.9)
    ap.add_argument("--bearings", type=int, default=8, help="candidate turnarounds to score")
    a = ap.parse_args()

    pt = lambda s: [float(x) for x in s.split(",")]
    home = pt(a.home)
    stops = [pt(s) for s in a.stop]
    if not stops:
        sys.exit("give at least one --stop")

    print(f"\nhome {home}, {len(stops)} errand(s), target {a.km:.0f} km, twistiness {a.twistiness}\n")

    print("BASELINE - a plain loop, no errands. This is the ceiling:")
    base = measure(post(dict(mode="loop", start=home, distance_m=int(a.km * 1000),
                             twistiness=a.twistiness)))
    row("loop, no errands", base)

    print("\nTHE CHAIN - errands with a detour budget (the obvious build):")
    chain = measure(post(dict(mode="point_to_point", start=home, end=home, via=stops,
                              style="curvy", twistiness=a.twistiness, max_extra_s=7200)))
    row("home -> errands -> home", chain)

    # The turnaround has to be far enough out that the round trip lands near the target. The chain
    # already spends some of the budget, so bill it against what is left.
    out_km = max(10.0, (a.km - chain["km"]) / 2)
    print(f"\nTHE RIDE - errands, then a scored turnaround {out_km:.0f} km out "
          f"({a.bearings} candidates):")
    best, best_m, scored, dropped = None, None, [], 0
    for i in range(a.bearings):
        bearing = i * 360 / a.bearings
        t = offset(home[0], home[1], bearing, out_km)
        d = post(dict(mode="point_to_point", start=home, end=home, via=stops + [t],
                      style="curvy", twistiness=a.twistiness), soft=True)
        if d is None:
            dropped += 1
            print(f"  {'bearing %3.0f' % bearing:<34} unroutable, dropped")
            continue
        m = measure(d)
        scored.append((bearing, m))
        if best_m is None or m["curvy_km"] > best_m["curvy_km"]:
            best, best_m = bearing, m
    for bearing, m in scored:
        row(f"bearing {bearing:3.0f}" + (" (best)" if bearing == best else ""), m)
    if best_m is None:
        sys.exit("every candidate turnaround was unroutable")
    if dropped:
        print(f"\n  {dropped} of {a.bearings} candidates were unroutable.")

    # The only construction left: do the errands as a short direct chain, then hand the REST of the
    # budget to the loop generator, which is the one thing that produces a genuine circuit. Costs a
    # path stitch in the server, which the waypoint constructions above were trying to avoid.
    print("\nTHE STITCH - errands direct, then a real round_trip for the remainder:")
    leg = measure(post(dict(mode="point_to_point", start=home, end=stops[-1],
                            via=stops[:-1], style="direct")))
    row("home -> errands (direct)", leg)
    rest_km = max(20.0, a.km - leg["km"])
    lp = measure(post(dict(mode="loop", start=stops[-1], distance_m=int(rest_km * 1000),
                           twistiness=a.twistiness)))
    row(f"loop from the last errand", lp)
    total_km = leg["km"] + lp["km"]
    stitched = {
        "km": total_km,
        "min": leg["min"] + lp["min"],
        "curvy_km": leg["curvy_km"] + lp["curvy_km"],
        "curvy_pct": 100 * (leg["curvy_km"] + lp["curvy_km"]) / total_km,
        "retraced_km": leg["retraced_km"] + lp["retraced_km"],
    }
    row("STITCHED TOTAL", stitched)

    print(f"\nVERDICT")
    print(f"  curvy road:  chain {chain['curvy_pct']:.1f}%   far-waypoint "
          f"{best_m['curvy_pct']:.1f}%   stitched {stitched['curvy_pct']:.1f}%   "
          f"plain loop {base['curvy_pct']:.1f}%")
    print(f"  retraced:    chain {chain['retraced_km']:.0f} km   far-waypoint "
          f"{best_m['retraced_km']:.0f} km   stitched {stitched['retraced_km']:.0f} km   "
          f"plain loop {base['retraced_km']:.0f} km")
    if base["curvy_km"] > 0:
        print(f"  the stitch keeps {100 * stitched['curvy_km'] / base['curvy_km']:.0f}% of the "
              f"plain loop's curvy kilometres; the far waypoint keeps "
              f"{100 * best_m['curvy_km'] / base['curvy_km']:.0f}%.\n")


if __name__ == "__main__":
    main()

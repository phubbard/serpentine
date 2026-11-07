#!/usr/bin/env python3
"""Fit the ADR-045 ride-time allowance (and check the energy model) against a logged ride.

    tools/calibrate_ride.py ride.gpx [--mbb MBB.bin] [--bms BMS.bin] [--gh http://axiom:8989]

Takes a timestamped GPX track, map-matches it to the routing graph, and reports measured delay
per road class x urban density against the rates in server/timing.go. With a Zero MBB log it
also subtracts time the bike was switched off, which is the difference between "the rider stopped
at a light" and "the rider parked"; with a BMS log it reads the SoC drop for the energy model.

Why map-matching and not re-planning: the rates have to be fitted on the roads actually ridden,
not on the roads we would have chosen. GraphHopper's /match gives both, snapped to the same
graph the planner uses, so road_class and urban_density come from the same source as in production.

Stdlib only (CLAUDE.md), except that it needs the LAN GraphHopper.
"""
import argparse, collections, datetime as dt, json, math, struct, sys, urllib.request
import xml.etree.ElementTree as ET

NS = '{http://www.topografix.com/GPX/1/1}'

# Keep in step with server/timing.go. Duplicated on purpose: this script is the thing that says
# whether those numbers are right, so it must not import them.
MODEL = {"arterial": {"city": 36.0, "residential": 24.0, "rural": 5.0},
         "local":    {"city": 10.0, "residential":  8.0, "rural": 3.0},
         "freeway":  {}}
CURVY_S, CURVY_BELOW = 12.0, 0.94
ARTERIAL = {"trunk", "trunk_link", "primary", "primary_link", "secondary", "secondary_link",
            "tertiary", "tertiary_link"}


def haversine_m(la1, lo1, la2, lo2):
    R = 6371000.0
    p1, p2 = math.radians(la1), math.radians(la2)
    h = (math.sin((p2 - p1) / 2) ** 2
         + math.cos(p1) * math.cos(p2) * math.sin(math.radians(lo2 - lo1) / 2) ** 2)
    return 2 * R * math.asin(math.sqrt(h))


def group(rc):
    if rc in ("motorway", "motorway_link"):
        return "freeway"
    return "arterial" if rc in ARTERIAL else "local"


def read_gpx(path):
    pts = []
    for p in ET.parse(path).getroot().iter(NS + 'trkpt'):
        t = p.find(NS + 'time')
        if t is None:
            sys.exit("this GPX has no <time> elements, so it cannot calibrate ride time")
        ele = p.find(NS + 'ele')
        pts.append((float(p.get('lat')), float(p.get('lon')),
                    dt.datetime.fromisoformat(t.text.replace('Z', '+00:00')),
                    float(ele.text) if ele is not None else None))
    if len(pts) < 2:
        sys.exit("not enough track points")
    return pts


def match(gpx_path, gh):
    """Snap the track to the graph. gps_accuracy=15 suits a handlebar GPS logging at 1 Hz."""
    q = ("profile=motorcycle&gps_accuracy=15&points_encoded=false"
         "&details=road_class&details=urban_density&details=curvature&details=max_speed"
         "&details=street_name")
    req = urllib.request.Request(f"{gh}/match?{q}", data=open(gpx_path, 'rb').read(),
                                 headers={'Content-Type': 'application/gpx+xml'})
    with urllib.request.urlopen(req, timeout=300) as r:
        return json.load(r)["paths"][0]


# ---- Zero log parsing -------------------------------------------------------------------
# Entries are framed 0xb2 | length | type | uint32-LE epoch (UTC) | payload. Type 0xfd carries a
# NUL-terminated text event; the BMS's type 0x4b is a periodic status record with SoC% at
# payload offset 20. Both offsets were established empirically against a known ride, not from a
# Zero specification -- see docs/calibration.md.
def zero_entries(path):
    data = open(path, 'rb').read()
    lo = int(dt.datetime(2020, 1, 1, tzinfo=dt.timezone.utc).timestamp())
    hi = int(dt.datetime(2040, 1, 1, tzinfo=dt.timezone.utc).timestamp())
    out, i = [], 0
    while i < len(data) - 8:
        if data[i] == 0xb2:
            ln, typ = data[i + 1], data[i + 2]
            ts = struct.unpack_from('<I', data, i + 3)[0]
            if lo < ts < hi and 6 <= ln <= 255:
                out.append((ts, typ, data[i + 7:i + 2 + ln]))
                i += max(ln + 1, 8)
                continue
        i += 1
    out.sort(key=lambda e: e[0])
    return out


def off_intervals(mbb, lo, hi):
    """Spans inside [lo, hi] when the bike was not in RUN: parked, not waiting at a light."""
    marks = []
    for ts, typ, pl in zero_entries(mbb):
        if typ != 0xfd or not (lo - 3600 <= ts <= hi + 3600):
            continue
        txt = ''.join(chr(b) if 32 <= b < 127 else ' ' for b in pl)
        if 'State change from RUN to STOP' in txt:
            marks.append((ts, 'off'))
        elif 'State change from STOP to RUN' in txt:
            marks.append((ts, 'on'))
    spans, start = [], None
    for ts, kind in marks:
        if kind == 'off' and start is None:
            start = ts
        elif kind == 'on' and start is not None:
            spans.append((start, ts))
            start = None
    return [(a, b) for a, b in spans if b > lo and a < hi]


def soc_series(bms, lo, hi):
    out = []
    for ts, typ, pl in zero_entries(bms):
        if typ == 0x4b and len(pl) > 20 and lo - 600 <= ts <= hi + 600:
            soc = pl[20]
            if 0 < soc <= 100:
                out.append((ts, soc))
    return out


# ---- the fit ---------------------------------------------------------------------------
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('gpx')
    ap.add_argument('--mbb', help='Zero MBB .bin, to subtract parked time')
    ap.add_argument('--bms', help='Zero BMS .bin, to read the SoC drop')
    ap.add_argument('--gh', default='http://axiom:8989')
    ap.add_argument('--mode', default='unrecorded',
                    help='ride mode (eco/sport/...): the log does not record it')
    a = ap.parse_args()

    trk = read_gpx(a.gpx)
    t0, t1 = trk[0][2].timestamp(), trk[-1][2].timestamp()
    path = match(a.gpx, a.gh)
    coords = path["points"]["coordinates"]
    n = len(coords)

    def spread(key, default):
        out = [default] * n
        for lo, hi, v in path["details"].get(key, []):
            for i in range(lo, min(hi, n)):
                out[i] = v
        return out

    dens = spread("urban_density", "rural")
    cls = spread("road_class", "")
    curv = spread("curvature", 1.0)
    msp = spread("max_speed", 50.0)

    off = off_intervals(a.mbb, t0, t1) if a.mbb else []
    parked = sum(min(b, t1) - max(aa, t0) for aa, b in off)

    # Nearest matched vertex per track point, constrained non-decreasing so a route that crosses
    # itself cannot jump backwards.
    idx, j = [], 0
    for la, lo, _, _ in trk:
        best, bj = 1e18, j
        for q in range(j, min(j + 60, n)):
            d = haversine_m(la, lo, coords[q][1], coords[q][0])
            if d < best:
                best, bj = d, q
        idx.append(bj)
        j = bj

    # One pass over track intervals so distance, measured time and free-flow time all come from
    # the same interval. Taking distance from the path and time from the track instead puts a
    # different denominator under each and manufactures nonsense rates.
    iv = []
    for i in range(len(trk) - 1):
        dts = (trk[i + 1][2] - trk[i][2]).total_seconds()
        if dts <= 0:
            continue
        ts = trk[i][2].timestamp()
        if any(aa <= ts < b for aa, b in off):
            continue
        d = haversine_m(trk[i][0], trk[i][1], trk[i + 1][0], trk[i + 1][1])
        q = idx[i]
        v = max((msp[q] or 50.0) * 0.9, 8.0)
        iv.append((group(cls[q]), dens[q], d / 1000.0, dts, d / 1000.0 / v * 3600.0,
                   curv[q] is not None and curv[q] < CURVY_BELOW))

    # GraphHopper reports one total; apportion it over the intervals by modelled speed so each
    # class gets a free-flow baseline whose sum is exactly what the engine said.
    raw = sum(x[4] for x in iv) or 1.0
    k = (path["time"] / 1000.0) / raw

    B = collections.defaultdict(lambda: dict(km=0.0, meas=0.0, ff=0.0, ckm=0.0))
    for g, dn, km, dts, ff, cy in iv:
        b = B[(g, dn)]
        b["km"] += km
        b["meas"] += dts
        b["ff"] += ff * k
        if cy:
            b["ckm"] += km

    moved = sum(x[2] for x in iv)
    print(f"ride      {trk[0][2]:%a %Y-%m-%d %H:%M}Z -> {trk[-1][2]:%H:%M}Z   mode={a.mode}")
    print(f"track     {len(trk)} pts, {moved:.2f} km, matched {path['distance']/1000:.2f} km")
    print(f"parked    {parked/60:.1f} min excluded"
          + (f" ({len(off)} key-off spans)" if off else " (no MBB log: traffic and parking are mixed)"))
    print()
    hdr = (f"{'class/density':22s}{'km':>7s}{'ff min':>8s}{'meas min':>9s}{'mph':>6s}"
           f"{'MEAS s/km':>10s}{'model':>7s}{'ratio':>7s}")
    print(hdr)
    TK = TF = TM = 0.0
    for key in sorted(B, key=lambda z: -B[z]["km"]):
        b = B[key]
        km = b["km"]
        if km < 0.2:
            continue
        ff, me = b["ff"], b["meas"]
        TK, TF, TM = TK + km, TF + ff, TM + me
        mod = MODEL[key[0]].get(key[1], 0.0)
        eff = (mod + CURVY_S * b["ckm"] / km) if key[0] != "freeway" else 0.0
        ratio = f"{(me-ff)/km/eff:.2f}" if eff > 0 else "  -"
        print(f"{key[0]+'/'+key[1]:22s}{km:7.2f}{ff/60:8.1f}{me/60:9.1f}"
              f"{km*0.621371/(me/3600):6.1f}{(me-ff)/km:10.1f}{eff:7.1f}{ratio:>7s}")
    print(f"{'TOTAL':22s}{TK:7.2f}{TF/60:8.1f}{TM/60:9.1f}"
          f"{TK*0.621371/(TM/3600):6.1f}{(TM-TF)/TK:10.1f}")
    print(f"\nfree-flow {TF/60:.1f} min -> measured {TM/60:.1f} min "
          f"({(TM/TF-1)*100:+.0f}%); our allowance would need "
          f"{(TM-TF)/TK:.0f} s/km on average")

    if a.bms:
        s = soc_series(a.bms, t0, t1)
        if len(s) >= 2:
            print(f"\nSoC {s[0][1]}% -> {s[-1][1]}% over "
                  f"{(s[-1][0]-s[0][0])/60:.0f} min ({s[0][1]-s[-1][1]} points), "
                  f"sampled every ~{(s[-1][0]-s[0][0])/max(len(s)-1,1):.0f}s")
            print("  energy: multiply the point drop by the pack's NOMINAL kWh/100 and compare "
                  "with the plan's energy_kwh.")
            print("  note: SoC is integer percent, so a short ride is mostly quantisation noise.")
        else:
            print("\nno SoC records found in the BMS window")


if __name__ == '__main__':
    main()

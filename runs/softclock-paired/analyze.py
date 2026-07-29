#!/usr/bin/env python3
"""Analyse the paired soft-clock control over cmd/sargon-symmatch logs.

Order of business is deliberate and matches the review discipline:
  1. SPEND SYMMETRY per arm (8fish total compute / Sargon total compute).
     sargon-symmatch is symmetric by construction only if this is ~1.0. An
     engine that quietly takes more compute than its opponent produces a large
     and entirely meaningless Elo gain, so this is checked BEFORE any result.
  2. Elo per arm and the paired difference, with intervals.
  3. Audit: how every game actually ended, and every harness-artifact marker.
"""
import glob
import math
import os
import re
import sys
from collections import Counter

ROOT = os.path.dirname(os.path.abspath(__file__))


def elo(p):
    if p <= 0:
        return float("-inf")
    if p >= 1:
        return float("inf")
    return -400.0 * math.log10(1.0 / p - 1.0)


class Arm:
    def __init__(self, name):
        self.name = name
        self.scores = []            # per-game 1.0 / 0.5 / 0.0 from 8fish's side
        self.w = self.l = self.d = 0
        self.f_own = self.f_pon = self.pon_asked = self.s_own = 0
        self.ponders = 0
        self.moves = 0
        self.intended = 0
        self.terms = Counter()
        self.desyncs = 0
        self.unreadable = 0
        self.mode_warnings = 0
        self.shards = 0
        self.game_spend = []        # per-game (8fish_total, sargon_total)
        self.clock_banner = set()
        self.sess_fish = self.sess_sargon = 0
        self.sess_shards = 0
        self.bank_income = self.bank_spent = 0   # own-move adherence (the gate's quantity)

    # ---- ingest -------------------------------------------------------
    def add_log(self, path):
        self.shards += 1
        txt = open(path, errors="replace").read()
        for m in re.finditer(r"clock=(.+?), symmetric", txt):
            self.clock_banner.add(m.group(1))
        for m in re.finditer(r"GAME \d+ RESULT: (\S+)", txt):
            r = m.group(1)
            if r == "8fish-wins":
                self.w += 1
                self.scores.append(1.0)
            elif r == "8fish-loses":
                self.l += 1
                self.scores.append(0.0)
            else:
                self.d += 1
                self.scores.append(0.5)
        for m in re.finditer(r"TERMINATION g\d+ result=(\S+) reason=(\S+)", txt):
            self.terms[(m.group(1), m.group(2))] += 1
        self.desyncs += len(re.findall(r"^DESYNC ", txt, re.M))
        self.unreadable += len(re.findall(r"unreadable/illegal", txt))
        self.mode_warnings += len(re.findall(r"SARGON MODE WRONG", txt))
        for m in re.finditer(
            r"SYMMATCH-SPEND-GAME-SUMMARY game=\d+ 8fish_total=(\d+) sargon_total=(\d+)", txt
        ):
            self.game_spend.append((int(m.group(1)), int(m.group(2))))
        # Own-move adherence: true cycles / intended income, summed over every
        # own move. This is EXACTLY the quantity sprt.TestSoftClockAdherence
        # gates on -- but measured here at the gauntlet's own budget (~29 s,
        # softMarginPct octave 100) rather than the gate's 4 s (octave 127).
        for m in re.finditer(r"^BANK g\d+ .* income=(\d+) alloc=\d+ spent=(\d+) ", txt, re.M):
            self.bank_income += int(m.group(1))
            self.bank_spent += int(m.group(2))
        # --- Spend, rebuilt from the PER-MOVE lines. ---
        #
        # Deliberately NOT from the SYMMATCH-SPEND-SESSION-SUMMARY line, for two
        # reasons: that line only exists once a shard has finished (so partial
        # shards would silently contribute nothing), and re-deriving the totals
        # from the raw per-move record is an INDEPENDENT check on the harness's
        # own accumulators. The two are compared in check_against_session().
        for m in re.finditer(
            r"^MOVE g\d+ ply\d+ 8fish move=\S+ think=(\d+) \| sargon_ponder_window=(\d+) \|\| "
            r"ply\d+ SARGON move=\S+ think=(\d+) \| 8fish_ponder=(\d+) ", txt, re.M):
            own, window, sthink, pon = (int(g) for g in m.groups())
            self.f_own += own
            self.s_own += sthink
            self.f_pon += pon
            if pon or sthink:
                # A ponder only runs when there was a prediction AND Sargon
                # actually thought; asked == Sargon's think in that case.
                if pon:
                    self.pon_asked += sthink
                    self.ponders += 1
            assert window == own, "harness broke its own mirror: %d != %d" % (window, own)
        # Sargon's opening move as White: it thinks, nothing of ours mirrors it.
        for m in re.finditer(r"^MOVE g\d+ ply1 SARGON\(open\) move=\S+ think=(\d+)", txt, re.M):
            self.s_own += int(m.group(1))
        # 8fish's final move when it ends the game: its think is still mirrored
        # into Sargon's ponder window by runSargonCycles before terminal() fires.
        for m in re.finditer(r"^MOVE g\d+ ply\d+ 8fish move=\S+ think=(\d+) -> ", txt, re.M):
            self.f_own += int(m.group(1))
        # Harness's own accumulators, for cross-check (finished shards only).
        for m in re.finditer(
            r"SYMMATCH-SPEND-SESSION-SUMMARY games=\d+ 8fish_total=(\d+) sargon_total=(\d+)", txt):
            self.sess_fish += int(m.group(1))
            self.sess_sargon += int(m.group(2))
            self.sess_shards += 1
        for m in re.finditer(
            r"SYMMATCH-TIME-SESSION-SUMMARY games=\d+ moves=(\d+) own_total=\d+ own_intended=(\d+)", txt):
            self.moves += int(m.group(1))
            self.intended += int(m.group(2))

    # ---- derived ------------------------------------------------------
    @property
    def n(self):
        return len(self.scores)

    @property
    def p(self):
        return sum(self.scores) / self.n if self.n else 0.0

    @property
    def se(self):
        """SE of the mean score. Uses the per-game sample variance, so draws
        (which really do reduce variance) tighten the interval honestly."""
        if self.n < 2:
            return float("inf")
        mu = self.p
        var = sum((s - mu) ** 2 for s in self.scores) / (self.n - 1)
        return math.sqrt(var / self.n)

    def fish_total(self):
        return self.f_own + self.f_pon

    def sargon_total(self):
        return self.s_own + self.f_own  # sargon ponder window == 8fish own spend

    def spend_ratio(self):
        s = self.sargon_total()
        return self.fish_total() / s if s else float("nan")

    def ponder_ratio(self):
        return self.f_pon / self.pon_asked if self.pon_asked else float("nan")

    def adherence(self):
        return self.bank_spent / self.bank_income if self.bank_income else float("nan")


def fmt_elo(p, lo, hi):
    def e(x):
        v = elo(x)
        return "%+.0f" % v if abs(v) != float("inf") else ("+inf" if v > 0 else "-inf")
    return "%s  [%s, %s]" % (e(p), e(lo), e(hi))


def main():
    arms = {}
    for arm in ("off", "soft"):
        a = Arm(arm)
        for d in sorted(glob.glob(os.path.join(ROOT, arm + "-*"))):
            f = os.path.join(d, "symmatch.log")
            if os.path.exists(f):
                a.add_log(f)
        arms[arm] = a

    print("=" * 78)
    print("1. SPEND SYMMETRY  (check this before reading any Elo)")
    print("=" * 78)
    print("%-6s %8s %13s %13s %11s %10s %10s %7s" %
          ("arm", "shards", "8fish Gcyc", "sargon Gcyc", "spend_ratio",
           "adherence", "ponder_rt", "ponders"))
    for k in ("off", "soft"):
        a = arms[k]
        print("%-6s %8d %13.1f %13.1f %11.4f %10.4f %10.4f %7d" %
              (a.name, a.shards, a.fish_total() / 1e9, a.sargon_total() / 1e9,
               a.spend_ratio(), a.adherence(), a.ponder_ratio(), a.ponders))
    if all(arms[k].pon_asked for k in arms):
        d = arms["soft"].spend_ratio() - arms["off"].spend_ratio()
        print("\n  soft - off spend_ratio delta: %+.4f  (%+.2f%% of compute)" % (d, d * 100))
        print("  own-move adherence soft/off = %.4f   (sprt.TestSoftClockAdherence"
              " accepts [0.90, 1.10])" % (arms["soft"].adherence() / arms["off"].adherence()))
        print("  ponder adherence   soft/off = %.4f   <-- the whole gap lives here;"
              " ponder spend is NOT mirrored to Sargon"
              % (arms["soft"].ponder_ratio() / arms["off"].ponder_ratio()))
        print("  clock banners: off=%s | soft=%s" %
              (sorted(arms["off"].clock_banner), sorted(arms["soft"].clock_banner)))
    print("\n  cross-check (per-move re-derivation vs the harness's own accumulators,")
    print("  finished shards only; a mismatch means one of the two is wrong):")
    for k in ("off", "soft"):
        a = arms[k]
        if a.sess_shards:
            print("    %-5s harness: 8fish=%.1f sargon=%.1f Gcyc over %d/%d finished shards"
                  % (a.name, a.sess_fish / 1e9, a.sess_sargon / 1e9, a.sess_shards, a.shards))
        else:
            print("    %-5s (no shard finished yet)" % a.name)

    print()
    print("=" * 78)
    print("2. RESULT")
    print("=" * 78)
    print("%-6s %6s %5s %5s %5s %8s   %s" % ("arm", "games", "W", "L", "D", "score", "Elo [95% CI]"))
    for k in ("off", "soft"):
        a = arms[k]
        if not a.n:
            print("%-6s   (no games yet)" % a.name)
            continue
        lo, hi = a.p - 1.96 * a.se, a.p + 1.96 * a.se
        print("%-6s %6d %5d %5d %5d %7.2f%%   %s" %
              (a.name, a.n, a.w, a.l, a.d, 100 * a.p, fmt_elo(a.p, lo, hi)))

    o, s = arms["off"], arms["soft"]
    if o.n > 1 and s.n > 1:
        dp = s.p - o.p
        dse = math.sqrt(o.se ** 2 + s.se ** 2)
        print("\n  PAIRED DIFFERENCE (soft - off), the only quantity this design measures:")
        print("    score delta = %+.4f +/- %.4f (1 SE);  95%% CI [%+.4f, %+.4f]" %
              (dp, dse, dp - 1.96 * dse, dp + 1.96 * dse))
        # Elo-space delta, evaluated around the pooled score so the derivative is honest.
        pooled = (sum(o.scores) + sum(s.scores)) / (o.n + s.n)
        deriv = 400.0 / (math.log(10) * pooled * (1 - pooled))
        print("    ~= %+.1f Elo +/- %.1f (1 SE);  95%% CI [%+.1f, %+.1f]  (delta method at p=%.3f)" %
              (dp * deriv, dse * deriv, (dp - 1.96 * dse) * deriv, (dp + 1.96 * dse) * deriv, pooled))
        z = dp / dse if dse else 0.0
        print("    z = %+.2f" % z)

    print()
    print("=" * 78)
    print("3. AUDIT")
    print("=" * 78)
    for k in ("off", "soft"):
        a = arms[k]
        print("\n-- arm %s: %d games, %d own-moves --" % (a.name, a.n, a.moves))
        quirk = 0
        for (res, why), c in sorted(a.terms.items(), key=lambda kv: -kv[1]):
            tag = "  <-- HARNESS ARTIFACT" if why.startswith("quirk") else ""
            if why.startswith("quirk"):
                quirk += c
            print("   %-24s %-14s %4d%s" % (why, res, c, tag))
        tot = sum(a.terms.values())
        print("   %-24s %-14s %4d" % ("TOTAL classified", "", tot))
        if tot != a.n:
            print("   !! %d games have no TERMINATION line (unclassified)" % (a.n - tot))
        if a.n:
            print("   quirk-adjudications: %d/%d = %.1f%%" % (quirk, a.n, 100.0 * quirk / a.n))
        print("   CrossCheckHistory DESYNC lines: %d %s" %
              (a.desyncs, "(CLEAN)" if a.desyncs == 0 else "*** INVESTIGATE ***"))
        print("   'unreadable/illegal' markers:   %d" % a.unreadable)
        print("   SARGON MODE WRONG warnings:     %d %s" %
              (a.mode_warnings, "(clean)" if a.mode_warnings == 0 else "*** INVESTIGATE ***"))


if __name__ == "__main__":
    main()

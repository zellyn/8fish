#!/usr/bin/env python3
"""Analyse the paired soft-clock control with PONDERING REMOVED FROM BOTH SIDES.

Adapted from runs/softclock-paired/analyze.py. The accounting differs in one
load-bearing way, and it is the reason this script exists separately:

  With pondering ON, symmatch's spend ratio is 1.0 BY ALGEBRAIC IDENTITY:
        8fish  = own + ponder      ~= own + sargon_think
        sargon = own(mirror) + sargon_think
  Both sides really do burn those cycles, so the match is genuinely symmetric --
  but the ratio can NEVER reveal an imbalance in the two engines' OWN-MOVE
  spend, because the own-move term appears on both sides of the fraction.

  With -noponder both mirror terms vanish, the identity is gone, and the ratio
  becomes an actual measurement:
        8fish  = own            sargon = sargon_think
  So this script reports OWN-MOVE PARITY (8fish own / Sargon own) as the gate.

Order of business is unchanged: spend symmetry, then Elo, then the audit.
"""
import glob
import math
import os
import re
import sys
from collections import Counter

ROOT = os.path.dirname(os.path.abspath(__file__))
# Completeness is checked as "every shard PRESENT ran to SYMMATCH-DONE, and
# both arms scored the same number of games" -- NOT against a hardcoded shard
# count. The shard count is the launcher's business (SEEDS is overridable and
# defaults to 6), and hardcoding 12 here made a COMPLETE 252-games-per-arm run
# report "6/12 shards finished, RUN INCOMPLETE".
#
# This is deliberately not a relaxation. The real failure mode is a shard that
# DIED PART-WAY -- which is exactly what happened when an agent was killed
# mid-gauntlet -- and a truncated shard is caught here in two ways a shard
# count would miss: it never prints SYMMATCH-DONE, and it leaves the arms with
# unequal game counts, which breaks the pairing the whole design rests on.
MIN_GAMES_PER_ARM = 200  # below this the interval is too wide to be worth quoting


def elo(p):
    if p <= 0:
        return float("-inf")
    if p >= 1:
        return float("inf")
    return -400.0 * math.log10(1.0 / p - 1.0)


class Arm:
    def __init__(self, name):
        self.name = name
        self.scores = []
        self.w = self.l = self.d = 0
        self.f_own = self.f_pon = self.s_own = self.s_window = 0
        self.ponders = 0
        self.moves = 0
        self.intended = 0
        self.terms = Counter()
        self.desyncs = 0
        self.unreadable = 0
        self.mode_warnings = 0
        self.shards = 0
        self.finished = 0
        self.banner = set()
        self.sess_fish = self.sess_sargon = 0
        self.bank_income = self.bank_spent = 0
        self.long_games = 0      # games past move 99 (Sargon move-column quirk)
        self.max_plies = 0

    def add_log(self, path):
        self.shards += 1
        txt = open(path, errors="replace").read()
        for m in re.finditer(r"clock=(.+?) ===", txt):
            self.banner.add(m.group(1))
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
        for m in re.finditer(r"TERMINATION g\d+ result=(\S+) reason=(\S+) plies=(\d+)", txt):
            self.terms[(m.group(1), m.group(2))] += 1
            plies = int(m.group(3))
            self.max_plies = max(self.max_plies, plies)
            if plies > 198:          # 99 full moves
                self.long_games += 1
        self.desyncs += len(re.findall(r"^DESYNC ", txt, re.M))
        self.unreadable += len(re.findall(r"unreadable/illegal", txt))
        self.mode_warnings += len(re.findall(r"SARGON MODE WRONG", txt))
        for m in re.finditer(r"^BANK g\d+ .* income=(\d+) alloc=\d+ spent=(\d+) ", txt, re.M):
            self.bank_income += int(m.group(1))
            self.bank_spent += int(m.group(2))
        # Per-move re-derivation, independent of the harness's accumulators.
        for m in re.finditer(
            r"^MOVE g\d+ ply\d+ 8fish move=\S+ think=(\d+) \| sargon_ponder_window=(\d+) \|\| "
            r"ply\d+ SARGON move=\S+ think=(\d+) \| 8fish_ponder=(\d+) ", txt, re.M):
            own, window, sthink, pon = (int(g) for g in m.groups())
            self.f_own += own
            self.s_own += sthink
            self.f_pon += pon
            self.s_window += window
            if pon:
                self.ponders += 1
        for m in re.finditer(r"^MOVE g\d+ ply1 SARGON\(open\) move=\S+ think=(\d+)", txt, re.M):
            self.s_own += int(m.group(1))
        for m in re.finditer(r"^MOVE g\d+ ply\d+ 8fish move=\S+ think=(\d+) -> ", txt, re.M):
            self.f_own += int(m.group(1))
        for m in re.finditer(
            r"SYMMATCH-SPEND-SESSION-SUMMARY games=\d+ 8fish_total=(\d+) sargon_total=(\d+)", txt):
            self.sess_fish += int(m.group(1))
            self.sess_sargon += int(m.group(2))
            self.finished += 1
        for m in re.finditer(
            r"SYMMATCH-TIME-SESSION-SUMMARY games=\d+ moves=(\d+) own_total=\d+ own_intended=(\d+)", txt):
            self.moves += int(m.group(1))
            self.intended += int(m.group(2))

    @property
    def n(self):
        return len(self.scores)

    @property
    def p(self):
        return sum(self.scores) / self.n if self.n else 0.0

    @property
    def se(self):
        if self.n < 2:
            return float("inf")
        mu = self.p
        var = sum((s - mu) ** 2 for s in self.scores) / (self.n - 1)
        return math.sqrt(var / self.n)

    def fish_total(self):
        return self.f_own + self.f_pon

    def sargon_total(self):
        return self.s_own + self.s_window

    def spend_ratio(self):
        s = self.sargon_total()
        return self.fish_total() / s if s else float("nan")

    def own_parity(self):
        return self.f_own / self.s_own if self.s_own else float("nan")

    def adherence(self):
        return self.bank_spent / self.bank_income if self.bank_income else float("nan")


def fmt_elo(p, lo, hi):
    def e(x):
        v = elo(x)
        return "%+.0f" % v if abs(v) != float("inf") else ("+inf" if v > 0 else "-inf")
    return "%s  [%s, %s]" % (e(p), e(lo), e(hi))


def main():
    partial_ok = "--partial" in sys.argv
    arms = {}
    for arm in ("off", "soft"):
        a = Arm(arm)
        for d in sorted(glob.glob(os.path.join(ROOT, arm + "-*"))):
            f = os.path.join(d, "symmatch.log")
            if os.path.exists(f):
                a.add_log(f)
        arms[arm] = a

    # A shard is "present" if it has a log at all; "finished" if that log
    # carried a session summary. Any present-but-unfinished shard is a
    # truncated run and disqualifies the Elo.
    present = {k: len([d for d in sorted(glob.glob(os.path.join(ROOT, k + "-*")))
                       if os.path.exists(os.path.join(d, "symmatch.log"))])
               for k in ("off", "soft")}
    done = (all(a.finished == present[k] and a.finished > 0
                for k, a in arms.items())
            and arms["off"].n == arms["soft"].n
            and min(a.n for a in arms.values()) >= MIN_GAMES_PER_ARM)
    print("=" * 78)
    print("0. COMPLETENESS  (a partial gauntlet is biased: short decisive games")
    print("   finish first, and the soft arm draws more)")
    print("=" * 78)
    for k in ("off", "soft"):
        a = arms[k]
        print("   arm %-5s %2d/%2d shards finished, %4d games scored"
              % (a.name, a.finished, present[k], a.n))
    if arms["off"].n != arms["soft"].n:
        print("   *** ARMS UNEQUAL (%d vs %d): the pairing is broken ***"
              % (arms["off"].n, arms["soft"].n))
    if not done and not partial_ok:
        print("\n   *** RUN INCOMPLETE — refusing to print Elo. Re-run with --partial")
        print("       to see interim spend/audit numbers only. ***")
        return

    print()
    print("=" * 78)
    print("1. SPEND SYMMETRY  (check this before reading any Elo)")
    print("=" * 78)
    print("%-6s %7s %12s %12s %11s %12s %10s" %
          ("arm", "shards", "8fish Gcyc", "sargon Gcyc", "spend_rt", "own_parity", "adherence"))
    for k in ("off", "soft"):
        a = arms[k]
        print("%-6s %7d %12.1f %12.1f %11.4f %12.4f %10.4f" %
              (a.name, a.shards, a.fish_total() / 1e9, a.sargon_total() / 1e9,
               a.spend_ratio(), a.own_parity(), a.adherence()))
    for k in ("off", "soft"):
        a = arms[k]
        if a.f_pon or a.s_window:
            print("  !! arm %s: ponder cycles present (8fish_ponder=%d sargon_window=%d)"
                  " — -noponder was NOT in effect" % (a.name, a.f_pon, a.s_window))
        else:
            print("   arm %-5s ponder cycles: 8fish=0 sargon_window=0  (no-ponder confirmed)"
                  % a.name)
    d = arms["soft"].spend_ratio() - arms["off"].spend_ratio()
    print("\n   soft - off spend_ratio delta: %+.4f (%+.2f%% of compute)" % (d, d * 100))
    print("   banners: off=%s | soft=%s"
          % (sorted(arms["off"].banner), sorted(arms["soft"].banner)))
    print("\n   cross-check (per-move re-derivation vs harness accumulators):")
    for k in ("off", "soft"):
        a = arms[k]
        print("     %-5s re-derived 8fish=%.1f sargon=%.1f | harness 8fish=%.1f sargon=%.1f Gcyc"
              % (a.name, a.fish_total() / 1e9, a.sargon_total() / 1e9,
                 a.sess_fish / 1e9, a.sess_sargon / 1e9))

    print()
    print("=" * 78)
    print("2. RESULT")
    print("=" * 78)
    print("%-6s %6s %5s %5s %5s %8s   %s" % ("arm", "games", "W", "L", "D", "score", "Elo [95% CI]"))
    for k in ("soft", "off"):
        a = arms[k]
        if not a.n:
            continue
        lo, hi = a.p - 1.96 * a.se, a.p + 1.96 * a.se
        print("%-6s %6d %5d %5d %5d %7.2f%%   %s" %
              (a.name, a.n, a.w, a.l, a.d, 100 * a.p, fmt_elo(a.p, lo, hi)))

    o, s = arms["off"], arms["soft"]
    pooled = ((sum(o.scores) + sum(s.scores)) / (o.n + s.n)) if (o.n + s.n) else 0.0
    if o.n > 1 and s.n > 1 and 0.0 < pooled < 1.0:
        dp = s.p - o.p
        dse = math.sqrt(o.se ** 2 + s.se ** 2)
        deriv = 400.0 / (math.log(10) * pooled * (1 - pooled))
        print("\n   CLOCK COST (soft - off), ponder removed from both sides:")
        print("     score delta = %+.4f +/- %.4f (1 SE); 95%% CI [%+.4f, %+.4f]" %
              (dp, dse, dp - 1.96 * dse, dp + 1.96 * dse))
        print("     ~= %+.1f Elo +/- %.1f (1 SE); 95%% CI [%+.1f, %+.1f]  (delta method at p=%.3f)" %
              (dp * deriv, dse * deriv, (dp - 1.96 * dse) * deriv, (dp + 1.96 * dse) * deriv, pooled))
        print("     z = %+.2f" % (dp / dse if dse else 0.0))

    print()
    print("=" * 78)
    print("3. AUDIT")
    print("=" * 78)
    for k in ("soft", "off"):
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
        print("   Hard+LEVEL 9 violations:        %d %s" %
              (a.mode_warnings, "(all games verified Hard + LEVEL 9)"
               if a.mode_warnings == 0 else "*** INVESTIGATE ***"))
        print("   games past move 99 (Sargon move-column quirk zone): %d, longest %d plies"
              % (a.long_games, a.max_plies))


if __name__ == "__main__":
    main()

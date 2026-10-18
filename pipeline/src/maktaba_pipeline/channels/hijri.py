"""The Hijri date of a Gregorian day: the tabular (arithmetical) Islamic calendar.

The tabular calendar (30-year cycle, leap years 2, 5, 7, 10, 13, 16, 18, 21, 24, 26, 29,
the civil epoch 16 July 622) agrees with Umm al-Qura and the sighted months to within a
day or so; the ``tv`` rule ``hijri_adjust_days`` moves it where the month starts a day
earlier or later. Pure: no tables, no dependencies.
"""

from __future__ import annotations

from datetime import date, timedelta

__all__ = ["RAMADAN", "hijri", "in_hijri_month"]

RAMADAN = 9

# Julian day number of 0001-01-01 (proleptic Gregorian) minus date.toordinal()'s 1
_JDN_OFFSET = 1_721_425
# the civil epoch: 1 Muharram 1 AH = Friday 16 July 622 (Julian) = JDN 1948440
_EPOCH = 1_948_440


def hijri(day: date, *, adjust_days: int = 0) -> tuple[int, int, int]:
    """``(year, month, day)`` of the Hijri date of ``day`` (``adjust_days`` added first)."""
    jdn = (day + timedelta(days=adjust_days)).toordinal() + _JDN_OFFSET
    n = jdn - _EPOCH  # days since 1 Muharram 1
    cycle, rest = divmod(n, 10_631)  # 30-year cycles of 10631 days
    year_in = min(29, (30 * rest + 15) // 10_631)  # the year inside the cycle (0..29)
    while year_in > 0 and _days_before_year(year_in) > rest:
        year_in -= 1
    while year_in < 29 and _days_before_year(year_in + 1) <= rest:
        year_in += 1
    doy = rest - _days_before_year(year_in)  # 0-based day of the year
    month = min(12, doy // 29 + 1)
    while month > 1 and _days_before_month(month) > doy:
        month -= 1
    while month < 12 and _days_before_month(month + 1) <= doy:
        month += 1
    return 30 * cycle + year_in + 1, month, doy - _days_before_month(month) + 1


def in_hijri_month(day: date, month: int, *, adjust_days: int = 0) -> bool:
    """Whether ``day`` falls in Hijri month ``month`` (9: Ramadan)."""
    return hijri(day, adjust_days=adjust_days)[1] == month


def _days_before_year(y: int) -> int:
    """Days in the first ``y`` years of a 30-year cycle (354 each, 11 leap days)."""
    return 354 * y + (11 * y + 14) // 30


def _days_before_month(m: int) -> int:
    """Days before month ``m`` (1-based): odd months 30 days, even ones 29."""
    return 29 * (m - 1) + m // 2

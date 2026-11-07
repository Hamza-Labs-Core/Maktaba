---
schema: 1
kind: pr
pr: 794
title: Both components test folders collect in one pytest session
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 670
---

## What happened
- The repo root's `tests/` and `pipeline/tests/` were both the package `tests`. Their `components` folders clashed in any one session that collected both.
- The root `tests/` lost its `__init__.py`. Its folders are now the packages `components`, `e2e` and `perf`, and a test collects both components folders together.

## What we learned
- The issue named the conftests, but they were only the first symptom. With the root conftest moved aside, the root's test modules could not be imported either. Python loads only one top-level `tests` package per session, so two repos-in-one with a `tests` package each cannot share one session.
- A zsh loop over a list of argument strings passes each string as one argument (zsh does not split words). The first check of the four CI commands "collected" nothing or everything, for that reason. Scripts that loop over lists run in bash.

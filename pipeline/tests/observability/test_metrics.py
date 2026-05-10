"""Metric registry + Prometheus text rendering tests (Story 6.9)."""

from __future__ import annotations

from maktaba_pipeline.observability import (
    DEFAULT_HISTOGRAM_BUCKETS,
    JOB_STAGES,
    JOB_STATES,
    JobAttemptOutcome,
    MetricRegistry,
    render_prometheus_text,
)


def test_counter_increments_and_labels_are_keyed() -> None:
    reg = MetricRegistry()
    reg.record_attempt(stage="transcribe", outcome=JobAttemptOutcome.DONE)
    reg.record_attempt(stage="transcribe", outcome=JobAttemptOutcome.DONE)
    reg.record_attempt(stage="transcribe", outcome=JobAttemptOutcome.FAILED)

    assert reg.job_attempts_total.value(stage="transcribe", outcome="done") == 2
    assert reg.job_attempts_total.value(stage="transcribe", outcome="failed") == 1
    assert reg.job_attempts_total.value(stage="transcribe", outcome="paused") == 0


def test_record_attempt_observes_duration() -> None:
    reg = MetricRegistry()
    reg.record_attempt(stage="probe", outcome="done", duration_sec=0.4)
    reg.record_attempt(stage="probe", outcome="done", duration_sec=2.5)

    h = reg.job_duration_seconds
    # Two observations land in count.
    assert h.count(stage="probe", outcome="done") == 2
    # Sum is the total wall-clock observed.
    assert abs(h.sum(stage="probe", outcome="done") - 2.9) < 1e-9
    # The 0.4 falls into the 0.5 bucket and all higher buckets; the
    # 2.5 falls into the 5 bucket and all higher buckets.
    samples = h.samples()
    assert len(samples) == 1
    _key, counts, total_sum, total_count = samples[0]
    # Bucket 0 = upper bound 0.5: only the 0.4 lands.
    assert counts[0] == 1
    # Bucket 1 = upper bound 1: only the 0.4 still.
    assert counts[1] == 1
    # Bucket 2 = upper bound 5: both observations.
    assert counts[2] == 2
    assert total_count == 2


def test_summary_observes_realtime_factor() -> None:
    reg = MetricRegistry()
    reg.record_progress(stage="transcribe", realtime_factor=0.2)
    reg.record_progress(stage="transcribe", realtime_factor=0.4)
    reg.record_progress(stage="transcribe", realtime_factor=0.6)

    s = reg.job_realtime_factor
    assert s.count(stage="transcribe") == 3
    assert abs(s.sum(stage="transcribe") - 1.2) < 1e-9


def test_jobs_total_gauge_set() -> None:
    reg = MetricRegistry()
    reg.set_jobs_count(stage="transcribe", state="running", n=3)
    reg.set_jobs_count(stage="transcribe", state="running", n=7)  # overwrites

    assert reg.jobs_total.value(stage="transcribe", state="running") == 7


def test_render_prometheus_text_has_all_required_metrics() -> None:
    reg = MetricRegistry()
    reg.set_jobs_count(stage="probe", state="running", n=2)
    reg.record_attempt(stage="probe", outcome="done", duration_sec=1.5)
    reg.record_progress(stage="transcribe", realtime_factor=0.3)

    text = render_prometheus_text(reg)

    assert "# TYPE maktaba_jobs_total gauge" in text
    assert "# TYPE maktaba_job_attempts_total counter" in text
    assert "# TYPE maktaba_job_duration_seconds histogram" in text
    assert "# TYPE maktaba_job_realtime_factor summary" in text

    assert 'maktaba_jobs_total{stage="probe",state="running"} 2.0' in text
    assert 'maktaba_job_attempts_total{stage="probe",outcome="done"} 1.0' in text
    assert 'maktaba_job_realtime_factor_count{stage="transcribe"} 1' in text


def test_histogram_emits_one_bucket_per_boundary_plus_inf() -> None:
    reg = MetricRegistry()
    reg.record_attempt(stage="extract", outcome="done", duration_sec=10.0)

    text = render_prometheus_text(reg)

    # Each declared bucket boundary appears, plus +Inf, plus _sum + _count.
    bucket_lines = [
        line for line in text.splitlines() if line.startswith("maktaba_job_duration_seconds_bucket")
    ]
    assert len(bucket_lines) == len(DEFAULT_HISTOGRAM_BUCKETS) + 1


def test_canonical_label_sets_match_schema() -> None:
    # The metric system's canonical sets must match the DB schema's
    # CHECK constraints (Story 6.1) so a new stage or state lights up
    # one obvious failure point.
    assert JOB_STAGES == (
        "scan",
        "probe",
        "extract",
        "transcribe",
        "subtitle_gen",
        "index",
        "thumbnail",
    )
    assert set(JOB_STATES) == {
        "pending",
        "claimed",
        "running",
        "resuming",
        "paused",
        "done",
        "failed",
        "cancelled",
    }

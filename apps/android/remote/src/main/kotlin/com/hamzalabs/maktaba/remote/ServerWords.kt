package com.hamzalabs.maktaba.remote

// The codes the home sends for its work, in this app's words: a stage's id, a running job's step
// and its subject's kind, an update's install kind, what installed a machine's last update, the
// update channel. Each code has its words in English and Arabic (res/values*/strings.xml): the web's
// where it has them (w_ and its key, from web/src/i18n/en.json and ar.json), so the Remote says what
// the web says. Only a code this app has no words for (a newer home's, a plugin's) shows as the home
// sent it: its label, else the code with its underscores as spaces. ServerWordsTest holds every
// stage of the pipeline's graph, every step the stages report and every cause the home groups
// failures by to having words in both languages.

/**
 * `queue.stage.<id>.title`: every stage the pipeline's graph ships (engine/graph.py, with the
 * stages its first-party plugins declare in their manifests), as web/src/lib/stageWords.ts.
 */
internal val stageWords: Map<String, Int> = mapOf(
    "youtube_sync" to R.string.w_queue_stage_youtube_sync_title,
    "exact_dates" to R.string.w_queue_stage_exact_dates_title,
    "content_hash" to R.string.w_queue_stage_content_hash_title,
    "probe" to R.string.w_queue_stage_probe_title,
    "thumb_hash" to R.string.w_queue_stage_thumb_hash_title,
    "link" to R.string.w_queue_stage_link_title,
    "fingerprint" to R.string.w_queue_stage_fingerprint_title,
    "style_sample" to R.string.w_queue_stage_style_sample_title,
    "thumb_small" to R.string.w_queue_stage_thumb_small_title,
    "detect" to R.string.w_queue_stage_detect_title,
    "fp_gate" to R.string.w_queue_stage_fp_gate_title,
    "tag" to R.string.w_queue_stage_tag_title,
    "check_tags" to R.string.w_queue_stage_check_tags_title,
    "check_clips" to R.string.w_queue_stage_check_clips_title,
    "display_title" to R.string.w_queue_stage_display_title_title,
    "books" to R.string.w_queue_stage_books_title,
    "topic_collections" to R.string.w_queue_stage_topic_collections_title,
    "promo_links" to R.string.w_queue_stage_promo_links_title,
    "clip_groups" to R.string.w_queue_stage_clip_groups_title,
    "rate_style" to R.string.w_queue_stage_rate_style_title,
    "review_pages" to R.string.w_queue_stage_review_pages_title,
    "plan_channels" to R.string.w_queue_stage_plan_channels_title,
    "write_channels" to R.string.w_queue_stage_write_channels_title,
    "plex_livetv" to R.string.w_queue_stage_plex_livetv_title,
    "tv_stage" to R.string.w_queue_stage_tv_stage_title,
    "tv_swap" to R.string.w_queue_stage_tv_swap_title,
    "tv_gc" to R.string.w_queue_stage_tv_gc_title,
    "tv_rollback" to R.string.w_queue_stage_tv_rollback_title,
    "scan" to R.string.w_queue_stage_scan_title,
    "extract" to R.string.w_queue_stage_extract_title,
    "transcribe" to R.string.w_queue_stage_transcribe_title,
    "second_listen" to R.string.w_queue_stage_second_listen_title,
    "subtitle_gen" to R.string.w_queue_stage_subtitle_gen_title,
    "index" to R.string.w_queue_stage_index_title,
    "chapter_infer" to R.string.w_queue_stage_chapter_infer_title,
    "chapter_thumbs" to R.string.w_queue_stage_chapter_thumbs_title,
    "topic_assign" to R.string.w_queue_stage_topic_assign_title,
    "topic_recluster" to R.string.w_queue_stage_topic_recluster_title,
    "thumbnail" to R.string.w_queue_stage_thumbnail_title,
    "repair_broken" to R.string.w_queue_stage_repair_broken_title,
    "storage_canonicalize" to R.string.w_queue_stage_storage_canonicalize_title,
    "component_catchup" to R.string.w_queue_stage_component_catchup_title,
    "access_check" to R.string.w_queue_stage_access_check_title,
    "categorize" to R.string.w_queue_stage_categorize_title,
    "enrich" to R.string.w_queue_stage_enrich_title,
    "convert_mp4" to R.string.w_queue_stage_convert_mp4_title,
    "dedup_scan" to R.string.w_queue_stage_dedup_scan_title,
    "dedup_apply" to R.string.w_queue_stage_dedup_apply_title,
    "trash_purge" to R.string.w_queue_stage_trash_purge_title,
    "file_restore" to R.string.w_queue_stage_file_restore_title,
    "youtube_fetch" to R.string.w_queue_stage_youtube_fetch_title,
    "youtube_backfill" to R.string.w_queue_stage_youtube_backfill_title,
    "youtube_tartube" to R.string.w_queue_stage_youtube_tartube_title,
    "youtube_collections" to R.string.w_queue_stage_youtube_collections_title,
    "word_check" to R.string.w_queue_stage_word_check_title,
    "content_screen" to R.string.w_queue_stage_content_screen_title,
    "keyword_index" to R.string.w_queue_stage_keyword_index_title,
)

/**
 * A running job's step (`ctx.progress("<step>", ...)` in the pipeline and its first-party
 * plugins; a step named with spaces is keyed with underscores). A step that is a stage's own name
 * ("transcribe") takes the stage's title.
 */
internal val jobStepWords: Map<String, Int> = mapOf(
    "ask" to R.string.job_step_ask,
    "catch_up" to R.string.job_step_catch_up,
    "cluster" to R.string.job_step_cluster,
    "convert" to R.string.job_step_convert,
    "download" to R.string.job_step_download,
    "embed" to R.string.job_step_embed,
    "exact_dates" to R.string.job_step_exact_dates,
    "fan_out" to R.string.job_step_fan_out,
    "frames" to R.string.job_step_frames,
    "full_hash" to R.string.job_step_full_hash,
    "listing" to R.string.job_step_listing,
    "matching" to R.string.job_step_matching,
    "plan" to R.string.job_step_plan,
    "plugin" to R.string.job_step_plugin,
    "purge" to R.string.job_step_purge,
    "read_vectors" to R.string.job_step_read_vectors,
    "render" to R.string.job_step_render,
    "repair" to R.string.job_step_repair,
    "restore" to R.string.job_step_restore,
    "sources" to R.string.job_step_sources,
    "swap" to R.string.job_step_swap,
    "verify" to R.string.job_step_verify,
    "waiting" to R.string.job_step_waiting,
)

/** `queue.subject.*`: what a job works on. */
internal val subjectWords: Map<String, Int> = mapOf(
    "video" to R.string.w_queue_subject_video,
    "library" to R.string.w_queue_subject_library,
    "series" to R.string.w_queue_subject_series,
    "season" to R.string.w_queue_subject_season,
    "catalog" to R.string.w_queue_subject_catalog,
)

/** `updates.kind.*`: how a machine's Maktaba is installed (an update target's kind). */
internal val installKindWords: Map<String, Int> = mapOf(
    "layout" to R.string.w_updates_kind_layout,
    "runtime" to R.string.w_updates_kind_runtime,
    "docker" to R.string.w_updates_kind_docker,
    "appbundle" to R.string.w_updates_kind_appbundle,
    "flat" to R.string.w_updates_kind_flat,
)

/** `updates.source.*`: what installed a machine's last update. */
internal val updateSourceWords: Map<String, Int> = mapOf(
    "app" to R.string.w_updates_source_app,
    "server" to R.string.w_updates_source_server,
    "run" to R.string.w_updates_source_run,
)

/** `update.channel.*`: the releases the home follows. */
internal val channelWords: Map<String, Int> = mapOf(
    "stable" to R.string.w_update_channel_stable,
    "beta" to R.string.w_update_channel_beta,
)

/** Why a stage is switched off (job_stages.disabled_reason), as the web's queue says it. */
internal val disabledWords: Map<String, Int> = mapOf(
    "operator" to R.string.w_queue_waiting_disabled,
    "not_configured" to R.string.w_queue_waiting_not_configured,
)

/** A stage's title: its words, else the [label] the home sent, else its id in words. */
internal fun Say.stageTitle(stage: String, label: String? = null): String =
    stageWords[stage]?.let { t(it) } ?: label?.takeIf { it.isNotBlank() } ?: Say.spaced(stage)

/** A running job's step: its words, a stage's title (a stage reports its own name), else as sent. */
internal fun Say.jobStep(step: String): String {
    val code = step.trim().replace(' ', '_')
    jobStepWords[code]?.let { return t(it) }
    stageWords[code]?.let { return t(it) }
    return Say.spaced(step)
}

/** What a job works on ("video"), in words. */
internal fun Say.subjectKind(kind: String): String = subjectWords[kind]?.let { t(it) } ?: Say.spaced(kind)

/** How a machine is installed ("appbundle": "desktop app"). */
internal fun Say.installKind(kind: String): String = installKindWords[kind]?.let { t(it) } ?: Say.spaced(kind)

/** What installed a machine's last update ("run": "the cluster's update"). */
internal fun Say.updateSource(source: String): String = updateSourceWords[source]?.let { t(it) } ?: Say.spaced(source)

/** Why a stage is off, in words; null for a component's hold (the stage's waiting says it). */
internal fun Say.disabledReason(reason: String): String? = when (reason) {
    "component_off", "component_held" -> null
    else -> disabledWords[reason]?.let { t(it) } ?: Say.spaced(reason)
}

/**
 * Automatic updates as the web says them: on, the channel and the quiet window ("03:00-05:00",
 * kept left to right in an Arabic line), else paused.
 */
internal fun Say.autoUpdates(on: Boolean, channel: String, window: String): String {
    if (!on) return t(R.string.auto_paused)
    val ch = channelWords[channel]?.let { t(it) } ?: Say.spaced(channel)
    if (ch.isEmpty()) return t(R.string.auto_on)
    val ends = window.split('-', '–').map { it.trim() }.filter { it.isNotEmpty() }
    if (ends.size == 2) return t(R.string.w_updates_home_autoOn, ch, "\u2066${ends[0]}", "${ends[1]}\u2069")
    return t(R.string.w_updates_home_autoOnNoWindow, ch)
}

/**
 * The machine an update step ran on, by its name among the run's machines ([names], by node id);
 * null for the home's own steps (the Steps are the home's unless a line says otherwise).
 */
internal fun stepHost(host: String, names: Map<String, String>): String? = when {
    host.isEmpty() || host == "home" -> null
    else -> names[host]?.takeIf { it.isNotEmpty() } ?: host.take(8)
}

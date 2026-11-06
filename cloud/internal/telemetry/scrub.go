package telemetry

import mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"

// Scrub is the cloud's own pass over free text (error messages and stack frames). Homes and
// apps scrub before sending; this pass assumes they missed something. It removes URLs,
// e-mail addresses, home folders, the directories of absolute paths (and the whole path
// unless its file is source code), IP addresses, host names and query strings. It is the
// shared grouping pass (shared/log/go GroupScrub, Epic 53 story 53.10): the home folds an
// error the same way.
func Scrub(s string) string { return mlog.GroupScrub(s) }

package main

import (
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/logs"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

// wireLogNotices is how the logs reach the admins (Epic 53): new and returning error groups
// (the errors_notifier loop of logs.index, story 53.10) and new problem reports (53.15).
// wireReportsForwarder declares the problem reports' forwarder to Maktaba (story 53.16): a
// loop of reports.share, so it runs only while the admin's setting is on (and cloud.link);
// off, nothing leaves the home.
func wireReportsForwarder(loops *runner.Runner, fw *logs.Forwarder, logger *slog.Logger) {
	fw.Log = logger
	loops.Declare("reports_forwarder", runner.Func(fw.Run))
}

func wireLogNotices(loops *runner.Runner, hub notify.Notifier, logger *slog.Logger) {
	loops.Declare("errors_notifier", runner.Func(logs.NewErrorNotifier(hub, logger).Run))
	logs.SetReportNotifier(hub)
}

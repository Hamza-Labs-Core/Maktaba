package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail/outbox"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
)

// mailDriver is the configured driver, kept to the deployment's recipients. fake stands in for
// the driver in tests (nil in production).
func mailDriver(cfg config.Config, logger *slog.Logger, fake mailpkg.Mailer) mailpkg.Mailer {
	var m mailpkg.Mailer = mailpkg.LogMailer{Logger: logger}
	switch {
	case fake != nil:
		m = fake
	case cfg.Mail.Delivers():
		m = mailpkg.NewResend(cfg.Mail.ResendKey, cfg.Mail.From, cfg.Mail.ReplyTo, 0)
	}
	return mailpkg.Guard{Next: m, Allow: cfg.Mail.AllowRecipients, NoReserved: cfg.Mail.Delivers(), Prefix: cfg.Mail.SubjectPrefix}
}

// mailOutbox is the outbox over the configured driver. The limits are a sending deployment's;
// the log costs nothing and has none.
func mailOutbox(cfg config.Config, pool *db.Pool, logger *slog.Logger, fake mailpkg.Mailer) (*outbox.Outbox, error) {
	var limits outbox.Limits
	if cfg.Mail.Delivers() {
		limits = outbox.DefaultLimits(cfg.Mail.DailyLimit, cfg.Mail.MonthlyLimit)
	}
	o, err := outbox.New(pool.DB, cfg.Auth.TokenSecret, mailDriver(cfg, logger, fake),
		mailpkg.Templates{WebURL: cfg.Server.WebURL}, limits, logger)
	if err != nil {
		return nil, err
	}
	o.Logs = !cfg.Mail.Delivers()
	return o, nil
}

// workerMailer puts the worker's notices in the outbox, which the api role sends. Without the
// token secret (only with the log driver: config.Validate) they go to the log.
func workerMailer(cfg config.Config, pool *db.Pool, logger *slog.Logger) mailpkg.Mailer {
	o, err := mailOutbox(cfg, pool, logger, nil)
	if err != nil {
		return mailpkg.LogMailer{Logger: logger}
	}
	return o
}

// sendNotice mails an account one of the notices, in its language.
func sendNotice(ctx context.Context, m mailpkg.Mailer, cfg config.Config, locale, to, kind string) error {
	msg, ok := mailpkg.Templates{WebURL: cfg.Server.WebURL}.Notice(mailpkg.Pick(locale), to, kind)
	if !ok {
		return fmt.Errorf("mail: no notice %q", kind)
	}
	return m.Send(ctx, msg)
}

func mailStatus(cfg config.Config) server.MailStatus {
	s := server.MailStatus{Driver: cfg.Mail.Driver, Delivers: cfg.Mail.Delivers(), Recipients: "all"}
	if len(cfg.Mail.AllowRecipients) > 0 {
		s.Recipients = "listed"
	}
	return s
}

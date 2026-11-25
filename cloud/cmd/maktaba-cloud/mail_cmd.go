package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail/outbox"
)

const mailUsage = `usage: maktaba-cloud mail <command> [--config path]
  drivers             the mail drivers this release has
  status              how mails go (driver, recipients, limits) and the last 30 days' by state
  test [--lang en|ar] send a test mail to the address on stdin, and wait for its outcome`

// runMail is the operator's look at the mails. It prints no address, key or content, and reads
// an address from stdin only.
func runMail(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, mailUsage)
		os.Exit(2)
	}
	sub := args[0]
	if sub == "drivers" {
		// what a deploy asks of the release's image before it writes one in cloud.toml (setup.sh)
		fmt.Fprintln(os.Stdout, config.MailLog)
		fmt.Fprintln(os.Stdout, config.MailResend)
		return
	}
	fs := flag.NewFlagSet("mail "+sub, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cloud.toml")
	lang := fs.String("lang", mailpkg.LangEN, "the test mail's language: en or ar")
	wait := fs.Duration("wait", time.Minute, "how long to wait for the test mail's outcome")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(2)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mail: config:", err)
		os.Exit(2)
	}
	pool, err := db.Open(cfg.Database.URL, 2, 1, time.Minute)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mail: database:", err)
		os.Exit(1)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *wait+30*time.Second)
	defer cancel()
	c := mailCmd{Cfg: cfg, Pool: pool, Out: os.Stdout, In: os.Stdin, Wait: *wait}
	switch sub {
	case "status":
		err = c.status(ctx)
	case "test":
		err = c.test(ctx, *lang)
	default:
		err = errors.New(mailUsage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mail "+sub+":", err)
		os.Exit(1)
	}
}

// mailCmd carries out one mail subcommand; tests drive it directly.
type mailCmd struct {
	Cfg  config.Config
	Pool *db.Pool
	In   io.Reader
	Out  io.Writer
	Wait time.Duration
}

func (c mailCmd) status(ctx context.Context) error {
	counts, err := outbox.Count(ctx, c.Pool.DB)
	if err != nil {
		return err
	}
	st := mailStatus(c.Cfg)
	key := "not needed"
	if c.Cfg.Mail.Delivers() {
		key = "set"
		if _, err := c.Cfg.Mail.ResendKey(); err != nil {
			key = "missing"
		}
	}
	enc := json.NewEncoder(c.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"driver": st.Driver, "delivers": st.Delivers, "recipients": st.Recipients, "api_key": key,
		"limits": map[string]int{"day": c.Cfg.Mail.DailyLimit, "month": c.Cfg.Mail.MonthlyLimit}, "mails": counts,
	})
}

func (c mailCmd) test(ctx context.Context, lang string) error {
	if mailpkg.Lang(lang) == "" {
		return errors.New("--lang is en or ar")
	}
	line, err := bufio.NewReader(io.LimitReader(c.In, 1024)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	to := strings.TrimSpace(line)
	if mailpkg.ParseAllow(to) != nil || strings.HasPrefix(to, "@") {
		return errors.New("give the address on stdin")
	}
	// the api role sends it: what is tested is the running cloud's mail
	o, err := mailOutbox(c.Cfg, c.Pool, newLogger("mail"), nil)
	if err != nil {
		return err
	}
	id, err := o.Keep(ctx, mailpkg.Templates{WebURL: c.Cfg.Server.WebURL}.Test(mailpkg.Pick(lang), to))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.Out, "mail %s is in the outbox\n", id); err != nil {
		return err
	}
	for end := time.Now().Add(c.Wait); time.Now().Before(end); time.Sleep(time.Second) {
		state, why, err := outbox.State(ctx, c.Pool.DB, id)
		if err != nil {
			return err
		}
		switch state {
		case outbox.StateSent:
			_, err = fmt.Fprintln(c.Out, "sent")
			return err
		case outbox.StateLogged:
			_, err = fmt.Fprintln(c.Out, "written to the api's log (driver log: nothing is delivered)")
			return err
		case outbox.StateReady, outbox.StateSending:
			if why != "" {
				return fmt.Errorf("not sent yet: %s; it is tried again", why)
			}
		default:
			return fmt.Errorf("%s %s", state, why)
		}
	}
	return errors.New("nothing has taken it: is the api role running, or a limit reached (mail status)?")
}

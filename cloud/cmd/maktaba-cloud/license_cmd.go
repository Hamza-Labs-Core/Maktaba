package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/entitlement"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
)

const licenseUsage = `usage:
  maktaba-cloud license issue    --tier home|pro [--seats N] [--days N] [--holder text] (--key-stdin | --config path)
  maktaba-cloud license pubkey   (--key-stdin | --config path)
  maktaba-cloud license revoke   --id ID --reason text [--config path]
  maktaba-cloud license unrevoke --id ID --reason text [--config path]

issue prints a signed offline license (JSON) on stdout, for any home:
paste it in the home's Settings → Subscription. With --key-stdin the
signing key is read from stdin (pipe it from a keychain; it is never
written anywhere) and the issue is not recorded; with --config the
cloud's entitlement key signs and the cloud database records it.
pubkey prints the key's public half as the PEM release builds embed.`

// runLicense is the operator's license tool (Epic 16 story 16.4).
func runLicense(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, licenseUsage)
		os.Exit(2)
	}
	if err := licenseCmd(context.Background(), args[0], args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "license "+args[0]+":", err)
		os.Exit(1)
	}
}

func licenseCmd(ctx context.Context, sub string, args []string, stdin io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("license "+sub, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cloud.toml (the cloud's key and database)")
	keyStdin := fs.Bool("key-stdin", false, "read the signing key from stdin")
	tier := fs.String("tier", "", "home | pro")
	seats := fs.Int("seats", 0, "seats (0: the tier's cap: home 4, pro unlimited)")
	days := fs.Int("days", 365, "days the license is valid")
	holder := fs.String("holder", "", "who the license is for (shown in the home's Settings)")
	id := fs.String("id", "", "license id")
	reason := fs.String("reason", "", "reason (audit log)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch sub {
	case "issue", "pubkey":
		is, closeDB, err := licenseIssuer(*cfgPath, *keyStdin, stdin)
		if err != nil {
			return err
		}
		defer closeDB()
		if sub == "pubkey" {
			der, err := x509.MarshalPKIXPublicKey(is.Priv.Public())
			if err != nil {
				return err
			}
			return pem.Encode(out, &pem.Block{Type: "PUBLIC KEY", Bytes: der})
		}
		lic, err := is.Offline(ctx, *tier, *seats, strings.TrimSpace(*holder), time.Duration(*days)*24*time.Hour)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(lic)
	case "revoke", "unrevoke":
		if *id == "" || strings.TrimSpace(*reason) == "" {
			return errors.New("--id and --reason are required")
		}
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			return err
		}
		pool, err := db.Open(cfg.Database.URL, 2, 1, time.Minute)
		if err != nil {
			return err
		}
		defer pool.Close()
		is := &license.Issuer{DB: pool.DB}
		action := "license.revoke"
		if sub == "revoke" {
			err = is.Revoke(ctx, *id, *reason, "cli")
		} else {
			action = "license.unrevoke"
			var found bool
			if found, err = is.Unrevoke(ctx, *id); err == nil && !found {
				err = errors.New("that license is not revoked")
			}
		}
		if err != nil {
			return err
		}
		return audit.New(pool.DB).Write(ctx, audit.Entry{ActorEmail: "cli", Action: action, TargetKind: "license",
			TargetID: *id, Detail: map[string]any{"reason": *reason}})
	}
	return fmt.Errorf("unknown subcommand %q\n%s", sub, licenseUsage)
}

// licenseIssuer builds the signer from stdin (no database) or from the
// cloud's configuration (its key file and database).
func licenseIssuer(cfgPath string, keyStdin bool, stdin io.Reader) (*license.Issuer, func(), error) {
	if keyStdin {
		b, err := io.ReadAll(io.LimitReader(stdin, 4096))
		if err != nil {
			return nil, nil, err
		}
		s, err := entitlement.ParseSigner(b)
		if err != nil {
			return nil, nil, err
		}
		return &license.Issuer{Priv: s.Priv}, func() {}, nil
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Entitlement.PrivateKeyPath == "" {
		return nil, nil, errors.New("the configuration has no entitlement.private_key_path")
	}
	s, err := entitlement.LoadSignerFromFile(cfg.Entitlement.PrivateKeyPath)
	if err != nil {
		return nil, nil, err
	}
	pool, err := db.Open(cfg.Database.URL, 2, 1, time.Minute)
	if err != nil {
		return nil, nil, err
	}
	return &license.Issuer{DB: pool.DB, Priv: s.Priv}, func() { _ = pool.Close() }, nil
}

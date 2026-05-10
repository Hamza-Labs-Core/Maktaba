package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
)

// runKeys implements `maktaba-api keys <action> [flags]` (Story 10.6
// AC-2 + AC-4 + AC-5).
//
// Subactions:
//
//	init     Generate a fresh 4096-bit RSA keypair, print PEM to stdout.
//	rotate   Print PEM for a fresh key the operator should swap into
//	         their env. With --immediate, asks for explicit
//	         confirmation. (The actual rotation happens at the next
//	         restart of the API — keys init/rotate are operator-side
//	         tools; the runtime swap is policy-driven by the LISTEN
//	         channel in plan-10-06 §3.)
//
// We keep `keys` *operator-only*: the CLI never writes to disk
// (AC-2: the operator stores the PEM where they want it).
func runKeys(argv []string) error {
	if len(argv) == 0 {
		return errors.New("keys: expected action (init|rotate)")
	}
	action := argv[0]
	rest := argv[1:]
	switch action {
	case "init":
		return runKeysInit(rest)
	case "rotate":
		return runKeysRotate(rest)
	case "help", "-h", "--help":
		printKeysUsage()
		return nil
	}
	return fmt.Errorf("keys: unknown action %q (expected init|rotate)", action)
}

func printKeysUsage() {
	fmt.Fprintln(os.Stderr, `Usage: maktaba-api keys <action> [flags]

Actions:
  init                Generate a fresh keypair and print PEM to stdout.
                      The PEMs are intended for the
                      MAKTABA_JWT_PRIVATE_KEY_PEM and
                      MAKTABA_JWT_PUBLIC_KEY_PEM env vars.
  rotate [--immediate]
                      Generate a replacement keypair. With --immediate,
                      requires typing 'yes-invalidate-all-tokens' to
                      proceed (Story 10.6 AC-5).
  help                Show this help.

This subcommand never writes to disk; PEM goes to stdout, instructions
to stderr. Story 10.6 AC-2.`)
}

func runKeysInit(argv []string) error {
	fs := flag.NewFlagSet("keys init", flag.ContinueOnError)
	bits := fs.Int("bits", keys.DefaultBits, "RSA key size in bits (>= 2048)")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	return generateAndPrint(*bits)
}

func runKeysRotate(argv []string) error {
	fs := flag.NewFlagSet("keys rotate", flag.ContinueOnError)
	bits := fs.Int("bits", keys.DefaultBits, "RSA key size in bits (>= 2048)")
	immediate := fs.Bool("immediate", false, "Invalidate every in-flight token (requires explicit confirmation).")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *immediate {
		// Story 10.6 AC-5: the magic string is the gate. If stdin
		// isn't a TTY (e.g. piped input), we still require the
		// literal string — automation should pass it explicitly.
		fmt.Fprintln(os.Stderr,
			"keys rotate --immediate: this will invalidate every in-flight token.")
		fmt.Fprint(os.Stderr,
			"Type 'yes-invalidate-all-tokens' to proceed: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) != "yes-invalidate-all-tokens" {
			return errors.New("keys rotate: aborted (confirmation string did not match)")
		}
	}
	return generateAndPrint(*bits)
}

func generateAndPrint(bits int) error {
	k, err := keys.Generate(bits)
	if err != nil {
		return err
	}
	priv, err := keys.EncodePrivatePEM(k)
	if err != nil {
		return err
	}
	pub, err := keys.EncodePublicPEM(k)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr,
		"# kid=%s\n# Set the following in your environment (or in [auth] config):\n#   MAKTABA_JWT_PRIVATE_KEY_PEM=...\n#   MAKTABA_JWT_PUBLIC_KEY_PEM=...\n",
		k.KID)
	fmt.Println("--- BEGIN PRIVATE PEM ---")
	fmt.Print(priv)
	fmt.Println("--- END PRIVATE PEM ---")
	fmt.Println("--- BEGIN PUBLIC PEM ---")
	fmt.Print(pub)
	fmt.Println("--- END PUBLIC PEM ---")
	return nil
}

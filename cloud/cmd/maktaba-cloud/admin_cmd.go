package main

import (
	"fmt"
	"io"
	"os"
)

// adminPointer is what the removed `maktaba-cloud admin grant|revoke|list` prints (story
// 50.33 §1): an operator is a staff account now, never a customer account with a role.
const adminPointer = `maktaba-cloud admin is gone: the operator console signs in with staff accounts.
  maktaba-cloud staff add --email ops@example.com --role service_admin
  maktaba-cloud staff list
(maktaba-cloud staff prints every command.)`

// runAdmin prints the pointer and fails, so a script that still calls it stops.
func runAdmin(_ []string) {
	printAdminPointer(os.Stderr)
	os.Exit(2)
}

func printAdminPointer(w io.Writer) { fmt.Fprintln(w, adminPointer) }

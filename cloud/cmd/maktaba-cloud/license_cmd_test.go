package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
)

// The operator mints an offline license with the key piped in, and the
// public key the home embeds verifies it.
func TestLicenseCmdIssueAndPubkey(t *testing.T) {
	seed := bytes.Repeat([]byte{9}, 32)
	keyHex := hex.EncodeToString(seed)
	var out bytes.Buffer
	if err := licenseCmd(context.Background(), "pubkey", []string{"--key-stdin"}, strings.NewReader(keyHex+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(out.Bytes())
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub := pubAny.(ed25519.PublicKey)

	out.Reset()
	if err := licenseCmd(context.Background(), "issue", []string{"--key-stdin", "--tier", "pro", "--days", "30", "--holder", "Owner"},
		strings.NewReader(keyHex), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), keyHex) {
		t.Fatal("the key leaked into the output")
	}
	var lic license.License
	if err := json.Unmarshal(out.Bytes(), &lic); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(lic.License)
	sig, _ := base64.StdEncoding.DecodeString(lic.Signature)
	if !ed25519.Verify(pub, payload, sig) {
		t.Fatal("issued license does not verify with the printed public key")
	}
	if lic.License.Tier != "pro" || lic.License.Holder != "Owner" || lic.License.ServerID != "" ||
		!strings.HasPrefix(lic.License.LicenseID, "lic_") || time.Until(lic.License.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("license: %+v", lic.License)
	}
	for _, bad := range [][]string{{"--key-stdin", "--tier", "premium"}, {"--key-stdin", "--tier", "home", "--days", "0"}} {
		if err := licenseCmd(context.Background(), "issue", bad, strings.NewReader(keyHex), &bytes.Buffer{}); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

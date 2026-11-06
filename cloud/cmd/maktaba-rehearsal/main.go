// Command maktaba-rehearsal runs the cloud rehearsal's stand-ins (Epic 50
// story 50.06) inside its Docker cluster. It is never deployed.
//
//	maktaba-rehearsal fakehcloud   the fake Hetzner Floating IP API (fakehcloud)
//
// fakehcloud reads: FAKEHCLOUD_ADDR (:8080), FAKEHCLOUD_DELAY (5s),
// FAKEHCLOUD_TOKEN, DOCKER_SOCKET (/var/run/docker.sock), FAKEHCLOUD_NETWORK,
// FAKEHCLOUD_ALIAS (relay.test), FAKEHCLOUD_SERVERS ("1=node-a@10.50.0.11,2=node-b@10.50.0.12").
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/fakehcloud"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "fakehcloud" {
		fmt.Fprintln(os.Stderr, "usage: maktaba-rehearsal fakehcloud")
		os.Exit(2)
	}
	delay, err := time.ParseDuration(env("FAKEHCLOUD_DELAY", "5s"))
	if err != nil {
		log.Fatal(err)
	}
	servers, ips, err := parseServers(env("FAKEHCLOUD_SERVERS", ""))
	if err != nil {
		log.Fatal(err)
	}
	mover := &fakehcloud.DockerMover{
		Socket:  env("DOCKER_SOCKET", "/var/run/docker.sock"),
		Network: env("FAKEHCLOUD_NETWORK", "maktaba-rehearsal_private"),
		Alias:   env("FAKEHCLOUD_ALIAS", "relay.test"),
		Servers: servers, IPs: ips,
	}
	api := fakehcloud.New(mover, delay)
	api.Token = os.Getenv("FAKEHCLOUD_TOKEN")
	addr := env("FAKEHCLOUD_ADDR", ":8080")
	log.Printf("fakehcloud on %s, delay %s, servers %v", addr, delay, servers)
	log.Fatal(http.ListenAndServe(addr, api))
}

// parseServers reads "1=node-a@10.50.0.11,2=node-b@10.50.0.12".
func parseServers(s string) (map[int64]string, map[int64]string, error) {
	names, ips := map[int64]string{}, map[int64]string{}
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		id, rest, ok := strings.Cut(part, "=")
		if !ok {
			return nil, nil, fmt.Errorf("FAKEHCLOUD_SERVERS: %q", part)
		}
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("FAKEHCLOUD_SERVERS: %q: %w", part, err)
		}
		name, ip, _ := strings.Cut(rest, "@")
		names[n], ips[n] = name, ip
	}
	return names, ips, nil
}

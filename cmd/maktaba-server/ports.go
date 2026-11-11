package main

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/portholder"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// portProbe is the probe setup and serve check binds with (tests replace it).
var portProbe = config.DefaultPortProbe

// reconcilePorts checks the web, API and streaming binds of cfg, moves the ones
// another program holds into the fallback range (config/ports.go), logs each move
// and, when save is set and something moved, writes cfg to path.
func reconcilePorts(cfg *config.Config, path string, o config.PortOptions, out io.Writer, save bool) ([]config.PortChange, error) {
	changes, err := cfg.ReconcilePorts(portProbe(), o)
	for i, ch := range changes {
		if ch.Taken {
			changes[i].Holder = portHolder(ch.OldPort)
			ch = changes[i]
		}
		fmt.Fprintf(out, "  port: %s\n", ch)
		if ch.Taken {
			en, ar := portMovedText(ch)
			fmt.Fprintf(out, "  port: %s\n  port: %s\n", en, ar)
		}
		// the web port is the one a household types: its new address, in both languages
		if ch.Key == "web_listen" {
			en, ar := webMovedText(cfg.PublicWebURL())
			fmt.Fprintf(out, "  port: %s\n  port: %s\n", en, ar)
		}
	}
	if err != nil {
		return changes, err
	}
	if save && len(changes) > 0 {
		if err := cfg.SaveTo(path); err != nil {
			return changes, err
		}
		fmt.Fprintf(out, "  port: wrote the new address(es) to %s\n", path)
	}
	return changes, nil
}

// nodeWebPreference is the web port a full node prefers on this OS (0: none).
func nodeWebPreference() int {
	if runtime.GOOS == "windows" {
		return config.WindowsNodeWebPort
	}
	return 0
}

// serveBinds are the addresses serve listens on itself, for CheckBinds: the web port
// and its HTTPS twin when it serves the web, and the supervisor's endpoint when it
// is on.
func serveBinds(cfg config.Config, web bool, supervisorAddr string) []ports.Bind {
	var out []ports.Bind
	if web {
		out = append(out, ports.Bind{Addr: cfg.Server.WebListen, Owner: "serve", What: "web port"},
			ports.Bind{Addr: httpsAddr, Owner: "serve", What: "HTTPS port"})
	}
	if supervisorAddr != "" {
		out = append(out, ports.Bind{Addr: supervisorAddr, Owner: "serve", What: "supervisor port"})
	}
	return out
}

// servePortWait is how long serve waits for a taken port before moving it (a
// previous instance may still be exiting); tests set it to 0.
var servePortWait = 10 * time.Second

// portHolder names the program holding port ("" when the system does not let us see it:
// in a container on the host's network the NAS's own processes are in another PID
// namespace). Tests replace it.
var portHolder = func(port int) string {
	h, ok := portholder.Find(port)
	if !ok {
		return ""
	}
	return h.Command
}

// portMoveCode is the setup note (setup.check.note.<code>) a taken port's move is: with
// its holder's name, or "another program".
func portMoveCode(ch config.PortChange) string {
	if ch.Holder != "" {
		return "port_moved"
	}
	return "port_moved_other"
}

// portMovedText is a taken port's move in English and Arabic.
func portMovedText(ch config.PortChange) (en, ar string) {
	t := webSetupTexts["setup.check.note."+portMoveCode(ch)]
	r := strings.NewReplacer("{old}", strconv.Itoa(ch.OldPort), "{new}", strconv.Itoa(ch.NewPort), "{holder}", ch.Holder)
	return r.Replace(t[0]), r.Replace(t[1])
}

// webMovedText is the web address after the web port moved, in English and Arabic.
func webMovedText(url string) (en, ar string) {
	t := webSetupTexts["setup.check.note.web_moved"]
	return strings.ReplaceAll(t[0], "{url}", url), strings.ReplaceAll(t[1], "{url}", url)
}

// portMove is one moved port as Settings → This machine shows it.
type portMove struct {
	Key    string `json:"key"`
	Old    int    `json:"old"`
	New    int    `json:"new"`
	Taken  bool   `json:"taken"`
	Holder string `json:"holder,omitempty"`
}

func portMovesOf(changes []config.PortChange) []portMove {
	out := []portMove{}
	for _, ch := range changes {
		out = append(out, portMove{Key: ch.Key, Old: ch.OldPort, New: ch.NewPort, Taken: ch.Taken, Holder: ch.Holder})
	}
	return out
}

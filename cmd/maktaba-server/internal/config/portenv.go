package config

// Ports chosen in the container's environment (Epic 59 story 59.08). On a NAS other apps
// (Plex, the NAS's own UI) may already use Maktaba's default ports, and a compose file or a
// NAS template is where a household sets things, not server.toml. Each port has a setting:
//
//	MAKTABA_WEB_PORT       [server] web_listen's port   (8088, the web app)
//	MAKTABA_API_PORT       [server] listen's port       (8080, the API the apps and machines use)
//	MAKTABA_STREAM_PORT    [server] stream_addr's port  (8081, live TV and streaming)
//	MAKTABA_POSTGRES_PORT  [postgres] port              (54329, the database the machines use)
//
// and, on a bridge network or behind a port forward, the port the household's devices reach
// each one at when it differs from the container's own ("9088:8088"):
//
//	MAKTABA_PUBLIC_WEB_PORT     [server] public_web_port
//	MAKTABA_PUBLIC_API_PORT     [server] public_api_port
//	MAKTABA_PUBLIC_STREAM_PORT  [server] public_stream_port
//
// Every Load applies them over server.toml, so every program that reads the config (serve,
// setup, the health check, the roles' environment) uses the same port. They are an
// override, never written to server.toml: a Save keeps the file's own value, so removing the
// variable goes back to it. A port set here is pinned: when another program holds it, it is
// not moved into the fallback range as a default port is (ports.go), since the household chose
// it (and a published port or a firewall rule may name it); the start stops with a message
// naming the port and the variable (ErrPortPinned).

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// The port variables.
const (
	EnvWebPort          = "MAKTABA_WEB_PORT"
	EnvAPIPort          = "MAKTABA_API_PORT"
	EnvStreamPort       = "MAKTABA_STREAM_PORT"
	EnvPostgresPort     = "MAKTABA_POSTGRES_PORT"
	EnvPublicWebPort    = "MAKTABA_PUBLIC_WEB_PORT"
	EnvPublicAPIPort    = "MAKTABA_PUBLIC_API_PORT"
	EnvPublicStreamPort = "MAKTABA_PUBLIC_STREAM_PORT"
)

// Keys of PortPins: server.toml's own keys.
const (
	PinWeb          = "web_listen"
	PinAPI          = "listen"
	PinStream       = "stream_addr"
	PinPostgres     = "postgres.port"
	PinPublicWeb    = "public_web_port"
	PinPublicAPI    = "public_api_port"
	PinPublicStream = "public_stream_port"
)

// PortEnv is one port variable and the server.toml key it overrides.
type PortEnv struct {
	Env string
	Key string
}

// PortEnvs are the port variables, in the order they are applied.
var PortEnvs = []PortEnv{
	{EnvWebPort, PinWeb},
	{EnvAPIPort, PinAPI},
	{EnvStreamPort, PinStream},
	{EnvPostgresPort, PinPostgres},
	{EnvPublicWebPort, PinPublicWeb},
	{EnvPublicAPIPort, PinPublicAPI},
	{EnvPublicStreamPort, PinPublicStream},
}

// PortEnvError: a port variable that is not a port. Its message is in English and Arabic,
// one line each (the container's log, setup's terminal).
type PortEnvError struct {
	Env, Value string
}

// Texts is the message in English and Arabic.
func (e PortEnvError) Texts() (en, ar string) {
	en = fmt.Sprintf("%s is %q, which is not a port: set it to a number from 1 to 65535, or remove it to use the default.", e.Env, e.Value)
	ar = fmt.Sprintf("قيمة %s هي %q، وهي ليست منفذًا: اضبطها على رقم من 1 إلى 65535، أو احذفها لاستخدام المنفذ الافتراضي.", e.Env, e.Value)
	return en, ar
}

func (e PortEnvError) Error() string {
	en, ar := e.Texts()
	return en + "\n" + ar
}

// filePorts are server.toml's own values of the keys a variable overrides, which a Save
// writes back.
type filePorts struct {
	web, api, stream               string
	postgres                       int
	publicWeb, publicAPI, publicSt int
}

// parsePort reads a port variable's value: 1 to 65535.
func parsePort(env, v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return 0, PortEnvError{Env: env, Value: v}
	}
	return n, nil
}

// WithPort is bind addr with its port replaced (its host kept: "0.0.0.0:8088" with 9088 is
// "0.0.0.0:9088", ":8081" is ":9081"); an addr that names no host:port becomes def's host
// with port.
func WithPort(addr, def string, port int) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		host, _, _ = net.SplitHostPort(def)
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// applyPortEnv sets c's ports from the port variables (an empty one is not set: Unraid
// passes a template's empty variable as ""), recording each in c.PortPins and server.toml's
// own value in c.file.
func (c *Config) applyPortEnv() error {
	c.PortPins, c.file = nil, filePorts{}
	file := filePorts{
		web: c.Server.WebListen, api: c.Server.Listen, stream: c.Server.StreamAddr,
		postgres:  c.Postgres.Port,
		publicWeb: c.Server.PublicWebPort, publicAPI: c.Server.PublicAPIPort, publicSt: c.Server.PublicStreamPort,
	}
	def := Defaults().Server
	for _, pe := range PortEnvs {
		v := strings.TrimSpace(os.Getenv(pe.Env))
		if v == "" {
			continue
		}
		n, err := parsePort(pe.Env, v)
		if err != nil {
			return err
		}
		switch pe.Key {
		case PinWeb:
			c.Server.WebListen = WithPort(c.Server.WebListen, def.WebListen, n)
		case PinAPI:
			c.Server.Listen = WithPort(c.Server.Listen, def.Listen, n)
		case PinStream:
			c.Server.StreamAddr = WithPort(c.Server.StreamAddr, def.StreamAddr, n)
		case PinPostgres:
			c.Postgres.Port = n
		case PinPublicWeb:
			c.Server.PublicWebPort = n
		case PinPublicAPI:
			c.Server.PublicAPIPort = n
		case PinPublicStream:
			c.Server.PublicStreamPort = n
		}
		if c.PortPins == nil {
			c.PortPins, c.file = map[string]string{}, file
		}
		c.PortPins[pe.Key] = pe.Env
	}
	return nil
}

// withFilePorts is c with server.toml's own value back in each key a variable overrides
// (what a Save writes).
func (c Config) withFilePorts() Config {
	for key := range c.PortPins {
		switch key {
		case PinWeb:
			c.Server.WebListen = c.file.web
		case PinAPI:
			c.Server.Listen = c.file.api
		case PinStream:
			c.Server.StreamAddr = c.file.stream
		case PinPostgres:
			c.Postgres.Port = c.file.postgres
		case PinPublicWeb:
			c.Server.PublicWebPort = c.file.publicWeb
		case PinPublicAPI:
			c.Server.PublicAPIPort = c.file.publicAPI
		case PinPublicStream:
			c.Server.PublicStreamPort = c.file.publicSt
		}
	}
	return c
}

// PinnedBy is the variable that sets server.toml key ("" when none does).
func (c Config) PinnedBy(key string) string { return c.PortPins[key] }

// ErrPortPinned: a port a variable sets is held by another program, so that part of Maktaba
// cannot start (a pinned port is never moved). Holder names the program when the system
// lets us see it; the caller fills it in.
type ErrPortPinned struct {
	Key, Env string
	Port     int
	Holder   string
}

// Setting is where the household changes the port: the variable, and for the database also
// its server.toml key.
func (e *ErrPortPinned) Setting() string { return e.Env }

func (e *ErrPortPinned) Error() string {
	who := "another program"
	if e.Holder != "" {
		who = e.Holder
	}
	return fmt.Sprintf("port %d (%s) is used by %s on this machine, and %s sets it, so it is not moved: set %s to a free port",
		e.Port, e.Key, who, e.Env, e.Env)
}

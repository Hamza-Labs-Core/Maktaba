// Package ctrlbreak asks a child process on Windows to stop with CTRL_BREAK
// without the event going astray (Epic 38 story 38.5). The event names the
// process group the child leads. Once the child has exited nobody leads that
// group, and Windows then sends the event to every process of the console, the
// sender included: a server asking its venv bootstrap to stop would stop
// itself and every role. Hold keeps the child suspended from the check that it
// still runs to the send. Nothing here is used on other systems.
package ctrlbreak

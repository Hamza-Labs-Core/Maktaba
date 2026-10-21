// Package ctrlbreak asks a child process to stop gently (Epic 38 story 38.5).
//
// On Windows that is CTRL_BREAK to the process group the child leads. Once
// the child has exited nobody leads that group, and Windows then sends the
// event to every process of the console, the sender included: a server asking
// its venv bootstrap to stop would stop itself and every role. Hold keeps the
// child suspended from the check that it still runs to the send, through a
// handle that keeps the child's pid its own.
//
// Stopper puts that in an exec.Cmd's Cancel, with the handle opened as soon
// as the child starts; elsewhere it sends SIGTERM.
package ctrlbreak

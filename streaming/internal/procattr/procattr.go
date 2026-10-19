// Package procattr ties the streaming service's own child processes
// (ffmpeg, yt-dlp) to it, so they never outlive it (Story 22.9).
//
//   - Windows: ContainSelf puts this process in a Job Object with
//     JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. Every process it starts after
//     that inherits the job, and when this process dies, however it dies,
//     the kernel closes the last job handle and ends them all.
//   - Linux: Tie sets Pdeathsig = SIGKILL on a child, so the kernel kills
//     it when the streaming process exits.
//   - Everywhere: under maktaba-server the child also runs in the
//     supervisor's process group or job, which the supervisor kills on
//     restart and stop.
package procattr

package main

import "time"

// stepStopWait is how long a cancelled step's program may take to stop after
// it is asked (stopGently) before it is killed: the venv bootstrap gives pip
// 10 s, then writes its status (interrupted, where it stopped); a download
// closes its partial file. 45 s leaves room on a slow, busy disk.
const stepStopWait = 45 * time.Second

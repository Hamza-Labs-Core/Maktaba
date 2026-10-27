package storage

import (
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Folder is one of the install's folders: where it is, what it holds, its disk.
type Folder struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Bytes  uint64 `json:"bytes"`
	Files  int    `json:"files"`
	Exists bool   `json:"exists"`
	Disk   string `json:"disk"`
}

// MoveState is the last move's, or the one under way.
type MoveState struct {
	// State: none | requested | moving | failed | done.
	State  string     `json:"state"`
	Target string     `json:"target,omitempty"`
	Error  string     `json:"error,omitempty"`
	At     *time.Time `json:"at,omitempty"`
	Old    []string   `json:"old,omitempty"`
}

// Doc is where the install keeps its data (storage show, Settings → Storage).
type Doc struct {
	DataDir        string    `json:"data_dir"`
	StateDir       string    `json:"state_dir"`
	DefaultDataDir string    `json:"default_data_dir"`
	TotalBytes     uint64    `json:"total_bytes"`
	Folders        []Folder  `json:"folders"`
	Disks          []Disk    `json:"disks"`
	Move           MoveState `json:"move"`
	CanMove        bool      `json:"can_move"`
	Reason         string    `json:"reason,omitempty"`
}

// Report is in's folders, the disks they are on and the state of its moves.
func Report(in Install, defaultData string) Doc {
	d := Doc{DataDir: in.From.Data, StateDir: in.From.Root, DefaultDataDir: defaultData, Folders: []Folder{},
		Disks: []Disk{}, CanMove: true}
	add := func(name, path string, exclude []string) {
		f := Folder{Name: name, Path: path, Exists: exists(path)}
		if f.Exists {
			f.Files, f.Bytes = sizeOf(path, exclude)
		}
		if disk, err := DiskOf(path); err == nil {
			f.Disk = disk.Mount
			seen := false
			for _, x := range d.Disks {
				seen = seen || x.Mount == disk.Mount
			}
			if !seen {
				disk.Current = true
				disk.SuggestedDir = filepath.Join(disk.Mount, "Maktaba")
				d.Disks = append(d.Disks, disk)
			}
		}
		d.TotalBytes += f.Bytes
		d.Folders = append(d.Folders, f)
	}
	exclude := []string{config.StorageMarkerFile}
	if in.From.Data == in.From.Root {
		exclude = append(exclude, config.StateRootFolders...)
	}
	add("data", in.From.Data, exclude)
	for _, n := range config.StateRootFolders {
		add(n, filepath.Join(in.From.Root, n), nil)
	}
	d.Move = moveState(in.ConfigDir)
	return d
}

func moveState(configDir string) MoveState {
	if r, ok := ReadRequest(configDir); ok {
		at := r.At
		return MoveState{State: "requested", Target: r.Target, At: &at}
	}
	if target, _, ok := ReadJournal(configDir); ok {
		return MoveState{State: "moving", Target: target}
	}
	if r, ok := ReadResult(configDir); ok {
		at := r.At
		if !r.OK {
			return MoveState{State: "failed", Target: r.Target, Error: r.Error, At: &at}
		}
		var old []string
		for _, o := range r.Old {
			if exists(o) {
				old = append(old, o)
			}
		}
		return MoveState{State: "done", Target: r.Target, At: &at, Old: old}
	}
	return MoveState{State: "none"}
}

package updates

import "testing"

// Story 59.12: a data folder without room for a safe update waits for a person (the run
// never goes on without the dump), and the run's error carries the need and the free space
// for the Updates page's message.
func TestDiskShortCarriesTheRoom(t *testing.T) {
	doc := machineErr(&MachineStatus{Error: &MachineError{Step: "preflight", Code: "disk_short",
		Detail: "Not enough free space", NeedBytes: 13 << 30, FreeBytes: 12 << 30}}, "prepare_failed")
	if doc["code"] != "disk_short" || doc["need_bytes"] != uint64(13<<30) || doc["free_bytes"] != uint64(12<<30) {
		t.Fatalf("%v", doc)
	}
	if !needsPerson(doc) {
		t.Fatal("disk_short must wait for a person")
	}
	if doc := machineErr(&MachineStatus{Error: &MachineError{Code: "smoke_failed"}}, "x"); doc["need_bytes"] != nil {
		t.Fatalf("no room check, no bytes: %v", doc)
	}
}

package pgbundle

import "testing"

// The tests here run on a disk with room (1 TiB, all of it free) unless one fakes its
// own (fakeDisk): a dump's room is 5% of the real disk free, 46 GB of a 926 GB Mac's,
// so on a fuller disk every test that dumps failed with NoRoomError while a CI runner's
// disk passed them.
func init() {
	diskSpace = func(string) (uint64, uint64, error) { return 1 << 40, 1 << 40, nil }
}

// The real disk is read: free to this user, never more than the disk.
func TestDiskSpaceOfReadsTheDisk(t *testing.T) {
	free, total, err := diskSpaceOf(t.TempDir())
	if err != nil || total == 0 || free > total {
		t.Fatalf("free %d total %d: %v", free, total, err)
	}
}

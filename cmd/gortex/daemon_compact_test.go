package main

import "testing"

// TestShouldCompactStore pins the boot-compaction trigger: all three gates
// (majority-dead file, absolute reclaimable floor, disk headroom for the
// VACUUM copy) must hold, and each boundary is exclusive — equality on any
// gate means "don't". Pure-function table so the policy is exercised without
// a store or a filesystem.
func TestShouldCompactStore(t *testing.T) {
	const (
		gib = int64(1) << 30
		tib = uint64(1) << 40
	)
	cases := []struct {
		name  string
		free  int64
		total int64
		avail uint64
		want  bool
	}{
		{name: "all gates hold", free: 2 * gib, total: 3 * gib, avail: tib, want: true},
		{name: "the observed live store shape (4.4/6.8 GB)", free: 4707074048, total: 7301444403, avail: tib, want: true},

		// Fraction gate: freelist must be a strict MAJORITY of the file.
		{name: "exactly half free — no", free: 2 * gib, total: 4 * gib, avail: tib, want: false},
		{name: "just under half free — no", free: 2*gib - 1, total: 4 * gib, avail: tib, want: false},
		{name: "just over half free — yes", free: 2*gib + 1, total: 4 * gib, avail: tib, want: true},

		// Absolute floor: a small file's majority is still not worth minutes
		// of exclusive I/O.
		{name: "90% free but only 900 MiB — no", free: 900 << 20, total: 1 << 30, avail: tib, want: false},
		{name: "exactly 1 GiB free — no (floor is exclusive)", free: gib, total: gib + 2, avail: tib, want: false},

		// Headroom gate: available disk must strictly exceed total × 1.5,
		// because VACUUM transiently needs up to a full extra copy.
		{name: "avail exactly 1.5× — no", free: 2 * gib, total: 3 * gib, avail: uint64(3*gib) + uint64(3*gib)/2, want: false},
		{name: "avail just over 1.5× — yes", free: 2 * gib, total: 3 * gib, avail: uint64(3*gib) + uint64(3*gib)/2 + 1, want: true},
		{name: "tight disk — no", free: 2 * gib, total: 3 * gib, avail: uint64(3 * gib), want: false},

		// Degenerate inputs: an unreadable store reports zeros; never fire.
		{name: "zero stats", free: 0, total: 0, avail: tib, want: false},
		{name: "zero free", free: 0, total: 4 * gib, avail: tib, want: false},
		{name: "zero total", free: 2 * gib, total: 0, avail: tib, want: false},
		{name: "negative total", free: 2 * gib, total: -1, avail: tib, want: false},
		{name: "zero avail", free: 2 * gib, total: 3 * gib, avail: 0, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldCompactStore(tc.free, tc.total, tc.avail); got != tc.want {
				t.Errorf("shouldCompactStore(free=%d, total=%d, avail=%d) = %v, want %v",
					tc.free, tc.total, tc.avail, got, tc.want)
			}
		})
	}
}

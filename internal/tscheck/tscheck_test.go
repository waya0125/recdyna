// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package tscheck

import "testing"

func pkt(pid uint16, cc uint8, flags uint8) []byte {
	p := make([]byte, packetSize)
	p[0] = 0x47
	p[1] = uint8(pid>>8) | flags
	p[2] = uint8(pid)
	p[3] = 0x10 | cc
	return p
}

func TestStats(t *testing.T) {
	s := New()
	var b []byte
	b = append(b, pkt(0x100, 0, 0)...)
	b = append(b, pkt(0x100, 1, 0)...)
	b = append(b, pkt(0x100, 1, 0)...) // 重複は 1 回なら正しい
	b = append(b, pkt(0x100, 3, 0)...) // 2 が抜けた
	b = append(b, pkt(0x1fff, 7, 0)...)
	b = append(b, pkt(0x100, 4, 0x80)...) // TEI
	bad := pkt(0x100, 5, 0)
	bad[0] = 0x46
	b = append(b, bad...)
	s.Add(b)
	if s.Packets != 7 || s.CCError != 1 || s.TEI != 1 || s.SyncError != 1 || s.PIDs[0x100] != 4 || s.PIDs[0x1fff] != 1 {
		t.Fatalf("%+v", s)
	}
}

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package dyud200

import "testing"

func makeTestPacket(pid uint16, cc byte, tei bool) []byte {
	p := make([]byte, PacketSize)
	p[0] = 0x47
	p[1] = byte((pid >> 8) & 0x1f)
	if tei {
		p[1] |= 0x80
	}
	p[2] = byte(pid & 0xff)
	p[3] = 0x10 | (cc & 0x0f) // payload only
	return p
}

func TestSettlerDropsTEIAndCCErrorAndNullOnly(t *testing.T) {
	s := &Settler{Clean: 5, MaxDrop: 100, lastCC: make(map[uint16]byte)}

	var in []byte
	// NULL パケットが 10 個続いても settled にならないこと
	for range 10 {
		in = append(in, makeTestPacket(0x1fff, 0, false)...)
	}
	if out := s.Feed(in); out != nil || s.Settled() {
		t.Fatalf("null-only stream should not settle")
	}

	in = nil
	// PAT + 2 パケット正常、3 番目で CC 飛び
	in = append(in, makeTestPacket(0x0000, 0, false)...)
	in = append(in, makeTestPacket(0x0100, 0, false)...)
	in = append(in, makeTestPacket(0x0100, 5, false)...) // CC error -> これまでの 13 個を捨てる
	// 続けて 1 パケット TEI
	in = append(in, makeTestPacket(0x0100, 6, true)...) // TEI -> 捨てる
	// ここから PAT 1 個 + 映像 5 個 = 非 NULL 6 個連続正常（Clean=5 で確定、残り 1 個も出力）
	in = append(in, makeTestPacket(0x0000, 1, false)...)
	for i := range 5 {
		in = append(in, makeTestPacket(0x0100, byte(i), false)...)
	}

	out := s.Feed(in)
	if !s.Settled() || s.GaveUp {
		t.Fatalf("expected settled without giveup: settled=%v gaveup=%v", s.Settled(), s.GaveUp)
	}
	if s.Dropped != 14 {
		t.Fatalf("dropped=%d want 14", s.Dropped)
	}
	if len(out) != 6*PacketSize {
		t.Fatalf("out len=%d want %d", len(out), 6*PacketSize)
	}
	// 先頭パケットは PID 0x0000 (PAT)
	if out[1]&0x1f != 0 || out[2] != 0 {
		t.Fatalf("first out pid=%#04x want 0x0000", uint16(out[1]&0x1f)<<8|uint16(out[2]))
	}
}

func TestSettlerGivesUp(t *testing.T) {
	s := &Settler{Clean: 10, MaxDrop: 5, lastCC: make(map[uint16]byte)}
	var in []byte
	for i := range 6 {
		in = append(in, makeTestPacket(0x100, byte(i), true)...)
	}
	out := s.Feed(in)
	if !s.Settled() || !s.GaveUp {
		t.Fatalf("expected giveup: settled=%v gaveup=%v", s.Settled(), s.GaveUp)
	}
	if len(out) != 1*PacketSize {
		t.Fatalf("out len=%d want %d", len(out), PacketSize)
	}
}

func TestFrequency(t *testing.T) {
	cases := []struct {
		ch   int
		catv bool
		want uint32
		err  bool
	}{
		{13, false, 473143, false},
		{21, false, 521143, false},
		{27, false, 557143, false},
		{62, false, 767143, false},
		{1, false, 93143, false},
		{13, true, 111143, false},
		{22, true, 167143, false},
		{23, true, 225143, false},
		{63, true, 465143, false},
		{0, false, 0, true},
		{63, false, 0, true},
		{12, true, 0, true},
		{64, true, 0, true},
	}
	for _, tc := range cases {
		got, err := Frequency(tc.ch, tc.catv)
		if tc.err {
			if err == nil {
				t.Errorf("Frequency(%d, %v): expected error, got %d", tc.ch, tc.catv, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Frequency(%d, %v): unexpected error: %v", tc.ch, tc.catv, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Frequency(%d, %v) = %d, want %d", tc.ch, tc.catv, got, tc.want)
		}
	}
}

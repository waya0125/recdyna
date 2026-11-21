// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package dyud200

import (
	"fmt"
	"io"
)

const (
	// PacketSize は MPEG-2 TS パケット長（188 バイト）。
	PacketSize = 188

	// StaleDiscardBytes はロック直後に読み捨てる初期データ量。
	// BonDriver_dyud.cpp の RecvTSThread はロック直後に 2048 パケット（385,024 バイト）読み捨てている。
	StaleDiscardBytes = 2048 * PacketSize
)

// Aligner は USB から届いたバイト列を 188 バイトの TS パケットに同期し、
// 必要であれば DY-UD200 の XOR 暗号を復号する。
type Aligner struct {
	stale int
	rest  []byte
	xor   *XORCipher

	Log     io.Writer
	Resyncs int
}

// NewAligner は Aligner を作る。xor が nil でなければパケット復号を行う。
func NewAligner(xor *XORCipher) *Aligner {
	return &Aligner{
		stale: StaleDiscardBytes,
		xor:   xor,
		Log:   io.Discard,
	}
}

// Feed は受信チャンクから同期済みの 188 バイト単位 TS パケット列を切り出して返す。
func (a *Aligner) Feed(chunk []byte) []byte {
	if a.stale > 0 {
		drop := min(len(chunk), a.stale)
		a.stale -= drop
		chunk = chunk[drop:]
		if len(chunk) == 0 {
			return nil
		}
	}

	buf := append(a.rest, chunk...)
	size := len(buf)
	out := make([]byte, 0, size/PacketSize*PacketSize)
	pos := 0
	for {
		if size-pos < PacketSize {
			break
		}
		if buf[pos] != 0x47 {
			// 4 パケット連続の 0x47 を確認できるだけの残りバイトが無ければ次チャンクへ持ち越す
			if size-pos <= PacketSize*3 {
				break
			}
			start := pos
			found := false
			for pos+PacketSize*3 < size {
				if buf[pos] == 0x47 &&
					buf[pos+PacketSize] == 0x47 &&
					buf[pos+PacketSize*2] == 0x47 &&
					buf[pos+PacketSize*3] == 0x47 {
					found = true
					break
				}
				pos++
			}
			if pos != start {
				a.Resyncs++
				fmt.Fprintf(a.Log, "resync: skipped %d bytes\n", pos-start)
			}
			if !found {
				break
			}
		}
		pktStart := len(out)
		out = append(out, buf[pos:pos+PacketSize]...)
		if a.xor != nil {
			a.xor.DecryptPacket(out[pktStart : pktStart+PacketSize])
		}
		pos += PacketSize
	}
	a.rest = append(a.rest[:0:0], buf[pos:]...)
	return out
}

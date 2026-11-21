// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package dyud200

// Settler は選局直後の復調部および USB FIFO が安定するまでの不安定なパケットを破棄する。
// DY-UD200 は選局・ロック直後の 0.5〜1.0 秒ほどに同期崩れ・CC 飛び・TEI が集中するため、
// PAT（PID 0）を受信しつつ非 NULL パケットで TEI・CC 飛び・再同期が発生しない状態が
// Clean 個連続するまで待機し、安定した区間の先頭から出力する。
// MaxDrop 個破棄しても安定しない場合は、それ以降のパケットをそのまま出力する。
type Settler struct {
	Clean   int
	MaxDrop int

	nonNullRun int
	seenPAT    bool
	pending    []byte
	lastCC     map[uint16]byte
	done       bool

	// Dropped は破棄した総パケット数
	Dropped int
	// GaveUp は安定しないまま出力を開始したか
	GaveUp bool
}

const (
	// DefaultSettleClean は安定と判定するために必要な連続正常非 NULL パケット数（約 1.2 秒分）。
	// DY-UD200 は選局後 1 秒前後まで復調器・FIFO の過渡的な 4096B 欠損が発生することがあるため余裕を取る。
	DefaultSettleClean = 15000
	// DefaultSettleMaxDrop は出力を保留する最大破棄パケット数（約 8〜10 秒分）。
	DefaultSettleMaxDrop = 200000
)

// NewSettler は既定の値の Settler を作る。
func NewSettler() *Settler {
	return &Settler{
		Clean:   DefaultSettleClean,
		MaxDrop: DefaultSettleMaxDrop,
		lastCC:  make(map[uint16]byte),
	}
}

// Settled は安定して出力を開始したかを返す。
func (s *Settler) Settled() bool { return s.done }

// ResetRun は Aligner で再同期（resync）が起きたときに呼び、蓄積中の候補を破棄する。
func (s *Settler) ResetRun() {
	if s.done {
		return
	}
	s.Dropped += len(s.pending) / PacketSize
	s.nonNullRun = 0
	s.seenPAT = false
	s.pending = s.pending[:0]
	clear(s.lastCC)
}

// Feed は 188 バイト単位の packets を受け取り、流してよいパケット列を返す。
func (s *Settler) Feed(packets []byte) []byte {
	if s.done {
		return packets
	}
	for p := 0; p+PacketSize <= len(packets); p += PacketSize {
		pkt := packets[p : p+PacketSize]
		bad := pkt[0] != 0x47 || pkt[1]&0x80 != 0
		isNull := false
		if !bad {
			pid := uint16(pkt[1]&0x1f)<<8 | uint16(pkt[2])
			if pid == 0x1fff {
				isNull = true
			} else {
				if pid == 0 {
					s.seenPAT = true
				}
				afc := (pkt[3] >> 4) & 0x03
				if afc&0x01 != 0 {
					cc := pkt[3] & 0x0f
					disc := afc&0x02 != 0 && pkt[4] > 0 && pkt[5]&0x80 != 0
					if prev, seen := s.lastCC[pid]; seen && !disc {
						if cc != (prev+1)&0x0f && cc != prev {
							bad = true
						}
					}
					if !bad {
						s.lastCC[pid] = cc
					}
				}
			}
		}

		if bad {
			s.Dropped += len(s.pending)/PacketSize + 1
			s.nonNullRun = 0
			s.seenPAT = false
			s.pending = s.pending[:0]
			clear(s.lastCC)
		} else {
			if !isNull {
				s.nonNullRun++
			}
			s.pending = append(s.pending, pkt...)
		}

		if s.seenPAT && s.nonNullRun >= s.Clean {
			s.done = true
			out := append(s.pending, packets[p+PacketSize:]...)
			s.pending = nil
			s.lastCC = nil
			return out
		}
		if s.Dropped+len(s.pending)/PacketSize >= s.MaxDrop {
			s.done = true
			s.GaveUp = true
			out := append(s.pending, packets[p+PacketSize:]...)
			s.pending = nil
			s.lastCC = nil
			return out
		}
	}
	return nil
}
